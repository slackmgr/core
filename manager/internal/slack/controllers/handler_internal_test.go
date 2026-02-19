package controllers

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
	"github.com/slackmgr/core/manager/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- Tests ---

func TestNewSocketModeHandler(t *testing.T) {
	t.Parallel()

	cfg := newTestManagerConfig()
	logger := &mockLogger{}
	logger.On("Debug", mock.Anything).Maybe()
	logger.On("Debugf", mock.Anything, mock.Anything).Maybe()
	logger.On("Info", mock.Anything).Maybe()
	logger.On("Infof", mock.Anything, mock.Anything).Maybe()

	handler := NewSocketModeHandler(
		&mockSlackAPIClient{},
		newMockSocketModeClient(),
		&mockFifoQueueProducer{},
		&mockIssueFinder{},
		&mockCacheStore{},
		cfg,
		newTestManagerSettings(),
		logger,
	)

	require.NotNil(t, handler)
	assert.NotNil(t, handler.sem)
	assert.NotNil(t, handler.eventMap)
	assert.NotNil(t, handler.interactionEventMap)
	assert.NotNil(t, handler.eventAPIMap)
	assert.NotNil(t, handler.slashCommandMap)
	assert.NotNil(t, handler.defaultHandlerFunc)

	// Verify handlers are registered
	assert.NotEmpty(t, handler.eventMap[socketmode.EventTypeHello])
	assert.NotEmpty(t, handler.eventMap[socketmode.EventTypeDisconnect])
	assert.NotEmpty(t, handler.eventMap[socketmode.EventTypeSlashCommand])
	assert.NotEmpty(t, handler.interactionEventMap[slack.InteractionTypeShortcut])
	assert.NotEmpty(t, handler.interactionEventMap[slack.InteractionTypeBlockActions])
}

func TestSocketModeHandler_RunEventLoop_ContextCancellation(t *testing.T) {
	t.Parallel()

	cfg := newTestManagerConfig()
	cfg.SocketModeDrainTimeout = 100 * time.Millisecond

	logger := &mockLogger{}
	logger.On("Debug", mock.Anything).Maybe()
	logger.On("Debugf", mock.Anything, mock.Anything).Maybe()

	socketClient := newMockSocketModeClient()
	socketClient.On("RunContext", mock.Anything).Return(nil)

	handler := NewSocketModeHandler(
		&mockSlackAPIClient{},
		socketClient,
		&mockFifoQueueProducer{},
		&mockIssueFinder{},
		&mockCacheStore{},
		cfg,
		newTestManagerSettings(),
		logger,
	)

	ctx, cancel := context.WithCancel(context.Background())

	// Run in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- handler.RunEventLoop(ctx)
	}()

	// Give it time to start
	time.Sleep(50 * time.Millisecond)

	// Cancel context
	cancel()

	// Should return with context.Canceled
	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("RunEventLoop did not return after context cancellation")
	}
}

func TestSocketModeHandler_RunEventLoop_ChannelClosure(t *testing.T) {
	t.Parallel()

	cfg := newTestManagerConfig()
	cfg.SocketModeDrainTimeout = 100 * time.Millisecond

	logger := &mockLogger{}
	logger.On("Debug", mock.Anything).Maybe()

	socketClient := newMockSocketModeClient()
	socketClient.On("RunContext", mock.Anything).Return(nil)

	handler := NewSocketModeHandler(
		&mockSlackAPIClient{},
		socketClient,
		&mockFifoQueueProducer{},
		&mockIssueFinder{},
		&mockCacheStore{},
		cfg,
		newTestManagerSettings(),
		logger,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- handler.RunEventLoop(ctx)
	}()

	// Give it time to start
	time.Sleep(50 * time.Millisecond)

	// Close the events channel (simulating Slack disconnect)
	close(socketClient.eventsChan)

	// Also signal RunContext to return (in real life, both happen together)
	close(socketClient.runContextDone)

	// Should return nil (clean shutdown)
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("RunEventLoop did not return after channel closure")
	}
}

