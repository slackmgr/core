package manager

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// mockLogger is a simple mock implementation of common.Logger for testing.
type mockLogger struct {
	mock.Mock
}

func (m *mockLogger) Debug(_ string)            {}
func (m *mockLogger) Debugf(_ string, _ ...any) {}
func (m *mockLogger) Info(_ string)             {}
func (m *mockLogger) Infof(_ string, _ ...any)  {}
func (m *mockLogger) Error(_ string)            {}
func (m *mockLogger) Errorf(_ string, _ ...any) {}

//nolint:ireturn // mock implementation returns interface
func (m *mockLogger) WithField(_ string, _ any) common.Logger {
	return m
}

//nolint:ireturn // mock implementation returns interface
func (m *mockLogger) WithFields(_ map[string]any) common.Logger {
	return m
}

func (m *mockLogger) HttpLoggingHandler() io.Writer {
	return nil
}

// mockRedisClient is a mock implementation of redis.UniversalClient for testing.
type mockRedisClient struct {
	mock.Mock
	redis.UniversalClient
}

func (m *mockRedisClient) XAdd(ctx context.Context, a *redis.XAddArgs) *redis.StringCmd {
	args := m.Called(ctx, a)
	cmd := redis.NewStringCmd(ctx)
	if result, ok := args.Get(0).(string); ok {
		cmd.SetVal(result)
	}
	if err := args.Error(1); err != nil {
		cmd.SetErr(err)
	}
	return cmd
}

func (m *mockRedisClient) SAdd(ctx context.Context, key string, members ...any) *redis.IntCmd {
	args := m.Called(ctx, key, members)
	cmd := redis.NewIntCmd(ctx)
	if result, ok := args.Get(0).(int64); ok {
		cmd.SetVal(result)
	}
	if err := args.Error(1); err != nil {
		cmd.SetErr(err)
	}
	return cmd
}

func (m *mockRedisClient) SMembers(ctx context.Context, key string) *redis.StringSliceCmd {
	args := m.Called(ctx, key)
	cmd := redis.NewStringSliceCmd(ctx)
	if result, ok := args.Get(0).([]string); ok {
		cmd.SetVal(result)
	}
	if err := args.Error(1); err != nil {
		cmd.SetErr(err)
	}
	return cmd
}

func (m *mockRedisClient) XGroupCreateMkStream(ctx context.Context, stream, group, start string) *redis.StatusCmd {
	args := m.Called(ctx, stream, group, start)
	cmd := redis.NewStatusCmd(ctx)
	if result, ok := args.Get(0).(string); ok {
		cmd.SetVal(result)
	}
	if err := args.Error(1); err != nil {
		cmd.SetErr(err)
	}
	return cmd
}

func (m *mockRedisClient) XReadGroup(ctx context.Context, a *redis.XReadGroupArgs) *redis.XStreamSliceCmd {
	args := m.Called(ctx, a)
	cmd := redis.NewXStreamSliceCmd(ctx)
	if result, ok := args.Get(0).([]redis.XStream); ok {
		cmd.SetVal(result)
	}
	if err := args.Error(1); err != nil {
		cmd.SetErr(err)
	}
	return cmd
}

func (m *mockRedisClient) XAck(ctx context.Context, stream, group string, ids ...string) *redis.IntCmd {
	args := m.Called(ctx, stream, group, ids)
	cmd := redis.NewIntCmd(ctx)
	if result, ok := args.Get(0).(int64); ok {
		cmd.SetVal(result)
	}
	if err := args.Error(1); err != nil {
		cmd.SetErr(err)
	}
	return cmd
}

func (m *mockRedisClient) XPendingExt(ctx context.Context, a *redis.XPendingExtArgs) *redis.XPendingExtCmd {
	args := m.Called(ctx, a)
	cmd := redis.NewXPendingExtCmd(ctx)
	if result, ok := args.Get(0).([]redis.XPendingExt); ok {
		cmd.SetVal(result)
	}
	if err := args.Error(1); err != nil {
		cmd.SetErr(err)
	}
	return cmd
}

func (m *mockRedisClient) XClaim(ctx context.Context, a *redis.XClaimArgs) *redis.XMessageSliceCmd {
	args := m.Called(ctx, a)
	cmd := redis.NewXMessageSliceCmd(ctx)
	if result, ok := args.Get(0).([]redis.XMessage); ok {
		cmd.SetVal(result)
	}
	if err := args.Error(1); err != nil {
		cmd.SetErr(err)
	}
	return cmd
}

func (m *mockRedisClient) ZAdd(ctx context.Context, key string, members ...redis.Z) *redis.IntCmd {
	args := m.Called(ctx, key, members)
	cmd := redis.NewIntCmd(ctx)
	if result, ok := args.Get(0).(int64); ok {
		cmd.SetVal(result)
	}
	if err := args.Error(1); err != nil {
		cmd.SetErr(err)
	}
	return cmd
}

func (m *mockRedisClient) ZRangeByScore(ctx context.Context, key string, opt *redis.ZRangeBy) *redis.StringSliceCmd {
	args := m.Called(ctx, key, opt)
	cmd := redis.NewStringSliceCmd(ctx)
	if result, ok := args.Get(0).([]string); ok {
		cmd.SetVal(result)
	}
	if err := args.Error(1); err != nil {
		cmd.SetErr(err)
	}
	return cmd
}

func (m *mockRedisClient) Del(ctx context.Context, keys ...string) *redis.IntCmd {
	args := m.Called(ctx, keys)
	cmd := redis.NewIntCmd(ctx)
	if result, ok := args.Get(0).(int64); ok {
		cmd.SetVal(result)
	}
	if err := args.Error(1); err != nil {
		cmd.SetErr(err)
	}
	return cmd
}

func (m *mockRedisClient) SRem(ctx context.Context, key string, members ...any) *redis.IntCmd {
	args := m.Called(ctx, key, members)
	cmd := redis.NewIntCmd(ctx)
	if result, ok := args.Get(0).(int64); ok {
		cmd.SetVal(result)
	}
	if err := args.Error(1); err != nil {
		cmd.SetErr(err)
	}
	return cmd
}

