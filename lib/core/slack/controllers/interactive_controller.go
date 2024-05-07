package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/peteraglen/slack-manager/lib/client"
	"github.com/peteraglen/slack-manager/lib/common"
	"github.com/peteraglen/slack-manager/lib/core/config"
	"github.com/peteraglen/slack-manager/lib/core/models"
	"github.com/peteraglen/slack-manager/lib/core/slack/handler"
	"github.com/peteraglen/slack-manager/lib/core/slack/views"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

const (
	AlertActions         = ""
	CreateIssueAction    = "issue_create"
	CreateIssueModal     = "issue_create_modal"
	MoveIssueAction      = "issue_move"
	MoveIssueModal       = "issue_move_modal"
	ViewIssueAction      = "view_issue_details"
	ViewIssueActionModal = "view_issue_details_modal" // #nosec G101
	ConfirmWebhookModal  = "confirm_webhook_modal"
	WebhookActionID      = "webhook_action"
)

type PrivateModalMetadata struct {
	Values map[string]string `json:"values,omitempty"`
}

type InteractiveController struct {
	client         handler.SocketClient
	commandHandler common.FifoQueueProducer
	issueFinder    handler.IssueFinder
	logger         common.Logger
	conf           *config.Config
}

func NewInteractiveController(eventhandler *handler.SocketModeHandler, client handler.SocketClient, commandHandler common.FifoQueueProducer, issueFinder handler.IssueFinder, logger common.Logger, conf *config.Config) *InteractiveController {
	c := &InteractiveController{
		client:         client,
		commandHandler: commandHandler,
		issueFinder:    issueFinder,
		logger:         logger,
		conf:           conf,
	}

	// Global shortcuts
	eventhandler.HandleInteraction(slack.InteractionTypeShortcut, c.globalShortcutHandler)

	// Message action
	eventhandler.HandleInteraction(slack.InteractionTypeMessageAction, c.messageActionHandler)

	// View submission
	eventhandler.HandleInteraction(slack.InteractionTypeViewSubmission, c.viewSubmissionHandler)

	// Block actions (buttons etc)
	eventhandler.HandleInteraction(slack.InteractionTypeBlockActions, c.blockActionsHandler)

	// Every other interaction
	eventhandler.Handle(socketmode.EventTypeInteractive, c.defaultInteractiveHandler)

	return c
}

// globalShortcutHandler handles all incoming interaction messages of type 'shortcut'
func (c *InteractiveController) globalShortcutHandler(ctx context.Context, evt *socketmode.Event, clt *socketmode.Client) {
	interaction, logger := getInteractionAndLoggerFromEvent(evt, c.logger)

	// No need to customize the ack in this context, so we send it immediately
	ack(evt, clt)

	switch interaction.CallbackID {
	case CreateIssueAction:
		c.handleCreateIssueRequest(ctx, interaction, logger)
	default:
		logger.Error("Unknown callback ID in interactive event")
	}
}

// messageActionHandler handles all incoming interaction messages of type 'message_action'
func (c *InteractiveController) messageActionHandler(ctx context.Context, evt *socketmode.Event, clt *socketmode.Client) {
	interaction, logger := getInteractionAndLoggerFromEvent(evt, c.logger)

	// No need to customize the ack in this context, so we send it immediately
	ack(evt, clt)

	switch interaction.CallbackID {
	case MoveIssueAction:
		c.handleMoveIssueRequest(ctx, interaction, logger)
	case ViewIssueAction:
		c.handleViewIssueDetailsRequest(ctx, interaction, logger)
	default:
		logger.Error("Unknown callback ID in interactive event")
	}
}

// viewSubmissionHandler handles all incoming interaction messages of type 'view_submission'
func (c *InteractiveController) viewSubmissionHandler(ctx context.Context, evt *socketmode.Event, clt *socketmode.Client) {
	interaction, logger := getInteractionAndLoggerFromEvent(evt, c.logger)

	// We can't ack just yet, the ack response may need to be customized with an error message

	switch interaction.View.CallbackID {
	case MoveIssueModal:
		c.moveIssueViewSubmission(ctx, evt, clt, interaction, logger)
	case CreateIssueModal:
		c.createIssueViewSubmission(ctx, evt, clt, interaction, logger)
	case ConfirmWebhookModal:
		c.webhookViewSubmission(ctx, evt, clt, interaction, logger)
	default:
		ack(evt, clt)
		logger.Error("Unknown callback ID in interactive event")
	}
}

