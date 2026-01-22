package controllers

import (
	"context"
	"testing"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
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
		client.On("Ack", req, []any(nil)).Once()

		controller := &interactiveController{
			logger: logger,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeInteractive,
			Data:    "not an InteractionCallback",
			Request: &req,
		}

		controller.globalShortcutHandler(context.Background(), evt, client)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("unknown callback ID logs error", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Debug", "Interactive event").Once()
		logger.On("Error", "Unknown callback ID in interactive event").Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		controller := &interactiveController{
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

		controller.globalShortcutHandler(context.Background(), evt, client)

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
		client.On("Ack", req, []any(nil)).Once()

		controller := &interactiveController{
			logger: logger,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeInteractive,
			Data:    "not an InteractionCallback",
			Request: &req,
		}

		controller.messageActionHandler(context.Background(), evt, client)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("unknown callback ID logs error", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Debug", "Interactive event").Once()
		logger.On("Error", "Unknown callback ID in interactive event").Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		controller := &interactiveController{
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

		controller.messageActionHandler(context.Background(), evt, client)

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
		client.On("Ack", req, []any(nil)).Once()

		controller := &interactiveController{
			logger: logger,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeInteractive,
			Data:    "not an InteractionCallback",
			Request: &req,
		}

		controller.viewSubmissionHandler(context.Background(), evt, client)

		client.AssertExpectations(t)
		logger.AssertExpectations(t)
	})

	t.Run("unknown view callback ID logs error", func(t *testing.T) {
		t.Parallel()

		logger := &mockLogger{}
		logger.On("Debug", "Interactive event").Once()
		logger.On("Error", "Unknown callback ID in interactive event").Once()

		client := newMockSocketModeClient()
		req := socketmode.Request{EnvelopeID: "test-envelope"}
		client.On("Ack", req, []any(nil)).Once()

		controller := &interactiveController{
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

		controller.viewSubmissionHandler(context.Background(), evt, client)

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
		client.On("Ack", req, []any(nil)).Once()

		controller := &interactiveController{
			logger: logger,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeInteractive,
			Data:    "not an InteractionCallback",
			Request: &req,
		}

		controller.blockActionsHandler(context.Background(), evt, client)

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
		client.On("Ack", req, []any(nil)).Once()

		controller := &interactiveController{
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

		controller.blockActionsHandler(context.Background(), evt, client)

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
		client.On("Ack", req, []any(nil)).Once()

		controller := &interactiveController{
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

		controller.blockActionsHandler(context.Background(), evt, client)

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
		client.On("Ack", req, []any(nil)).Once()

		controller := &interactiveController{
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

		controller.blockActionsHandler(context.Background(), evt, client)

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
		client.On("Ack", req, []any(nil)).Once()

		controller := &interactiveController{
			logger: logger,
		}

		evt := &socketmode.Event{
			Type:    socketmode.EventTypeInteractive,
			Data:    "not an InteractionCallback",
			Request: &req,
		}

		controller.defaultInteractiveHandler(context.Background(), evt, client)

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
		client.On("Ack", req, []any(nil)).Once()

		controller := &interactiveController{
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

		controller.defaultInteractiveHandler(context.Background(), evt, client)

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
