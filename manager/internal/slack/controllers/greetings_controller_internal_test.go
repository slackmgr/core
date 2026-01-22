package controllers

import (
	"context"
	"errors"
	"testing"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
	"github.com/stretchr/testify/mock"
)

func TestGreetingsController_memberJoinedChannel(t *testing.T) {
	t.Parallel()

	t.Run("failed to cast event returns early", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Error", "Failed to cast MemberJoinedChannelEvent").Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		apiClient := &mockSlackAPIClient{}

		controller := &greetingsController{
			apiClient:       apiClient,
			logger:          logger,
			cfg:             newTestManagerConfig(),
			managerSettings: newTestManagerSettings(),
		}

		// Invalid inner event type
		innerEvent := slackevents.EventsAPIInnerEvent{
			Data: "not a MemberJoinedChannelEvent",
		}
		apiEvent := slackevents.EventsAPIEvent{
			InnerEvent: innerEvent,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeEventsAPI,
			Data:    apiEvent,
			Request: &req,
		}

		controller.memberJoinedChannel(context.Background(), evt, client)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
		apiClient.AssertNotCalled(t, "GetUserInfo")
	})

	t.Run("GetUserInfo error returns early", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Errorf", mock.Anything, mock.Anything).Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		apiClient := &mockSlackAPIClient{}
		apiClient.On("GetUserInfo", mock.Anything, "U12345").Return(nil, errors.New("api error")).Once()

		controller := &greetingsController{
			apiClient:       apiClient,
			logger:          logger,
			cfg:             newTestManagerConfig(),
			managerSettings: newTestManagerSettings(),
		}

		joinedEvent := &slackevents.MemberJoinedChannelEvent{
			User:    "U12345",
			Channel: "C12345",
		}
		innerEvent := slackevents.EventsAPIInnerEvent{
			Data: joinedEvent,
		}
		apiEvent := slackevents.EventsAPIEvent{
			InnerEvent: innerEvent,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeEventsAPI,
			Data:    apiEvent,
			Request: &req,
		}

		controller.memberJoinedChannel(context.Background(), evt, client)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
		apiClient.AssertExpectations(t)
	})

	t.Run("bot user returns early", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		apiClient := &mockSlackAPIClient{}
		apiClient.On("GetUserInfo", mock.Anything, "U12345").Return(&slack.User{
			ID:    "U12345",
			IsBot: true,
		}, nil).Once()

		controller := &greetingsController{
			apiClient:       apiClient,
			logger:          logger,
			cfg:             newTestManagerConfig(),
			managerSettings: newTestManagerSettings(),
		}

		joinedEvent := &slackevents.MemberJoinedChannelEvent{
			User:    "U12345",
			Channel: "C12345",
		}
		innerEvent := slackevents.EventsAPIInnerEvent{
			Data: joinedEvent,
		}
		apiEvent := slackevents.EventsAPIEvent{
			InnerEvent: innerEvent,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeEventsAPI,
			Data:    apiEvent,
			Request: &req,
		}

		controller.memberJoinedChannel(context.Background(), evt, client)

		client.AssertExpectations(t)
		apiClient.AssertExpectations(t)
		// No IsAlertChannel call should be made for bots
		apiClient.AssertNotCalled(t, "IsAlertChannel")
	})

	t.Run("app user returns early", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		apiClient := &mockSlackAPIClient{}
		apiClient.On("GetUserInfo", mock.Anything, "U12345").Return(&slack.User{
			ID:        "U12345",
			IsAppUser: true,
		}, nil).Once()

		controller := &greetingsController{
			apiClient:       apiClient,
			logger:          logger,
			cfg:             newTestManagerConfig(),
			managerSettings: newTestManagerSettings(),
		}

		joinedEvent := &slackevents.MemberJoinedChannelEvent{
			User:    "U12345",
			Channel: "C12345",
		}
		innerEvent := slackevents.EventsAPIInnerEvent{
			Data: joinedEvent,
		}
		apiEvent := slackevents.EventsAPIEvent{
			InnerEvent: innerEvent,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeEventsAPI,
			Data:    apiEvent,
			Request: &req,
		}

		controller.memberJoinedChannel(context.Background(), evt, client)

		client.AssertExpectations(t)
		apiClient.AssertExpectations(t)
		// No IsAlertChannel call should be made for app users
		apiClient.AssertNotCalled(t, "IsAlertChannel")
	})

	t.Run("IsAlertChannel error returns early", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Errorf", mock.Anything, mock.Anything, mock.Anything).Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		apiClient := &mockSlackAPIClient{}
		apiClient.On("GetUserInfo", mock.Anything, "U12345").Return(&slack.User{
			ID:       "U12345",
			RealName: "Test User",
		}, nil).Once()
		apiClient.On("IsAlertChannel", mock.Anything, "C12345").Return(false, "", errors.New("api error")).Once()

		controller := &greetingsController{
			apiClient:       apiClient,
			logger:          logger,
			cfg:             newTestManagerConfig(),
			managerSettings: newTestManagerSettings(),
		}

		joinedEvent := &slackevents.MemberJoinedChannelEvent{
			User:    "U12345",
			Channel: "C12345",
		}
		innerEvent := slackevents.EventsAPIInnerEvent{
			Data: joinedEvent,
		}
		apiEvent := slackevents.EventsAPIEvent{
			InnerEvent: innerEvent,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeEventsAPI,
			Data:    apiEvent,
			Request: &req,
		}

		controller.memberJoinedChannel(context.Background(), evt, client)

		client.AssertExpectations(t)
		apiClient.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("logs when user joins unmanaged channel", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Info", "User joined un-managed channel").Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		apiClient := &mockSlackAPIClient{}
		apiClient.On("GetUserInfo", mock.Anything, "U12345").Return(&slack.User{
			ID:       "U12345",
			RealName: "Test User",
		}, nil).Once()
		apiClient.On("IsAlertChannel", mock.Anything, "C12345").Return(false, "", nil).Once()

		controller := &greetingsController{
			apiClient:       apiClient,
			logger:          logger,
			cfg:             newTestManagerConfig(),
			managerSettings: newTestManagerSettings(),
		}

		joinedEvent := &slackevents.MemberJoinedChannelEvent{
			User:    "U12345",
			Channel: "C12345",
		}
		innerEvent := slackevents.EventsAPIInnerEvent{
			Data: joinedEvent,
		}
		apiEvent := slackevents.EventsAPIEvent{
			InnerEvent: innerEvent,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeEventsAPI,
			Data:    apiEvent,
			Request: &req,
		}

		controller.memberJoinedChannel(context.Background(), evt, client)

		client.AssertExpectations(t)
		apiClient.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("logs when user joins managed channel and posts greeting", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Info", "User joined managed channel").Once()
		logger.On("Info", "Post ephemeral message").Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		apiClient := &mockSlackAPIClient{}
		apiClient.On("GetUserInfo", mock.Anything, "U12345").Return(&slack.User{
			ID:       "U12345",
			RealName: "Test User",
		}, nil).Once()
		apiClient.On("IsAlertChannel", mock.Anything, "C12345").Return(true, "", nil).Once()
		apiClient.On("UserIsInGroup", mock.Anything, mock.Anything, "U12345").Return(false).Maybe()
		apiClient.On("PostEphemeral", mock.Anything, "C12345", "U12345", mock.Anything).Return("", nil).Once()

		controller := &greetingsController{
			apiClient:       apiClient,
			logger:          logger,
			cfg:             newTestManagerConfig(),
			managerSettings: newTestManagerSettings(),
		}

		joinedEvent := &slackevents.MemberJoinedChannelEvent{
			User:    "U12345",
			Channel: "C12345",
		}
		innerEvent := slackevents.EventsAPIInnerEvent{
			Data: joinedEvent,
		}
		apiEvent := slackevents.EventsAPIEvent{
			InnerEvent: innerEvent,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeEventsAPI,
			Data:    apiEvent,
			Request: &req,
		}

		controller.memberJoinedChannel(context.Background(), evt, client)

		client.AssertExpectations(t)
		apiClient.AssertExpectations(t)
		logger.AssertExpectations(t)
	})
}