// blockActionsHandler handles all incoming interaction messages of type 'block_actions'
func (c *InteractiveController) blockActionsHandler(ctx context.Context, evt *socketmode.Event, clt *socketmode.Client) {
	interaction, logger := getInteractionAndLoggerFromEvent(evt, c.logger)

	// No need to customize the ack in this context, so we send it immediately
	ack(evt, clt)

	if len(interaction.ActionCallback.BlockActions) == 0 {
		logger.Error("Block actions callback without any actions")
		return
	}

	if len(interaction.ActionCallback.BlockActions) > 1 {
		logger.Error("Block actions callback with more than one action")
		return
	}

	actionID := interaction.ActionCallback.BlockActions[0].ActionID

	if strings.HasPrefix(actionID, WebhookActionID) {
		c.handleWebhookRequest(ctx, interaction, interaction.ActionCallback.BlockActions[0], logger)
	} else {
		logger.ErrorfUnlessContextCanceled("Unknown action ID %s in block action event", interaction.ActionCallback.BlockActions[0].ActionID)
	}
}

// defaultInteractiveHandler handles all incoming interaction messages not matched by any of the explicit handlers
func (c *InteractiveController) defaultInteractiveHandler(_ context.Context, evt *socketmode.Event, clt *socketmode.Client) {
	_, logger := getInteractionAndLoggerFromEvent(evt, c.logger)

	ack(evt, clt)

	logger.Error("Unhandled interactive event")
}

// handleMoveIssueRequest handles a request to move an issue from one channel to another. It is triggered by a message shortcut.
// It opens a modal view with options to move the issue, IF the user is allowed to perform this action.
// https://api.slack.com/interactivity/shortcuts/using#message_shortcuts
func (c *InteractiveController) handleMoveIssueRequest(ctx context.Context, interaction slack.InteractionCallback, logger common.Logger) {
	userIsGlobalAdmin := c.conf.UserIsGlobalAdmin(interaction.User.ID)

	if !userIsGlobalAdmin {
		if err := c.client.SendResponse(ctx, interaction.Channel.ID, interaction.ResponseURL, "ephemeral", "Sorry, but this feature is currently only available to SUDO Slack Manager global admins."); err != nil {
			logger.ErrorfUnlessContextCanceled("Failed to send interactive response: %s", err)
		}
		return
	}

	managedChannel, _, err := c.client.IsAlertChannel(ctx, interaction.Channel.ID)
	if err != nil {
		logger.ErrorfUnlessContextCanceled("Failed to verify if Slack manager is in channel: %s", err)
		return
	}

	if !managedChannel {
		if err := c.client.SendResponse(ctx, interaction.Channel.ID, interaction.ResponseURL, "ephemeral", "Sorry, but you can only move messages in channels managed by the SUDO Slack Manager."); err != nil {
			logger.ErrorfUnlessContextCanceled("Failed to send interactive response: %s", err)
		}
		return
	}

	metadata := newPrivateModalMetadata().Set("channelId", interaction.Channel.ID).Set("messageTs", interaction.Message.Timestamp).ToJSON()

	blocks, err := views.MoveIssueModal()
	if err != nil {
		logger.ErrorfUnlessContextCanceled("Failed to generate view: %s", err)
		return
	}

	request := slack.ModalViewRequest{
		Type:            "modal",
		CallbackID:      MoveIssueModal,
		Title:           slack.NewTextBlockObject("plain_text", "Move issue", false, false),
		NotifyOnClose:   false,
		Close:           slack.NewTextBlockObject("plain_text", "Cancel", false, false),
		Submit:          slack.NewTextBlockObject("plain_text", "Move", false, false),
		ClearOnClose:    true,
		Blocks:          blocks,
		PrivateMetadata: metadata,
	}

	if err := c.client.OpenModal(ctx, interaction.TriggerID, request); err != nil {
		logger.ErrorfUnlessContextCanceled("failed to open modal view for move issue action: %s", err)
	}
}