func TestSocketModeHandler_dispatchHandler_ConcurrencyLimit(t *testing.T) {
	t.Parallel()

	cfg := newTestManagerConfig()
	cfg.SocketModeMaxWorkers = 2 // Only allow 2 concurrent handlers

	logger := &mockLogger{}
	logger.On("Debug", mock.Anything).Maybe()
	logger.On("Errorf", mock.Anything, mock.Anything).Maybe()

	handler := NewSocketModeHandler(
		&mockSlackAPIClient{},
		newMockSocketModeClient(),
		&mockFifoQueueProducer{},
		&mockIssueFinder{},
		&mockCacheStore{},
		cfg,
		newTestManagerSettings(),
		logger,
	)

	var activeCount atomic.Int32
	var maxActive atomic.Int32
	var wg sync.WaitGroup

	handlerFunc := func(ctx context.Context, evt *socketmode.Event, clt SocketModeClient) {
		current := activeCount.Add(1)
		// Track maximum concurrent handlers
		for {
			old := maxActive.Load()
			if current <= old || maxActive.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		activeCount.Add(-1)
		wg.Done()
	}

	evt := &socketmode.Event{Type: socketmode.EventTypeHello}
	ctx := context.Background()

	// Dispatch 5 handlers (more than the limit of 2)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		handler.dispatchHandler(ctx, evt, handlerFunc)
	}

	wg.Wait()

	// Max active should not exceed the limit
	assert.LessOrEqual(t, maxActive.Load(), int32(2), "Concurrency limit was exceeded")
}

func TestSocketModeHandler_dispatchHandler_PanicRecovery(t *testing.T) {
	t.Parallel()

	cfg := newTestManagerConfig()

	logger := &mockLogger{}
	logger.On("Debug", mock.Anything).Maybe()
	logger.On("Errorf", "Panic in socket mode handler: %v", mock.Anything).Once()

	handler := NewSocketModeHandler(
		&mockSlackAPIClient{},
		newMockSocketModeClient(),
		&mockFifoQueueProducer{},
		&mockIssueFinder{},
		&mockCacheStore{},
		cfg,
		newTestManagerSettings(),
		logger,
	)

	var completed atomic.Bool

	panicHandler := func(ctx context.Context, evt *socketmode.Event, clt SocketModeClient) {
		defer func() { completed.Store(true) }()
		panic("test panic")
	}

	evt := &socketmode.Event{Type: socketmode.EventTypeHello}
	ctx := context.Background()

	handler.dispatchHandler(ctx, evt, panicHandler)

	// Wait for handler to complete
	handler.wg.Wait()

	assert.True(t, completed.Load(), "Handler should have run")
	logger.AssertExpectations(t)
}

func TestSocketModeHandler_dispatchHandler_ContextCancelled(t *testing.T) {
	t.Parallel()

	cfg := newTestManagerConfig()

	logger := &mockLogger{}
	logger.On("Debug", mock.Anything).Maybe()

	handler := NewSocketModeHandler(
		&mockSlackAPIClient{},
		newMockSocketModeClient(),
		&mockFifoQueueProducer{},
		&mockIssueFinder{},
		&mockCacheStore{},
		cfg,
		newTestManagerSettings(),
		logger,
	)

	var called atomic.Bool
	handlerFunc := func(ctx context.Context, evt *socketmode.Event, clt SocketModeClient) {
		called.Store(true)
	}

	evt := &socketmode.Event{Type: socketmode.EventTypeHello}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	handler.dispatchHandler(ctx, evt, handlerFunc)

	// Give some time for potential execution
	time.Sleep(50 * time.Millisecond)

	assert.False(t, called.Load(), "Handler should not be called when context is cancelled")
}