func (m *mockRedisClient) ZRem(ctx context.Context, key string, members ...any) *redis.IntCmd {
	args := m.Called(ctx, key, members)
	cmd := redis.NewIntCmd(ctx)
	if result, ok := args.Get(0).(int64); ok {
		cmd.SetVal(result)
	}
	if err := args.Error(1); err != nil {
		cmd.SetErr(err)
	}
	return cmd
}

func (m *mockRedisClient) XAutoClaim(ctx context.Context, a *redis.XAutoClaimArgs) *redis.XAutoClaimCmd {
	args := m.Called(ctx, a)
	cmd := redis.NewXAutoClaimCmd(ctx)
	if result, ok := args.Get(0).([]redis.XMessage); ok {
		cmd.SetVal(result, args.String(1))
	}
	if err := args.Error(2); err != nil {
		cmd.SetErr(err)
	}
	return cmd
}

// mockChannelLocker is a mock implementation of ChannelLocker for testing.
type mockChannelLocker struct {
	mock.Mock
}

//nolint:ireturn // mock implementation returns interface
func (m *mockChannelLocker) Obtain(ctx context.Context, key string, ttl, maxWait time.Duration) (ChannelLock, error) {
	args := m.Called(ctx, key, ttl, maxWait)
	if lock, ok := args.Get(0).(ChannelLock); ok {
		return lock, args.Error(1)
	}
	return nil, args.Error(1)
}

// mockChannelLock is a mock implementation of ChannelLock for testing.
type mockChannelLock struct {
	mock.Mock

	key string
}

func (m *mockChannelLock) Key() string {
	return m.key
}

func (m *mockChannelLock) Release(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func TestNewRedisFifoQueueOptions(t *testing.T) {
	t.Parallel()

	opts := newRedisFifoQueueOptions()

	assert.Equal(t, "slack-manager:queue", opts.keyPrefix)
	assert.Equal(t, "slack-manager", opts.consumerGroup)
	assert.Equal(t, 5*time.Second, opts.pollInterval)
	assert.Equal(t, int64(10000), opts.maxStreamLength)
	assert.Equal(t, 30*time.Second, opts.streamRefreshInterval)
	assert.Equal(t, 120*time.Second, opts.claimMinIdleTime)
	assert.Equal(t, 140*time.Second, opts.lockTTL)
	assert.Equal(t, 48*time.Hour, opts.streamInactivityTimeout)
}

func TestRedisFifoQueueOptions_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		modify      func(*RedisFifoQueueOptions)
		expectError string
	}{
		{
			name:        "valid default options",
			modify:      func(_ *RedisFifoQueueOptions) {},
			expectError: "",
		},
		{
			name: "empty key prefix",
			modify: func(o *RedisFifoQueueOptions) {
				o.keyPrefix = ""
			},
			expectError: "key prefix cannot be empty",
		},
		{
			name: "lock TTL too short",
			modify: func(o *RedisFifoQueueOptions) {
				o.lockTTL = 10 * time.Second
			},
			expectError: "lock TTL must be between 30 seconds and 30 minutes",
		},
		{
			name: "lock TTL too long",
			modify: func(o *RedisFifoQueueOptions) {
				o.lockTTL = time.Hour
			},
			expectError: "lock TTL must be between 30 seconds and 30 minutes",
		},
		{
			name: "claimMinIdleTime equals lockTTL",
			modify: func(o *RedisFifoQueueOptions) {
				o.claimMinIdleTime = 5 * time.Minute
				o.lockTTL = 5 * time.Minute
			},
			expectError: "claim min idle time must be less than lock TTL to ensure strict ordering",
		},
		{
			name: "claimMinIdleTime greater than lockTTL",
			modify: func(o *RedisFifoQueueOptions) {
				o.claimMinIdleTime = 6 * time.Minute
				o.lockTTL = 5 * time.Minute
			},
			expectError: "claim min idle time must be less than lock TTL to ensure strict ordering",
		},
		{
			name: "streamInactivityTimeout too short",
			modify: func(o *RedisFifoQueueOptions) {
				o.streamInactivityTimeout = 30 * time.Minute
			},
			expectError: "stream inactivity timeout must be at least 1 hour (or 0 to disable)",
		},
		{
			name: "streamInactivityTimeout disabled (0)",
			modify: func(o *RedisFifoQueueOptions) {
				o.streamInactivityTimeout = 0
			},
			expectError: "",
		},
		{
			name: "streamInactivityTimeout valid custom value",
			modify: func(o *RedisFifoQueueOptions) {
				o.streamInactivityTimeout = 24 * time.Hour
			},
			expectError: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := newRedisFifoQueueOptions()
			tt.modify(opts)

			err := opts.validate()

			if tt.expectError == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Equal(t, tt.expectError, err.Error())
			}
		})
	}
}

func TestRedisFifoQueueOptionFunctions(t *testing.T) {
	t.Parallel()

	t.Run("WithLockTTL", func(t *testing.T) {
		t.Parallel()
		opts := newRedisFifoQueueOptions()
		WithLockTTL(10 * time.Minute)(opts)
		assert.Equal(t, 10*time.Minute, opts.lockTTL)
	})

	t.Run("WithStreamInactivityTimeout", func(t *testing.T) {
		t.Parallel()
		opts := newRedisFifoQueueOptions()
		WithStreamInactivityTimeout(72 * time.Hour)(opts)
		assert.Equal(t, 72*time.Hour, opts.streamInactivityTimeout)
	})

	t.Run("WithStreamInactivityTimeout disabled", func(t *testing.T) {
		t.Parallel()
		opts := newRedisFifoQueueOptions()
		WithStreamInactivityTimeout(0)(opts)
		assert.Equal(t, time.Duration(0), opts.streamInactivityTimeout)
	})
}