// moveIssueViewSubmission handles the callback from the modal move issue dialog (created by handleMoveIssueRequest).
// It dispatches an async command with info about the move request, IF the Slack manager app is in the receiving channel.
func (c *InteractiveController) moveIssueViewSubmission(ctx context.Context, evt *socketmode.Event, clt *socketmode.Client, interaction slack.InteractionCallback, logger common.Logger) {
	selectedConversation, err := getSelectedValue(interaction, "select_channel", "select_channel_input")
	if err != nil {
		ack(evt, clt)
		logger.Errorf("Move issue view failed: %s", err)
		return
	}

	if interaction.View.PrivateMetadata == "" {
		ack(evt, clt)
		logger.Error("Missing value in private_metadata field")
		return
	}

	// Check if the receiving channel is managed by the Slack manager
	isManagedChannel, _, err := c.client.IsAlertChannel(ctx, selectedConversation)
	if err != nil {
		ack(evt, clt)
		logger.ErrorfUnlessContextCanceled("Failed to verify if Slack manager is in channel: %s", err)
		return
	}

	// Receiving channel is not managed - send ack with error message
	if !isManagedChannel {
		ackWithFieldErrorMsg(evt, clt, "select_channel", "The channel must be managed by SUDO Slack Manager")
		return
	}

	// All is good - send normal ack and clear the modal view
	ackWithPayload(evt, clt, slack.NewClearViewSubmissionResponse())

	// Fetch infor about the user
	userInfo, err := c.client.GetUserInfo(ctx, interaction.User.ID)
	if err != nil {
		logger.ErrorfUnlessContextCanceled("Failed to get user info: %s", err)
		return
	}

	// Parse the incoming metadata
	metadata := c.parsePrivateModalMetadata(interaction.View.PrivateMetadata)

	params := map[string]interface{}{
		"targetChannelId": selectedConversation,
	}

	// Send an async command to move the issue
	action := models.CommandActionMoveIssue
	cmd := models.NewCommand(metadata.Get("channelId"), metadata.Get("messageTs"), "", userInfo.ID, userInfo.RealName, action, params)

	if err := sendCommand(ctx, c.commandHandler, cmd); err != nil {
		logger.ErrorfUnlessContextCanceled("Failed to send command '%s': %s", action, err)
	}
}

// handleCreateIssueRequest handles a request to create a new issue in the current channel. It is triggered by a global shortcut.
// It opens a modal view with options to create the issue.
func (c *InteractiveController) handleCreateIssueRequest(ctx context.Context, interaction slack.InteractionCallback, logger common.Logger) {
	blocks, err := views.CreateIssueModal()
	if err != nil {
		logger.ErrorfUnlessContextCanceled("Failed to generate view: %s", err)
		return
	}

	request := slack.ModalViewRequest{
		Type:          "modal",
		CallbackID:    CreateIssueModal,
		Title:         slack.NewTextBlockObject("plain_text", "Create issue", false, false),
		NotifyOnClose: false,
		Close:         slack.NewTextBlockObject("plain_text", "Cancel", false, false),
		Submit:        slack.NewTextBlockObject("plain_text", "Create", false, false),
		ClearOnClose:  true,
		Blocks:        blocks,
	}

	if err := c.client.OpenModal(ctx, interaction.TriggerID, request); err != nil {
		logger.ErrorfUnlessContextCanceled("failed to open modal view for move issue action: %s", err)
	}
}

