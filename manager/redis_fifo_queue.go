package manager

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"strings"
	"sync"
	"time"

	redis "github.com/redis/go-redis/v9"
	"github.com/segmentio/ksuid"
	"github.com/slackmgr/types"
)

// ackNackTimeout is the timeout for ack/nack operations.
// These operations use context.Background() because they must complete regardless
// of the caller's context state - acknowledgment is a commitment, not a cancellable request.
// This should be shorter than the drain timeouts (default 3-5s) since these are simple Redis operations.
const ackNackTimeout = 2 * time.Second

// sendMessageScript atomically adds a message to a stream, adds the stream to the index,
// and updates the activity timestamp. Returns the message ID.
// This ensures consistency: if any operation succeeds, all operations succeed.
//
//nolint:gochecknoglobals // Lua script is immutable and shared across all instances for Redis script caching.
var sendMessageScript = redis.NewScript(`
	local streamKey = KEYS[1]
	local indexKey = KEYS[2]
	local activityKey = KEYS[3]
	local maxLen = tonumber(ARGV[1])
	local channelId = ARGV[2]
	local body = ARGV[3]
	local activityTime = tonumber(ARGV[4])

	-- Add message to stream with approximate trimming
	local messageId = redis.call('XADD', streamKey, 'MAXLEN', '~', maxLen, '*',
		'channel_id', channelId,
		'body', body)

	-- Add stream to index set
	redis.call('SADD', indexKey, streamKey)

	-- Update activity timestamp
	redis.call('ZADD', activityKey, activityTime, streamKey)

	return messageId
`)

// cleanupStaleStreamScript atomically checks if a stream is still stale and deletes it.
// Returns 1 if the stream was deleted, 0 if it was updated since the stale check.
// This prevents the TOCTOU race where a message could be sent between checking
// staleness and deletion.
//
//nolint:gochecknoglobals // Lua script is immutable and shared across all instances for Redis script caching.
var cleanupStaleStreamScript = redis.NewScript(`
	local streamKey = KEYS[1]
	local indexKey = KEYS[2]
	local activityKey = KEYS[3]
	local cutoff = tonumber(ARGV[1])

	-- Check current activity score
	local score = redis.call('ZSCORE', activityKey, streamKey)
	if score ~= false and tonumber(score) > cutoff then
		-- Stream was updated since stale check, do not delete
		return 0
	end

	-- Still stale (or not in activity set), delete atomically
	redis.call('DEL', streamKey)
	redis.call('SREM', indexKey, streamKey)
	redis.call('ZREM', activityKey, streamKey)
	return 1
`)

// RedisFifoQueueProducer is a lightweight write-only queue producer.
// Use this when the service only needs to send messages (e.g., API server in a separate service).
// For full queue functionality including Receive(), use RedisFifoQueue instead.
//
// This producer has minimal overhead: no consumer groups, no distributed locks,
// no in-process notification mechanism. It simply writes messages to Redis streams.
type RedisFifoQueueProducer struct {
	client      redis.UniversalClient
	opts        *RedisFifoQueueOptions
	name        string
	logger      types.Logger
	initialized bool
}

// RedisFifoQueue implements the FifoQueue interface using Redis Streams.
// It uses one stream per Slack channel to ensure message ordering within a channel,
// mimicking the behavior of SQS FIFO message groups.
//
// Multiple instances can consume from the same queue using Redis consumer groups,
// providing at-least-once delivery semantics. Messages for different channels
// can be processed in parallel across instances.
//
// Strict per-channel ordering is enforced using distributed locks. When a consumer
// reads from a stream, it acquires a lock for that channel. Other consumers cannot
// read from that stream until the lock is released (after ack/nack). This ensures
// that messages for a single channel are processed strictly in order, even across
// multiple instances.
//
// For write-only usage (e.g., API server), consider using RedisFifoQueueProducer instead.
type RedisFifoQueue struct {
	*RedisFifoQueueProducer // Embedded for Send() and Name()

	locker       ChannelLocker
	consumerName string

	// knownStreams tracks streams known to this instance for immediate notification.
	// When Send() adds a message to a stream, it updates this map so the receiver
	// can discover new streams immediately without waiting for the refresh interval.
	// Protected by knownStreamsMu.
	knownStreams   map[string]bool
	knownStreamsMu sync.RWMutex

	// notifyCh signals the receiver to check for new messages immediately.
	// Non-blocking sends are used to avoid blocking the sender.
	// This enables immediate message processing for in-process sends.
	notifyCh chan struct{}
}