func TestNewRedisFifoQueue(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)

	assert.NotNil(t, queue)
	assert.Equal(t, "test-queue", queue.Name())
	assert.False(t, queue.initialized)
	assert.Equal(t, "slack-manager:queue", queue.opts.keyPrefix)
}

func TestRedisFifoQueue_Init(t *testing.T) {
	t.Parallel()

	t.Run("successful initialization", func(t *testing.T) {
		t.Parallel()

		mockClient := &mockRedisClient{}
		mockLocker := &mockChannelLocker{}
		logger := &mockLogger{}

		queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
		result, err := queue.Init()

		require.NoError(t, err)
		assert.Equal(t, queue, result)
		assert.True(t, queue.initialized)
		assert.NotEmpty(t, queue.consumerName)
	})

	t.Run("initialization with nil locker fails", func(t *testing.T) {
		t.Parallel()

		mockClient := &mockRedisClient{}
		logger := &mockLogger{}

		queue := NewRedisFifoQueue(mockClient, nil, "test-queue", logger)
		_, err := queue.Init()

		require.Error(t, err)
		assert.Equal(t, "locker cannot be nil", err.Error())
	})

	t.Run("initialization with empty name fails", func(t *testing.T) {
		t.Parallel()

		mockClient := &mockRedisClient{}
		mockLocker := &mockChannelLocker{}
		logger := &mockLogger{}

		queue := NewRedisFifoQueue(mockClient, mockLocker, "", logger)
		_, err := queue.Init()

		require.Error(t, err)
		assert.Equal(t, "queue name cannot be empty", err.Error())
	})

	t.Run("initialization with invalid name containing colon fails", func(t *testing.T) {
		t.Parallel()

		mockClient := &mockRedisClient{}
		mockLocker := &mockChannelLocker{}
		logger := &mockLogger{}

		queue := NewRedisFifoQueue(mockClient, mockLocker, "test:queue", logger)
		_, err := queue.Init()

		require.Error(t, err)
		assert.Equal(t, "queue name cannot contain colons, spaces, or whitespace", err.Error())
	})

	t.Run("initialization with invalid name containing space fails", func(t *testing.T) {
		t.Parallel()

		mockClient := &mockRedisClient{}
		mockLocker := &mockChannelLocker{}
		logger := &mockLogger{}

		queue := NewRedisFifoQueue(mockClient, mockLocker, "test queue", logger)
		_, err := queue.Init()

		require.Error(t, err)
		assert.Equal(t, "queue name cannot contain colons, spaces, or whitespace", err.Error())
	})

	t.Run("initialization with invalid options fails", func(t *testing.T) {
		t.Parallel()

		mockClient := &mockRedisClient{}
		mockLocker := &mockChannelLocker{}
		logger := &mockLogger{}

		queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger,
			WithKeyPrefix(""),
		)
		_, err := queue.Init()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "key prefix cannot be empty")
	})
}

func TestRedisFifoQueue_LockKey(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger,
		WithKeyPrefix("test-prefix"),
	)

	key := queue.lockKey("C12345")
	assert.Equal(t, "test-prefix:test-queue:lock:C12345", key)
}

func TestRedisFifoQueue_Send_Success(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	mockClient.On("XAdd", mock.Anything, mock.MatchedBy(func(args *redis.XAddArgs) bool {
		values, ok := args.Values.(map[string]any)
		if !ok {
			return false
		}
		return args.Stream == "slack-manager:queue:test-queue:stream:C12345" &&
			values["channel_id"] == "C12345" &&
			values["body"] == `{"test": "body"}`
	})).Return("1234567890-0", nil)

	mockClient.On("SAdd", mock.Anything, "slack-manager:queue:test-queue:streams", mock.Anything).Return(int64(1), nil)
	mockClient.On("ZAdd", mock.Anything, "slack-manager:queue:test-queue:stream-activity", mock.Anything).Return(int64(1), nil)

	err = queue.Send(context.Background(), "C12345", "dedup-1", `{"test": "body"}`)

	require.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestRedisFifoQueue_ProcessMessageWithLock(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	mockLock := &mockChannelLock{key: "test-lock"}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	msg := redis.XMessage{
		ID: "1234567890-0",
		Values: map[string]any{
			"channel_id": "C12345",
			"dedup_id":   "dedup-1",
			"body":       `{"test": "body"}`,
		},
	}

	sinkCh := make(chan *common.FifoQueueItem, 10)
	ctx := context.Background()

	err = queue.processMessageWithLock(ctx, "slack-manager:queue:test-queue:stream:C12345", "C12345", msg, mockLock, sinkCh)
	require.NoError(t, err)

	require.Len(t, sinkCh, 1)

	item := <-sinkCh
	assert.Equal(t, "1234567890-0", item.MessageID)
	assert.Equal(t, "C12345", item.SlackChannelID)
	assert.JSONEq(t, `{"test": "body"}`, item.Body)
	assert.NotNil(t, item.Ack)
	assert.NotNil(t, item.Nack)
}

func TestRedisFifoQueue_ProcessMessageWithLock_MissingBody(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	mockLock := &mockChannelLock{key: "test-lock"}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	mockClient.On("XAck", mock.Anything, "slack-manager:queue:test-queue:stream:C12345", "slack-manager", []string{"1234567890-0"}).Return(int64(1), nil)
	mockLock.On("Release", mock.Anything).Return(nil)

	msg := redis.XMessage{
		ID: "1234567890-0",
		Values: map[string]any{
			"channel_id": "C12345",
		},
	}

	sinkCh := make(chan *common.FifoQueueItem, 10)
	ctx := context.Background()

	err = queue.processMessageWithLock(ctx, "slack-manager:queue:test-queue:stream:C12345", "C12345", msg, mockLock, sinkCh)
	require.NoError(t, err)

	assert.Empty(t, sinkCh)
	mockClient.AssertExpectations(t)
	mockLock.AssertExpectations(t)
}

func TestRedisFifoQueue_AckReleasesLock(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	mockLock := &mockChannelLock{key: "test-lock"}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	mockClient.On("XAck", mock.Anything, "slack-manager:queue:test-queue:stream:C12345", "slack-manager", []string{"1234567890-0"}).Return(int64(1), nil).Once()
	mockLock.On("Release", mock.Anything).Return(nil).Once()

	msg := redis.XMessage{
		ID: "1234567890-0",
		Values: map[string]any{
			"channel_id": "C12345",
			"body":       `{"test": "body"}`,
		},
	}

	sinkCh := make(chan *common.FifoQueueItem, 10)
	ctx := context.Background()

	err = queue.processMessageWithLock(ctx, "slack-manager:queue:test-queue:stream:C12345", "C12345", msg, mockLock, sinkCh)
	require.NoError(t, err)

	item := <-sinkCh

	// Call Ack - should ack message and release lock.
	item.Ack(ctx)

	// Call Ack again - should be no-op due to sync.Once.
	item.Ack(ctx)

	mockClient.AssertExpectations(t)
	mockLock.AssertExpectations(t)
}

func TestRedisFifoQueue_NackReleasesLock(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	mockLock := &mockChannelLock{key: "test-lock"}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	mockLock.On("Release", mock.Anything).Return(nil).Once()

	msg := redis.XMessage{
		ID: "1234567890-0",
		Values: map[string]any{
			"channel_id": "C12345",
			"body":       `{"test": "body"}`,
		},
	}

	sinkCh := make(chan *common.FifoQueueItem, 10)
	ctx := context.Background()

	err = queue.processMessageWithLock(ctx, "slack-manager:queue:test-queue:stream:C12345", "C12345", msg, mockLock, sinkCh)
	require.NoError(t, err)

	item := <-sinkCh

	// Call Nack - should release lock without acking.
	item.Nack(ctx)

	// Call Nack again - should be no-op.
	item.Nack(ctx)

	mockClient.AssertExpectations(t)
	mockLock.AssertExpectations(t)
}

func TestRedisFifoQueue_CleanupStaleStreams_Disabled(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	// Create queue with cleanup disabled.
	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger,
		WithStreamInactivityTimeout(0),
	)
	_, err := queue.Init()
	require.NoError(t, err)

	// Should not call any Redis commands.
	cleanedUp, err := queue.cleanupStaleStreams(context.Background())

	require.NoError(t, err)
	assert.Empty(t, cleanedUp)
	mockClient.AssertExpectations(t)
}