func TestSocketModeHandler_dispatcher_EventTypeInteractive(t *testing.T) {
	t.Parallel()

	cfg := newTestManagerConfig()
	logger := &mockLogger{}
	logger.On("Debug", mock.Anything).Maybe()
	logger.On("Infof", mock.Anything, mock.Anything).Maybe()
	logger.On("Errorf", mock.Anything, mock.Anything).Maybe()
	logger.On("Error", mock.Anything).Maybe()

	handler := NewSocketModeHandler(
		&mockSlackAPIClient{},
		newMockSocketModeClient(),
		&mockFifoQueueProducer{},
		&mockIssueFinder{},
		&mockCacheStore{},
		cfg,
		newTestManagerSettings(),
		logger,
	)

	// Clear existing handlers and add our test handler
	handler.interactionEventMap = make(map[slack.InteractionType][]SocketModeHandlerFunc)

	var handlerCalled atomic.Bool
	handler.interactionEventMap[slack.InteractionTypeShortcut] = []SocketModeHandlerFunc{
		func(ctx context.Context, evt *socketmode.Event, clt SocketModeClient) {
			handlerCalled.Store(true)
		},
	}

	evt := socketmode.Event{
		Type: socketmode.EventTypeInteractive,
		Data: slack.InteractionCallback{
			Type: slack.InteractionTypeShortcut,
		},
	}

	handler.dispatcher(context.Background(), evt)
	handler.wg.Wait()

	assert.True(t, handlerCalled.Load())
}

func TestSocketModeHandler_dispatcher_EventTypeEventsAPI(t *testing.T) {
	t.Parallel()

	cfg := newTestManagerConfig()
	logger := &mockLogger{}
	logger.On("Debug", mock.Anything).Maybe()

	handler := NewSocketModeHandler(
		&mockSlackAPIClient{},
		newMockSocketModeClient(),
		&mockFifoQueueProducer{},
		&mockIssueFinder{},
		&mockCacheStore{},
		cfg,
		newTestManagerSettings(),
		logger,
	)

	var handlerCalled atomic.Bool
	handler.eventAPIMap["test_event"] = []SocketModeHandlerFunc{
		func(ctx context.Context, evt *socketmode.Event, clt SocketModeClient) {
			handlerCalled.Store(true)
		},
	}

	evt := socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: slackevents.EventsAPIEvent{
			InnerEvent: slackevents.EventsAPIInnerEvent{
				Type: "test_event",
			},
		},
	}

	handler.dispatcher(context.Background(), evt)
	handler.wg.Wait()

	assert.True(t, handlerCalled.Load())
}

func TestSocketModeHandler_dispatcher_EventTypeSlashCommand(t *testing.T) {
	t.Parallel()

	cfg := newTestManagerConfig()
	logger := &mockLogger{}
	logger.On("Debug", mock.Anything).Maybe()

	handler := NewSocketModeHandler(
		&mockSlackAPIClient{},
		newMockSocketModeClient(),
		&mockFifoQueueProducer{},
		&mockIssueFinder{},
		&mockCacheStore{},
		cfg,
		newTestManagerSettings(),
		logger,
	)

	var handlerCalled atomic.Bool
	handler.slashCommandMap["/test"] = []SocketModeHandlerFunc{
		func(ctx context.Context, evt *socketmode.Event, clt SocketModeClient) {
			handlerCalled.Store(true)
		},
	}

	evt := socketmode.Event{
		Type: socketmode.EventTypeSlashCommand,
		Data: slack.SlashCommand{
			Command: "/test",
		},
	}

	handler.dispatcher(context.Background(), evt)
	handler.wg.Wait()

	assert.True(t, handlerCalled.Load())
}

func TestSocketModeHandler_dispatcher_DefaultHandler(t *testing.T) {
	t.Parallel()

	cfg := newTestManagerConfig()
	logger := &mockLogger{}
	logger.On("Debug", mock.Anything).Maybe()
	logger.On("Info", mock.Anything).Maybe()

	handler := NewSocketModeHandler(
		&mockSlackAPIClient{},
		newMockSocketModeClient(),
		&mockFifoQueueProducer{},
		&mockIssueFinder{},
		&mockCacheStore{},
		cfg,
		newTestManagerSettings(),
		logger,
	)

	var defaultCalled atomic.Bool
	handler.defaultHandlerFunc = func(ctx context.Context, evt *socketmode.Event, clt SocketModeClient) {
		defaultCalled.Store(true)
	}

	// Use an event type that has no registered handlers
	evt := socketmode.Event{
		Type: "unknown_event_type",
	}

	handler.dispatcher(context.Background(), evt)
	handler.wg.Wait()

	assert.True(t, defaultCalled.Load())
}