// NewRedisFifoQueueProducer creates a new lightweight write-only queue producer.
// Use this when the service only needs to send messages and doesn't need to receive.
// The client should be a configured Redis client (can be a single node, sentinel, or cluster client).
// The name is used as part of the Redis key prefix and for identification.
func NewRedisFifoQueueProducer(client redis.UniversalClient, name string, logger types.Logger, opts ...RedisFifoQueueOption) *RedisFifoQueueProducer {
	options := newRedisFifoQueueOptions()

	for _, o := range opts {
		o(options)
	}

	logger = logger.
		WithField("component", "redis-fifo-queue-producer").
		WithField("queue_name", name)

	return &RedisFifoQueueProducer{
		client: client,
		name:   name,
		opts:   options,
		logger: logger,
	}
}

// NewRedisFifoQueue creates a new RedisFifoQueue instance with full send and receive capabilities.
// The client should be a configured Redis client (can be a single node, sentinel, or cluster client).
// The locker is used to ensure strict per-channel ordering across multiple instances.
// The name is used as part of the Redis key prefix and for identification.
//
// For write-only usage (e.g., API server), consider using NewRedisFifoQueueProducer instead.
func NewRedisFifoQueue(client redis.UniversalClient, locker ChannelLocker, name string, logger types.Logger, opts ...RedisFifoQueueOption) *RedisFifoQueue {
	producer := NewRedisFifoQueueProducer(client, name, logger, opts...)

	// Update logger component for full queue
	producer.logger = producer.logger.WithField("component", "redis-fifo-queue")

	return &RedisFifoQueue{
		RedisFifoQueueProducer: producer,
		locker:                 locker,
	}
}

// Init initializes the RedisFifoQueueProducer.
// It validates options and marks the producer as ready for use.
func (p *RedisFifoQueueProducer) Init() (*RedisFifoQueueProducer, error) {
	if p.initialized {
		return p, nil
	}

	if p.name == "" {
		return nil, errors.New("queue name cannot be empty")
	}

	if strings.ContainsAny(p.name, ": \t\n") {
		return nil, errors.New("queue name cannot contain colons, spaces, or whitespace")
	}

	if err := p.opts.validate(); err != nil {
		return nil, fmt.Errorf("invalid Redis FIFO queue options: %w", err)
	}

	p.initialized = true
	p.logger.Info("Redis FIFO queue producer initialized")

	return p, nil
}

// Init initializes the RedisFifoQueue.
// It validates options and generates a unique consumer name for this instance.
func (q *RedisFifoQueue) Init() (*RedisFifoQueue, error) {
	if q.initialized {
		return q, nil
	}

	if q.locker == nil {
		return nil, errors.New("locker cannot be nil")
	}

	// Initialize the embedded producer first
	if _, err := q.RedisFifoQueueProducer.Init(); err != nil {
		return nil, err
	}

	// Generate a unique consumer name for this instance.
	// This allows multiple instances to consume from the same consumer group.
	hostname, _ := os.Hostname()

	if hostname == "" {
		hostname = "unknown"
	}

	q.consumerName = fmt.Sprintf("%s-%s", hostname, ksuid.New().String())
	q.logger = q.logger.WithField("consumer_name", q.consumerName)

	// Initialize in-process notification mechanism.
	q.knownStreams = make(map[string]bool)
	q.notifyCh = make(chan struct{}, 1) // Buffered to allow one pending notification.

	return q, nil
}

// Name returns the name of the queue.
func (p *RedisFifoQueueProducer) Name() string {
	return p.name
}