func TestRedisFifoQueue_CleanupStaleStreams_NoStaleStreams(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	// Mock ZRangeByScore to return no stale streams.
	mockClient.On("ZRangeByScore", mock.Anything, "slack-manager:queue:test-queue:stream-activity", mock.Anything).
		Return([]string{}, nil)

	cleanedUp, err := queue.cleanupStaleStreams(context.Background())

	require.NoError(t, err)
	assert.Empty(t, cleanedUp)
	mockClient.AssertExpectations(t)
}

func TestRedisFifoQueue_CleanupStaleStreams_CleansUpStaleStreams(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	staleStreamKey := "slack-manager:queue:test-queue:stream:C12345"

	// Mock ZRangeByScore to return one stale stream.
	mockClient.On("ZRangeByScore", mock.Anything, "slack-manager:queue:test-queue:stream-activity", mock.Anything).
		Return([]string{staleStreamKey}, nil)

	// Mock Del to delete the stream.
	mockClient.On("Del", mock.Anything, []string{staleStreamKey}).Return(int64(1), nil)

	// Mock SRem to remove from index.
	mockClient.On("SRem", mock.Anything, "slack-manager:queue:test-queue:streams", []any{staleStreamKey}).Return(int64(1), nil)

	// Mock ZRem to remove from activity set.
	mockClient.On("ZRem", mock.Anything, "slack-manager:queue:test-queue:stream-activity", []any{staleStreamKey}).Return(int64(1), nil)

	cleanedUp, err := queue.cleanupStaleStreams(context.Background())

	require.NoError(t, err)
	assert.Equal(t, []string{staleStreamKey}, cleanedUp)
	mockClient.AssertExpectations(t)
}

func TestRedisFifoQueue_StreamActivityKey(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger,
		WithKeyPrefix("test-prefix"),
	)

	key := queue.streamActivityKey()
	assert.Equal(t, "test-prefix:test-queue:stream-activity", key)
}

func TestRedisFifoQueueOptionFunctions_AllOptions(t *testing.T) {
	t.Parallel()

	t.Run("WithConsumerGroup", func(t *testing.T) {
		t.Parallel()
		opts := newRedisFifoQueueOptions()
		WithConsumerGroup("custom-group")(opts)
		assert.Equal(t, "custom-group", opts.consumerGroup)
	})

	t.Run("WithPollInterval", func(t *testing.T) {
		t.Parallel()
		opts := newRedisFifoQueueOptions()
		WithPollInterval(10 * time.Second)(opts)
		assert.Equal(t, 10*time.Second, opts.pollInterval)
	})

	t.Run("WithMaxStreamLength", func(t *testing.T) {
		t.Parallel()
		opts := newRedisFifoQueueOptions()
		WithMaxStreamLength(50000)(opts)
		assert.Equal(t, int64(50000), opts.maxStreamLength)
	})

	t.Run("WithStreamRefreshInterval", func(t *testing.T) {
		t.Parallel()
		opts := newRedisFifoQueueOptions()
		WithStreamRefreshInterval(60 * time.Second)(opts)
		assert.Equal(t, 60*time.Second, opts.streamRefreshInterval)
	})

	t.Run("WithClaimMinIdleTime", func(t *testing.T) {
		t.Parallel()
		opts := newRedisFifoQueueOptions()
		WithClaimMinIdleTime(60 * time.Second)(opts)
		assert.Equal(t, 60*time.Second, opts.claimMinIdleTime)
	})
}

