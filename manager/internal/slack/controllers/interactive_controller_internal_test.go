package controllers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
	"github.com/slackmgr/core/manager/internal/models"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestInteractiveController_globalShortcutHandler(t *testing.T) {
	t.Parallel()

	t.Run("failed to cast interaction returns early", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Errorf", mock.Anything, mock.Anything).Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", &req).Once()

		controller := &interactiveController{
			clt:    client,
			logger: logger,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeInteractive,
			Data:    "not an InteractionCallback",
			Request: &req,
		}

		controller.globalShortcutHandler(context.Background(), evt)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("unknown callback ID logs error", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Debug", "Interactive event").Once()
		logger.On("Errorf", "Unknown callback ID '%s' in global shortcut event", []any{"unknown_callback"}).Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", &req).Once()

		controller := &interactiveController{
			clt:    client,
			logger: logger,
		}

		interaction := slack.InteractionCallback{
			Type:       slack.InteractionTypeShortcut,
			CallbackID: "unknown_callback",
			Channel: slack.Channel{
				GroupConversation: slack.GroupConversation{
					Conversation: slack.Conversation{
						ID: "C12345",
					},
				},
			},
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeInteractive,
			Data:    interaction,
			Request: &req,
		}

		controller.globalShortcutHandler(context.Background(), evt)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})
}

func TestInteractiveController_messageActionHandler(t *testing.T) {
	t.Parallel()

	t.Run("failed to cast interaction returns early", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Errorf", mock.Anything, mock.Anything).Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", &req).Once()

		controller := &interactiveController{
			clt:    client,
			logger: logger,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeInteractive,
			Data:    "not an InteractionCallback",
			Request: &req,
		}

		controller.messageActionHandler(context.Background(), evt)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("unknown callback ID logs error", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Debug", "Interactive event").Once()
		logger.On("Errorf", "Unknown callback ID '%s' in message action event", []any{"unknown_callback"}).Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", &req).Once()

		controller := &interactiveController{
			clt:    client,
			logger: logger,
		}

		interaction := slack.InteractionCallback{
			Type:       slack.InteractionTypeMessageAction,
			CallbackID: "unknown_callback",
			Channel: slack.Channel{
				GroupConversation: slack.GroupConversation{
					Conversation: slack.Conversation{
						ID: "C12345",
					},
				},
			},
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeInteractive,
			Data:    interaction,
			Request: &req,
		}

		controller.messageActionHandler(context.Background(), evt)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})
}

func TestInteractiveController_viewSubmissionHandler(t *testing.T) {
	t.Parallel()

	t.Run("failed to cast interaction returns early", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Errorf", mock.Anything, mock.Anything).Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", &req).Once()

		controller := &interactiveController{
			clt:    client,
			logger: logger,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeInteractive,
			Data:    "not an InteractionCallback",
			Request: &req,
		}

		controller.viewSubmissionHandler(context.Background(), evt)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("unknown view callback ID logs error", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Debug", "Interactive event").Once()
		logger.On("Errorf", "Unknown callback ID '%s' in view submission event", []any{"unknown_callback"}).Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", &req).Once()

		controller := &interactiveController{
			clt:    client,
			logger: logger,
		}

		interaction := slack.InteractionCallback{
			Type: slack.InteractionTypeViewSubmission,
			View: slack.View{
				CallbackID: "unknown_callback",
			},
			Channel: slack.Channel{
				GroupConversation: slack.GroupConversation{
					Conversation: slack.Conversation{
						ID: "C12345",
					},
				},
			},
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeInteractive,
			Data:    interaction,
			Request: &req,
		}

		controller.viewSubmissionHandler(context.Background(), evt)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})
}

