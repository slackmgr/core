package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	common "github.com/peteraglen/slack-manager-common"
	redis "github.com/redis/go-redis/v9"
	"github.com/segmentio/ksuid"
)

// ackNackTimeout is the timeout for ack/nack operations.
// These operations use context.Background() because they must complete regardless
// of the caller's context state - acknowledgment is a commitment, not a cancellable request.
// This should be shorter than the drain timeouts (default 3-5s) since these are simple Redis operations.
const ackNackTimeout = 2 * time.Second

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
type RedisFifoQueue struct {
	client       redis.UniversalClient
	locker       ChannelLocker
	name         string
	consumerName string
	opts         *RedisFifoQueueOptions
	logger       common.Logger
	initialized  bool
}

// NewRedisFifoQueue creates a new RedisFifoQueue instance.
// The client should be a configured Redis client (can be a single node, sentinel, or cluster client).
// The locker is used to ensure strict per-channel ordering across multiple instances.
// The name is used as part of the Redis key prefix and for identification.
func NewRedisFifoQueue(client redis.UniversalClient, locker ChannelLocker, name string, logger common.Logger, opts ...RedisFifoQueueOption) *RedisFifoQueue {
	options := newRedisFifoQueueOptions()

	for _, o := range opts {
		o(options)
	}

	logger = logger.
		WithField("component", "redis-fifo-queue").
		WithField("queue_name", name)

	return &RedisFifoQueue{
		client: client,
		locker: locker,
		name:   name,
		opts:   options,
		logger: logger,
	}
}

// Init initializes the RedisFifoQueue.
// It validates options and generates a unique consumer name for this instance.
func (q *RedisFifoQueue) Init() (*RedisFifoQueue, error) {
	if q.initialized {
		return q, nil
	}

	if q.name == "" {
		return nil, errors.New("queue name cannot be empty")
	}

	if strings.ContainsAny(q.name, ": \t\n") {
		return nil, errors.New("queue name cannot contain colons, spaces, or whitespace")
	}

	if q.locker == nil {
		return nil, errors.New("locker cannot be nil")
	}

	if err := q.opts.validate(); err != nil {
		return nil, fmt.Errorf("invalid Redis FIFO queue options: %w", err)
	}

	// Generate a unique consumer name for this instance.
	// This allows multiple instances to consume from the same consumer group.
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}

	q.consumerName = fmt.Sprintf("%s-%s", hostname, ksuid.New().String())
	q.logger = q.logger.WithField("consumer_name", q.consumerName)

	q.initialized = true

	return q, nil
}

// Name returns the name of the queue.
func (q *RedisFifoQueue) Name() string {
	return q.name
}

// Send sends a message to the queue.
// The slackChannelID is used to route the message to a specific stream, ensuring ordering per channel.
// Note: The dedupID parameter is accepted for interface compatibility but is not used for deduplication.
// Redis Streams does not natively support message deduplication like SQS FIFO queues.
// Deduplication should be handled at the application level if needed.
func (q *RedisFifoQueue) Send(ctx context.Context, slackChannelID, dedupID, body string) error {
	_ = dedupID // Unused - Redis Streams does not support native deduplication.

	if !q.initialized {
		return errors.New("redis FIFO queue not initialized")
	}

	if slackChannelID == "" {
		return errors.New("slackChannelID cannot be empty")
	}

	if body == "" {
		return errors.New("body cannot be empty")
	}

	streamKey := q.streamKey(slackChannelID)

	// Add message to the stream.
	// MAXLEN ~ provides approximate trimming for memory management.
	args := &redis.XAddArgs{
		Stream: streamKey,
		MaxLen: q.opts.maxStreamLength,
		Approx: true,
		Values: map[string]any{
			"channel_id": slackChannelID,
			"body":       body,
			"timestamp":  time.Now().UnixMilli(),
		},
	}

	messageID, err := q.client.XAdd(ctx, args).Result()
	if err != nil {
		return fmt.Errorf("failed to add message to Redis stream %s: %w", streamKey, err)
	}

	// Track this stream in the index set for discovery.
	if err := q.client.SAdd(ctx, q.streamsIndexKey(), streamKey).Err(); err != nil {
		// Log but don't fail - the message was added successfully.
		q.logger.WithField("stream_key", streamKey).Errorf("Failed to add stream to index: %v", err)
	}

	// Record activity timestamp for this stream (used for stale stream cleanup).
	if err := q.client.ZAdd(ctx, q.streamActivityKey(), redis.Z{
		Score:  float64(time.Now().Unix()),
		Member: streamKey,
	}).Err(); err != nil {
		// Log but don't fail - the message was added successfully.
		q.logger.WithField("stream_key", streamKey).Errorf("Failed to record stream activity: %v", err)
	}

	q.logger.WithField("message_id", messageID).WithField("channel_id", slackChannelID).Debug("Message sent to Redis stream")

	return nil
}