// Send sends a message to the queue.
// The slackChannelID is used to route the message to a specific stream, ensuring ordering per channel.
// Note: The dedupID parameter is accepted for interface compatibility but is not used for deduplication.
// Redis Streams does not natively support message deduplication like SQS FIFO queues.
// Deduplication should be handled at the application level if needed.
func (p *RedisFifoQueueProducer) Send(ctx context.Context, slackChannelID, _, body string) error {
	if !p.initialized {
		return errors.New("redis FIFO queue producer not initialized")
	}

	if slackChannelID == "" {
		return errors.New("slackChannelID cannot be empty")
	}

	if body == "" {
		return errors.New("body cannot be empty")
	}

	streamKey := p.streamKey(slackChannelID)

	// Atomically add message to stream, update index, and record activity.
	// Using a Lua script ensures consistency: all operations succeed together or none do.
	messageID, err := p.sendMessageAtomic(ctx, streamKey, slackChannelID, body)
	if err != nil {
		return fmt.Errorf("failed to add message to Redis stream %s: %w", streamKey, err)
	}

	p.logger.WithField("message_id", messageID).WithField("channel_id", slackChannelID).Debug("Message sent to Redis stream")

	return nil
}

// Send sends a message to the queue with additional support for in-process receivers.
// This overrides the embedded producer's Send to add:
// - Consumer group creation for new streams (enables immediate consumption)
// - Local stream tracking for fast discovery by in-process receiver
// - Notification to wake up in-process receiver immediately
func (q *RedisFifoQueue) Send(ctx context.Context, slackChannelID, dedupID, body string) error {
	// Use embedded producer's send for the atomic Redis operations
	if err := q.RedisFifoQueueProducer.Send(ctx, slackChannelID, dedupID, body); err != nil {
		return err
	}

	streamKey := q.streamKey(slackChannelID)

	// Update local known streams for immediate discovery by in-process receiver.
	q.knownStreamsMu.Lock()
	isNew := !q.knownStreams[streamKey]
	q.knownStreams[streamKey] = true
	q.knownStreamsMu.Unlock()

	// If this is a new stream, ensure consumer group exists so receiver can read immediately.
	if isNew {
		err := q.client.XGroupCreateMkStream(ctx, streamKey, q.opts.consumerGroup, "0").Err()
		if err != nil && !isCtxCanceledErr(err) && !strings.Contains(err.Error(), "BUSYGROUP") {
			q.logger.WithField("stream_key", streamKey).Errorf("Failed to create consumer group in Send: %v", err)
		}
	}

	// Signal receiver to check for messages (non-blocking).
	select {
	case q.notifyCh <- struct{}{}:
	default:
		// Notification already pending, receiver will wake up anyway.
	}

	return nil
}

// Receive receives messages from the queue until the context is cancelled.
// Messages are sent to the provided channel, which is closed when Receive returns.
// The receiver uses the shared knownStreams map which is updated by Send() for
// immediate discovery of new streams created by in-process senders.
func (q *RedisFifoQueue) Receive(ctx context.Context, sinkCh chan<- *types.FifoQueueItem) error {
	defer close(sinkCh)

	if !q.initialized {
		return errors.New("redis FIFO queue not initialized")
	}

	// Refresh streams on startup (populates shared knownStreams).
	if err := q.refreshStreams(ctx); err != nil {
		return fmt.Errorf("failed to refresh streams on startup: %w", err)
	}

	streamRefreshTicker := time.NewTicker(q.opts.streamRefreshInterval)
	defer streamRefreshTicker.Stop()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-streamRefreshTicker.C:
			if err := q.refreshStreams(ctx); err != nil && !isCtxCanceledErr(err) {
				q.logger.Errorf("Failed to refresh streams: %v", err)
			}
		case <-q.notifyCh:
			// Notification received from in-process Send(), try to read immediately.
			if _, err := q.readMessagesWithLocking(ctx, sinkCh); err != nil && !isCtxCanceledErr(err) {
				q.logger.Errorf("Error reading messages: %v", err)
			}
		default:
			messagesRead, err := q.readMessagesWithLocking(ctx, sinkCh)
			if err != nil && !isCtxCanceledErr(err) {
				q.logger.Errorf("Error reading messages: %v", err)
			}

			if messagesRead {
				continue
			}

			// If no messages were read from any stream (or there was an error), wait briefly before trying again.
			// Also listen on notifyCh to wake up immediately when a message is sent in-process.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-q.notifyCh: // Woken up by in-process Send(), continue loop immediately.
			case <-time.After(q.opts.pollInterval):
			}
		}
	}
}