// createIssueViewSubmission handles the callback from the modal create issue dialog (created by handleCreateIssueRequest).
// It dispatches an async command with info about the create request, IF the Slack manager app is in the receiving channel.
func (c *InteractiveController) createIssueViewSubmission(ctx context.Context, evt *socketmode.Event, clt *socketmode.Client, interaction slack.InteractionCallback, logger common.Logger) {
	targetChannelID, err := getSelectedValue(interaction, "select_channel", "select_channel_input")
	if err != nil {
		ack(evt, clt)
		logger.Errorf("Create issue view failed: %s", err)
		return
	}

	// Check if the receiving channel is managed by the Slack manager
	managedChannel, _, err := c.client.IsAlertChannel(ctx, targetChannelID)
	if err != nil {
		ack(evt, clt)
		logger.ErrorfUnlessContextCanceled("Failed to verify if Slack manager is in channel: %s", err)
		return
	}

	// Receiving channel is not managed - send ack with error message
	if !managedChannel {
		ackWithFieldErrorMsg(evt, clt, "select_channel", "The channel must be managed by SUDO Slack Manager")
		return
	}

	// Fetch infor about the user
	userInfo, err := c.client.GetUserInfo(ctx, interaction.User.ID)
	if err != nil {
		logger.ErrorfUnlessContextCanceled("Failed to get user info: %s", err)
		return
	}

	isAdminInTargetChannel := c.conf.UserIsChannelAdmin(ctx, targetChannelID, userInfo.ID, c.client.UserIsInGroup)

	if !isAdminInTargetChannel {
		ackWithFieldErrorMsg(evt, clt, "select_channel", "You need to be SUDO Slack manager admin in the selected channel")
		return
	}

	header, err := getInputValue(interaction, "issue_header", "issue_header_input")
	if err != nil {
		ack(evt, clt)
		logger.Errorf("Create issue view failed: %s", err)
		return
	}

	text, err := getInputValue(interaction, "issue_text", "issue_text_input")
	if err != nil {
		ack(evt, clt)
		logger.Errorf("Create issue view failed: %s", err)
		return
	}

	iconEmoji, err := getInputValue(interaction, "issue_emoji", "issue_emoji_input")
	if err != nil {
		ack(evt, clt)
		logger.Errorf("Create issue view failed: %s", err)
		return
	}

	if header == "" && text == "" {
		ackWithFieldErrorMsg(evt, clt, "issue_text", "Both header and text cannot be empty")
		return
	}

	severity, err := getSelectedValue(interaction, "issue_severity", "issue_severity_input")
	if err != nil {
		ack(evt, clt)
		logger.Errorf("Create issue view failed: %s", err)
		return
	}

	followUpEnabled, err := getCheckboxSelected(interaction, "issue_follow_up_enabled", "issue_follow_up_enabled_input", "enabled")
	if err != nil {
		ack(evt, clt)
		logger.Errorf("Create issue view failed: %s", err)
		return
	}

	autoResolveHoursString, err := getInputValue(interaction, "issue_auto_resolve", "issue_auto_resolve_input")
	if err != nil {
		ack(evt, clt)
		logger.Errorf("Create issue view failed: %s", err)
		return
	}

	autoResolveHours, err := strconv.Atoi(autoResolveHoursString)
	if err != nil || autoResolveHours < 0 || autoResolveHours > 8760 {
		ackWithFieldErrorMsg(evt, clt, "issue_auto_resolve", "Value must be integer 0-8760")
		return
	}

	// All is good - send normal ack and clear the modal view
	ackWithPayload(evt, clt, slack.NewClearViewSubmissionResponse())

	params := map[string]interface{}{
		"targetChannelId":    targetChannelID,
		"severity":           severity,
		"followUpEnabled":    followUpEnabled,
		"header":             header,
		"text":               text,
		"autoResolveSeconds": autoResolveHours * 3600,
		"iconEmoji":          iconEmoji,
	}

	// Send an async command to create the issue
	action := models.CommandActionCreateIssue
	cmd := models.NewCommand(targetChannelID, "", "", userInfo.ID, userInfo.RealName, action, params)

	if err := sendCommand(ctx, c.commandHandler, cmd); err != nil {
		logger.ErrorfUnlessContextCanceled("Failed to send command '%s': %s", action, err)
	}
}