func TestRedisFifoQueue_Send_NotInitialized(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	// Don't call Init()

	err := queue.Send(context.Background(), "C12345", "dedup-1", `{"test": "body"}`)

	require.Error(t, err)
	assert.Equal(t, "redis FIFO queue not initialized", err.Error())
}

func TestRedisFifoQueue_Send_EmptyChannelID(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	err = queue.Send(context.Background(), "", "dedup-1", `{"test": "body"}`)

	require.Error(t, err)
	assert.Equal(t, "slackChannelID cannot be empty", err.Error())
}

func TestRedisFifoQueue_Send_EmptyBody(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	err = queue.Send(context.Background(), "C12345", "dedup-1", "")

	require.Error(t, err)
	assert.Equal(t, "body cannot be empty", err.Error())
}

func TestRedisFifoQueue_Send_XAddError(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	mockClient.On("XAdd", mock.Anything, mock.Anything).Return("", errors.New("redis connection error"))

	err = queue.Send(context.Background(), "C12345", "dedup-1", `{"test": "body"}`)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to add message to Redis stream")
	assert.Contains(t, err.Error(), "redis connection error")
}

func TestRedisFifoQueue_Receive_NotInitialized(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	// Don't call Init()

	sinkCh := make(chan *common.FifoQueueItem, 10)
	err := queue.Receive(context.Background(), sinkCh)

	require.Error(t, err)
	assert.Equal(t, "redis FIFO queue not initialized", err.Error())
}

func TestRedisFifoQueue_ChannelIDFromStreamKey(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger,
		WithKeyPrefix("slack-manager:queue"),
	)

	channelID := queue.channelIDFromStreamKey("slack-manager:queue:test-queue:stream:C12345")
	assert.Equal(t, "C12345", channelID)
}

func TestRedisFifoQueue_StreamsIndexKey(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger,
		WithKeyPrefix("test-prefix"),
	)

	key := queue.streamsIndexKey()
	assert.Equal(t, "test-prefix:test-queue:streams", key)
}

func TestRedisFifoQueue_StreamKey(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger,
		WithKeyPrefix("test-prefix"),
	)

	key := queue.streamKey("C12345")
	assert.Equal(t, "test-prefix:test-queue:stream:C12345", key)
}

func TestRedisFifoQueue_ReleaseLock_NilLock(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)

	// Should not panic when lock is nil.
	queue.releaseLock(context.Background(), nil)
}

func TestRedisFifoQueue_ReleaseLock_Error(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	mockLock := &mockChannelLock{key: "test-lock"}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)

	mockLock.On("Release", mock.Anything).Return(errors.New("release error"))

	// Should not panic, just log the error.
	queue.releaseLock(context.Background(), mockLock)

	mockLock.AssertExpectations(t)
}

func TestRedisFifoQueue_RefreshStreams_Success(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger,
		WithStreamInactivityTimeout(0), // Disable cleanup for this test.
	)
	_, err := queue.Init()
	require.NoError(t, err)

	streamKey := "slack-manager:queue:test-queue:stream:C12345"

	mockClient.On("SMembers", mock.Anything, "slack-manager:queue:test-queue:streams").
		Return([]string{streamKey}, nil)
	mockClient.On("XGroupCreateMkStream", mock.Anything, streamKey, "slack-manager", "0").
		Return("OK", nil)

	knownStreams := make(map[string]bool)
	err = queue.refreshStreams(context.Background(), knownStreams)

	require.NoError(t, err)
	assert.True(t, knownStreams[streamKey])
	mockClient.AssertExpectations(t)
}

func TestRedisFifoQueue_RefreshStreams_BusyGroupError(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger,
		WithStreamInactivityTimeout(0),
	)
	_, err := queue.Init()
	require.NoError(t, err)

	streamKey := "slack-manager:queue:test-queue:stream:C12345"

	mockClient.On("SMembers", mock.Anything, "slack-manager:queue:test-queue:streams").
		Return([]string{streamKey}, nil)
	mockClient.On("XGroupCreateMkStream", mock.Anything, streamKey, "slack-manager", "0").
		Return("", errors.New("BUSYGROUP Consumer Group name already exists"))

	knownStreams := make(map[string]bool)
	err = queue.refreshStreams(context.Background(), knownStreams)

	require.NoError(t, err)
	assert.True(t, knownStreams[streamKey]) // Should still be added despite BUSYGROUP.
	mockClient.AssertExpectations(t)
}

func TestRedisFifoQueue_RefreshStreams_SMembersError(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger,
		WithStreamInactivityTimeout(0),
	)
	_, err := queue.Init()
	require.NoError(t, err)

	mockClient.On("SMembers", mock.Anything, "slack-manager:queue:test-queue:streams").
		Return([]string{}, errors.New("redis connection error"))

	knownStreams := make(map[string]bool)
	err = queue.refreshStreams(context.Background(), knownStreams)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get streams from index")
}

func TestRedisFifoQueue_RefreshStreams_SkipsKnownStreams(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger,
		WithStreamInactivityTimeout(0),
	)
	_, err := queue.Init()
	require.NoError(t, err)

	streamKey := "slack-manager:queue:test-queue:stream:C12345"

	mockClient.On("SMembers", mock.Anything, "slack-manager:queue:test-queue:streams").
		Return([]string{streamKey}, nil)

	// Pre-populate the known streams.
	knownStreams := map[string]bool{streamKey: true}
	err = queue.refreshStreams(context.Background(), knownStreams)

	require.NoError(t, err)
	// XGroupCreateMkStream should NOT be called since stream is already known.
	mockClient.AssertNotCalled(t, "XGroupCreateMkStream")
}

func TestRedisFifoQueue_ReadMessagesWithLocking_EmptyStreams(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	knownStreams := make(map[string]bool)
	sinkCh := make(chan *common.FifoQueueItem, 10)

	messagesRead, err := queue.readMessagesWithLocking(context.Background(), knownStreams, sinkCh)

	require.NoError(t, err)
	assert.False(t, messagesRead)
}