// refreshStreams updates the list of known streams from the index set.
// It also ensures the consumer group exists for each stream, and cleans up stale streams.
// This function uses the shared knownStreams map protected by knownStreamsMu.
func (q *RedisFifoQueue) refreshStreams(ctx context.Context) error {
	// Clean up stale streams first (if cleanup is enabled).
	// In case of error, log and continue. Cleanup is not critical for normal operation.
	staleStreams, err := q.cleanupStaleStreams(ctx)
	if err != nil {
		if isCtxCanceledErr(err) {
			return err
		}

		q.logger.Errorf("Failed to cleanup stale streams: %v", err)
	}

	// Remove stale streams from shared map.
	if len(staleStreams) > 0 {
		q.knownStreamsMu.Lock()

		for _, streamKey := range staleStreams {
			delete(q.knownStreams, streamKey)
		}

		q.knownStreamsMu.Unlock()
	}

	streams, err := q.client.SMembers(ctx, q.streamsIndexKey()).Result()
	if err != nil {
		return fmt.Errorf("failed to get streams from index: %w", err)
	}

	// Get snapshot of current known streams (read lock only).
	// This minimizes lock contention with Send() which needs a write lock.
	q.knownStreamsMu.RLock()

	currentKnown := make(map[string]bool, len(q.knownStreams))
	maps.Copy(currentKnown, q.knownStreams)

	q.knownStreamsMu.RUnlock()

	// Find streams that need consumer group creation (no lock held).
	var newStreams []string

	for _, streamKey := range streams {
		if !currentKnown[streamKey] {
			newStreams = append(newStreams, streamKey)
		}
	}

	// Create consumer groups without holding the lock.
	// This allows Send() to update knownStreams during potentially slow Redis operations.
	successfulStreams := make([]string, 0, len(newStreams))

	for _, streamKey := range newStreams {
		// Ensure consumer group exists for this stream.
		// Use MKSTREAM to create the stream if it doesn't exist.
		// Start from "0" to read all existing messages.
		err := q.client.XGroupCreateMkStream(ctx, streamKey, q.opts.consumerGroup, "0").Err()
		if err != nil {
			if isCtxCanceledErr(err) {
				return err
			}

			if !strings.Contains(err.Error(), "BUSYGROUP") {
				q.logger.WithField("stream_key", streamKey).Errorf("Failed to create consumer group in refreshStreams: %v", err)
				continue
			}
		}

		successfulStreams = append(successfulStreams, streamKey)

		q.logger.WithField("stream_key", streamKey).Info("Discovered new Redis stream")
	}

	// Update known streams map (brief write lock).
	if len(successfulStreams) > 0 {
		q.knownStreamsMu.Lock()

		for _, streamKey := range successfulStreams {
			q.knownStreams[streamKey] = true
		}

		q.knownStreamsMu.Unlock()
	}

	return nil
}

// sendMessageAtomic atomically adds a message to a stream, adds the stream to the index,
// and updates the activity timestamp. Returns the message ID.
// This ensures consistency: all three operations succeed together or none do.
func (p *RedisFifoQueueProducer) sendMessageAtomic(ctx context.Context, streamKey, channelID, body string) (string, error) {
	keys := []string{streamKey, p.streamsIndexKey(), p.streamActivityKey()}

	args := []any{
		p.opts.maxStreamLength,
		channelID,
		body,
		time.Now().Unix(),
	}

	result, err := sendMessageScript.Run(ctx, p.client, keys, args...).Text()
	if err != nil {
		return "", fmt.Errorf("failed to send message atomically: %w", err)
	}

	return result, nil
}

// cleanupStaleStreamAtomic atomically checks if a stream is still stale and deletes it.
// Returns true if the stream was deleted, false if it was updated since the stale check.
// This prevents the TOCTOU race where a message could be sent between checking staleness and deletion.
func (q *RedisFifoQueue) cleanupStaleStreamAtomic(ctx context.Context, streamKey string, cutoff int64) (bool, error) {
	keys := []string{streamKey, q.streamsIndexKey(), q.streamActivityKey()}
	args := []any{cutoff}

	result, err := cleanupStaleStreamScript.Run(ctx, q.client, keys, args...).Int()
	if err != nil {
		return false, err
	}

	return result == 1, nil
}

