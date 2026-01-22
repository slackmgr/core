package controllers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/peteraglen/slack-manager/internal"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
	"github.com/stretchr/testify/mock"
)

func TestReactionsController_reactionAdded(t *testing.T) {
	t.Parallel()

	t.Run("failed to cast EventsAPIEvent returns early", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Error", "Failed to cast EventsAPIEvent").Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		controller := &reactionsController{
			logger: logger,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeEventsAPI,
			Data:    "not an EventsAPIEvent",
			Request: &req,
		}

		controller.reactionAdded(context.Background(), evt, client)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("failed to cast ReactionAddedEvent returns early", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Error", "Failed to cast ReactionAddedEvent").Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		controller := &reactionsController{
			logger: logger,
		}

		innerEvent := slackevents.EventsAPIInnerEvent{
			Data: "not a ReactionAddedEvent",
		}
		apiEvent := slackevents.EventsAPIEvent{
			InnerEvent: innerEvent,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeEventsAPI,
			Data:    apiEvent,
			Request: &req,
		}

		controller.reactionAdded(context.Background(), evt, client)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("unrecognized reaction logs and does nothing", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Debugf", mock.Anything, mock.Anything).Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		controller := &reactionsController{
			logger:          logger,
			managerSettings: newTestManagerSettings(),
		}

		reactionEvent := &slackevents.ReactionAddedEvent{
			Reaction: "unknown_reaction",
			User:     "U12345",
			Item: slackevents.Item{
				Channel:   "C12345",
				Timestamp: "1234567890.123456",
			},
		}
		innerEvent := slackevents.EventsAPIInnerEvent{
			Data: reactionEvent,
		}
		apiEvent := slackevents.EventsAPIEvent{
			InnerEvent: innerEvent,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeEventsAPI,
			Data:    apiEvent,
			Request: &req,
		}

		controller.reactionAdded(context.Background(), evt, client)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})
}

func TestReactionsController_reactionRemoved(t *testing.T) {
	t.Parallel()

	t.Run("failed to cast EventsAPIEvent returns early", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Error", "Failed to cast EventsAPIEvent").Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		controller := &reactionsController{
			logger: logger,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeEventsAPI,
			Data:    "not an EventsAPIEvent",
			Request: &req,
		}

		controller.reactionRemoved(context.Background(), evt, client)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("failed to cast ReactionRemovedEvent returns early", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Error", "Failed to cast ReactionRemovedEvent").Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		controller := &reactionsController{
			logger: logger,
		}

		innerEvent := slackevents.EventsAPIInnerEvent{
			Data: "not a ReactionRemovedEvent",
		}
		apiEvent := slackevents.EventsAPIEvent{
			InnerEvent: innerEvent,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeEventsAPI,
			Data:    apiEvent,
			Request: &req,
		}

		controller.reactionRemoved(context.Background(), evt, client)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("unrecognized reaction logs and does nothing", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Debugf", mock.Anything, mock.Anything).Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		controller := &reactionsController{
			logger:          logger,
			managerSettings: newTestManagerSettings(),
		}

		reactionEvent := &slackevents.ReactionRemovedEvent{
			Reaction: "unknown_reaction",
			User:     "U12345",
			Item: slackevents.Item{
				Channel:   "C12345",
				Timestamp: "1234567890.123456",
			},
		}
		innerEvent := slackevents.EventsAPIInnerEvent{
			Data: reactionEvent,
		}
		apiEvent := slackevents.EventsAPIEvent{
			InnerEvent: innerEvent,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeEventsAPI,
			Data:    apiEvent,
			Request: &req,
		}

		controller.reactionRemoved(context.Background(), evt, client)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})
}