// handleViewIssueDetailsRequest handles a request to view issue details. It is triggered by a message shortcut.
// It opens a modal view with the issue details.
func (c *InteractiveController) handleViewIssueDetailsRequest(ctx context.Context, interaction slack.InteractionCallback, logger common.Logger) {
	managedChannel, _, err := c.client.IsAlertChannel(ctx, interaction.Channel.ID)
	if err != nil {
		logger.ErrorfUnlessContextCanceled("Failed to verify if Slack manager is in channel: %s", err)
		return
	}

	if !managedChannel {
		if err := c.client.SendResponse(ctx, interaction.Channel.ID, interaction.ResponseURL, "ephemeral", "Sorry, but you can only view issue details in channels managed by the SUDO Slack Manager."); err != nil {
			logger.ErrorfUnlessContextCanceled("Failed to send interactive response: %s", err)
		}
		return
	}

	issue := c.issueFinder.FindIssueBySlackPost(ctx, interaction.Channel.ID, interaction.Message.Timestamp, true)

	if issue == nil {
		if err := c.client.SendResponse(ctx, interaction.Channel.ID, interaction.ResponseURL, "ephemeral", "Sorry, but no issue was found for this Slack message."); err != nil {
			logger.ErrorfUnlessContextCanceled("Failed to send interactive response: %s", err)
		}
		return
	}

	blocks, err := views.IssueDetailsAssets(issue, c.conf)
	if err != nil {
		logger.ErrorfUnlessContextCanceled("Failed to generate view: %s", err)
		return
	}

	request := slack.ModalViewRequest{
		Type:          "modal",
		CallbackID:    ViewIssueActionModal,
		Title:         slack.NewTextBlockObject("plain_text", "Issue details", false, false),
		NotifyOnClose: false,
		Close:         slack.NewTextBlockObject("plain_text", "Close", false, false),
		ClearOnClose:  true,
		Blocks:        blocks,
	}

	if err := c.client.OpenModal(ctx, interaction.TriggerID, request); err != nil {
		logger.ErrorfUnlessContextCanceled("failed to open modal view for issue details action: %s", err)
	}
}

// handleWebhookRequest handles a request to post to a webhook. It is triggered by an action button in a message.
// It opens a modal view IF the user is allowed to perform this action.
func (c *InteractiveController) handleWebhookRequest(ctx context.Context, interaction slack.InteractionCallback, action *slack.BlockAction, logger common.Logger) {
	issue := c.issueFinder.FindIssueBySlackPost(ctx, interaction.Channel.ID, interaction.Message.Timestamp, false)

	if issue == nil {
		if err := c.client.SendResponse(ctx, interaction.Channel.ID, interaction.ResponseURL, "ephemeral", "Sorry, but no active issue was found for this Slack message."); err != nil {
			logger.ErrorfUnlessContextCanceled("Failed to send interactive response: %s", err)
		}
		return
	}

	if len(issue.LastAlert.Webhooks) == 0 {
		if err := c.client.SendResponse(ctx, interaction.Channel.ID, interaction.ResponseURL, "ephemeral", "Sorry, but the issue doesn't have any webhooks."); err != nil {
			logger.ErrorfUnlessContextCanceled("Failed to send interactive response: %s", err)
		}
		return
	}

	var webhook *client.Webhook

	for _, hook := range issue.LastAlert.Webhooks {
		if hook.ID == action.Value {
			webhook = hook
			break
		}
	}

	if webhook == nil {
		if err := c.client.SendResponse(ctx, interaction.Channel.ID, interaction.ResponseURL, "ephemeral", fmt.Sprintf("Sorry, but the issue doesn't contain a webhook with ID '%s'.", action.Value)); err != nil {
			logger.ErrorfUnlessContextCanceled("Failed to send interactive response: %s", err)
		}
		return
	}

	if userHasAccess := c.verifyWebhookAccess(ctx, interaction, webhook, logger); !userHasAccess {
		return
	}

	metadata := newPrivateModalMetadata().
		Set("channelId", interaction.Channel.ID).
		Set("messageTs", interaction.Message.Timestamp).
		Set("webhookId", webhook.ID).ToJSON()

	request := slack.ModalViewRequest{
		Type:            "modal",
		CallbackID:      ConfirmWebhookModal,
		Title:           slack.NewTextBlockObject("plain_text", "Confirm webhook", false, false),
		NotifyOnClose:   false,
		Close:           slack.NewTextBlockObject("plain_text", "Cancel", false, false),
		Submit:          slack.NewTextBlockObject("plain_text", "Send", false, false),
		ClearOnClose:    true,
		Blocks:          slack.Blocks{BlockSet: views.WebhookInputView(webhook)},
		PrivateMetadata: metadata,
	}

	if err := c.client.OpenModal(ctx, interaction.TriggerID, request); err != nil {
		logger.ErrorfUnlessContextCanceled("failed to open modal view for confirm webhook action: %s", err)
	}
}