// cleanupStaleStreams removes streams that have been inactive for longer than streamInactivityTimeout.
// Returns the list of stream keys that were cleaned up.
func (q *RedisFifoQueue) cleanupStaleStreams(ctx context.Context) ([]string, error) {
	if q.opts.streamInactivityTimeout == 0 {
		return nil, nil
	}

	cutoff := time.Now().Add(-q.opts.streamInactivityTimeout).Unix()

	// Find streams with activity older than cutoff.
	opt := &redis.ZRangeBy{
		Min: "-inf",
		Max: fmt.Sprintf("%d", cutoff),
	}

	staleStreams, err := q.client.ZRangeByScore(ctx, q.streamActivityKey(), opt).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to find stale streams: %w", err)
	}

	if len(staleStreams) == 0 {
		return nil, nil
	}

	cleanedUp := make([]string, 0, len(staleStreams))

	for _, streamKey := range staleStreams {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		logger := q.logger.WithField("stream_key", streamKey)

		// Atomically check if stream is still stale and delete it.
		// This prevents the TOCTOU race: if Send() updated the activity timestamp
		// after ZRangeByScore but before deletion, the script will detect this
		// and skip the deletion.
		deleted, err := q.cleanupStaleStreamAtomic(ctx, streamKey, cutoff)
		if err != nil {
			if isCtxCanceledErr(err) {
				return nil, err
			}

			logger.Errorf("Failed to cleanup stale Redis stream: %v", err)

			continue
		}

		if deleted {
			cleanedUp = append(cleanedUp, streamKey)
			logger.Info("Cleaned up stale Redis stream")
		} else {
			logger.Debug("Redis stream was updated since stale check, skipping cleanup")
		}
	}

	return cleanedUp, nil
}

// readMessagesWithLocking reads messages from streams one at a time, acquiring a lock for each.
// This ensures strict per-channel ordering: while a message from a stream is being processed,
// no other consumer can read from that stream.
// Returns true if at least one message was read.
// Uses the shared knownStreams via getKnownStreams() for safe concurrent access.
func (q *RedisFifoQueue) readMessagesWithLocking(ctx context.Context, sinkCh chan<- *types.FifoQueueItem) (bool, error) {
	knownStreams := q.getKnownStreams()

	if len(knownStreams) == 0 {
		return false, nil
	}

	messagesRead := false

	for streamKey := range knownStreams {
		if err := ctx.Err(); err != nil {
			return false, err
		}

		channelID := q.channelIDFromStreamKey(streamKey)
		logger := q.logger.WithField("stream_key", streamKey).WithField("channel_id", channelID)

		// Try to acquire a lock for this stream/channel.
		// Use a short maxWait (0) to avoid blocking - if the lock is held, skip to the next stream.
		lock, err := q.locker.Obtain(ctx, q.lockKey(channelID), q.opts.lockTTL, 0)
		if err != nil {
			if isCtxCanceledErr(err) {
				return false, err
			}

			if errors.Is(err, ErrChannelLockUnavailable) {
				logger.Debug("Redis stream locked by another consumer, skipping")
			} else {
				logger.Errorf("Failed to obtain lock: %v", err)
			}

			continue
		}

		// We have the lock. Read ONE message from this stream.
		read, err := q.readOneMessageFromStream(ctx, streamKey, channelID, lock, sinkCh)
		if err != nil {
			// Release the lock on error.
			q.releaseLock(lock)
			return messagesRead, err
		}

		if read {
			messagesRead = true
			// Lock will be released by the ack/nack function.
		} else {
			// No message was available, release the lock.
			q.releaseLock(lock)
		}
	}

	return messagesRead, nil
}

// getKnownStreams returns a snapshot of known streams for safe iteration.
// This allows iteration without holding the lock for the entire duration.
func (q *RedisFifoQueue) getKnownStreams() map[string]bool {
	q.knownStreamsMu.RLock()
	defer q.knownStreamsMu.RUnlock()

	snapshot := make(map[string]bool, len(q.knownStreams))
	maps.Copy(snapshot, q.knownStreams)

	return snapshot
}