func TestRedisFifoQueue_ReadMessagesWithLocking_LockUnavailable(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	streamKey := "slack-manager:queue:test-queue:stream:C12345"

	mockLocker.On("Obtain", mock.Anything, "slack-manager:queue:test-queue:lock:C12345", mock.Anything, mock.Anything).
		Return(nil, ErrChannelLockUnavailable)

	knownStreams := map[string]bool{streamKey: true}
	sinkCh := make(chan *common.FifoQueueItem, 10)

	messagesRead, err := queue.readMessagesWithLocking(context.Background(), knownStreams, sinkCh)

	require.NoError(t, err)
	assert.False(t, messagesRead)
	mockLocker.AssertExpectations(t)
}

func TestRedisFifoQueue_ReadMessagesWithLocking_ContextCancelled(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	streamKey := "slack-manager:queue:test-queue:stream:C12345"

	// Create a cancelled context.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	knownStreams := map[string]bool{streamKey: true}
	sinkCh := make(chan *common.FifoQueueItem, 10)

	// Should return immediately with context.Canceled error.
	messagesRead, err := queue.readMessagesWithLocking(ctx, knownStreams, sinkCh)

	require.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	assert.False(t, messagesRead)
	// Locker should NOT be called since context was already cancelled.
	mockLocker.AssertNotCalled(t, "Obtain")
}

func TestRedisFifoQueue_ReadMessagesWithLocking_LockError(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	streamKey := "slack-manager:queue:test-queue:stream:C12345"

	mockLocker.On("Obtain", mock.Anything, "slack-manager:queue:test-queue:lock:C12345", mock.Anything, mock.Anything).
		Return(nil, errors.New("redis connection error"))

	knownStreams := map[string]bool{streamKey: true}
	sinkCh := make(chan *common.FifoQueueItem, 10)

	messagesRead, err := queue.readMessagesWithLocking(context.Background(), knownStreams, sinkCh)

	require.NoError(t, err)
	assert.False(t, messagesRead)
	mockLocker.AssertExpectations(t)
}

func TestRedisFifoQueue_TryClaimPendingMessage_NoPending(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	mockClient.On("XAutoClaim", mock.Anything, mock.Anything).
		Return([]redis.XMessage{}, "0-0", nil)

	claimed, err := queue.tryClaimPendingMessage(context.Background(), "test-stream")

	require.NoError(t, err)
	assert.Nil(t, claimed)
	mockClient.AssertExpectations(t)
}

func TestRedisFifoQueue_TryClaimPendingMessage_NoGroupError(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	mockClient.On("XAutoClaim", mock.Anything, mock.Anything).
		Return([]redis.XMessage{}, "", errors.New("NOGROUP No such key 'test-stream' or consumer group"))

	claimed, err := queue.tryClaimPendingMessage(context.Background(), "test-stream")

	require.NoError(t, err) // NOGROUP is not an error.
	assert.Nil(t, claimed)
	mockClient.AssertExpectations(t)
}

func TestRedisFifoQueue_TryClaimPendingMessage_OtherError(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	mockClient.On("XAutoClaim", mock.Anything, mock.Anything).
		Return([]redis.XMessage{}, "", errors.New("redis connection error"))

	claimed, err := queue.tryClaimPendingMessage(context.Background(), "test-stream")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to auto-claim pending message")
	assert.Nil(t, claimed)
	mockClient.AssertExpectations(t)
}

func TestRedisFifoQueue_TryClaimPendingMessage_Success(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	pendingMsg := redis.XMessage{
		ID: "1234567890-0",
		Values: map[string]any{
			"body":       `{"test": "pending"}`,
			"channel_id": "C12345",
		},
	}

	mockClient.On("XAutoClaim", mock.Anything, mock.Anything).
		Return([]redis.XMessage{pendingMsg}, "0-0", nil)

	claimed, err := queue.tryClaimPendingMessage(context.Background(), "test-stream")

	require.NoError(t, err)
	require.NotNil(t, claimed)
	assert.Equal(t, "1234567890-0", claimed.ID)
	mockClient.AssertExpectations(t)
}

func TestRedisFifoQueue_ReadOneMessageFromStream_NoMessages(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	mockLock := &mockChannelLock{key: "test-lock"}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	// No pending messages.
	mockClient.On("XAutoClaim", mock.Anything, mock.Anything).
		Return([]redis.XMessage{}, "0-0", nil)

	// No new messages (redis.Nil).
	mockClient.On("XReadGroup", mock.Anything, mock.Anything).
		Return([]redis.XStream{}, redis.Nil)

	sinkCh := make(chan *common.FifoQueueItem, 10)
	read, err := queue.readOneMessageFromStream(context.Background(), "test-stream", "C12345", mockLock, sinkCh)

	require.NoError(t, err)
	assert.False(t, read)
	mockClient.AssertExpectations(t)
}

func TestRedisFifoQueue_ReadOneMessageFromStream_XReadGroupError(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	mockLock := &mockChannelLock{key: "test-lock"}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	// No pending messages.
	mockClient.On("XAutoClaim", mock.Anything, mock.Anything).
		Return([]redis.XMessage{}, "0-0", nil)

	// XReadGroup error.
	mockClient.On("XReadGroup", mock.Anything, mock.Anything).
		Return([]redis.XStream{}, errors.New("redis connection error"))

	sinkCh := make(chan *common.FifoQueueItem, 10)
	read, err := queue.readOneMessageFromStream(context.Background(), "test-stream", "C12345", mockLock, sinkCh)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read from stream")
	assert.False(t, read)
	mockClient.AssertExpectations(t)
}

func TestRedisFifoQueue_ReadOneMessageFromStream_ClaimsPending(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	mockLock := &mockChannelLock{key: "test-lock"}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	pendingMsg := redis.XMessage{
		ID: "1234567890-0",
		Values: map[string]any{
			"body":       `{"test": "pending"}`,
			"channel_id": "C12345",
		},
	}

	mockClient.On("XAutoClaim", mock.Anything, mock.Anything).
		Return([]redis.XMessage{pendingMsg}, "0-0", nil)

	sinkCh := make(chan *common.FifoQueueItem, 10)
	read, err := queue.readOneMessageFromStream(context.Background(), "test-stream", "C12345", mockLock, sinkCh)

	require.NoError(t, err)
	assert.True(t, read)
	require.Len(t, sinkCh, 1)

	item := <-sinkCh
	assert.Equal(t, "1234567890-0", item.MessageID)
	mockClient.AssertExpectations(t)
}