func TestInteractiveController_blockActionsHandler(t *testing.T) {
	t.Parallel()

	t.Run("failed to cast interaction returns early", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Errorf", mock.Anything, mock.Anything).Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", &req).Once()

		controller := &interactiveController{
			clt:    client,
			logger: logger,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeInteractive,
			Data:    "not an InteractionCallback",
			Request: &req,
		}

		controller.blockActionsHandler(context.Background(), evt)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("no block actions logs error", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Debug", "Interactive event").Once()
		logger.On("Error", "Block actions callback without any actions").Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", &req).Once()

		controller := &interactiveController{
			clt:    client,
			logger: logger,
		}

		interaction := slack.InteractionCallback{
			Type: slack.InteractionTypeBlockActions,
			ActionCallback: slack.ActionCallbacks{
				BlockActions: []*slack.BlockAction{},
			},
			Channel: slack.Channel{
				GroupConversation: slack.GroupConversation{
					Conversation: slack.Conversation{
						ID: "C12345",
					},
				},
			},
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeInteractive,
			Data:    interaction,
			Request: &req,
		}

		controller.blockActionsHandler(context.Background(), evt)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("multiple block actions logs error", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Debug", "Interactive event").Once()
		logger.On("Error", "Block actions callback with more than one action").Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", &req).Once()

		controller := &interactiveController{
			clt:    client,
			logger: logger,
		}

		interaction := slack.InteractionCallback{
			Type: slack.InteractionTypeBlockActions,
			ActionCallback: slack.ActionCallbacks{
				BlockActions: []*slack.BlockAction{
					{ActionID: "action1"},
					{ActionID: "action2"},
				},
			},
			Channel: slack.Channel{
				GroupConversation: slack.GroupConversation{
					Conversation: slack.Conversation{
						ID: "C12345",
					},
				},
			},
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeInteractive,
			Data:    interaction,
			Request: &req,
		}

		controller.blockActionsHandler(context.Background(), evt)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("unknown action ID logs error", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Debug", "Interactive event").Once()
		logger.On("Errorf", mock.Anything, mock.Anything).Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", &req).Once()

		controller := &interactiveController{
			clt:    client,
			logger: logger,
		}

		interaction := slack.InteractionCallback{
			Type: slack.InteractionTypeBlockActions,
			ActionCallback: slack.ActionCallbacks{
				BlockActions: []*slack.BlockAction{
					{ActionID: "unknown_action"},
				},
			},
			Channel: slack.Channel{
				GroupConversation: slack.GroupConversation{
					Conversation: slack.Conversation{
						ID: "C12345",
					},
				},
			},
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeInteractive,
			Data:    interaction,
			Request: &req,
		}

		controller.blockActionsHandler(context.Background(), evt)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})
}

func TestInteractiveController_defaultInteractiveHandler(t *testing.T) {
	t.Parallel()

	t.Run("failed to cast interaction returns early", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Errorf", mock.Anything, mock.Anything).Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", &req).Once()

		controller := &interactiveController{
			clt:    client,
			logger: logger,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeInteractive,
			Data:    "not an InteractionCallback",
			Request: &req,
		}

		controller.defaultInteractiveHandler(context.Background(), evt)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("logs unhandled interactive event", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Debug", "Interactive event").Once()
		logger.On("Error", "Unhandled interactive event").Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", &req).Once()

		controller := &interactiveController{
			clt:    client,
			logger: logger,
		}

		interaction := slack.InteractionCallback{
			Type: "some_unknown_type",
			Channel: slack.Channel{
				GroupConversation: slack.GroupConversation{
					Conversation: slack.Conversation{
						ID: "C12345",
					},
				},
			},
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeInteractive,
			Data:    interaction,
			Request: &req,
		}

		controller.defaultInteractiveHandler(context.Background(), evt)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})
}