// readOneMessageFromStream reads a single message from the specified stream.
// It first checks for pending messages (from crashed consumers) to maintain strict ordering,
// then falls back to reading new messages if no pending messages exist.
// Returns true if a message was read.
// The lock is passed to the message's ack/nack functions so it can be released after processing.
func (q *RedisFifoQueue) readOneMessageFromStream(ctx context.Context, streamKey, channelID string, lock ChannelLock, sinkCh chan<- *types.FifoQueueItem) (bool, error) {
	// First, check for pending messages that need to be reclaimed.
	// This ensures strict ordering: pending messages must be processed before new ones.
	// If this fails, we must not read new messages as it could break ordering.
	claimed, err := q.tryClaimPendingMessage(ctx, streamKey)
	if err != nil {
		return false, fmt.Errorf("failed to check pending messages: %w", err)
	}

	if claimed != nil {
		q.logger.WithField("message_id", claimed.ID).WithField("stream_key", streamKey).Debug("Claimed pending message from Redis stream before reading new")

		if err := q.processMessageWithLock(ctx, streamKey, channelID, *claimed, lock, sinkCh); err != nil {
			return false, err
		}

		return true, nil
	}

	// No pending messages, read a new one.
	args := &redis.XReadGroupArgs{
		Group:    q.opts.consumerGroup,
		Consumer: q.consumerName,
		Streams:  []string{streamKey, ">"},
		Count:    1,  // Only read one message to maintain strict ordering.
		Block:    -1, // Negative value means don't block (Block >= 0 would send BLOCK to Redis)
	}

	results, err := q.client.XReadGroup(ctx, args).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return false, nil
		}

		return false, fmt.Errorf("failed to read from stream %s: %w", streamKey, err)
	}

	// Process the message (there should be at most one stream with at most one message).
	if len(results) > 0 && len(results[0].Messages) > 0 {
		msg := results[0].Messages[0]

		if err := q.processMessageWithLock(ctx, streamKey, channelID, msg, lock, sinkCh); err != nil {
			return false, err
		}

		return true, nil
	}

	return false, nil
}

// tryClaimPendingMessage attempts to claim a pending message from the stream.
// Returns nil if no pending messages are available or claimable.
func (q *RedisFifoQueue) tryClaimPendingMessage(ctx context.Context, streamKey string) (*redis.XMessage, error) {
	args := &redis.XAutoClaimArgs{
		Stream:   streamKey,
		Group:    q.opts.consumerGroup,
		Consumer: q.consumerName,
		MinIdle:  q.opts.claimMinIdleTime,
		Start:    "0-0",
		Count:    1,
	}

	// Use XAUTOCLAIM to atomically find and claim a pending message in a single round trip.
	claimed, _, err := q.client.XAutoClaim(ctx, args).Result()
	if err != nil {
		if strings.Contains(err.Error(), "NOGROUP") {
			return nil, nil
		}

		return nil, fmt.Errorf("failed to auto-claim pending message: %w", err)
	}

	if len(claimed) == 0 {
		return nil, nil
	}

	return &claimed[0], nil
}