func TestReactionsController_getUserInfo(t *testing.T) {
	t.Parallel()

	t.Run("IsAlertChannel error returns error", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}

		apiClient := &mockSlackAPIClient{}
		apiClient.On("IsAlertChannel", mock.Anything, "C12345").Return(false, "", errors.New("api error")).Once()

		controller := &reactionsController{
			apiClient: apiClient,
			logger:    logger,
		}

		user, err := controller.getUserInfo(context.Background(), "C12345", "U12345", false, logger)

		if err == nil {
			t.Error("expected error, got nil")
		}
		if user != nil {
			t.Error("expected nil user, got", user)
		}
		apiClient.AssertExpectations(t)
	})

	t.Run("unmanaged channel returns errUnmanagedChannel", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Debug", "User reaction to message in un-managed channel").Once()

		apiClient := &mockSlackAPIClient{}
		apiClient.On("IsAlertChannel", mock.Anything, "C12345").Return(false, "", nil).Once()

		controller := &reactionsController{
			apiClient: apiClient,
			logger:    logger,
		}

		user, err := controller.getUserInfo(context.Background(), "C12345", "U12345", false, logger)

		if !errors.Is(err, errUnmanagedChannel) {
			t.Errorf("expected errUnmanagedChannel, got %v", err)
		}
		if user != nil {
			t.Error("expected nil user, got", user)
		}
		apiClient.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("GetUserInfo error returns error", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Info", "User reaction to message in managed channel").Once()

		apiClient := &mockSlackAPIClient{}
		apiClient.On("IsAlertChannel", mock.Anything, "C12345").Return(true, "", nil).Once()
		apiClient.On("GetUserInfo", mock.Anything, "U12345").Return(nil, errors.New("api error")).Once()

		controller := &reactionsController{
			apiClient: apiClient,
			logger:    logger,
		}

		user, err := controller.getUserInfo(context.Background(), "C12345", "U12345", false, logger)

		if err == nil {
			t.Error("expected error, got nil")
		}
		if user != nil {
			t.Error("expected nil user, got", user)
		}
		apiClient.AssertExpectations(t)
	})

	t.Run("returns user info when not requiring admin", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Info", "User reaction to message in managed channel").Once()

		expectedUser := &slack.User{
			ID:       "U12345",
			RealName: "Test User",
		}

		apiClient := &mockSlackAPIClient{}
		apiClient.On("IsAlertChannel", mock.Anything, "C12345").Return(true, "", nil).Once()
		apiClient.On("GetUserInfo", mock.Anything, "U12345").Return(expectedUser, nil).Once()

		controller := &reactionsController{
			apiClient: apiClient,
			logger:    logger,
		}

		user, err := controller.getUserInfo(context.Background(), "C12345", "U12345", false, logger)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if user != expectedUser {
			t.Errorf("expected user %v, got %v", expectedUser, user)
		}
		apiClient.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("non-admin user returns errUserIsNotChannelAdmin when requiring admin", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Info", "User reaction to message in managed channel").Once()
		logger.On("Info", "User is not admin in channel").Once()
		logger.On("Info", "Post ephemeral message").Once()
		logger.On("Errorf", mock.Anything, mock.Anything).Maybe() // Cache read failure logging

		expectedUser := &slack.User{
			ID:       "U12345",
			RealName: "Test User",
		}

		apiClient := &mockSlackAPIClient{}
		apiClient.On("IsAlertChannel", mock.Anything, "C12345").Return(true, "", nil).Once()
		apiClient.On("GetUserInfo", mock.Anything, "U12345").Return(expectedUser, nil).Once()
		apiClient.On("UserIsInGroup", mock.Anything, mock.Anything, "U12345").Return(false).Maybe()
		apiClient.On("PostEphemeral", mock.Anything, "C12345", "U12345", mock.Anything).Return("", nil).Once()

		cacheStore := &mockCacheStore{}
		cacheStore.On("Get", mock.Anything, mock.Anything).Return(nil, errors.New("not found")).Once()
		cacheStore.On("Set", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

		controller := &reactionsController{
			apiClient:       apiClient,
			cache:           internal.NewCache(cacheStore, "test:", logger),
			logger:          logger,
			managerSettings: newTestManagerSettings(),
		}

		user, err := controller.getUserInfo(context.Background(), "C12345", "U12345", true, logger)

		if !errors.Is(err, errUserIsNotChannelAdmin) {
			t.Errorf("expected errUserIsNotChannelAdmin, got %v", err)
		}
		if user != nil {
			t.Error("expected nil user, got", user)
		}
		apiClient.AssertExpectations(t)
		logger.AssertExpectations(t)
	})
}

func TestReactionsController_postNotAdminAlert(t *testing.T) {
	t.Parallel()

	t.Run("skips post if already cached", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}

		apiClient := &mockSlackAPIClient{}
		// PostEphemeral should not be called

		cacheStore := &mockCacheStore{}
		cacheStore.On("Get", mock.Anything, mock.Anything).Return("cached", nil).Once()

		controller := &reactionsController{
			apiClient:       apiClient,
			cache:           internal.NewCache(cacheStore, "test:", logger),
			logger:          logger,
			managerSettings: newTestManagerSettings(),
		}

		controller.postNotAdminAlert(context.Background(), "C12345", "U12345", "Test User", logger)

		apiClient.AssertNotCalled(t, "PostEphemeral")
		cacheStore.AssertExpectations(t)
	})

	t.Run("posts ephemeral and caches on success", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Errorf", mock.Anything, mock.Anything).Maybe() // Cache read failure logging
		logger.On("Info", "Post ephemeral message").Once()

		apiClient := &mockSlackAPIClient{}
		apiClient.On("PostEphemeral", mock.Anything, "C12345", "U12345", mock.Anything).Return("", nil).Once()

		cacheStore := &mockCacheStore{}
		cacheStore.On("Get", mock.Anything, mock.Anything).Return(nil, errors.New("not found")).Once()
		cacheStore.On("Set", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

		controller := &reactionsController{
			apiClient:       apiClient,
			cache:           internal.NewCache(cacheStore, "test:", logger),
			logger:          logger,
			managerSettings: newTestManagerSettings(),
		}

		controller.postNotAdminAlert(context.Background(), "C12345", "U12345", "Test User", logger)

		apiClient.AssertExpectations(t)
		cacheStore.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("logs error if PostEphemeral fails", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Errorf", mock.Anything, mock.Anything).Maybe() // May be called for cache errors and PostEphemeral error

		apiClient := &mockSlackAPIClient{}
		apiClient.On("PostEphemeral", mock.Anything, "C12345", "U12345", mock.Anything).Return("", errors.New("api error")).Once()

		cacheStore := &mockCacheStore{}
		cacheStore.On("Get", mock.Anything, mock.Anything).Return(nil, errors.New("not found")).Once()

		controller := &reactionsController{
			apiClient:       apiClient,
			cache:           internal.NewCache(cacheStore, "test:", logger),
			logger:          logger,
			managerSettings: newTestManagerSettings(),
		}

		controller.postNotAdminAlert(context.Background(), "C12345", "U12345", "Test User", logger)

		apiClient.AssertExpectations(t)
	})
}