func (c *InteractiveController) verifyWebhookAccess(ctx context.Context, interaction slack.InteractionCallback, webhook *client.Webhook, logger common.Logger) bool {
	if webhook.AccessLevel == client.WebhookAccessLevelGlobalAdmins || webhook.AccessLevel == "" {
		userIsGlobalAdmin := c.conf.UserIsGlobalAdmin(interaction.User.ID)

		if !userIsGlobalAdmin {
			if err := c.client.SendResponse(ctx, interaction.Channel.ID, interaction.ResponseURL, "ephemeral", "Sorry, but this webhook is available only to SUDO Slack Manager global admins."); err != nil {
				logger.ErrorfUnlessContextCanceled("Failed to send interactive response: %s", err)
			}
			return false
		}

		return true
	}

	if webhook.AccessLevel == client.WebhookAccessLevelChannelAdmins {
		userIsChannelAdmin := c.conf.UserIsChannelAdmin(ctx, interaction.Channel.ID, interaction.User.ID, c.client.UserIsInGroup)

		if !userIsChannelAdmin {
			if err := c.client.SendResponse(ctx, interaction.Channel.ID, interaction.ResponseURL, "ephemeral", "Sorry, but this webhook is available only to channel admins and above."); err != nil {
				logger.ErrorfUnlessContextCanceled("Failed to send interactive response: %s", err)
			}
			return false
		}
	}

	return true
}

// webhookViewSubmission handles the callback from the modal confirm webhook dialog (created by handleWebhookRequest).
// It dispatches an async command with info about the webhook.
func (c *InteractiveController) webhookViewSubmission(ctx context.Context, evt *socketmode.Event, clt *socketmode.Client, interaction slack.InteractionCallback, logger common.Logger) {
	// Fetch info about the user
	userInfo, err := c.client.GetUserInfo(ctx, interaction.User.ID)
	if err != nil {
		logger.ErrorfUnlessContextCanceled("Failed to get user info: %s", err)
		return
	}

	// All is good - send normal ack and clear the modal view
	ackWithPayload(evt, clt, slack.NewClearViewSubmissionResponse())

	// Parse the incoming metadata
	metadata := c.parsePrivateModalMetadata(interaction.View.PrivateMetadata)

	input := getViewStatePlainTextValues(interaction.View)
	checkboxInput := getViewStateCheckboxSelectedValues(interaction.View)

	params := &models.WebhookCommandParams{
		WebhookID:     metadata.Get("webhookId"),
		Input:         input,
		CheckboxInput: checkboxInput,
	}

	// Send an async command to handle the webhook
	cmdAction := models.CommandActionWebhook
	cmd := models.NewCommand(metadata.Get("channelId"), metadata.Get("messageTs"), "", userInfo.ID, userInfo.RealName, cmdAction, nil)
	cmd.WebhookParameters = params

	if err := sendCommand(ctx, c.commandHandler, cmd); err != nil {
		logger.ErrorfUnlessContextCanceled("Failed to send command '%s': %s", cmdAction, err)
	}
}

func getInputValue(interaction slack.InteractionCallback, key1, key2 string) (string, error) {
	blockAction := getViewStateValue(interaction.View, key1, key2)

	if blockAction == nil {
		return "", fmt.Errorf("missing state value %s/%s", key1, key2)
	}

	return blockAction.Value, nil
}