func TestSocketModeHandler_socketmodeDispatcher_HandlerRegistered(t *testing.T) {
	t.Parallel()

	cfg := newTestManagerConfig()
	logger := &mockLogger{}
	logger.On("Debug", mock.Anything).Maybe()

	handler := NewSocketModeHandler(
		&mockSlackAPIClient{},
		newMockSocketModeClient(),
		&mockFifoQueueProducer{},
		&mockIssueFinder{},
		&mockCacheStore{},
		cfg,
		newTestManagerSettings(),
		logger,
	)

	var handlerCalled atomic.Bool
	handler.eventMap[socketmode.EventTypeHello] = []SocketModeHandlerFunc{
		func(ctx context.Context, evt *socketmode.Event, clt SocketModeClient) {
			handlerCalled.Store(true)
		},
	}

	evt := &socketmode.Event{Type: socketmode.EventTypeHello}

	result := handler.socketmodeDispatcher(context.Background(), evt)
	handler.wg.Wait()

	assert.True(t, result)
	assert.True(t, handlerCalled.Load())
}

func TestSocketModeHandler_socketmodeDispatcher_NoHandler(t *testing.T) {
	t.Parallel()

	cfg := newTestManagerConfig()
	logger := &mockLogger{}
	logger.On("Debug", mock.Anything).Maybe()

	handler := NewSocketModeHandler(
		&mockSlackAPIClient{},
		newMockSocketModeClient(),
		&mockFifoQueueProducer{},
		&mockIssueFinder{},
		&mockCacheStore{},
		cfg,
		newTestManagerSettings(),
		logger,
	)

	// Clear all handlers for this event type
	handler.eventMap = make(map[socketmode.EventType][]SocketModeHandlerFunc)

	evt := &socketmode.Event{Type: "unregistered_type"}

	result := handler.socketmodeDispatcher(context.Background(), evt)

	assert.False(t, result)
}

func TestSocketModeHandler_interactionDispatcher_InvalidData(t *testing.T) {
	t.Parallel()

	cfg := newTestManagerConfig()
	logger := &mockLogger{}
	logger.On("Debug", mock.Anything).Maybe()
	logger.On("Infof", mock.Anything, mock.Anything).Once()

	handler := NewSocketModeHandler(
		&mockSlackAPIClient{},
		newMockSocketModeClient(),
		&mockFifoQueueProducer{},
		&mockIssueFinder{},
		&mockCacheStore{},
		cfg,
		newTestManagerSettings(),
		logger,
	)

	evt := &socketmode.Event{
		Type: socketmode.EventTypeInteractive,
		Data: "invalid data", // Not an InteractionCallback
	}

	result := handler.interactionDispatcher(context.Background(), evt)

	assert.False(t, result)
	logger.AssertExpectations(t)
}

func TestSocketModeHandler_interactionDispatcher_BlockActions(t *testing.T) {
	t.Parallel()

	cfg := newTestManagerConfig()
	logger := &mockLogger{}
	logger.On("Debug", mock.Anything).Maybe()

	handler := NewSocketModeHandler(
		&mockSlackAPIClient{},
		newMockSocketModeClient(),
		&mockFifoQueueProducer{},
		&mockIssueFinder{},
		&mockCacheStore{},
		cfg,
		newTestManagerSettings(),
		logger,
	)

	// Clear existing handlers to prevent real handlers from being triggered
	handler.interactionEventMap = make(map[slack.InteractionType][]SocketModeHandlerFunc)
	handler.interactionBlockActionEventMap = make(map[string][]SocketModeHandlerFunc)

	var handlerCalled atomic.Bool
	handler.interactionBlockActionEventMap["test_action"] = []SocketModeHandlerFunc{
		func(ctx context.Context, evt *socketmode.Event, clt SocketModeClient) {
			handlerCalled.Store(true)
		},
	}

	evt := &socketmode.Event{
		Type: socketmode.EventTypeInteractive,
		Data: slack.InteractionCallback{
			Type: slack.InteractionTypeBlockActions,
			ActionCallback: slack.ActionCallbacks{
				BlockActions: []*slack.BlockAction{
					{ActionID: "test_action"},
				},
			},
		},
	}

	result := handler.interactionDispatcher(context.Background(), evt)
	handler.wg.Wait()

	assert.True(t, result)
	assert.True(t, handlerCalled.Load())
}