func TestGreetingsController_memberLeftChannel(t *testing.T) {
	t.Parallel()

	t.Run("failed to cast event returns early", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Error", "Failed to cast MemberLeftChannelEvent").Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		apiClient := &mockSlackAPIClient{}

		controller := &greetingsController{
			apiClient:       apiClient,
			logger:          logger,
			cfg:             newTestManagerConfig(),
			managerSettings: newTestManagerSettings(),
		}

		// Invalid inner event type
		innerEvent := slackevents.EventsAPIInnerEvent{
			Data: "not a MemberLeftChannelEvent",
		}
		apiEvent := slackevents.EventsAPIEvent{
			InnerEvent: innerEvent,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeEventsAPI,
			Data:    apiEvent,
			Request: &req,
		}

		controller.memberLeftChannel(context.Background(), evt, client)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
		apiClient.AssertNotCalled(t, "GetUserInfo")
	})

	t.Run("GetUserInfo error returns early", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Errorf", mock.Anything, mock.Anything).Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		apiClient := &mockSlackAPIClient{}
		apiClient.On("GetUserInfo", mock.Anything, "U12345").Return(nil, errors.New("api error")).Once()

		controller := &greetingsController{
			apiClient:       apiClient,
			logger:          logger,
			cfg:             newTestManagerConfig(),
			managerSettings: newTestManagerSettings(),
		}

		leftEvent := &slackevents.MemberLeftChannelEvent{
			User:    "U12345",
			Channel: "C12345",
		}
		innerEvent := slackevents.EventsAPIInnerEvent{
			Data: leftEvent,
		}
		apiEvent := slackevents.EventsAPIEvent{
			InnerEvent: innerEvent,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeEventsAPI,
			Data:    apiEvent,
			Request: &req,
		}

		controller.memberLeftChannel(context.Background(), evt, client)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
		apiClient.AssertExpectations(t)
	})

	t.Run("bot user returns early", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		apiClient := &mockSlackAPIClient{}
		apiClient.On("GetUserInfo", mock.Anything, "U12345").Return(&slack.User{
			ID:       "U12345",
			RealName: "Bot User",
			IsBot:    true,
		}, nil).Once()

		controller := &greetingsController{
			apiClient:       apiClient,
			logger:          logger,
			cfg:             newTestManagerConfig(),
			managerSettings: newTestManagerSettings(),
		}

		leftEvent := &slackevents.MemberLeftChannelEvent{
			User:    "U12345",
			Channel: "C12345",
		}
		innerEvent := slackevents.EventsAPIInnerEvent{
			Data: leftEvent,
		}
		apiEvent := slackevents.EventsAPIEvent{
			InnerEvent: innerEvent,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeEventsAPI,
			Data:    apiEvent,
			Request: &req,
		}

		controller.memberLeftChannel(context.Background(), evt, client)

		client.AssertExpectations(t)
		apiClient.AssertExpectations(t)
		// No IsAlertChannel call should be made for bots
		apiClient.AssertNotCalled(t, "IsAlertChannel")
	})

	t.Run("IsAlertChannel error logs and returns", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Errorf", mock.Anything, mock.Anything, mock.Anything).Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		apiClient := &mockSlackAPIClient{}
		apiClient.On("GetUserInfo", mock.Anything, "U12345").Return(&slack.User{
			ID:       "U12345",
			RealName: "Test User",
		}, nil).Once()
		apiClient.On("IsAlertChannel", mock.Anything, "C12345").Return(false, "", errors.New("api error")).Once()

		controller := &greetingsController{
			apiClient:       apiClient,
			logger:          logger,
			cfg:             newTestManagerConfig(),
			managerSettings: newTestManagerSettings(),
		}

		leftEvent := &slackevents.MemberLeftChannelEvent{
			User:    "U12345",
			Channel: "C12345",
		}
		innerEvent := slackevents.EventsAPIInnerEvent{
			Data: leftEvent,
		}
		apiEvent := slackevents.EventsAPIEvent{
			InnerEvent: innerEvent,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeEventsAPI,
			Data:    apiEvent,
			Request: &req,
		}

		controller.memberLeftChannel(context.Background(), evt, client)

		client.AssertExpectations(t)
		apiClient.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("logs when user left managed channel", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Info", "User left managed channel").Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		apiClient := &mockSlackAPIClient{}
		apiClient.On("GetUserInfo", mock.Anything, "U12345").Return(&slack.User{
			ID:       "U12345",
			RealName: "Test User",
		}, nil).Once()
		apiClient.On("IsAlertChannel", mock.Anything, "C12345").Return(true, "", nil).Once()

		controller := &greetingsController{
			apiClient:       apiClient,
			logger:          logger,
			cfg:             newTestManagerConfig(),
			managerSettings: newTestManagerSettings(),
		}

		leftEvent := &slackevents.MemberLeftChannelEvent{
			User:    "U12345",
			Channel: "C12345",
		}
		innerEvent := slackevents.EventsAPIInnerEvent{
			Data: leftEvent,
		}
		apiEvent := slackevents.EventsAPIEvent{
			InnerEvent: innerEvent,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeEventsAPI,
			Data:    apiEvent,
			Request: &req,
		}

		controller.memberLeftChannel(context.Background(), evt, client)

		client.AssertExpectations(t)
		apiClient.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("logs debug when user left unmanaged channel", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Debug", "User left un-managed channel").Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		apiClient := &mockSlackAPIClient{}
		apiClient.On("GetUserInfo", mock.Anything, "U12345").Return(&slack.User{
			ID:       "U12345",
			RealName: "Test User",
		}, nil).Once()
		apiClient.On("IsAlertChannel", mock.Anything, "C12345").Return(false, "", nil).Once()

		controller := &greetingsController{
			apiClient:       apiClient,
			logger:          logger,
			cfg:             newTestManagerConfig(),
			managerSettings: newTestManagerSettings(),
		}

		leftEvent := &slackevents.MemberLeftChannelEvent{
			User:    "U12345",
			Channel: "C12345",
		}
		innerEvent := slackevents.EventsAPIInnerEvent{
			Data: leftEvent,
		}
		apiEvent := slackevents.EventsAPIEvent{
			InnerEvent: innerEvent,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeEventsAPI,
			Data:    apiEvent,
			Request: &req,
		}

		controller.memberLeftChannel(context.Background(), evt, client)

		client.AssertExpectations(t)
		apiClient.AssertExpectations(t)
		logger.AssertExpectations(t)
	})
}