func TestGetInteractionAndLoggerFromEvent(t *testing.T) {
	t.Parallel()

	t.Run("returns error for invalid data type", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}

		req := socketmode.Request{EnvelopeID: "test-envelope"}
		evt := &socketmode.Event{
			Type:    socketmode.EventTypeInteractive,
			Data:    "not an InteractionCallback",
			Request: &req,
		}

		_, _, err := getInteractionAndLoggerFromEvent(evt, logger)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to cast InteractionCallback")
	})

	t.Run("returns interaction and logger for valid data", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Debug", "Interactive event").Once()

		req := socketmode.Request{EnvelopeID: "test-envelope"}
		interaction := slack.InteractionCallback{
			Type:       slack.InteractionTypeShortcut,
			CallbackID: "test_callback",
			Channel: slack.Channel{
				GroupConversation: slack.GroupConversation{
					Conversation: slack.Conversation{
						ID: "C12345",
					},
				},
			},
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeInteractive,
			Data:    interaction,
			Request: &req,
		}

		result, _, err := getInteractionAndLoggerFromEvent(evt, logger)

		require.NoError(t, err)
		assert.Equal(t, slack.InteractionTypeShortcut, result.Type)
		assert.Equal(t, "test_callback", result.CallbackID)
		logger.AssertExpectations(t)
	})
}

func TestGetViewStateValue(t *testing.T) {
	t.Parallel()

	t.Run("returns nil for nil state", func(t *testing.T) {
		t.Parallel()

		view := slack.View{
			State: nil,
		}

		result := getViewStateValue(view, "key1", "key2")

		assert.Nil(t, result)
	})

	t.Run("returns nil for nil values", func(t *testing.T) {
		t.Parallel()

		view := slack.View{
			State: &slack.ViewState{
				Values: nil,
			},
		}

		result := getViewStateValue(view, "key1", "key2")

		assert.Nil(t, result)
	})

	t.Run("returns nil for missing key1", func(t *testing.T) {
		t.Parallel()

		view := slack.View{
			State: &slack.ViewState{
				Values: map[string]map[string]slack.BlockAction{
					"other_key": {},
				},
			},
		}

		result := getViewStateValue(view, "key1", "key2")

		assert.Nil(t, result)
	})

	t.Run("returns nil for missing key2", func(t *testing.T) {
		t.Parallel()

		view := slack.View{
			State: &slack.ViewState{
				Values: map[string]map[string]slack.BlockAction{
					"key1": {
						"other_key": {},
					},
				},
			},
		}

		result := getViewStateValue(view, "key1", "key2")

		assert.Nil(t, result)
	})

	t.Run("returns value for valid keys", func(t *testing.T) {
		t.Parallel()

		expectedAction := slack.BlockAction{
			Value: "test_value",
		}

		view := slack.View{
			State: &slack.ViewState{
				Values: map[string]map[string]slack.BlockAction{
					"key1": {
						"key2": expectedAction,
					},
				},
			},
		}

		result := getViewStateValue(view, "key1", "key2")

		require.NotNil(t, result)
		assert.Equal(t, "test_value", result.Value)
	})
}

func TestGetInputValue(t *testing.T) {
	t.Parallel()

	t.Run("returns error for missing state", func(t *testing.T) {
		t.Parallel()

		interaction := slack.InteractionCallback{
			View: slack.View{
				State: nil,
			},
		}

		_, err := getInputValue(interaction, "key1", "key2")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing state value")
	})

	t.Run("returns value for valid keys", func(t *testing.T) {
		t.Parallel()

		interaction := slack.InteractionCallback{
			View: slack.View{
				State: &slack.ViewState{
					Values: map[string]map[string]slack.BlockAction{
						"key1": {
							"key2": {
								Value: "test_value",
							},
						},
					},
				},
			},
		}

		result, err := getInputValue(interaction, "key1", "key2")

		require.NoError(t, err)
		assert.Equal(t, "test_value", result)
	})
}