func TestSocketModeHandler_eventAPIDispatcher_InvalidData(t *testing.T) {
	t.Parallel()

	cfg := newTestManagerConfig()
	logger := &mockLogger{}
	logger.On("Debug", mock.Anything).Maybe()
	logger.On("Infof", mock.Anything, mock.Anything).Once()

	handler := NewSocketModeHandler(
		&mockSlackAPIClient{},
		newMockSocketModeClient(),
		&mockFifoQueueProducer{},
		&mockIssueFinder{},
		&mockCacheStore{},
		cfg,
		newTestManagerSettings(),
		logger,
	)

	evt := &socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: "invalid data", // Not an EventsAPIEvent
	}

	result := handler.eventAPIDispatcher(context.Background(), evt)

	assert.False(t, result)
	logger.AssertExpectations(t)
}

func TestSocketModeHandler_slashCommandDispatcher_InvalidData(t *testing.T) {
	t.Parallel()

	cfg := newTestManagerConfig()
	logger := &mockLogger{}
	logger.On("Debug", mock.Anything).Maybe()
	logger.On("Infof", mock.Anything, mock.Anything).Once()

	handler := NewSocketModeHandler(
		&mockSlackAPIClient{},
		newMockSocketModeClient(),
		&mockFifoQueueProducer{},
		&mockIssueFinder{},
		&mockCacheStore{},
		cfg,
		newTestManagerSettings(),
		logger,
	)

	evt := &socketmode.Event{
		Type: socketmode.EventTypeSlashCommand,
		Data: "invalid data", // Not a SlashCommand
	}

	result := handler.slashCommandDispatcher(context.Background(), evt)

	assert.False(t, result)
	logger.AssertExpectations(t)
}

func TestSocketModeHandler_handle(t *testing.T) {
	t.Parallel()

	cfg := newTestManagerConfig()
	logger := &mockLogger{}
	logger.On("Debug", mock.Anything).Maybe()

	handler := NewSocketModeHandler(
		&mockSlackAPIClient{},
		newMockSocketModeClient(),
		&mockFifoQueueProducer{},
		&mockIssueFinder{},
		&mockCacheStore{},
		cfg,
		newTestManagerSettings(),
		logger,
	)

	// Clear existing handlers
	handler.eventMap = make(map[socketmode.EventType][]SocketModeHandlerFunc)

	testHandler := func(ctx context.Context, evt *socketmode.Event, clt SocketModeClient) {}

	handler.handle(socketmode.EventTypeHello, testHandler)

	assert.Len(t, handler.eventMap[socketmode.EventTypeHello], 1)

	// Add another handler
	handler.handle(socketmode.EventTypeHello, testHandler)

	assert.Len(t, handler.eventMap[socketmode.EventTypeHello], 2)
}

func TestSocketModeHandler_handleMultiple(t *testing.T) {
	t.Parallel()

	cfg := newTestManagerConfig()
	logger := &mockLogger{}
	logger.On("Debug", mock.Anything).Maybe()

	handler := NewSocketModeHandler(
		&mockSlackAPIClient{},
		newMockSocketModeClient(),
		&mockFifoQueueProducer{},
		&mockIssueFinder{},
		&mockCacheStore{},
		cfg,
		newTestManagerSettings(),
		logger,
	)

	// Clear existing handlers
	handler.eventMap = make(map[socketmode.EventType][]SocketModeHandlerFunc)

	testHandler := func(ctx context.Context, evt *socketmode.Event, clt SocketModeClient) {}

	events := []socketmode.EventType{
		socketmode.EventTypeHello,
		socketmode.EventTypeConnected,
		socketmode.EventTypeDisconnect,
	}

	handler.handleMultiple(events, testHandler)

	for _, et := range events {
		assert.Len(t, handler.eventMap[et], 1)
	}
}