func TestRedisFifoQueue_ReadOneMessageFromStream_ReadsNew(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	mockLock := &mockChannelLock{key: "test-lock"}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	// No pending messages.
	mockClient.On("XAutoClaim", mock.Anything, mock.Anything).
		Return([]redis.XMessage{}, "0-0", nil)

	// New message available.
	newMsg := redis.XMessage{
		ID: "1234567890-0",
		Values: map[string]any{
			"body":       `{"test": "new"}`,
			"channel_id": "C12345",
		},
	}
	mockClient.On("XReadGroup", mock.Anything, mock.Anything).
		Return([]redis.XStream{{Stream: "test-stream", Messages: []redis.XMessage{newMsg}}}, nil)

	sinkCh := make(chan *common.FifoQueueItem, 10)
	read, err := queue.readOneMessageFromStream(context.Background(), "test-stream", "C12345", mockLock, sinkCh)

	require.NoError(t, err)
	assert.True(t, read)
	require.Len(t, sinkCh, 1)

	item := <-sinkCh
	assert.Equal(t, "1234567890-0", item.MessageID)
	mockClient.AssertExpectations(t)
}

func TestRedisFifoQueue_CleanupStaleStreams_ZRangeByScoreError(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	mockClient.On("ZRangeByScore", mock.Anything, "slack-manager:queue:test-queue:stream-activity", mock.Anything).
		Return([]string{}, errors.New("redis connection error"))

	cleanedUp, err := queue.cleanupStaleStreams(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to find stale streams")
	assert.Nil(t, cleanedUp)
}

func TestRedisFifoQueue_CleanupStaleStreams_DelError(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	staleStreamKey := "slack-manager:queue:test-queue:stream:C12345"

	mockClient.On("ZRangeByScore", mock.Anything, "slack-manager:queue:test-queue:stream-activity", mock.Anything).
		Return([]string{staleStreamKey}, nil)
	mockClient.On("Del", mock.Anything, []string{staleStreamKey}).
		Return(int64(0), errors.New("redis connection error"))

	cleanedUp, err := queue.cleanupStaleStreams(context.Background())

	require.NoError(t, err) // Del error is logged but not returned.
	assert.Empty(t, cleanedUp)
	mockClient.AssertExpectations(t)
}

func TestRedisFifoQueue_Init_AlreadyInitialized(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)

	// First init.
	result1, err := queue.Init()
	require.NoError(t, err)
	assert.Equal(t, queue, result1)

	consumerName := queue.consumerName

	// Second init should return early.
	result2, err := queue.Init()
	require.NoError(t, err)
	assert.Equal(t, queue, result2)
	assert.Equal(t, consumerName, queue.consumerName) // Consumer name should not change.
}

func TestRedisFifoQueue_ReadMessagesWithLocking_Success(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	mockLock := &mockChannelLock{key: "test-lock"}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	streamKey := "slack-manager:queue:test-queue:stream:C12345"

	mockLocker.On("Obtain", mock.Anything, "slack-manager:queue:test-queue:lock:C12345", mock.Anything, mock.Anything).
		Return(mockLock, nil)

	// No pending messages.
	mockClient.On("XAutoClaim", mock.Anything, mock.Anything).
		Return([]redis.XMessage{}, "0-0", nil)

	// New message available.
	newMsg := redis.XMessage{
		ID: "1234567890-0",
		Values: map[string]any{
			"body":       `{"test": "new"}`,
			"channel_id": "C12345",
		},
	}
	mockClient.On("XReadGroup", mock.Anything, mock.Anything).
		Return([]redis.XStream{{Stream: streamKey, Messages: []redis.XMessage{newMsg}}}, nil)

	knownStreams := map[string]bool{streamKey: true}
	sinkCh := make(chan *common.FifoQueueItem, 10)

	messagesRead, err := queue.readMessagesWithLocking(context.Background(), knownStreams, sinkCh)

	require.NoError(t, err)
	assert.True(t, messagesRead)
	require.Len(t, sinkCh, 1)

	item := <-sinkCh
	assert.Equal(t, "1234567890-0", item.MessageID)
	mockLocker.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestRedisFifoQueue_ReadMessagesWithLocking_NoMessageReleasesLock(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	mockLock := &mockChannelLock{key: "test-lock"}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	streamKey := "slack-manager:queue:test-queue:stream:C12345"

	mockLocker.On("Obtain", mock.Anything, "slack-manager:queue:test-queue:lock:C12345", mock.Anything, mock.Anything).
		Return(mockLock, nil)

	// No pending messages.
	mockClient.On("XAutoClaim", mock.Anything, mock.Anything).
		Return([]redis.XMessage{}, "0-0", nil)

	// No new messages (redis.Nil).
	mockClient.On("XReadGroup", mock.Anything, mock.Anything).
		Return([]redis.XStream{}, redis.Nil)

	// Lock should be released since no message was read.
	mockLock.On("Release", mock.Anything).Return(nil)

	knownStreams := map[string]bool{streamKey: true}
	sinkCh := make(chan *common.FifoQueueItem, 10)

	messagesRead, err := queue.readMessagesWithLocking(context.Background(), knownStreams, sinkCh)

	require.NoError(t, err)
	assert.False(t, messagesRead)
	assert.Empty(t, sinkCh)
	mockLocker.AssertExpectations(t)
	mockClient.AssertExpectations(t)
	mockLock.AssertExpectations(t)
}

func TestRedisFifoQueue_ReadMessagesWithLocking_ErrorReleasesLock(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	mockLock := &mockChannelLock{key: "test-lock"}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	streamKey := "slack-manager:queue:test-queue:stream:C12345"

	mockLocker.On("Obtain", mock.Anything, "slack-manager:queue:test-queue:lock:C12345", mock.Anything, mock.Anything).
		Return(mockLock, nil)

	// Claim error.
	mockClient.On("XAutoClaim", mock.Anything, mock.Anything).
		Return([]redis.XMessage{}, "", errors.New("redis error"))

	// Lock should be released on error.
	mockLock.On("Release", mock.Anything).Return(nil)

	knownStreams := map[string]bool{streamKey: true}
	sinkCh := make(chan *common.FifoQueueItem, 10)

	messagesRead, err := queue.readMessagesWithLocking(context.Background(), knownStreams, sinkCh)

	require.Error(t, err)
	assert.False(t, messagesRead)
	mockLocker.AssertExpectations(t)
	mockClient.AssertExpectations(t)
	mockLock.AssertExpectations(t)
}

func TestRedisFifoQueue_RefreshStreams_XGroupCreateError(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger,
		WithStreamInactivityTimeout(0),
	)
	_, err := queue.Init()
	require.NoError(t, err)

	streamKey := "slack-manager:queue:test-queue:stream:C12345"

	mockClient.On("SMembers", mock.Anything, "slack-manager:queue:test-queue:streams").
		Return([]string{streamKey}, nil)
	mockClient.On("XGroupCreateMkStream", mock.Anything, streamKey, "slack-manager", "0").
		Return("", errors.New("WRONGTYPE Operation against a key holding the wrong kind of value"))

	knownStreams := make(map[string]bool)
	err = queue.refreshStreams(context.Background(), knownStreams)

	require.NoError(t, err) // Error is logged but stream is skipped.
	assert.False(t, knownStreams[streamKey])
	mockClient.AssertExpectations(t)
}