func getSelectedValue(interaction slack.InteractionCallback, key1, key2 string) (string, error) {
	blockAction := getViewStateValue(interaction.View, key1, key2)

	if blockAction == nil {
		return "", fmt.Errorf("missing state value %s/%s", key1, key2)
	}

	if blockAction.SelectedConversation != "" {
		return blockAction.SelectedConversation, nil
	}

	if blockAction.SelectedOption.Value != "" {
		return blockAction.SelectedOption.Value, nil
	}

	return "", fmt.Errorf("missing selected value in %s/%s field", key1, key2)
}

func getCheckboxSelected(interaction slack.InteractionCallback, key1, key2, value string) (bool, error) {
	blockAction := getViewStateValue(interaction.View, key1, key2)

	if blockAction == nil {
		return false, fmt.Errorf("missing state value %s/%s", key1, key2)
	}

	for _, option := range blockAction.SelectedOptions {
		if option.Value == value {
			return true, nil
		}
	}

	return false, nil
}

// getViewStateValue gets a view state value, two levels down in the view state dictionary structure
func getViewStateValue(view slack.View, key1, key2 string) *slack.BlockAction {
	if view.State == nil || view.State.Values == nil {
		return nil
	}

	v1, ok := view.State.Values[key1]
	if !ok {
		return nil
	}

	v2, ok := v1[key2]
	if !ok {
		return nil
	}

	return &v2
}

// getViewStatePlainTextValues gets all plain_text_input values in the view state
func getViewStatePlainTextValues(view slack.View) map[string]string {
	values := make(map[string]string)

	if view.State == nil || view.State.Values == nil {
		return values
	}

	for _, innerValues := range view.State.Values {
		for key, value := range innerValues {
			if value.Type != slack.ActionType(slack.METPlainTextInput) {
				continue
			}

			values[key] = value.Value
		}
	}

	return values
}

// getViewStatePlainTextValues gets all plain_text_input values in the view state
func getViewStateCheckboxSelectedValues(view slack.View) map[string][]string {
	values := make(map[string][]string)

	if view.State == nil || view.State.Values == nil {
		return values
	}

	for _, innerValues := range view.State.Values {
		for key, value := range innerValues {
			if value.Type != slack.ActionType(slack.METCheckboxGroups) {
				continue
			}

			selectedOptions := []string{}

			for _, s := range value.SelectedOptions {
				selectedOptions = append(selectedOptions, s.Value)
			}

			values[key] = selectedOptions
		}
	}

	return values
}

// getInteractionAndLoggerFromEvent casts the event data to interaction callback, and creates a log entry with suitable fields
func getInteractionAndLoggerFromEvent(evt *socketmode.Event, logger common.Logger) (slack.InteractionCallback, common.Logger) {
	interaction := evt.Data.(slack.InteractionCallback)

	logger = logger.
		WithField("operation", "slack").
		WithField("event", evt.Type).
		WithField("envelope_id", evt.Request.EnvelopeID).
		WithField("type", interaction.Type).
		WithField("callback_id", interaction.CallbackID).
		WithField("slack_channel_id", interaction.Channel.ID)

	logger.Debug("Interactive event")

	return interaction, logger
}

// parsePrivateModalMetadata parses the private metadata object from a modal view
func (c *InteractiveController) parsePrivateModalMetadata(data string) PrivateModalMetadata {
	var p PrivateModalMetadata

	if err := json.Unmarshal([]byte(data), &p); err != nil {
		c.logger.ErrorfUnlessContextCanceled("Failed to unmarshal private modal metadata: %s", err)
	}

	return p
}

func newPrivateModalMetadata() *PrivateModalMetadata {
	return &PrivateModalMetadata{
		Values: make(map[string]string),
	}
}

func (p *PrivateModalMetadata) ToJSON() string {
	s, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}
	return string(s)
}

func (p *PrivateModalMetadata) Set(key, value string) *PrivateModalMetadata {
	if p.Values == nil {
		p.Values = make(map[string]string)
	}

	p.Values[key] = value

	return p
}

func (p *PrivateModalMetadata) Get(key string) string {
	if p.Values == nil {
		return ""
	}

	return p.Values[key]
}