func TestSocketModeHandler_handleInteraction(t *testing.T) {
	t.Parallel()

	cfg := newTestManagerConfig()
	logger := &mockLogger{}
	logger.On("Debug", mock.Anything).Maybe()

	handler := NewSocketModeHandler(
		&mockSlackAPIClient{},
		newMockSocketModeClient(),
		&mockFifoQueueProducer{},
		&mockIssueFinder{},
		&mockCacheStore{},
		cfg,
		newTestManagerSettings(),
		logger,
	)

	// Clear existing handlers
	handler.interactionEventMap = make(map[slack.InteractionType][]SocketModeHandlerFunc)

	testHandler := func(ctx context.Context, evt *socketmode.Event, clt SocketModeClient) {}

	handler.handleInteraction(slack.InteractionTypeShortcut, testHandler)

	assert.Len(t, handler.interactionEventMap[slack.InteractionTypeShortcut], 1)
}

func TestSocketModeHandler_handleEventsAPI(t *testing.T) {
	t.Parallel()

	cfg := newTestManagerConfig()
	logger := &mockLogger{}
	logger.On("Debug", mock.Anything).Maybe()

	handler := NewSocketModeHandler(
		&mockSlackAPIClient{},
		newMockSocketModeClient(),
		&mockFifoQueueProducer{},
		&mockIssueFinder{},
		&mockCacheStore{},
		cfg,
		newTestManagerSettings(),
		logger,
	)

	// Clear existing handlers
	handler.eventAPIMap = make(map[string][]SocketModeHandlerFunc)

	testHandler := func(ctx context.Context, evt *socketmode.Event, clt SocketModeClient) {}

	handler.handleEventsAPI("test_event", testHandler)

	assert.Len(t, handler.eventAPIMap["test_event"], 1)
}

func TestSocketModeHandler_handleDefault(t *testing.T) {
	t.Parallel()

	cfg := newTestManagerConfig()
	logger := &mockLogger{}
	logger.On("Debug", mock.Anything).Maybe()

	handler := NewSocketModeHandler(
		&mockSlackAPIClient{},
		newMockSocketModeClient(),
		&mockFifoQueueProducer{},
		&mockIssueFinder{},
		&mockCacheStore{},
		cfg,
		newTestManagerSettings(),
		logger,
	)

	var called bool
	testHandler := func(ctx context.Context, evt *socketmode.Event, clt SocketModeClient) {
		called = true
	}

	handler.handleDefault(testHandler)

	assert.NotNil(t, handler.defaultHandlerFunc)

	// Call it to verify it's the right handler
	handler.defaultHandlerFunc(context.Background(), &socketmode.Event{}, nil)
	assert.True(t, called)
}

func TestSocketModeHandler_drainHandlers_Timeout(t *testing.T) {
	t.Parallel()

	cfg := newTestManagerConfig()
	cfg.SocketModeDrainTimeout = 50 * time.Millisecond

	logger := &mockLogger{}
	logger.On("Debug", mock.Anything).Maybe()
	logger.On("Info", mock.Anything).Once() // Timeout warning

	handler := NewSocketModeHandler(
		&mockSlackAPIClient{},
		newMockSocketModeClient(),
		&mockFifoQueueProducer{},
		&mockIssueFinder{},
		&mockCacheStore{},
		cfg,
		newTestManagerSettings(),
		logger,
	)

	// Add a handler that takes longer than drain timeout
	handler.wg.Add(1)
	go func() {
		time.Sleep(200 * time.Millisecond)
		handler.wg.Done()
	}()

	start := time.Now()
	handler.drainHandlers()
	elapsed := time.Since(start)

	// Should return after timeout, not wait for full 200ms
	assert.Less(t, elapsed, 150*time.Millisecond)
	logger.AssertExpectations(t)

	// Clean up
	handler.wg.Wait()
}