func TestRedisFifoQueue_ProcessMessageWithLock_ContextCancelled(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	mockLock := &mockChannelLock{key: "test-lock"}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	msg := redis.XMessage{
		ID: "1234567890-0",
		Values: map[string]any{
			"channel_id": "C12345",
			"body":       `{"test": "body"}`,
		},
	}

	// Lock should be released when context is cancelled.
	mockLock.On("Release", mock.Anything).Return(nil)

	// Create a cancelled context.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Use an unbuffered channel that will block (context is already cancelled).
	sinkCh := make(chan *common.FifoQueueItem)

	err = queue.processMessageWithLock(ctx, "slack-manager:queue:test-queue:stream:C12345", "C12345", msg, mockLock, sinkCh)

	require.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	mockLock.AssertExpectations(t)
}

func TestRedisFifoQueue_ProcessMessageWithLock_UsesChannelIDFromMessage(t *testing.T) {
	t.Parallel()

	mockClient := &mockRedisClient{}
	mockLocker := &mockChannelLocker{}
	mockLock := &mockChannelLock{key: "test-lock"}
	logger := &mockLogger{}

	queue := NewRedisFifoQueue(mockClient, mockLocker, "test-queue", logger)
	_, err := queue.Init()
	require.NoError(t, err)

	// Message has a different channel_id than what was passed.
	msg := redis.XMessage{
		ID: "1234567890-0",
		Values: map[string]any{
			"channel_id": "C99999", // Different channel ID.
			"body":       `{"test": "body"}`,
		},
	}

	sinkCh := make(chan *common.FifoQueueItem, 10)
	ctx := context.Background()

	err = queue.processMessageWithLock(ctx, "slack-manager:queue:test-queue:stream:C12345", "C12345", msg, mockLock, sinkCh)
	require.NoError(t, err)

	require.Len(t, sinkCh, 1)

	item := <-sinkCh
	// Should use channel_id from message, not from parameter.
	assert.Equal(t, "C99999", item.SlackChannelID)
}

func TestRedisFifoQueueOptions_Validate_AllErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		modify      func(*RedisFifoQueueOptions)
		expectError string
	}{
		{
			name: "empty consumer group",
			modify: func(o *RedisFifoQueueOptions) {
				o.consumerGroup = ""
			},
			expectError: "consumer group cannot be empty",
		},
		{
			name: "poll interval too short",
			modify: func(o *RedisFifoQueueOptions) {
				o.pollInterval = 500 * time.Millisecond
			},
			expectError: "poll interval must be between 1 second and 1 minute",
		},
		{
			name: "poll interval too long",
			modify: func(o *RedisFifoQueueOptions) {
				o.pollInterval = 2 * time.Minute
			},
			expectError: "poll interval must be between 1 second and 1 minute",
		},
		{
			name: "max stream length too short",
			modify: func(o *RedisFifoQueueOptions) {
				o.maxStreamLength = 50
			},
			expectError: "max stream length must be at least 100",
		},
		{
			name: "stream refresh interval too short",
			modify: func(o *RedisFifoQueueOptions) {
				o.streamRefreshInterval = 2 * time.Second
			},
			expectError: "stream refresh interval must be between 5 seconds and 5 minutes",
		},
		{
			name: "stream refresh interval too long",
			modify: func(o *RedisFifoQueueOptions) {
				o.streamRefreshInterval = 10 * time.Minute
			},
			expectError: "stream refresh interval must be between 5 seconds and 5 minutes",
		},
		{
			name: "claim min idle time too short",
			modify: func(o *RedisFifoQueueOptions) {
				o.claimMinIdleTime = 5 * time.Second
			},
			expectError: "claim min idle time must be between 10 seconds and 10 minutes",
		},
		{
			name: "claim min idle time too long",
			modify: func(o *RedisFifoQueueOptions) {
				o.claimMinIdleTime = 15 * time.Minute
			},
			expectError: "claim min idle time must be between 10 seconds and 10 minutes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := newRedisFifoQueueOptions()
			tt.modify(opts)

			err := opts.validate()

			require.Error(t, err)
			assert.Equal(t, tt.expectError, err.Error())
		})
	}
}
