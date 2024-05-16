package slack

import (
	"context"
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
)

type Client struct {
	api            *slackapi.Client
	commandHandler handler.FifoQueueProducer
	issueFinder    handler.IssueFinder
	cacheStore     store.StoreInterface
	logger         common.Logger
	metrics        common.Metrics
	conf           *config.ManagerConfig
}

var location *time.Location

func New(commandHandler handler.FifoQueueProducer, cacheStore store.StoreInterface, logger common.Logger, metrics common.Metrics, conf *config.ManagerConfig) *Client {
	location = conf.Location

	return &Client{
		commandHandler: commandHandler,
		cacheStore:     cacheStore,
		logger:         logger,
		metrics:        metrics,
		conf:           conf,
	}
}

func (c *Client) SetIssueFinder(issueFinder handler.IssueFinder) {
	c.issueFinder = issueFinder
}

func (c *Client) Connect(ctx context.Context) error {
	c.api = slackapi.New(c.cacheStore, c.conf.CachePrefix, c.logger, c.metrics, &c.conf.Slack)

	_, err := c.api.Connect(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) RunSocketMode(ctx context.Context) error {
	c.logger.Debug("common.RunSocketMode started")
	defer c.logger.Debug("common.RunSocketMode exited")

	if c.issueFinder == nil {
		return fmt.Errorf("issue finder must be set before running socket mode")
	}

	socketModeClient := c.api.NewSocketModeClient()
	handler := handler.NewsSocketModeHandler(socketModeClient, c.logger)

	// Internal slack client events
	controllers.NewInternalEventsController(handler, c.logger)

	// Interactive actions
	controllers.NewInteractiveController(handler, c, c.commandHandler, c.issueFinder, c.logger, c.conf)

	// Slash commands actions
	controllers.NewSlashCommandsController(handler, c.logger)

	// Post reactions (emojis)
	controllers.NewReactionsController(handler, c, c.commandHandler, c.cacheStore, c.logger, c.conf)

	// Greeting situations (joined channel etc)
	controllers.NewGreetingsController(handler, c, c.logger, c.conf)

	// Events API
	controllers.NewEventsAPIController(handler, c.logger)

	// Default controller (fallback when nothing else matches)
	controllers.NewDefaultController(handler, c.logger)

	return handler.RunEventLoop(ctx)
}

func (c *Client) SendResponse(ctx context.Context, channelID, responseURL, responseType, text string) error {
	return c.api.SendResponse(ctx, channelID, responseURL, responseType, text)
}

func (c *Client) OpenModal(ctx context.Context, triggerID string, request slack.ModalViewRequest) error {
	return c.api.OpenModal(ctx, triggerID, request)
}

func (c *Client) PostEphemeral(ctx context.Context, channelID, userID string, options ...slack.MsgOption) (string, error) {
	if c.conf.Slack.DryRun {
		c.logger.Infof("DRYRUN: Slack post ephemeral message to user %s in channel %s", userID, channelID)
		return "", nil
	}

	return c.api.PostEphemeral(ctx, channelID, userID, options...)
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
	allowIssueReordering := c.conf.OrderIssuesBySeverity(channelID) && openIssueCount <= c.conf.ReorderIssueLimit
	atLeastOnePostDeleted := false

	sem := semaphore.NewWeighted(int64(c.conf.Slack.Concurrency))
	errg, gctx := errgroup.WithContext(ctx)

	for _, _issue := range issues {
		issue := _issue

		// Delete Slack post when requested by the issue (for whatever reason)
		if issue.SlackPostNeedsDelete {
			errg.Go(func() error {
				return c.Delete(gctx, issue, true, sem)
			})
			atLeastOnePostDeleted = true
			continue
		}

		// Delete Slack post when reordering is allowed AND issue follow up is enabled AND at least one Slack post exists where the connected issue has lower priority
		if allowIssueReordering && issue.FollowUpEnabled() && newerSlackPostWithLowerPriorityExists(issue, issues) {
			errg.Go(func() error {
				return c.Delete(gctx, issue, true, sem)
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

		if c.conf.Slack.DryRun {
			c.logger.Infof("DRYRUN: Slack %s issue %s", action, issue.CorrelationID)
			continue
		}

		if err := c.createOrUpdate(ctx, issue, action); err != nil {
			return err
		}
	}

	return nil
}

func (c *Client) UpdateSingleIssue(ctx context.Context, issue *models.Issue) error {
	action := issue.GetSlackAction()

	if action == models.ActionNone {
		return nil
	}

	return c.createOrUpdate(ctx, issue, issue.GetSlackAction())
}

func (c *Client) UpdateSingleIssueWithThrottling(ctx context.Context, issue *models.Issue, issuesInChannel int) error {
	action := issue.GetSlackAction()

	if action == models.ActionNone {
		return nil
	}

	// We may temporarily ignore issues with too much activity (to reduce risk of rate limit trouble)
	if c.throttleIssue(issue, action, issuesInChannel) {
		c.logger.WithFields(issue.LogFields()).Info("Throttle Slack post")
		return nil
	}

	return c.createOrUpdate(ctx, issue, action)
}

func (c *Client) Delete(ctx context.Context, issue *models.Issue, updateIfMessageHasReplies bool, sem *semaphore.Weighted) error {
	if !issue.HasSlackPost() || issue.SlackPostID == PostIDInvalidChannel {
		issue.RegisterSlackPostDeleted()
		return nil
	}

	if c.conf.Slack.DryRun {
		c.logger.Infof("DRYRUN: Slack DELETE issue %s, post %s", issue.CorrelationID, issue.SlackPostID)
		return nil
	}

	if sem != nil {
		if err := sem.Acquire(ctx, 1); err != nil {
			return err
		}
		defer sem.Release(1)
	}

	hardDelete := true
	logger := c.logger.WithFields(issue.LogFields())

	if updateIfMessageHasReplies {
		hasReplies, err := c.api.MessageHasReplies(ctx, issue.LastAlert.SlackChannelID, issue.SlackPostID)
		if err != nil {
			logger.Errorf("Failed to check if Slack message has replies: %s", err)
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
		options := c.getMessageOptionsBlocks(issue, models.ActionNone, UPDATE_DELETED)
		_, err := c.api.ChatUpdateMessage(ctx, issue.LastAlert.SlackChannelID, options...)
		deleteOrUpdateErr = err
	}

	if deleteOrUpdateErr != nil {
		switch {
		case deleteOrUpdateErr.Error() == SlackErrChannelNotFound || deleteOrUpdateErr.Error() == SlackErrIsArchived:
			logger.WithField("error", "channel is private, archived or does not exist").Error("Failed to delete Slack post for issue")
		case deleteOrUpdateErr.Error() == "message_not_found":
			logger.WithField("error", "message not found").Info("Failed to deleted Slack post for issue")
		case deleteOrUpdateErr.Error() == "cant_delete_message":
			logger.WithField("error", "can't delete message").Info("Failed to delete Slack post for issue")
		default:
			return fmt.Errorf("failed to delete Slack post for issue %s and message %s in channel %s: %w", issue.CorrelationID, issue.SlackPostID, issue.LastAlert.SlackChannelID, deleteOrUpdateErr)
		}
	} else {
		logger.Info("Delete Slack post")
	}

	issue.RegisterSlackPostDeleted()

	return nil
}

func (c *Client) IsAlertChannel(ctx context.Context, channelID string) (bool, string, error) {
	if c.conf.IsInfoChannel(channelID) {
		return false, "channel is defined as 'info channel' (no alerts allowed)", nil
	}

	botIsInChannel, err := c.api.BotIsInChannel(ctx, channelID)
	if err != nil {
		return false, "", err
	}

	if !botIsInChannel {
		return false, "Slack Manager is not in channel", nil
	}

	return true, "", nil
}

func (c *Client) GetUserInfo(ctx context.Context, userID string) (*slack.User, error) {
	return c.api.GetUserInfo(ctx, userID)
}

func (c *Client) UserIsInGroup(ctx context.Context, groupID, userID string) bool {
	userIDs, err := c.api.ListUserGroupMembers(ctx, groupID)
	if err != nil {
		c.logger.Errorf("Failed to list user group members: %s", err)
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
		c.logger.Errorf("Failed to find Slack channel info: %s", err)
		return ""
	}

	return info.Name
}

func (c *Client) createOrUpdate(ctx context.Context, issue *models.Issue, action models.SlackAction) error {
	if issue.SlackPostID == PostIDInvalidChannel {
		issue.RegisterSlackPostCreatedOrUpdated(PostIDInvalidChannel, action)
		return nil
	}

	if !issue.HasSlackPost() {
		return c.create(ctx, issue, action)
	}

	return c.update(ctx, issue, action)
}

func (c *Client) create(ctx context.Context, issue *models.Issue, action models.SlackAction) error {
	options := c.getMessageOptionsBlocks(issue, action, POST)

	postID, err := c.api.ChatPostMessage(ctx, issue.LastAlert.SlackChannelID, options...)
	if err != nil {
		logger := c.logger.WithFields(issue.LogFields()).WithField("reason", err.Error())
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

		// Slack Manager is not in channel. Should only happen in race conditions, since the alert API checks this.
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

	c.logger.WithFields(issue.LogFields()).Info("Create Slack post")

	return nil
}

func (c *Client) update(ctx context.Context, issue *models.Issue, action models.SlackAction) error {
	options := c.getMessageOptionsBlocks(issue, action, UPDATE)

	postID, err := c.api.ChatUpdateMessage(ctx, issue.LastAlert.SlackChannelID, options...)
	if err != nil {
		logger := c.logger.WithFields(issue.LogFields()).WithField("reason", err.Error())
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

		// Slack Manager is not in channel. Should only happen in race conditions, since the alert API checks this.
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

	c.logger.WithFields(issue.LogFields()).Info("Update Slack post")

	return nil
}

func (c *Client) getMessageOptionsBlocks(issue *models.Issue, action models.SlackAction, method Method) []slack.MsgOption {
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

	if method != UPDATE_DELETED {
		webhookButtons := getWebhookButtons(issue)
		if len(webhookButtons) > 0 {
			blocks = append(blocks, slack.NewActionBlock("alert_actions", webhookButtons...))
		}
	}

	if issue.LastAlert.Footer != "" {
		text := newMrkdwnTextBlock(issue.LastAlert.Footer)
		blocks = append(blocks, slack.NewContextBlock("", text))
	}

	if issue.FollowUpEnabled() {
		fields := []slack.MixedElement{}
		fields = append(fields, slack.NewTextBlockObject("plain_text", fmt.Sprintf("First: %s", issue.Created.In(location).Format("01-02 15:04:05")), false, false))
		fields = append(fields, slack.NewTextBlockObject("plain_text", fmt.Sprintf("Last: %s", issue.LastAlertReceived.In(location).Format("01-02 15:04:05")), false, false))
		fields = append(fields, slack.NewTextBlockObject("plain_text", fmt.Sprintf("#%d", issue.AlertCount), false, false))
		blocks = append(blocks, slack.NewContextBlock("", fields...))
	}

	// Issue is manually resolved by user
	if method != UPDATE_DELETED && issue.IsInfoOrResolved() && issue.IsEmojiResolved {
		text := newMrkdwnTextBlock(fmt.Sprintf(":raising_hand: The issue was resolved by *%s* at %s", issue.ResolvedByUser, issue.ResolveTime.In(location).Format("2006-01-02 15:04:05")))
		blocks = append(blocks, slack.NewContextBlock("", text))
	}

	// Issue is not resolved AND being investigated by user
	if method != UPDATE_DELETED && !issue.IsInfoOrResolved() && issue.IsEmojiInvestigated {
		text := newMrkdwnTextBlock(fmt.Sprintf(":cop: The issue is investigated by *%s* since %s", issue.InvestigatedByUser, issue.InvestigatedSince.In(location).Format("2006-01-02 15:04:05")))
		blocks = append(blocks, slack.NewContextBlock("", text))
	}

	// Issue is not resolved AND muted by user
	if method != UPDATE_DELETED && !issue.IsInfoOrResolved() && issue.IsEmojiMuted {
		text := newMrkdwnTextBlock(fmt.Sprintf(":mask: The issue was muted by *%s* at %s", issue.MutedByUser, issue.MutedSince.In(location).Format("2006-01-02 15:04:05")))
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

	if method == UPDATE || method == UPDATE_DELETED {
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

		actionID := fmt.Sprintf("%s-%d", controllers.WebhookActionID, index)
		button := slack.NewButtonBlockElement(actionID, hook.ID, slack.NewTextBlockObject("plain_text", hook.ButtonText, false, false))

		if hook.ButtonStyle == "default" {
			hook.ButtonStyle = ""
		}

		button = button.WithStyle(slack.Style(hook.ButtonStyle))

		buttons = append(buttons, button)
	}

	return buttons
}

func (c *Client) getStatusEmoji(issue *models.Issue, action models.SlackAction, method Method) string {
	if action == models.ActionResolve {
		if issue.IsResolvedAsInconclusive() {
			return ":monitor_unresolved:"
		}
		return ":monitor_ok:"
	}

	switch issue.LastAlert.Severity {
	case common.AlertPanic:
		if issue.IsEmojiMuted || method == UPDATE_DELETED {
			return ":monitor_mute_panic:"
		}
		return ":monitor_panic:"
	case common.AlertError:
		if issue.IsEmojiMuted || method == UPDATE_DELETED {
			return ":monitor_mute_error:"
		}
		return ":monitor_error:"
	case common.AlertWarning:
		if issue.IsEmojiMuted || method == UPDATE_DELETED {
			return ":monitor_mute_warning:"
		}
		return ":monitor_warning:"
	case common.AlertResolved:
		return ":monitor_ok:"
	case common.AlertInfo:
		return ":monitor_info:"
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

func getTextBlocks(alertText, statusEmoji string, method Method) []slack.Block {
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
		if len(alertText) <= 2800 || method == UPDATE_DELETED {
			if method == UPDATE_DELETED {
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

		if method == UPDATE_DELETED {
			part = strikethrough(part)
		}

		text := newMrkdwnTextBlock(setStatusEmoji(part, statusEmoji))
		blocks = append(blocks, slack.NewSectionBlock(text, nil, nil))

		if method == UPDATE_DELETED {
			break
		}
	}

	return blocks
}

func (c *Client) throttleIssue(issue *models.Issue, action models.SlackAction, issuesInChannel int) bool {
	// Don't throttle if total number of open issues in channel is less than configured value
	if issuesInChannel < c.conf.Throttle.MinIssueCountForThrottle {
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
	if limit > c.conf.Throttle.UpperLimit {
		limit = c.conf.Throttle.UpperLimit
	}

	// Reduce the upper limit if the alert text has changed
	if issue.SlackPostText != issue.LastAlert.Text {
		limit /= 2
	}

	// Throttle if the time since the last Slack post is smaller than the limit
	return timeSinceLastPost < limit
}