func TestSocketModeHandler_drainHandlers_Success(t *testing.T) {
	t.Parallel()

	cfg := newTestManagerConfig()
	cfg.SocketModeDrainTimeout = 1 * time.Second

	logger := &mockLogger{}
	logger.On("Debug", "All socket mode handlers completed gracefully").Once()

	handler := NewSocketModeHandler(
		&mockSlackAPIClient{},
		newMockSocketModeClient(),
		&mockFifoQueueProducer{},
		&mockIssueFinder{},
		&mockCacheStore{},
		cfg,
		newTestManagerSettings(),
		logger,
	)

	// Add a handler that completes quickly
	handler.wg.Add(1)
	go func() {
		time.Sleep(10 * time.Millisecond)
		handler.wg.Done()
	}()

	handler.drainHandlers()

	logger.AssertExpectations(t)
}

// --- Tests for utils.go ---

func TestAck_WithRequest(t *testing.T) {
	t.Parallel()

	client := newMockSocketModeClient()
	req := socketmode.Request{EnvelopeID: "test-envelope-id"}
	evt := &socketmode.Event{
		Request: &req,
	}

	client.On("Ack", req, []any(nil)).Once()

	ack(evt, client)

	client.AssertExpectations(t)
}

func TestAck_NilRequest(t *testing.T) {
	t.Parallel()

	client := newMockSocketModeClient()
	evt := &socketmode.Event{
		Request: nil,
	}

	// Ack should not be called when Request is nil
	ack(evt, client)

	client.AssertNotCalled(t, "Ack", mock.Anything, mock.Anything)
}

func TestAckWithPayload_WithRequest(t *testing.T) {
	t.Parallel()

	client := newMockSocketModeClient()
	req := socketmode.Request{EnvelopeID: "test-envelope-id"}
	evt := &socketmode.Event{
		Request: &req,
	}
	payload := map[string]string{"key": "value"}

	client.On("Ack", req, []any{payload}).Once()

	ackWithPayload(evt, client, payload)

	client.AssertExpectations(t)
}

func TestAckWithPayload_NilRequest(t *testing.T) {
	t.Parallel()

	client := newMockSocketModeClient()
	evt := &socketmode.Event{
		Request: nil,
	}
	payload := map[string]string{"key": "value"}

	// Ack should not be called when Request is nil
	ackWithPayload(evt, client, payload)

	client.AssertNotCalled(t, "Ack", mock.Anything, mock.Anything)
}

func TestAckWithFieldErrorMsg(t *testing.T) {
	t.Parallel()

	client := newMockSocketModeClient()
	req := socketmode.Request{EnvelopeID: "test-envelope-id"}
	evt := &socketmode.Event{
		Request: &req,
	}

	// Capture the payload to verify it's a ViewSubmissionResponse with errors
	client.On("Ack", req, mock.MatchedBy(func(payload []any) bool {
		if len(payload) != 1 {
			return false
		}
		resp, ok := payload[0].(*slack.ViewSubmissionResponse)
		if !ok {
			return false
		}
		return resp.ResponseAction == slack.RAErrors && resp.Errors["field_name"] == "error message"
	})).Once()

	ackWithFieldErrorMsg(evt, client, "field_name", "error message")

	client.AssertExpectations(t)
}

func TestSendCommand_Success(t *testing.T) {
	t.Parallel()

	queue := &mockFifoQueueProducer{}
	ctx := context.Background()

	cmd := &models.Command{
		Action:         models.CommandActionResolveIssue,
		SlackChannelID: "C12345",
		Timestamp:      time.Now(),
	}

	// The queue.Send should be called with the channel ID, dedup ID, and JSON body
	queue.On("Send", ctx, "C12345", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(nil).Once()

	err := sendCommand(ctx, queue, cmd)

	require.NoError(t, err)
	queue.AssertExpectations(t)
}

func TestSendCommand_QueueError(t *testing.T) {
	t.Parallel()

	queue := &mockFifoQueueProducer{}
	ctx := context.Background()

	cmd := &models.Command{
		Action:         models.CommandActionResolveIssue,
		SlackChannelID: "C12345",
		Timestamp:      time.Now(),
	}

	expectedErr := fmt.Errorf("queue error")
	queue.On("Send", ctx, "C12345", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(expectedErr).Once()

	err := sendCommand(ctx, queue, cmd)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "queue error")
	queue.AssertExpectations(t)
}