func TestGetSelectedValue(t *testing.T) {
	t.Parallel()

	t.Run("returns error for missing state", func(t *testing.T) {
		t.Parallel()

		interaction := slack.InteractionCallback{
			View: slack.View{
				State: nil,
			},
		}

		_, err := getSelectedValue(interaction, "key1", "key2")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing state value")
	})

	t.Run("returns selected conversation", func(t *testing.T) {
		t.Parallel()

		interaction := slack.InteractionCallback{
			View: slack.View{
				State: &slack.ViewState{
					Values: map[string]map[string]slack.BlockAction{
						"key1": {
							"key2": {
								SelectedConversation: "C12345",
							},
						},
					},
				},
			},
		}

		result, err := getSelectedValue(interaction, "key1", "key2")

		require.NoError(t, err)
		assert.Equal(t, "C12345", result)
	})

	t.Run("returns selected option value", func(t *testing.T) {
		t.Parallel()

		interaction := slack.InteractionCallback{
			View: slack.View{
				State: &slack.ViewState{
					Values: map[string]map[string]slack.BlockAction{
						"key1": {
							"key2": {
								SelectedOption: slack.OptionBlockObject{
									Value: "option_value",
								},
							},
						},
					},
				},
			},
		}

		result, err := getSelectedValue(interaction, "key1", "key2")

		require.NoError(t, err)
		assert.Equal(t, "option_value", result)
	})

	t.Run("returns error when no selection found", func(t *testing.T) {
		t.Parallel()

		interaction := slack.InteractionCallback{
			View: slack.View{
				State: &slack.ViewState{
					Values: map[string]map[string]slack.BlockAction{
						"key1": {
							"key2": {}, // No selection
						},
					},
				},
			},
		}

		_, err := getSelectedValue(interaction, "key1", "key2")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing selected value")
	})
}

func TestGetCheckboxSelected(t *testing.T) {
	t.Parallel()

	t.Run("returns error for missing state", func(t *testing.T) {
		t.Parallel()

		interaction := slack.InteractionCallback{
			View: slack.View{
				State: nil,
			},
		}

		_, err := getCheckboxSelected(interaction, "key1", "key2", "value")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing state value")
	})

	t.Run("returns true when value is selected", func(t *testing.T) {
		t.Parallel()

		interaction := slack.InteractionCallback{
			View: slack.View{
				State: &slack.ViewState{
					Values: map[string]map[string]slack.BlockAction{
						"key1": {
							"key2": {
								SelectedOptions: []slack.OptionBlockObject{
									{Value: "value1"},
									{Value: "value2"},
								},
							},
						},
					},
				},
			},
		}

		result, err := getCheckboxSelected(interaction, "key1", "key2", "value1")

		require.NoError(t, err)
		assert.True(t, result)
	})

	t.Run("returns false when value is not selected", func(t *testing.T) {
		t.Parallel()

		interaction := slack.InteractionCallback{
			View: slack.View{
				State: &slack.ViewState{
					Values: map[string]map[string]slack.BlockAction{
						"key1": {
							"key2": {
								SelectedOptions: []slack.OptionBlockObject{
									{Value: "other_value"},
								},
							},
						},
					},
				},
			},
		}

		result, err := getCheckboxSelected(interaction, "key1", "key2", "value")

		require.NoError(t, err)
		assert.False(t, result)
	})
}

func TestPrivateModalMetadata(t *testing.T) {
	t.Parallel()

	t.Run("Set and Get", func(t *testing.T) {
		t.Parallel()

		metadata := newPrivateModalMetadata()
		metadata.Set("key1", "value1")
		metadata.Set("key2", "value2")

		assert.Equal(t, "value1", metadata.Get("key1"))
		assert.Equal(t, "value2", metadata.Get("key2"))
		assert.Empty(t, metadata.Get("nonexistent"))
	})

	t.Run("ToJSON", func(t *testing.T) {
		t.Parallel()

		metadata := newPrivateModalMetadata()
		metadata.Set("key", "value")

		jsonStr, err := metadata.ToJSON()

		require.NoError(t, err)
		assert.Contains(t, jsonStr, "key")
		assert.Contains(t, jsonStr, "value")
	})

	t.Run("Get returns empty for nil values", func(t *testing.T) {
		t.Parallel()

		metadata := &privateModalMetadata{Values: nil}

		result := metadata.Get("key")

		assert.Empty(t, result)
	})

	t.Run("Set initializes nil values", func(t *testing.T) {
		t.Parallel()

		metadata := &privateModalMetadata{Values: nil}
		metadata.Set("key", "value")

		assert.Equal(t, "value", metadata.Get("key"))
	})

	t.Run("method chaining", func(t *testing.T) {
		t.Parallel()

		metadata := newPrivateModalMetadata().Set("key1", "value1").Set("key2", "value2")

		assert.Equal(t, "value1", metadata.Get("key1"))
		assert.Equal(t, "value2", metadata.Get("key2"))
	})
}