// Receive receives messages from the queue until the context is cancelled.
// Messages are sent to the provided channel, which is closed when Receive returns.
func (q *RedisFifoQueue) Receive(ctx context.Context, sinkCh chan<- *common.FifoQueueItem) error {
	defer close(sinkCh)

	if !q.initialized {
		return errors.New("redis FIFO queue not initialized")
	}

	// Track known streams and their state.
	knownStreams := make(map[string]bool)

	// Refresh streams on startup.
	if err := q.refreshStreams(ctx, knownStreams); err != nil {
		return fmt.Errorf("failed to refresh streams on startup: %w", err)
	}

	streamRefreshTicker := time.NewTicker(q.opts.streamRefreshInterval)
	defer streamRefreshTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-streamRefreshTicker.C:
			if err := q.refreshStreams(ctx, knownStreams); err != nil {
				q.logger.Errorf("Failed to refresh streams: %v", err)
			}
		default:
			messagesRead, err := q.readMessagesWithLocking(ctx, knownStreams, sinkCh)
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return ctx.Err()
				}

				q.logger.Errorf("Error reading messages: %v", err)
			}

			// If no messages were read from any stream (or there was an error), wait briefly before trying again.
			if !messagesRead {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(q.opts.pollInterval):
				}
			}
		}
	}
}

// refreshStreams updates the list of known streams from the index set.
// It also ensures the consumer group exists for each stream, and cleans up stale streams.
func (q *RedisFifoQueue) refreshStreams(ctx context.Context, knownStreams map[string]bool) error {
	// Clean up stale streams first (if cleanup is enabled).
	staleStreams, err := q.cleanupStaleStreams(ctx)
	if err != nil {
		q.logger.Errorf("Failed to cleanup stale streams: %v", err)
		// Continue anyway - cleanup failure shouldn't prevent normal operation.
	}

	// Remove stale streams from local map.
	for _, streamKey := range staleStreams {
		delete(knownStreams, streamKey)
	}

	streams, err := q.client.SMembers(ctx, q.streamsIndexKey()).Result()
	if err != nil {
		return fmt.Errorf("failed to get streams from index: %w", err)
	}

	for _, streamKey := range streams {
		if knownStreams[streamKey] {
			continue
		}

		// Ensure consumer group exists for this stream.
		// Use MKSTREAM to create the stream if it doesn't exist.
		// Start from "0" to read all existing messages.
		err := q.client.XGroupCreateMkStream(ctx, streamKey, q.opts.consumerGroup, "0").Err()
		if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
			q.logger.WithField("stream_key", streamKey).Errorf("Failed to create consumer group: %v", err)
			continue
		}

		knownStreams[streamKey] = true
		q.logger.WithField("stream_key", streamKey).Debug("Discovered new stream")
	}

	return nil
}

