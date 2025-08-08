package slack

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/eko/gocache/lib/v4/store"
	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/peteraglen/slack-manager/internal/slackapi"
	"github.com/peteraglen/slack-manager/manager/internal/models"
	"github.com/peteraglen/slack-manager/manager/internal/slack/controllers"
	"github.com/peteraglen/slack-manager/manager/internal/slack/handler"
	slack "github.com/slack-go/slack"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

const (
	// PostIDInvalidChannel represents the post ID for issues with invalid channel ID
	PostIDInvalidChannel    = "INVALID_CHANNEL_ID"
	SlackErrIsArchived      = "is_archived"
	SlackErrChannelNotFound = "channel_not_found"
	SlackErrMessageNotFound = "message_not_found"
	SlackErrCantDelete      = "cant_delete_message"
)

type Client struct {
	api             *slackapi.Client
	commandHandler  handler.FifoQueueProducer
	issueFinder     handler.IssueFinder
	cacheStore      store.StoreInterface
	logger          common.Logger
	metrics         common.Metrics
	cfg             *config.ManagerConfig
	managerSettings *models.ManagerSettingsWrapper
}

func New(commandHandler handler.FifoQueueProducer, cacheStore store.StoreInterface, logger common.Logger, metrics common.Metrics, cfg *config.ManagerConfig, managerSettings *models.ManagerSettingsWrapper) *Client {
	return &Client{
		commandHandler:  commandHandler,
		cacheStore:      cacheStore,
		logger:          logger,
		metrics:         metrics,
		cfg:             cfg,
		managerSettings: managerSettings,
	}
}

func (c *Client) SetIssueFinder(issueFinder handler.IssueFinder) {
	c.issueFinder = issueFinder
}

func (c *Client) Connect(ctx context.Context) error {
	if c.commandHandler == nil {
		return errors.New("command handler must be set before connecting")
	}

	if c.cacheStore == nil {
		return errors.New("cache store must be set before connecting")
	}

	if c.logger == nil {
		return errors.New("logger must be set before connecting")
	}

	if c.metrics == nil {
		return errors.New("metrics must be set before connecting")
	}

	if c.cfg == nil {
		return errors.New("config must be set before connecting")
	}

	if c.managerSettings == nil {
		return errors.New("channel settings must be set before connecting")
	}

	c.api = slackapi.New(c.cacheStore, c.cfg.CacheKeyPrefix, c.logger, c.metrics, c.cfg.SlackClient)

	if _, err := c.api.Connect(ctx); err != nil {
		return err
	}

	return nil
}

func (c *Client) RunSocketMode(ctx context.Context) error {
	c.logger.Debug("common.RunSocketMode started")
	defer c.logger.Debug("common.RunSocketMode exited")

	if c.issueFinder == nil {
		return errors.New("issue finder must be set before running socket mode")
	}

	socketModeClient := c.api.NewSocketModeClient()
	handler := handler.NewsSocketModeHandler(socketModeClient, c.logger)

	// Internal slack client events
	controllers.NewInternalEventsController(handler, c.logger)

	// Interactive actions
	controllers.NewInteractiveController(handler, c, c.commandHandler, c.issueFinder, c.logger, c.cfg, c.managerSettings)

	// Slash commands actions
	controllers.NewSlashCommandsController(handler, c.logger)

	// Post reactions (emojis)
	controllers.NewReactionsController(handler, c, c.commandHandler, c.cacheStore, c.logger, c.cfg, c.managerSettings)

	// Greeting situations (joined channel etc)
	controllers.NewGreetingsController(handler, c, c.logger, c.cfg, c.managerSettings)

	// Events API
	controllers.NewEventsAPIController(handler, c.logger)

	// Default controller (fallback when nothing else matches)
	controllers.NewDefaultController(handler, c.logger)

	return handler.RunEventLoop(ctx)
}

func (c *Client) SendResponse(ctx context.Context, channelID, responseURL, responseType, text string) error {
	if err := c.api.SendResponse(ctx, channelID, responseURL, responseType, text); err != nil {
		return fmt.Errorf("failed to send Slack response in channel %s: %w", channelID, err)
	}

	return nil
}