func TestReactionsController_sendReactionAddedCommand(t *testing.T) {
	t.Parallel()

	t.Run("unmanaged channel returns early without error log", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Debug", "User reaction to message in un-managed channel").Once()

		apiClient := &mockSlackAPIClient{}
		apiClient.On("IsAlertChannel", mock.Anything, "C12345").Return(false, "", nil).Once()

		commandQueue := &mockFifoQueueProducer{}

		controller := &reactionsController{
			apiClient:    apiClient,
			commandQueue: commandQueue,
			logger:       logger,
		}

		evt := &slackevents.ReactionAddedEvent{
			Reaction: "white_check_mark",
			User:     "U12345",
			Item: slackevents.Item{
				Channel:   "C12345",
				Timestamp: "1234567890.123456",
			},
		}

		controller.sendReactionAddedCommand(context.Background(), evt, "resolve", false)

		apiClient.AssertExpectations(t)
		logger.AssertExpectations(t)
		commandQueue.AssertNotCalled(t, "Send")
	})

	t.Run("sends command successfully", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Info", "User reaction to message in managed channel").Once()

		expectedUser := &slack.User{
			ID:       "U12345",
			RealName: "Test User",
		}

		apiClient := &mockSlackAPIClient{}
		apiClient.On("IsAlertChannel", mock.Anything, "C12345").Return(true, "", nil).Once()
		apiClient.On("GetUserInfo", mock.Anything, "U12345").Return(expectedUser, nil).Once()

		commandQueue := &mockFifoQueueProducer{}
		commandQueue.On("Send", mock.Anything, "C12345", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(nil).Once()

		controller := &reactionsController{
			apiClient:    apiClient,
			commandQueue: commandQueue,
			logger:       logger,
		}

		evt := &slackevents.ReactionAddedEvent{
			Reaction: "white_check_mark",
			User:     "U12345",
			Item: slackevents.Item{
				Channel:   "C12345",
				Timestamp: "1234567890.123456",
			},
		}

		controller.sendReactionAddedCommand(context.Background(), evt, "resolve", false)

		apiClient.AssertExpectations(t)
		logger.AssertExpectations(t)
		commandQueue.AssertExpectations(t)
	})

	t.Run("logs error on queue failure", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Info", "User reaction to message in managed channel").Once()
		logger.On("Errorf", mock.Anything, mock.Anything, mock.Anything).Once()

		expectedUser := &slack.User{
			ID:       "U12345",
			RealName: "Test User",
		}

		apiClient := &mockSlackAPIClient{}
		apiClient.On("IsAlertChannel", mock.Anything, "C12345").Return(true, "", nil).Once()
		apiClient.On("GetUserInfo", mock.Anything, "U12345").Return(expectedUser, nil).Once()

		commandQueue := &mockFifoQueueProducer{}
		commandQueue.On("Send", mock.Anything, "C12345", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(errors.New("queue error")).Once()

		controller := &reactionsController{
			apiClient:    apiClient,
			commandQueue: commandQueue,
			logger:       logger,
		}

		evt := &slackevents.ReactionAddedEvent{
			Reaction: "white_check_mark",
			User:     "U12345",
			Item: slackevents.Item{
				Channel:   "C12345",
				Timestamp: "1234567890.123456",
			},
		}

		controller.sendReactionAddedCommand(context.Background(), evt, "resolve", false)

		apiClient.AssertExpectations(t)
		logger.AssertExpectations(t)
		commandQueue.AssertExpectations(t)
	})
}

// Unused import guard for time package
var _ = time.Second
