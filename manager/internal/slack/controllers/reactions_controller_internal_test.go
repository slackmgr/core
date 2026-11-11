package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
	"github.com/slackmgr/core/config"
	"github.com/slackmgr/core/internal"
	"github.com/slackmgr/core/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestReactionsController_reactionAdded(t *testing.T) {
	t.Parallel()

	t.Run("failed to cast EventsAPIEvent returns early", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Errorf", "Failed to cast event data to EventsAPIEvent, got: %T", []any{"not an EventsAPIEvent"}).Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", &req).Once()

		controller := newReactionsController(client, nil, nil, nil, logger, nil, nil)

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeEventsAPI,
			Data:    "not an EventsAPIEvent",
			Request: &req,
		}

		controller.reactionAdded(context.Background(), evt)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("failed to cast ReactionAddedEvent returns early", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Errorf", "Failed to cast event data to ReactionAddedEvent, got: %T", []any{"not a ReactionAddedEvent"}).Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", &req).Once()

		controller := newReactionsController(client, nil, nil, nil, logger, nil, nil)

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

		controller.reactionAdded(context.Background(), evt)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("unrecognized reaction logs and does nothing", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Debugf", mock.Anything, mock.Anything).Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", &req).Once()

		controller := newReactionsController(client, nil, nil, nil, logger, nil, newTestManagerSettings())

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

		controller.reactionAdded(context.Background(), evt)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})
}

func TestReactionsController_reactionRemoved(t *testing.T) {
	t.Parallel()

	t.Run("failed to cast EventsAPIEvent returns early", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Errorf", "Failed to cast event data to EventsAPIEvent, got: %T", []any{"not an EventsAPIEvent"}).Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", &req).Once()

		controller := newReactionsController(client, nil, nil, nil, logger, nil, nil)

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeEventsAPI,
			Data:    "not an EventsAPIEvent",
			Request: &req,
		}

		controller.reactionRemoved(context.Background(), evt)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("failed to cast ReactionRemovedEvent returns early", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Errorf", "Failed to cast event data to ReactionRemovedEvent, got: %T", []any{"not a ReactionRemovedEvent"}).Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", &req).Once()

		controller := newReactionsController(client, nil, nil, nil, logger, nil, nil)

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

		controller.reactionRemoved(context.Background(), evt)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("unrecognized reaction logs and does nothing", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Debugf", mock.Anything, mock.Anything).Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", &req).Once()

		controller := newReactionsController(client, nil, nil, nil, logger, nil, newTestManagerSettings())

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

		controller.reactionRemoved(context.Background(), evt)

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

		controller := newReactionsController(nil, apiClient, nil, nil, logger, nil, nil)

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

		controller := newReactionsController(nil, apiClient, nil, nil, logger, nil, nil)

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

		controller := newReactionsController(nil, apiClient, nil, nil, logger, nil, nil)

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

		controller := newReactionsController(nil, apiClient, nil, nil, logger, nil, nil)

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

		controller := newReactionsController(nil, apiClient, nil, internal.NewCache(cacheStore, "test:", logger), logger, nil, newTestManagerSettings())

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

		controller := newReactionsController(nil, apiClient, nil, internal.NewCache(cacheStore, "test:", logger), logger, nil, newTestManagerSettings())

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

		controller := newReactionsController(nil, apiClient, nil, internal.NewCache(cacheStore, "test:", logger), logger, nil, newTestManagerSettings())

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

		controller := newReactionsController(nil, apiClient, nil, internal.NewCache(cacheStore, "test:", logger), logger, nil, newTestManagerSettings())

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

		controller := newReactionsController(nil, apiClient, commandQueue, nil, logger, nil, nil)

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

		controller := newReactionsController(nil, apiClient, commandQueue, nil, logger, nil, nil)

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

		controller := newReactionsController(nil, apiClient, commandQueue, nil, logger, nil, nil)

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