func (c *Client) OpenModal(ctx context.Context, triggerID string, request slack.ModalViewRequest) error {
	if err := c.api.OpenModal(ctx, triggerID, request); err != nil {
		return fmt.Errorf("failed to open modal Slack view: %w", err)
	}

	return nil
}

func (c *Client) PostEphemeral(ctx context.Context, channelID, userID string, options ...slack.MsgOption) (string, error) {
	if c.cfg.SlackClient.DryRun {
		c.logger.Infof("DRYRUN: Slack post ephemeral message to user %s in channel %s", userID, channelID)
		return "", nil
	}

	ts, err := c.api.PostEphemeral(ctx, channelID, userID, options...)
	if err != nil {
		return "", fmt.Errorf("failed to post ephemeral Slack message to user %s in channel %s: %w", userID, channelID, err)
	}

	return ts, nil
}

func (c *Client) Update(ctx context.Context, channelID string, allChannelIssues []*models.Issue) error {
	issues := []*models.Issue{}
	openIssueCount := 0

	for _, issue := range allChannelIssues {
		if issue.Archived {
			continue
		}

		issues = append(issues, issue)

		if !issue.IsInfoOrResolved() {
			openIssueCount++
		}
	}

	// Sort the issues according to priority, from lower to higher
	sort.SliceStable(issues, func(i, j int) bool {
		return issues[i].IsLowerPriorityThan(issues[j])
	})

	// Reordering is allowed when the channel config allows it AND the number of active issues in the channel is small enough
	allowIssueReordering := c.managerSettings.Settings.OrderIssuesBySeverity(channelID, openIssueCount)
	atLeastOnePostDeleted := false

	sem := semaphore.NewWeighted(int64(c.cfg.SlackClient.Concurrency))
	errg, gctx := errgroup.WithContext(ctx)

	for _, _issue := range issues {
		issue := _issue

		// Delete Slack post when requested by the issue (for whatever reason)
		if issue.SlackPostNeedsDelete {
			errg.Go(func() error {
				return c.Delete(gctx, issue, "Issue flagged with SlackPostNeedsDelete", true, sem)
			})
			atLeastOnePostDeleted = true
			continue
		}

		// Delete Slack post when reordering is allowed AND issue follow up is enabled AND at least one Slack post exists where the connected issue has lower priority
		if allowIssueReordering && issue.FollowUpEnabled() && newerSlackPostWithLowerPriorityExists(issue, issues) {
			errg.Go(func() error {
				return c.Delete(gctx, issue, "Issue re-ordering", true, sem)
			})
			atLeastOnePostDeleted = true
		}
	}

	if err := errg.Wait(); err != nil {
		return err
	}

	// Sort the issues again IFF at least one Slack post was deleted in the previous step.
	// This is required since deleted issues are sorted based on both priority and timestamp.
	if atLeastOnePostDeleted {
		sort.SliceStable(issues, func(i, j int) bool {
			return issues[i].IsLowerPriorityThan(issues[j])
		})
	}

	// Go through all the issues and create/update Slack post where needed
	for _, issue := range issues {
		action := issue.GetSlackAction()

		if action == models.ActionNone {
			continue
		}

		// We may temporarily ignore issues with too much activity (to reduce risk of rate limit trouble)
		if c.throttleIssue(issue, action, len(issues)) {
			c.logger.WithFields(issue.LogFields()).Info("Throttle Slack post")
			continue
		}

		if c.cfg.SlackClient.DryRun {
			c.logger.Infof("DRYRUN: Slack %s issue %s", action, issue.CorrelationID)
			continue
		}

		reason := fmt.Sprintf("channel update: issue requests action '%s'", action)

		if err := c.createOrUpdate(ctx, issue, reason, action); err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) UpdateSingleIssue(ctx context.Context, issue *models.Issue, reason string) error {
	action := issue.GetSlackAction()

	if action == models.ActionNone {
		return nil
	}

	return c.createOrUpdate(ctx, issue, reason, issue.GetSlackAction())
}

func (c *Client) UpdateSingleIssueWithThrottling(ctx context.Context, issue *models.Issue, reason string, issuesInChannel int) error {
	action := issue.GetSlackAction()

	if action == models.ActionNone {
		return nil
	}

	// We may temporarily ignore issues with too much activity (to reduce risk of rate limit trouble)
	if c.throttleIssue(issue, action, issuesInChannel) {
		c.logger.WithFields(issue.LogFields()).Info("Throttle Slack post")
		return nil
	}

	return c.createOrUpdate(ctx, issue, reason, action)
}

func (c *Client) Delete(ctx context.Context, issue *models.Issue, reason string, updateIfMessageHasReplies bool, sem *semaphore.Weighted) error {
	if !issue.HasSlackPost() || issue.SlackPostID == PostIDInvalidChannel {
		issue.RegisterSlackPostDeleted()
		return nil
	}

	logger := c.logger.WithFields(issue.LogFields()).WithField("post_update_reason", reason)

	if c.cfg.SlackClient.DryRun {
		logger.Infof("DRYRUN: Slack DELETE issue %s, post %s", issue.CorrelationID, issue.SlackPostID)
		return nil
	}

	if sem != nil {
		if err := sem.Acquire(ctx, 1); err != nil {
			return err
		}
		defer sem.Release(1)
	}

	hardDelete := true

	if updateIfMessageHasReplies {
		hasReplies, err := c.api.MessageHasReplies(ctx, issue.LastAlert.SlackChannelID, issue.SlackPostID)
		if err != nil {
			logger.Errorf("Failed to check if Slack message %s has replies in channel %s: %s", issue.SlackPostID, issue.LastAlert.SlackChannelID, err)
			hasReplies = false
		}

		if hasReplies {
			hardDelete = false
		}
	}

	var deleteOrUpdateErr error

	if hardDelete {
		deleteOrUpdateErr = c.api.ChatDeleteMessage(ctx, issue.LastAlert.SlackChannelID, issue.SlackPostID)
	} else {
		options := c.getMessageOptionsBlocks(issue, models.ActionNone, UpdateMethodUpdateDeleted)
		_, err := c.api.ChatUpdateMessage(ctx, issue.LastAlert.SlackChannelID, options...)
		deleteOrUpdateErr = err
	}

	if deleteOrUpdateErr != nil {
		if deleteOrUpdateErr.Error() != SlackErrChannelNotFound && deleteOrUpdateErr.Error() != SlackErrIsArchived && deleteOrUpdateErr.Error() != SlackErrMessageNotFound && deleteOrUpdateErr.Error() != SlackErrCantDelete {
			return fmt.Errorf("failed to delete Slack post for issue %s and message %s in channel %s: %w", issue.CorrelationID, issue.SlackPostID, issue.LastAlert.SlackChannelID, deleteOrUpdateErr)
		}

		logger.WithField("error", deleteOrUpdateErr.Error()).Info("Failed to delete Slack post for issue")
	} else {
		logger.Info("Delete Slack post")
	}

	issue.RegisterSlackPostDeleted()

	return nil
}

func (c *Client) DeletePost(ctx context.Context, channelID, ts string) error {
	if ts == PostIDInvalidChannel {
		return nil
	}

	if c.cfg.SlackClient.DryRun {
		c.logger.Infof("DRYRUN: Slack DELETE post %s", ts)
		return nil
	}

	logger := c.logger.WithField("channel_id", channelID).WithField("slack_post_id", ts)

	err := c.api.ChatDeleteMessage(ctx, channelID, ts)
	if err != nil {
		if err.Error() != SlackErrChannelNotFound && err.Error() != SlackErrIsArchived && err.Error() != SlackErrMessageNotFound && err.Error() != SlackErrCantDelete {
			return fmt.Errorf("failed to delete Slack post %s in channel %s: %w", ts, channelID, err)
		}

		logger.WithField("error", err.Error()).Info("Failed to delete Slack post")
	} else {
		logger.Info("Delete Slack post")
	}

	return nil
}

func (c *Client) IsAlertChannel(ctx context.Context, channelID string) (bool, string, error) {
	if c.managerSettings.Settings.IsInfoChannel(channelID) {
		return false, "channel is defined as 'info channel' (no alerts allowed)", nil
	}

	botIsInChannel, err := c.api.BotIsInChannel(ctx, channelID)
	if err != nil {
		return false, "", fmt.Errorf("failed to check if Slack App integration is in channel: %w", err)
	}

	if !botIsInChannel {
		return false, "Slack App integration is not in channel", nil
	}

	return true, "", nil
}

func (c *Client) GetUserInfo(ctx context.Context, userID string) (*slack.User, error) {
	user, err := c.api.GetUserInfo(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to read Slack user info for %s: %w", userID, err)
	}

	return user, nil
}

func (c *Client) UserIsInGroup(ctx context.Context, groupID, userID string) bool {
	userIDs, err := c.api.ListUserGroupMembers(ctx, groupID)
	if err != nil {
		c.logger.Errorf("Failed to list user group members for %s: %s", groupID, err)
		return false
	}

	if userIDs == nil {
		return false
	}

	if _, ok := userIDs[userID]; ok {
		return true
	}

	return false
}

func (c *Client) GetChannelName(ctx context.Context, channelID string) string {
	if channelID == "" {
		return ""
	}

	info, err := c.api.GetChannelInfo(ctx, channelID)
	if err != nil {
		// We log the error unless it's a channel not found error, which is expected in some cases.
		if err.Error() != slackapi.ChannelNotFoundError {
			c.logger.Errorf("Failed to find Slack channel info for %s: %s", channelID, err)
		}
		return ""
	}

	return info.Name
}

func (c *Client) createOrUpdate(ctx context.Context, issue *models.Issue, reason string, action models.SlackAction) error {
	if issue.SlackPostID == PostIDInvalidChannel {
		issue.RegisterSlackPostCreatedOrUpdated(PostIDInvalidChannel, action)
		return nil
	}

	if !issue.HasSlackPost() {
		reason += ": issue has no Slack post"
		return c.create(ctx, issue, reason, action)
	}

	reason += ": issue has existing Slack post"

	return c.update(ctx, issue, reason, action)
}

func (c *Client) create(ctx context.Context, issue *models.Issue, reason string, action models.SlackAction) error {
	options := c.getMessageOptionsBlocks(issue, action, UpdateMethodPost)

	logger := c.logger.WithFields(issue.LogFields()).WithField("post_update_reason", reason)

	postID, err := c.api.ChatPostMessage(ctx, issue.LastAlert.SlackChannelID, options...)
	if err != nil {
		logger = logger.WithField("reason", err.Error())
		errMsg := fmt.Sprintf("Failed to create Slack post for issue %s in channel %s: %s", issue.CorrelationID, issue.LastAlert.SlackChannelID, err.Error())

		// Channel not found or channel is archived.
		// Log error and use dummy post ID to avoid further updates on this issue.
		if err.Error() == SlackErrChannelNotFound || err.Error() == SlackErrIsArchived {
			logger.Error(errMsg)
			issue.RegisterSlackPostCreatedOrUpdated(PostIDInvalidChannel, action)
			return nil
		}

		// Invalid blocks means something is wrong with the message formatting. It may be a bug in this application, or some unexpected data from the alert API.
		// Log error and register the issue as invalid.
		if err.Error() == "invalid_blocks" {
			logger.Error(errMsg)
			issue.RegisterSlackPostInvalidBlocks()
			return nil
		}

		// Slack App is not added as integration in channel. Should only happen in race conditions, since the alert API checks this.
		// Log error and reset post ID.
		if err.Error() == "not_in_channel" {
			logger.Error(errMsg)
			issue.RegisterSlackPostDeleted()
			return nil
		}

		return fmt.Errorf("failed to create Slack post for issue %s in channel %s: %w", issue.CorrelationID, issue.LastAlert.SlackChannelID, err)
	}

	if postID == "" {
		return fmt.Errorf("failed to create Slack post for issue %s in channel %s: Slack returned empty timestamp", issue.CorrelationID, issue.LastAlert.SlackChannelID)
	}

	issue.RegisterSlackPostCreatedOrUpdated(postID, action)

	logger.Info("Create Slack post")

	return nil
}

func (c *Client) update(ctx context.Context, issue *models.Issue, reason string, action models.SlackAction) error {
	options := c.getMessageOptionsBlocks(issue, action, UpdateMethodUpdate)

	logger := c.logger.WithFields(issue.LogFields()).WithField("post_update_reason", reason)

	postID, err := c.api.ChatUpdateMessage(ctx, issue.LastAlert.SlackChannelID, options...)
	if err != nil {
		logger = logger.WithField("reason", err.Error())
		errMsg := fmt.Sprintf("Failed to update Slack post for issue %s in channel %s: %s", issue.CorrelationID, issue.LastAlert.SlackChannelID, err.Error())

		// Channel not found or channel is archived.
		// Log error and use dummy post ID to avoid further updates on this issue.
		if err.Error() == SlackErrChannelNotFound || err.Error() == SlackErrIsArchived {
			logger.Error(errMsg)
			issue.RegisterSlackPostCreatedOrUpdated(PostIDInvalidChannel, action)
			return nil
		}

		// Message not found or some other possibly transient message error.
		// Log info and reset post ID.
		if err.Error() == "message_not_found" || err.Error() == "cant_update_message" {
			logger.Info(errMsg)
			issue.RegisterSlackPostDeleted()
			return nil
		}

		// Invalid blocks means something is wrong with the message formatting, i.e. a bug in this application.
		// Log error and register the issue as invalid.
		if err.Error() == "invalid_blocks" {
			logger.Error(errMsg)
			issue.RegisterSlackPostInvalidBlocks()
			return nil
		}

		// Slack App integration is not in channel. Should only happen in race conditions, since the alert API checks this.
		// Log error and reset post ID.
		if err.Error() == "not_in_channel" {
			logger.Error(errMsg)
			issue.RegisterSlackPostDeleted()
			return nil
		}

		return fmt.Errorf("failed to update Slack post for issue %s and message %s in channel %s: %w", issue.CorrelationID, issue.SlackPostID, issue.LastAlert.SlackChannelID, err)
	}

	if postID == "" {
		return fmt.Errorf("failed to update Slack post for issue %s: Slack returned empty timestamp", issue.CorrelationID)
	}

	issue.RegisterSlackPostCreatedOrUpdated(postID, action)

	logger.Info("Update Slack post")

	return nil
}

func (c *Client) getMessageOptionsBlocks(issue *models.Issue, action models.SlackAction, method UpdateMethod) []slack.MsgOption {
	blocks := []slack.Block{}
	statusEmoji := c.getStatusEmoji(issue, action, method)

	if issue.IsInfoOrResolved() && issue.LastAlert.HeaderWhenResolved != "" {
		text := newPlainTextTextBlock(setStatusEmoji(issue.LastAlert.HeaderWhenResolved, statusEmoji))
		blocks = append(blocks, slack.NewHeaderBlock(text))
	} else if issue.LastAlert.Header != "" {
		text := newPlainTextTextBlock(setStatusEmoji(issue.LastAlert.Header, statusEmoji))
		blocks = append(blocks, slack.NewHeaderBlock(text))
	}

	if issue.IsInfoOrResolved() && issue.LastAlert.TextWhenResolved != "" {
		blocks = append(blocks, getTextBlocks(issue.LastAlert.TextWhenResolved, statusEmoji, method)...)
	} else if issue.LastAlert.Text != "" {
		blocks = append(blocks, getTextBlocks(issue.LastAlert.Text, statusEmoji, method)...)
	}

	if len(issue.LastAlert.Fields) > 0 {
		fields := []*slack.TextBlockObject{}
		for _, f := range issue.LastAlert.Fields {
			fields = append(fields, newMrkdwnTextBlock(fmt.Sprintf("*%s:*\n%s", f.Title, f.Value)))
		}
		blocks = append(blocks, slack.NewSectionBlock(nil, fields, nil))
	}

	if issue.LastAlert.Author != "" {
		text := newMrkdwnTextBlock(fmt.Sprintf("*%s*", issue.LastAlert.Author))
		blocks = append(blocks, slack.NewContextBlock("", text))
	}

	if issue.LastAlert.Host != "" {
		text := newMrkdwnTextBlock(fmt.Sprintf("*%s*", issue.LastAlert.Host))
		blocks = append(blocks, slack.NewContextBlock("", text))
	}

	if issue.LastAlert.Link != "" {
		text := newMrkdwnTextBlock(issue.LastAlert.Link)
		blocks = append(blocks, slack.NewContextBlock("", text))
	}

	if method != UpdateMethodUpdateDeleted {
		webhookButtons := getWebhookButtons(issue)

		if len(webhookButtons) > 0 {
			blocks = append(blocks, slack.NewActionBlock("issue_actions", webhookButtons...))
		}

		if c.managerSettings.Settings.AlwaysShowOptionButtons || issue.IsEmojiButtonsActivated {
			optionButtons := getIssueOptionButtons(issue)

			if len(optionButtons) > 0 {
				blocks = append(blocks, slack.NewActionBlock("issue_options", optionButtons...))
			}
		}
	}

	if issue.LastAlert.Footer != "" {
		text := newMrkdwnTextBlock(issue.LastAlert.Footer)
		blocks = append(blocks, slack.NewContextBlock("", text))
	}

	if c.managerSettings.Settings.ShowIssueCorrelationIDInSlackPost {
		text := "Correlation ID: " + issue.CorrelationID
		fields := []slack.MixedElement{
			slack.NewTextBlockObject("plain_text", text, false, false),
		}
		blocks = append(blocks, slack.NewContextBlock("", fields...))
	}

	if issue.FollowUpEnabled() {
		first := "First: " + issue.Created.In(c.cfg.Location).Format("01-02 15:04:05")
		last := "Last: " + issue.LastAlertReceived.In(c.cfg.Location).Format("01-02 15:04:05")
		alertCount := fmt.Sprintf("#%d", issue.AlertCount)

		fields := []slack.MixedElement{}
		fields = append(fields, slack.NewTextBlockObject("plain_text", first, false, false))
		fields = append(fields, slack.NewTextBlockObject("plain_text", last, false, false))
		fields = append(fields, slack.NewTextBlockObject("plain_text", alertCount, false, false))
		blocks = append(blocks, slack.NewContextBlock("", fields...))
	}

	// Issue is manually resolved by user
	if method != UpdateMethodUpdateDeleted && issue.IsInfoOrResolved() && issue.IsEmojiResolved {
		text := newMrkdwnTextBlock(fmt.Sprintf(":raising_hand: The issue was resolved by *%s* at %s", issue.ResolvedByUser, issue.ResolveTime.In(c.cfg.Location).Format("2006-01-02 15:04:05")))
		blocks = append(blocks, slack.NewContextBlock("", text))
	}

	// Issue is not resolved AND being investigated by user
	if method != UpdateMethodUpdateDeleted && !issue.IsInfoOrResolved() && issue.IsEmojiInvestigated {
		text := newMrkdwnTextBlock(fmt.Sprintf(":cop: The issue is investigated by *%s* since %s", issue.InvestigatedByUser, issue.InvestigatedSince.In(c.cfg.Location).Format("2006-01-02 15:04:05")))
		blocks = append(blocks, slack.NewContextBlock("", text))
	}

	// Issue is not resolved AND muted by user
	if method != UpdateMethodUpdateDeleted && !issue.IsInfoOrResolved() && issue.IsEmojiMuted {
		text := newMrkdwnTextBlock(fmt.Sprintf(":mask: The issue was muted by *%s* at %s", issue.MutedByUser, issue.MutedSince.In(c.cfg.Location).Format("2006-01-02 15:04:05")))
		blocks = append(blocks, slack.NewContextBlock("", text))
	}

	options := []slack.MsgOption{slack.MsgOptionBlocks(blocks...), slack.MsgOptionUsername(issue.LastAlert.Username), slack.MsgOptionAsUser(false)}

	params := slack.NewPostMessageParameters()

	if strings.HasPrefix(issue.LastAlert.IconEmoji, "http") {
		params.IconURL = issue.LastAlert.IconEmoji
	} else if issue.LastAlert.IconEmoji != "" {
		params.IconEmoji = issue.LastAlert.IconEmoji
	}

	options = append(options, slack.MsgOptionPostMessageParameters(params))

	if method == UpdateMethodUpdate || method == UpdateMethodUpdateDeleted {
		options = append(options, slack.MsgOptionUpdate(issue.SlackPostID))
	} else {
		options = append(options, slack.MsgOptionPost())
	}

	if issue.LastAlert.FallbackText != "" {
		options = append(options, slack.MsgOptionText(issue.LastAlert.FallbackText, false))
	}

	return options
}

func newMrkdwnTextBlock(text string) *slack.TextBlockObject {
	text = strings.ReplaceAll(text, "<!everyone>", "everyone")
	return slack.NewTextBlockObject("mrkdwn", text, false, false)
}

func newPlainTextTextBlock(text string) *slack.TextBlockObject {
	text = strings.ReplaceAll(text, "<!everyone>", "everyone")
	return slack.NewTextBlockObject("plain_text", text, true, false)
}

func getWebhookButtons(issue *models.Issue) []slack.BlockElement {
	buttons := []slack.BlockElement{}

	for index, hook := range issue.LastAlert.Webhooks {
		if issue.IsResolved() && (hook.DisplayMode == "" || hook.DisplayMode == common.WebhookDisplayModeOpenIssue) {
			continue
		}

		if !issue.IsResolved() && hook.DisplayMode == common.WebhookDisplayModeResolvedIssue {
			continue
		}

		actionID := fmt.Sprintf("%s_%d", controllers.WebhookActionIDPrefix, index)
		button := slack.NewButtonBlockElement(actionID, hook.ID, slack.NewTextBlockObject("plain_text", hook.ButtonText, false, false))

		if hook.ButtonStyle == "default" {
			hook.ButtonStyle = ""
		}

		button = button.WithStyle(slack.Style(hook.ButtonStyle))

		buttons = append(buttons, button)
	}

	return buttons
}

func getIssueOptionButtons(issue *models.Issue) []slack.BlockElement {
	buttons := []slack.BlockElement{}

	if !issue.IsResolved() {
		actionID := fmt.Sprintf("%s_%s", controllers.OptionButtonActionIDPrefix, controllers.MoveIssueAction)
		button := slack.NewButtonBlockElement(actionID, controllers.MoveIssueAction, slack.NewTextBlockObject("plain_text", "Move issue...", false, false))
		buttons = append(buttons, button)
	}

	actionID := fmt.Sprintf("%s_%s", controllers.OptionButtonActionIDPrefix, controllers.ViewIssueAction)
	button := slack.NewButtonBlockElement(actionID, controllers.ViewIssueAction, slack.NewTextBlockObject("plain_text", "Details...", false, false))
	buttons = append(buttons, button)

	return buttons
}

func (c *Client) getStatusEmoji(issue *models.Issue, action models.SlackAction, method UpdateMethod) string {
	if action == models.ActionResolve {
		if issue.IsResolvedAsInconclusive() {
			return c.managerSettings.Settings.IssueStatus.InconclusiveEmoji
		}
		return c.managerSettings.Settings.IssueStatus.ResolvedEmoji
	}

	switch issue.LastAlert.Severity {
	case common.AlertPanic:
		if issue.IsEmojiMuted || method == UpdateMethodUpdateDeleted {
			return c.managerSettings.Settings.IssueStatus.MutePanicEmoji
		}
		return c.managerSettings.Settings.IssueStatus.PanicEmoji
	case common.AlertError:
		if issue.IsEmojiMuted || method == UpdateMethodUpdateDeleted {
			return c.managerSettings.Settings.IssueStatus.MuteErrorEmoji
		}
		return c.managerSettings.Settings.IssueStatus.ErrorEmoji
	case common.AlertWarning:
		if issue.IsEmojiMuted || method == UpdateMethodUpdateDeleted {
			return c.managerSettings.Settings.IssueStatus.MuteWarningEmoji
		}
		return c.managerSettings.Settings.IssueStatus.WarningEmoji
	case common.AlertResolved:
		return c.managerSettings.Settings.IssueStatus.ResolvedEmoji
	case common.AlertInfo:
		return c.managerSettings.Settings.IssueStatus.InfoEmoji
	default:
		c.logger.WithFields(issue.LogFields()).Errorf("Unknown alert severity %s", issue.LastAlert.Severity)
		return ""
	}
}

func setStatusEmoji(text string, status string) string {
	return strings.ReplaceAll(text, ":status:", status)
}

func newerSlackPostWithLowerPriorityExists(currentIssue *models.Issue, allIssues []*models.Issue) bool {
	if !currentIssue.HasSlackPost() {
		return false
	}

	for _, otherIssue := range allIssues {
		if currentIssue.CorrelationID == otherIssue.CorrelationID || !otherIssue.HasSlackPost() {
			continue
		}

		if otherIssue.SlackPostID > currentIssue.SlackPostID && otherIssue.IsLowerPriorityThan(currentIssue) {
			return true
		}
	}

	return false
}

func getTextBlocks(alertText, statusEmoji string, method UpdateMethod) []slack.Block {
	endsWithCodeBlock := strings.HasSuffix(alertText, "```")

	blocks := []slack.Block{}

	strikethrough := func(s string) string {
		s = strings.TrimSpace(s)
		s = strings.ReplaceAll(s, "_", "")
		s = strings.ReplaceAll(s, "*", "")
		s = strings.ReplaceAll(s, "~", "")
		s = strings.ReplaceAll(s, "\n", "~\n~")
		return "~" + s + "~"
	}

	for {
		if len(alertText) <= 2800 || method == UpdateMethodUpdateDeleted {
			if method == UpdateMethodUpdateDeleted {
				alertText = strikethrough(alertText)
			}

			text := newMrkdwnTextBlock(setStatusEmoji(alertText, statusEmoji))
			blocks = append(blocks, slack.NewSectionBlock(text, nil, nil))

			break
		}

		part := alertText[0:2700]
		alertText = alertText[2700:]

		if endsWithCodeBlock {
			part += "```"
			alertText = "```" + alertText
		}

		if method == UpdateMethodUpdateDeleted {
			part = strikethrough(part)
		}

		text := newMrkdwnTextBlock(setStatusEmoji(part, statusEmoji))
		blocks = append(blocks, slack.NewSectionBlock(text, nil, nil))

		if method == UpdateMethodUpdateDeleted {
			break
		}
	}

	return blocks
}

func (c *Client) throttleIssue(issue *models.Issue, action models.SlackAction, issuesInChannel int) bool {
	// Don't throttle if total number of open issues in channel is less than the configured minimum
	if issuesInChannel < c.managerSettings.Settings.MinIssueCountForThrottle {
		return false
	}

	// Don't throttle issues where the planned Slack action differs from the last posted action
	if action != issue.SlackPostLastAction {
		return false
	}

	// Don't throttle issues with no current Slack posts
	if !issue.HasSlackPost() {
		return false
	}

	// Don't throttle if the last alert has active mentions
	if issue.LastAlertHasActiveMentions() {
		return false
	}

	// Don't throttle if the alert header has changed
	if issue.SlackPostHeader != issue.LastAlert.Header {
		return false
	}

	// Find the duration since the last Slack post for this issue
	timeSinceLastPost := time.Since(issue.SlackPostUpdated)

	// Calculate throttle limit as (2 x alert count) seconds
	limit := time.Duration(2*issue.AlertCount) * time.Second

	// Limit is never more than the configured upper limit
	upperLimitInSettings := time.Second * time.Duration(c.managerSettings.Settings.MaxThrottleDurationSeconds)
	if limit > upperLimitInSettings {
		limit = upperLimitInSettings
	}

	// Reduce the upper limit if the alert text has changed
	if issue.SlackPostText != issue.LastAlert.Text {
		limit /= 2
	}

	// Throttle if the time since the last Slack post is smaller than the limit
	return timeSinceLastPost < limit
}