// cleanupStaleStreams removes streams that have been inactive for longer than streamInactivityTimeout.
// Returns the list of stream keys that were cleaned up.
func (q *RedisFifoQueue) cleanupStaleStreams(ctx context.Context) ([]string, error) {
	if q.opts.streamInactivityTimeout == 0 {
		// Cleanup is disabled.
		return nil, nil
	}

	cutoff := time.Now().Add(-q.opts.streamInactivityTimeout).Unix()

	// Find streams with activity older than cutoff.
	staleStreams, err := q.client.ZRangeByScore(ctx, q.streamActivityKey(), &redis.ZRangeBy{
		Min: "-inf",
		Max: fmt.Sprintf("%d", cutoff),
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to find stale streams: %w", err)
	}

	if len(staleStreams) == 0 {
		return nil, nil
	}

	cleanedUp := make([]string, 0, len(staleStreams))

	for _, streamKey := range staleStreams {
		logger := q.logger.WithField("stream_key", streamKey)

		// Delete the stream from Redis.
		if err := q.client.Del(ctx, streamKey).Err(); err != nil {
			logger.Errorf("Failed to delete stale stream: %v", err)
			continue
		}

		// Remove from the streams index set.
		if err := q.client.SRem(ctx, q.streamsIndexKey(), streamKey).Err(); err != nil {
			logger.Errorf("Failed to remove stale stream from index: %v", err)
		}

		// Remove from the activity sorted set.
		if err := q.client.ZRem(ctx, q.streamActivityKey(), streamKey).Err(); err != nil {
			logger.Errorf("Failed to remove stale stream from activity set: %v", err)
		}

		cleanedUp = append(cleanedUp, streamKey)
		logger.Info("Cleaned up stale stream")
	}

	return cleanedUp, nil
}

// readMessagesWithLocking reads messages from streams one at a time, acquiring a lock for each.
// This ensures strict per-channel ordering: while a message from a stream is being processed,
// no other consumer can read from that stream.
// Returns true if at least one message was read.
func (q *RedisFifoQueue) readMessagesWithLocking(ctx context.Context, knownStreams map[string]bool, sinkCh chan<- *common.FifoQueueItem) (bool, error) {
	if len(knownStreams) == 0 {
		return false, nil
	}

	messagesRead := false

	for streamKey := range knownStreams {
		// Check for context cancellation at the start of each iteration.
		select {
		case <-ctx.Done():
			return messagesRead, ctx.Err()
		default:
		}

		channelID := q.channelIDFromStreamKey(streamKey)
		logger := q.logger.WithField("stream_key", streamKey).WithField("channel_id", channelID)

		// Try to acquire a lock for this stream/channel.
		// Use a short maxWait (0) to avoid blocking - if the lock is held, skip to the next stream.
		lock, err := q.locker.Obtain(ctx, q.lockKey(channelID), q.opts.lockTTL, 0)
		if err != nil {
			if errors.Is(err, ErrChannelLockUnavailable) {
				// Another consumer is processing this stream, skip it.
				logger.Debug("Stream locked by another consumer, skipping")
				continue
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return messagesRead, ctx.Err()
			}
			// Unexpected error obtaining lock.
			logger.Errorf("Failed to obtain lock: %v", err)
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

// readOneMessageFromStream reads a single message from the specified stream.
// It first checks for pending messages (from crashed consumers) to maintain strict ordering,
// then falls back to reading new messages if no pending messages exist.
// Returns true if a message was read.
// The lock is passed to the message's ack/nack functions so it can be released after processing.
func (q *RedisFifoQueue) readOneMessageFromStream(ctx context.Context, streamKey, channelID string, lock ChannelLock, sinkCh chan<- *common.FifoQueueItem) (bool, error) {
	// First, check for pending messages that need to be reclaimed.
	// This ensures strict ordering: pending messages must be processed before new ones.
	// If this fails, we must not read new messages as it could break ordering.
	claimed, err := q.tryClaimPendingMessage(ctx, streamKey)
	if err != nil {
		return false, fmt.Errorf("failed to check pending messages: %w", err)
	}

	if claimed != nil {
		q.logger.WithField("message_id", claimed.ID).WithField("stream_key", streamKey).Debug("Claimed pending message before reading new")
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
			// No messages available in this stream.
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
	// Use XAUTOCLAIM to atomically find and claim a pending message in a single round trip.
	claimed, _, err := q.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   streamKey,
		Group:    q.opts.consumerGroup,
		Consumer: q.consumerName,
		MinIdle:  q.opts.claimMinIdleTime,
		Start:    "0-0",
		Count:    1,
	}).Result()
	if err != nil {
		if strings.Contains(err.Error(), "NOGROUP") {
			// Group doesn't exist yet, that's fine.
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
func (q *RedisFifoQueue) processMessageWithLock(ctx context.Context, streamKey, channelID string, msg redis.XMessage, lock ChannelLock, sinkCh chan<- *common.FifoQueueItem) error {
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
			ctx, cancel := context.WithTimeout(context.Background(), ackNackTimeout)
			defer cancel()
			q.ackMessage(ctx, streamKey, msg.ID)
			q.releaseLock(lock)
		})
	}

	nack := func() {
		once.Do(func() {
			// For Redis Streams, nack means we don't ack.
			// The message remains pending and will be reclaimed after claimMinIdleTime.
			// We do however need to release the channel lock.
			q.logger.WithField("message_id", msg.ID).WithField("stream_key", streamKey).Debug("Message nacked, will be reclaimed")
			q.releaseLock(lock)
		})
	}

	item := &common.FifoQueueItem{
		MessageID:        msg.ID,
		SlackChannelID:   channelID,
		ReceiveTimestamp: time.Now(),
		Body:             body,
		Ack:              ack,
		Nack:             nack,
	}

	select {
	case sinkCh <- item:
		q.logger.WithField("message_id", msg.ID).WithField("channel_id", channelID).Debug("Message received from Redis stream")
	case <-ctx.Done():
		// Context cancelled before we could send. Release lock and return error.
		q.releaseLock(lock)
		return ctx.Err()
	}

	return nil
}

// releaseLock releases a channel lock.
func (q *RedisFifoQueue) releaseLock(lock ChannelLock) {
	if lock == nil {
		return
	}

	key := lock.Key()

	if err := lock.Release(); err != nil {
		q.logger.WithField("lock_key", key).Errorf("Failed to release lock: %v", err)
	} else {
		q.logger.WithField("lock_key", key).Debug("Lock released")
	}
}

// ackMessage acknowledges a message, removing it from the pending list.
func (q *RedisFifoQueue) ackMessage(ctx context.Context, streamKey, messageID string) {
	logger := q.logger.WithField("message_id", messageID).WithField("stream_key", streamKey)

	if err := q.client.XAck(ctx, streamKey, q.opts.consumerGroup, messageID).Err(); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logger.Errorf("Failed to acknowledge message: %v", err)
		}
		return
	}

	logger.Debug("Message acknowledged")
}

// streamKey returns the Redis key for a channel's stream.
// The queue name is included to prevent collisions between different queue instances.
func (q *RedisFifoQueue) streamKey(slackChannelID string) string {
	return fmt.Sprintf("%s:%s:stream:%s", q.opts.keyPrefix, q.name, slackChannelID)
}

// streamsIndexKey returns the Redis key for the streams index set.
// The queue name is included to prevent collisions between different queue instances.
func (q *RedisFifoQueue) streamsIndexKey() string {
	return fmt.Sprintf("%s:%s:streams", q.opts.keyPrefix, q.name)
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

// streamActivityKey returns the Redis key for the sorted set tracking stream activity.
// The queue name is included to prevent collisions between different queue instances.
func (q *RedisFifoQueue) streamActivityKey() string {
	return fmt.Sprintf("%s:%s:stream-activity", q.opts.keyPrefix, q.name)
}