// processMessageWithLock processes a single message and sends it to the sink channel.
// The lock is stored in the ack/nack closures so it can be released after processing.
func (q *RedisFifoQueue) processMessageWithLock(ctx context.Context, streamKey, channelID string, msg redis.XMessage, lock ChannelLock, sinkCh chan<- *types.FifoQueueItem) error {
	body, ok := msg.Values["body"].(string)
	if !ok {
		q.logger.WithField("message_id", msg.ID).Error("Message has no body, acknowledging and skipping")
		q.ackMessage(ctx, streamKey, msg.ID)
		q.releaseLock(lock)
		return nil
	}

	// Use channel ID from message if available, otherwise use the one extracted from stream key.
	if msgChannelID, ok := msg.Values["channel_id"].(string); ok && msgChannelID != "" {
		channelID = msgChannelID
	}

	// Create thread-safe ack/nack functions that also release the lock.
	// These functions use context.Background() with a timeout because acknowledgment
	// must complete regardless of the caller's context state.
	var once sync.Once

	ack := func() { //nolint:contextcheck // Deliberately uses context.Background() - ack must complete regardless of caller's context
		once.Do(func() {
			ackCtx, cancel := context.WithTimeout(context.Background(), ackNackTimeout)
			defer cancel()
			q.ackMessage(ackCtx, streamKey, msg.ID)
			q.releaseLock(lock)
		})
	}

	nack := func() {
		once.Do(func() {
			// For Redis Streams, nack means we don't ack.
			// The message remains pending and will be reclaimed after claimMinIdleTime.
			// We do however need to release the channel lock.
			q.logger.WithField("message_id", msg.ID).WithField("stream_key", streamKey).Debug("Redis stream message nacked, will be reclaimed")
			q.releaseLock(lock)
		})
	}

	item := &types.FifoQueueItem{
		MessageID:        msg.ID,
		SlackChannelID:   channelID,
		ReceiveTimestamp: time.Now(),
		Body:             body,
		Ack:              ack,
		Nack:             nack,
	}

	// Send to the sink channel. Note: We intentionally hold the channel lock while
	// sending to sinkCh. If sinkCh is full, we block here with the lock held.
	// This is by design to ensure strict per-channel ordering: while we're waiting
	// to deliver this message, no other consumer can read the next message from
	// this channel. This prevents out-of-order processing when the downstream
	// consumer is slow. The lock timeout (opts.lockTTL) provides an upper bound
	// on how long we can block.
	select {
	case sinkCh <- item:
		return nil
	case <-ctx.Done():
		// Context cancelled before we could send. Release lock and return error.
		q.releaseLock(lock)
		return ctx.Err()
	}
}

// releaseLock releases a channel lock.
func (q *RedisFifoQueue) releaseLock(lock ChannelLock) {
	if lock == nil {
		return
	}

	key := lock.Key()

	if err := lock.Release(); err != nil {
		q.logger.WithField("lock_key", key).Errorf("Failed to release lock: %v", err)
	}
}

// ackMessage acknowledges a message, removing it from the pending list.
// This method should always be called with context.Background() or a non-cancellable context
// since acknowledgment is a commitment that must complete.
func (q *RedisFifoQueue) ackMessage(ctx context.Context, streamKey, messageID string) {
	logger := q.logger.WithField("message_id", messageID).WithField("stream_key", streamKey)

	if err := q.client.XAck(ctx, streamKey, q.opts.consumerGroup, messageID).Err(); err != nil {
		logger.Errorf("Failed to acknowledge message: %v", err)
		return
	}

	logger.Debug("Redis stream message acknowledged")
}

// streamKey returns the Redis key for a channel's stream.
// The queue name is included to prevent collisions between different queue instances.
func (p *RedisFifoQueueProducer) streamKey(slackChannelID string) string {
	return fmt.Sprintf("%s:%s:stream:%s", p.opts.keyPrefix, p.name, slackChannelID)
}

// streamsIndexKey returns the Redis key for the streams index set.
// The queue name is included to prevent collisions between different queue instances.
func (p *RedisFifoQueueProducer) streamsIndexKey() string {
	return fmt.Sprintf("%s:%s:streams", p.opts.keyPrefix, p.name)
}

// streamActivityKey returns the Redis key for the sorted set tracking stream activity.
// The queue name is included to prevent collisions between different queue instances.
func (p *RedisFifoQueueProducer) streamActivityKey() string {
	return fmt.Sprintf("%s:%s:stream-activity", p.opts.keyPrefix, p.name)
}

// lockKey returns the key used for locking a channel's stream.
// The queue name is included to prevent collisions between different queue instances.
func (q *RedisFifoQueue) lockKey(channelID string) string {
	return fmt.Sprintf("%s:%s:lock:%s", q.opts.keyPrefix, q.name, channelID)
}

// channelIDFromStreamKey extracts the channel ID from a stream key.
func (q *RedisFifoQueue) channelIDFromStreamKey(streamKey string) string {
	prefix := fmt.Sprintf("%s:%s:stream:", q.opts.keyPrefix, q.name)
	return strings.TrimPrefix(streamKey, prefix)
}