func TestGetViewStatePlainTextValues(t *testing.T) {
	t.Parallel()

	t.Run("returns empty map for nil state", func(t *testing.T) {
		t.Parallel()

		view := slack.View{State: nil}

		result := getViewStatePlainTextValues(view)

		assert.Empty(t, result)
	})

	t.Run("returns values for plain_text_input types", func(t *testing.T) {
		t.Parallel()

		view := slack.View{
			State: &slack.ViewState{
				Values: map[string]map[string]slack.BlockAction{
					"block1": {
						"input1": {
							Type:  slack.ActionType(slack.METPlainTextInput),
							Value: "value1",
						},
						"input2": {
							Type:  slack.ActionType(slack.METCheckboxGroups),
							Value: "should_be_ignored",
						},
					},
					"block2": {
						"input3": {
							Type:  slack.ActionType(slack.METPlainTextInput),
							Value: "value3",
						},
					},
				},
			},
		}

		result := getViewStatePlainTextValues(view)

		assert.Len(t, result, 2)
		assert.Equal(t, "value1", result["input1"])
		assert.Equal(t, "value3", result["input3"])
	})
}

func TestGetViewStateCheckboxSelectedValues(t *testing.T) {
	t.Parallel()

	t.Run("returns empty map for nil state", func(t *testing.T) {
		t.Parallel()

		view := slack.View{State: nil}

		result := getViewStateCheckboxSelectedValues(view)

		assert.Empty(t, result)
	})

	t.Run("returns values for checkbox types", func(t *testing.T) {
		t.Parallel()

		view := slack.View{
			State: &slack.ViewState{
				Values: map[string]map[string]slack.BlockAction{
					"block1": {
						"checkbox1": {
							Type: slack.ActionType(slack.METCheckboxGroups),
							SelectedOptions: []slack.OptionBlockObject{
								{Value: "option1"},
								{Value: "option2"},
							},
						},
						"input1": {
							Type:  slack.ActionType(slack.METPlainTextInput),
							Value: "should_be_ignored",
						},
					},
				},
			},
		}

		result := getViewStateCheckboxSelectedValues(view)

		assert.Len(t, result, 1)
		assert.ElementsMatch(t, []string{"option1", "option2"}, result["checkbox1"])
	})
}

func TestInteractiveController_parsePrivateModalMetadata(t *testing.T) {
	t.Parallel()

	t.Run("parses valid JSON", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}

		controller := &interactiveController{
			logger: logger,
		}

		metadata := controller.parsePrivateModalMetadata(`{"values":{"key1":"value1","key2":"value2"}}`)

		assert.Equal(t, "value1", metadata.Get("key1"))
		assert.Equal(t, "value2", metadata.Get("key2"))
	})

	t.Run("logs error for invalid JSON", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Errorf", mock.Anything, mock.Anything).Once()

		controller := &interactiveController{
			logger: logger,
		}

		metadata := controller.parsePrivateModalMetadata("invalid json")

		assert.Empty(t, metadata.Get("key1"))
		logger.AssertExpectations(t)
	})
}