func TestReactionsController_postOwnership(t *testing.T) {
	t.Parallel()

	const botUserID = "UBOT"

	tests := []struct {
		name         string
		removed      bool
		reaction     string
		itemUser     string
		expectedCall bool
	}{
		{name: "added on post owned by bot sends command", reaction: "eyes", itemUser: botUserID, expectedCall: true},
		{name: "added on post owned by other app is ignored", reaction: "skull", itemUser: "UOTHERAPP"},
		{name: "added on post owned by other app is ignored for non-command reaction", reaction: "unknown", itemUser: "UOTHERAPP"},
		{name: "added with empty item user sends command", reaction: "eyes", itemUser: "", expectedCall: true},
		{name: "removed on post owned by bot sends command", removed: true, reaction: "eyes", itemUser: botUserID, expectedCall: true},
		{name: "removed on post owned by other app is ignored", removed: true, reaction: "eyes", itemUser: "UOTHERAPP"},
		{name: "removed with empty item user sends command", removed: true, reaction: "eyes", itemUser: "", expectedCall: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger := &mockLogger{}
			logger.On("Debugf", mock.Anything, mock.Anything).Maybe()
			logger.On("Info", "User reaction to message in managed channel").Maybe()
			logger.On("Debug", "Ignoring reaction to post not owned by this app").Maybe()

			client := newMockSocketModeClient()
			req := socketmode.Request{EnvelopeID: "test-envelope"}
			client.On("Ack", &req).Once()

			apiClient := &mockSlackAPIClient{}
			if tt.itemUser != "" {
				apiClient.On("BotUserID").Return(botUserID).Once()
			}
			if tt.expectedCall {
				apiClient.On("IsAlertChannel", mock.Anything, "C12345").Return(true, "", nil).Once()
				apiClient.On("GetUserInfo", mock.Anything, "U12345").Return(&slack.User{ID: "U12345", RealName: "Test User"}, nil).Once()
			}

			commandQueue := &mockFifoQueueProducer{}
			var sentBody string
			if tt.expectedCall {
				commandQueue.On("Send", mock.Anything, "C12345", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
					Run(func(args mock.Arguments) { sentBody = args.String(3) }).Return(nil).Once()
			}

			controller := newReactionsController(client, apiClient, commandQueue, nil, logger, nil, newInitializedTestManagerSettings(t))

			item := slackevents.Item{Channel: "C12345", Timestamp: "1234567890.123456"}
			var data any
			if tt.removed {
				data = &slackevents.ReactionRemovedEvent{Reaction: tt.reaction, User: "U12345", ItemUser: tt.itemUser, Item: item}
			} else {
				data = &slackevents.ReactionAddedEvent{Reaction: tt.reaction, User: "U12345", ItemUser: tt.itemUser, Item: item}
			}

			evt := &socketmode.Event{
				Type:    socketmode.EventTypeEventsAPI,
				Data:    slackevents.EventsAPIEvent{InnerEvent: slackevents.EventsAPIInnerEvent{Data: data}},
				Request: &req,
			}

			if tt.removed {
				controller.reactionRemoved(context.Background(), evt)
			} else {
				controller.reactionAdded(context.Background(), evt)
			}

			client.AssertExpectations(t)
			apiClient.AssertExpectations(t)
			commandQueue.AssertExpectations(t)

			if tt.expectedCall {
				expectedAction := models.CommandActionInvestigateIssue
				if tt.removed {
					expectedAction = models.CommandActionUninvestigateIssue
				}

				var cmd models.Command
				require.NoError(t, json.Unmarshal([]byte(sentBody), &cmd))
				assert.Equal(t, expectedAction, cmd.Action)
				assert.Equal(t, "1234567890.123456", cmd.SlackPostID)
				logger.AssertNotCalled(t, "Debug", "Ignoring reaction to post not owned by this app")
			} else {
				apiClient.AssertNotCalled(t, "IsAlertChannel", mock.Anything, mock.Anything)
				apiClient.AssertNotCalled(t, "GetUserInfo", mock.Anything, mock.Anything)
				apiClient.AssertNotCalled(t, "PostEphemeral", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
				commandQueue.AssertNotCalled(t, "Send", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
				logger.AssertCalled(t, "Debug", "Ignoring reaction to post not owned by this app")
				logger.AssertNotCalled(t, "Debugf", mock.Anything, mock.Anything)
			}
		})
	}
}

func newInitializedTestManagerSettings(t *testing.T) *models.ManagerSettingsWrapper {
	t.Helper()

	settings := &config.ManagerSettings{
		IssueReactions: &config.IssueReactionSettings{
			TerminateEmojis:   []string{":skull:"},
			InvestigateEmojis: []string{":eyes:"},
		},
	}
	require.NoError(t, settings.InitAndValidate())

	return models.NewManagerSettingsWrapper(settings)
}

// Unused import guard for time package
var _ = time.Second