func TestInteractiveController_handleWebhookRequest(t *testing.T) {
	t.Parallel()

	const (
		channelID   = "C12345"
		messageTs   = "1700000000.000100"
		userID      = "U12345"
		userName    = "Test User"
		webhookID   = "deploy"
		actionID    = WebhookActionIDPrefix + "_deploy"
		responseURL = "https://hooks.slack.com/response"
	)

	// newIssueWithWebhook builds a minimal issue containing a single webhook with the given properties.
	newIssueWithWebhook := func(webhook *types.Webhook) *models.Issue {
		alert := &models.Alert{
			Alert: types.Alert{
				SlackChannelID: channelID,
				Webhooks:       []*types.Webhook{webhook},
			},
		}
		return &models.Issue{LastAlert: alert}
	}

	newInteraction := func() slack.InteractionCallback {
		return slack.InteractionCallback{
			User:        slack.User{ID: userID},
			TriggerID:   "trigger-123",
			ResponseURL: responseURL,
			Channel: slack.Channel{
				GroupConversation: slack.GroupConversation{
					Conversation: slack.Conversation{ID: channelID},
				},
			},
			Message: slack.Message{
				Msg: slack.Msg{Timestamp: messageTs},
			},
		}
	}

	t.Run("skip confirmation dialog dispatches command without opening modal", func(t *testing.T) {
		t.Parallel()

		webhook := &types.Webhook{
			ID:                     webhookID,
			URL:                    "https://example.com/hook",
			AccessLevel:            types.WebhookAccessLevelChannelMembers,
			SkipConfirmationDialog: true,
		}
		issue := newIssueWithWebhook(webhook)

		issueFinder := &mockIssueFinder{}
		issueFinder.On("FindIssueBySlackPost", mock.Anything, channelID, messageTs, false).Return(issue).Once()

		apiClient := &mockSlackAPIClient{}
		apiClient.On("GetUserInfo", mock.Anything, userID).Return(&slack.User{ID: userID, RealName: userName}, nil).Once()

		queue := &mockFifoQueueProducer{}
		queue.On("Send", mock.Anything, channelID, mock.Anything, mock.MatchedBy(func(body string) bool {
			return assert.Contains(t, body, `"action":"webhook"`) &&
				assert.Contains(t, body, `"webhookId":"deploy"`)
		})).Return(nil).Once()

		logger := &mockLogger{}

		controller := &interactiveController{
			apiClient:       apiClient,
			commandQueue:    queue,
			issueFinder:     issueFinder,
			logger:          logger,
			managerSettings: newTestManagerSettings(),
			lastWebhook:     make(map[string]time.Time),
		}

		controller.handleWebhookRequest(context.Background(), newInteraction(), &slack.BlockAction{ActionID: actionID, Value: webhookID}, logger)

		issueFinder.AssertExpectations(t)
		apiClient.AssertExpectations(t)
		queue.AssertExpectations(t)
		logger.AssertExpectations(t)
		// The modal must NOT be opened in the skip path.
		apiClient.AssertNotCalled(t, "OpenModal", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("skip confirmation logs error when GetUserInfo fails and does not dispatch", func(t *testing.T) {
		t.Parallel()

		webhook := &types.Webhook{
			ID:                     webhookID,
			URL:                    "https://example.com/hook",
			AccessLevel:            types.WebhookAccessLevelChannelMembers,
			SkipConfirmationDialog: true,
		}
		issue := newIssueWithWebhook(webhook)

		issueFinder := &mockIssueFinder{}
		issueFinder.On("FindIssueBySlackPost", mock.Anything, channelID, messageTs, false).Return(issue).Once()

		apiClient := &mockSlackAPIClient{}
		apiClient.On("GetUserInfo", mock.Anything, userID).Return(nil, errors.New("slack down")).Once()

		queue := &mockFifoQueueProducer{}

		logger := &mockLogger{}
		logger.On("Errorf", "Failed to get user info: %s", mock.Anything).Once()

		controller := &interactiveController{
			apiClient:       apiClient,
			commandQueue:    queue,
			issueFinder:     issueFinder,
			logger:          logger,
			managerSettings: newTestManagerSettings(),
			lastWebhook:     make(map[string]time.Time),
		}

		controller.handleWebhookRequest(context.Background(), newInteraction(), &slack.BlockAction{ActionID: actionID, Value: webhookID}, logger)

		issueFinder.AssertExpectations(t)
		apiClient.AssertExpectations(t)
		logger.AssertExpectations(t)
		queue.AssertNotCalled(t, "Send", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("skip confirmation logs error when sendCommand fails", func(t *testing.T) {
		t.Parallel()

		webhook := &types.Webhook{
			ID:                     webhookID,
			URL:                    "https://example.com/hook",
			AccessLevel:            types.WebhookAccessLevelChannelMembers,
			SkipConfirmationDialog: true,
		}
		issue := newIssueWithWebhook(webhook)

		issueFinder := &mockIssueFinder{}
		issueFinder.On("FindIssueBySlackPost", mock.Anything, channelID, messageTs, false).Return(issue).Once()

		apiClient := &mockSlackAPIClient{}
		apiClient.On("GetUserInfo", mock.Anything, userID).Return(&slack.User{ID: userID, RealName: userName}, nil).Once()

		queue := &mockFifoQueueProducer{}
		queue.On("Send", mock.Anything, channelID, mock.Anything, mock.Anything).Return(errors.New("queue full")).Once()

		logger := &mockLogger{}
		logger.On("Errorf", "Failed to send command '%s': %s", mock.Anything).Once()

		controller := &interactiveController{
			apiClient:       apiClient,
			commandQueue:    queue,
			issueFinder:     issueFinder,
			logger:          logger,
			managerSettings: newTestManagerSettings(),
			lastWebhook:     make(map[string]time.Time),
		}

		controller.handleWebhookRequest(context.Background(), newInteraction(), &slack.BlockAction{ActionID: actionID, Value: webhookID}, logger)

		issueFinder.AssertExpectations(t)
		apiClient.AssertExpectations(t)
		queue.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("default path opens confirmation modal", func(t *testing.T) {
		t.Parallel()

		webhook := &types.Webhook{
			ID:          webhookID,
			URL:         "https://example.com/hook",
			AccessLevel: types.WebhookAccessLevelChannelMembers,
			// SkipConfirmationDialog: false (default)
		}
		issue := newIssueWithWebhook(webhook)

		issueFinder := &mockIssueFinder{}
		issueFinder.On("FindIssueBySlackPost", mock.Anything, channelID, messageTs, false).Return(issue).Once()

		apiClient := &mockSlackAPIClient{}
		apiClient.On("OpenModal", mock.Anything, "trigger-123", mock.MatchedBy(func(req slack.ModalViewRequest) bool {
			return req.CallbackID == ConfirmWebhookModal
		})).Return(nil).Once()

		queue := &mockFifoQueueProducer{}

		logger := &mockLogger{}

		controller := &interactiveController{
			apiClient:       apiClient,
			commandQueue:    queue,
			issueFinder:     issueFinder,
			logger:          logger,
			managerSettings: newTestManagerSettings(),
			lastWebhook:     make(map[string]time.Time),
		}

		controller.handleWebhookRequest(context.Background(), newInteraction(), &slack.BlockAction{ActionID: actionID, Value: webhookID}, logger)

		issueFinder.AssertExpectations(t)
		apiClient.AssertExpectations(t)
		logger.AssertExpectations(t)
		// Skip path should not run.
		apiClient.AssertNotCalled(t, "GetUserInfo", mock.Anything, mock.Anything)
		queue.AssertNotCalled(t, "Send", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("rapid double click is debounced and only dispatches once", func(t *testing.T) {
		t.Parallel()

		webhook := &types.Webhook{
			ID:                     webhookID,
			URL:                    "https://example.com/hook",
			AccessLevel:            types.WebhookAccessLevelChannelMembers,
			SkipConfirmationDialog: true,
		}
		issue := newIssueWithWebhook(webhook)

		issueFinder := &mockIssueFinder{}
		// FindIssueBySlackPost is called on every click — both pass through to it before debounce check.
		issueFinder.On("FindIssueBySlackPost", mock.Anything, channelID, messageTs, false).Return(issue).Twice()

		apiClient := &mockSlackAPIClient{}
		// GetUserInfo and Send must run exactly once — the second click is debounced before reaching them.
		apiClient.On("GetUserInfo", mock.Anything, userID).Return(&slack.User{ID: userID, RealName: userName}, nil).Once()

		queue := &mockFifoQueueProducer{}
		queue.On("Send", mock.Anything, channelID, mock.Anything, mock.Anything).Return(nil).Once()

		logger := &mockLogger{}

		controller := &interactiveController{
			apiClient:       apiClient,
			commandQueue:    queue,
			issueFinder:     issueFinder,
			logger:          logger,
			managerSettings: newTestManagerSettings(),
			lastWebhook:     make(map[string]time.Time),
		}

		// Two clicks back-to-back on the same webhook button.
		action := &slack.BlockAction{ActionID: actionID, Value: webhookID}
		controller.handleWebhookRequest(context.Background(), newInteraction(), action, logger)
		controller.handleWebhookRequest(context.Background(), newInteraction(), action, logger)

		issueFinder.AssertExpectations(t)
		apiClient.AssertExpectations(t)
		queue.AssertExpectations(t)
		logger.AssertExpectations(t)
	})
}

func TestInteractiveController_isWebhookClickAllowed(t *testing.T) {
	t.Parallel()

	const (
		channelID = "C12345"
		messageTs = "1700000000.000100"
		webhookID = "deploy"
	)

	t.Run("first click on new key is allowed and recorded", func(t *testing.T) {
		t.Parallel()

		controller := &interactiveController{
			lastWebhook: make(map[string]time.Time),
		}

		assert.True(t, controller.isWebhookClickAllowed(channelID, messageTs, webhookID))
		assert.Len(t, controller.lastWebhook, 1, "click should be recorded in the map")
	})

	t.Run("second click within debounce window is rejected", func(t *testing.T) {
		t.Parallel()

		controller := &interactiveController{
			lastWebhook: make(map[string]time.Time),
		}

		assert.True(t, controller.isWebhookClickAllowed(channelID, messageTs, webhookID))
		assert.False(t, controller.isWebhookClickAllowed(channelID, messageTs, webhookID))
	})

	t.Run("clicks on different webhooks are independent", func(t *testing.T) {
		t.Parallel()

		controller := &interactiveController{
			lastWebhook: make(map[string]time.Time),
		}

		assert.True(t, controller.isWebhookClickAllowed(channelID, messageTs, "deploy"))
		assert.True(t, controller.isWebhookClickAllowed(channelID, messageTs, "rollback"))
	})

	t.Run("clicks in different channels are independent", func(t *testing.T) {
		t.Parallel()

		controller := &interactiveController{
			lastWebhook: make(map[string]time.Time),
		}

		assert.True(t, controller.isWebhookClickAllowed("C111", messageTs, webhookID))
		assert.True(t, controller.isWebhookClickAllowed("C222", messageTs, webhookID))
	})

	t.Run("clicks on different message timestamps are independent", func(t *testing.T) {
		t.Parallel()

		controller := &interactiveController{
			lastWebhook: make(map[string]time.Time),
		}

		assert.True(t, controller.isWebhookClickAllowed(channelID, "1700000000.000100", webhookID))
		assert.True(t, controller.isWebhookClickAllowed(channelID, "1700000001.000200", webhookID))
	})

	t.Run("click is allowed again after debounce window has passed", func(t *testing.T) {
		t.Parallel()

		key := channelID + "::" + messageTs + "::" + webhookID
		controller := &interactiveController{
			lastWebhook: map[string]time.Time{
				// Pretend the last click happened well outside MinWebhookClickInterval (2s).
				key: time.Now().Add(-MinWebhookClickInterval - time.Second),
			},
		}

		assert.True(t, controller.isWebhookClickAllowed(channelID, messageTs, webhookID))
	})
}
