package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/eko/gocache/lib/v4/store"
	"github.com/go-resty/resty/v2"
	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/peteraglen/slack-manager/internal"
	"github.com/peteraglen/slack-manager/manager/internal/models"
	"github.com/peteraglen/slack-manager/manager/internal/slack"
)

const alertsTotal = "processed_alerts_total"

type cmdFunc func(ctx context.Context, issue *models.Issue, cmd *models.Command, logger common.Logger) error

type channelManager struct {
	channelID       string
	issueCollection *models.IssueCollection
	slackClient     *slack.Client
	db              DB
	webhookClient   *resty.Client
	cache           *internal.Cache[string]
	logger          common.Logger
	metrics         common.Metrics
	cfg             *config.ManagerConfig
	managerSettings *models.ManagerSettingsWrapper
	alertCh         chan *models.Alert
	commandCh       chan *models.Command
	movedIssueCh    chan *models.Issue
	moveRequestCh   chan<- *models.MoveRequest
	cmdFuncs        map[models.CommandAction]cmdFunc
	initialized     bool
}

func newChannelManager(channelID string, slackClient *slack.Client, db DB, moveRequestCh chan<- *models.MoveRequest, cacheStore store.StoreInterface, logger common.Logger, metrics common.Metrics, cfg *config.ManagerConfig, managerSettings *models.ManagerSettingsWrapper) *channelManager {
	cacheKeyPrefix := cfg.CacheKeyPrefix + "channel-manager::" + channelID + "::"
	cache := internal.NewCache[string](cacheStore, cacheKeyPrefix, logger)

	restyLogger := newRestyLogger(logger)

	webhookClient := resty.New().
		SetRetryCount(2).
		SetRetryWaitTime(time.Second).
		AddRetryCondition(webhookRetryPolicy).
		SetLogger(restyLogger).
		SetTimeout(time.Duration(cfg.WebhookTimeoutSeconds) * time.Second)

	logger = logger.WithField("slack_channel_id", channelID)

	c := &channelManager{
		channelID:       channelID,
		slackClient:     slackClient,
		db:              db,
		webhookClient:   webhookClient,
		moveRequestCh:   moveRequestCh,
		cache:           cache,
		logger:          logger,
		metrics:         metrics,
		cfg:             cfg,
		managerSettings: managerSettings,
		alertCh:         make(chan *models.Alert, 1000),
		commandCh:       make(chan *models.Command, 100),
		movedIssueCh:    make(chan *models.Issue, 10),
	}

	c.cmdFuncs = map[models.CommandAction]cmdFunc{
		models.CommandActionTerminateIssue:     c.terminateIssue,
		models.CommandActionResolveIssue:       c.resolveIssue,
		models.CommandActionUnresolveIssue:     c.unresolveIssue,
		models.CommandActionInvestigateIssue:   c.investigateIssue,
		models.CommandActionUninvestigateIssue: c.uninvestigateIssue,
		models.CommandActionMuteIssue:          c.muteIssue,
		models.CommandActionUnmuteIssue:        c.unmuteIssue,
		models.CommandActionMoveIssue:          c.handleMoveIssueCmd,
		models.CommandActionWebhook:            c.handleWebhook,
	}

	metrics.RegisterCounter(alertsTotal, "Total number of handled alerts", "channel")

	return c
}

// init initializes the channel manager with the specified issues.
func (c *channelManager) init(ctx context.Context, issues []*models.Issue) error {
	if c.initialized {
		return fmt.Errorf("channel manager can only be initialized once")
	}

	c.issueCollection = models.NewIssueCollection(issues)

	currentChannelName := c.slackClient.GetChannelName(ctx, c.channelID)

	// The channel name may have changed since the issue(s) where created. We update where needed.
	updated := c.issueCollection.UpdateChannelName(currentChannelName)

	if err := c.saveIssuesToDB(ctx, updated); err != nil {
		return fmt.Errorf("failed to save issues with updated Slack channel name: %w", err)
	}

	for _, issue := range updated {
		c.logger.WithFields(issue.LogFields()).Info("Updated Slack channel name")
	}

	c.logger.Infof("Channel manager initialized with %d issue(s)", len(issues))

	c.initialized = true

	return nil
}

// Run waits for and handles incoming alerts, commands and issues.
// It processes existing issues at given intervals, e.g. for archiving and escalation.
// This method blocks until the context is cancelled.
// All errors are logged and not returned.
func (c *channelManager) Run(ctx context.Context, waitGroup *sync.WaitGroup) {
	c.logger.Info("Channel manager started")
	defer c.logger.Info("Channel manager exited")

	defer waitGroup.Done()

	processorTimeout := time.After(time.Second)

	for {
		select {
		case <-ctx.Done():
			return
		case alert, ok := <-c.alertCh:
			if !ok {
				return
			}

			if err := c.processAlert(ctx, alert); err != nil {
				alert.MarkAsFailed()
				c.logger.WithFields(alert.LogFields()).Errorf("Failed to process alert: %s", err)
			}
		case cmd, ok := <-c.commandCh:
			if !ok {
				return
			}

			if err := c.processCmd(ctx, cmd); err != nil {
				cmd.MarkAsFailed()
				c.logger.WithFields(cmd.LogFields()).Errorf("Failed to process command: %s", err)
			}
		case issue, ok := <-c.movedIssueCh:
			if !ok {
				return
			}

			if err := c.processIncomingMovedIssue(ctx, issue); err != nil {
				c.logger.WithFields(issue.LogFields()).Errorf("Failed to process incoming moved issue: %s", err)
			}
		case <-processorTimeout:
			if err := c.processActiveIssues(ctx); err != nil {
				c.logger.Errorf("Failed to process active issues: %s", err)
			}

			processorTimeout = time.After(c.managerSettings.Settings.IssueProcessingInterval(c.channelID))
		}
	}
}

// QueueAlert adds an alert to the alert queue channel.
func (c *channelManager) QueueAlert(ctx context.Context, alert *models.Alert) error {
	if err := internal.TrySend(ctx, alert, c.alertCh); err != nil {
		return fmt.Errorf("failed to queue alert: %w", err)
	}
	return nil
}

// QueueCommand adds a command to the command queue channel.
func (c *channelManager) QueueCommand(ctx context.Context, cmd *models.Command) error {
	if err := internal.TrySend(ctx, cmd, c.commandCh); err != nil {
		return fmt.Errorf("failed to queue command: %w", err)
	}
	return nil
}

// QueueMovedIssue adds a moved issue to the issue queue channel.
func (c *channelManager) QueueMovedIssue(ctx context.Context, issue *models.Issue) error {
	if err := internal.TrySend(ctx, issue, c.movedIssueCh); err != nil {
		return fmt.Errorf("failed to queue moved issue: %w", err)
	}
	return nil
}

// FindIssueBySlackPost attempts to find an existing issue by the specified Slack post ID.
// If archived issues should be included in the search, it will search the database for older issues after first searching for active issues in memory.
func (c *channelManager) FindIssueBySlackPost(ctx context.Context, slackPostID string, includeArchived bool) (*models.Issue, error) {
	// Search the active issues first
	if activeIssue, found := c.issueCollection.FindActiveIssueBySlackPost(slackPostID); found {
		return activeIssue, nil
	}

	// No active issue found - give up if includeArchived is false
	if !includeArchived {
		return nil, nil
	}

	_, issueBody, err := c.db.FindSingleIssue(ctx, common.WithFieldEquals("lastAlert.slackChannelId", c.channelID), common.WithFieldEquals("slackPostId", slackPostID))
	if err != nil {
		return nil, fmt.Errorf("failed to search for issue in database: %w", err)
	}

	issue := &models.Issue{}

	if err := json.Unmarshal(issueBody, issue); err != nil {
		return nil, fmt.Errorf("failed to unmarshal issue body: %w", err)
	}

	// Sanity check
	if issue.LastAlert.SlackChannelID != c.channelID {
		return nil, fmt.Errorf("issue found in database is not associated with the current channel")
	}

	// Sanity check
	if issue.SlackPostID != slackPostID {
		return nil, fmt.Errorf("issue found in database does not match the specified Slack post ID")
	}

	return issue, nil
}

// processAlert handles an incoming alert, by adding it to an existing issue or creating a new issue.
// The alert may be ignored in certain situations.
func (c *channelManager) processAlert(ctx context.Context, alert *models.Alert) error {
	c.metrics.AddToCounter(alertsTotal, float64(1), c.channelID)

	alert.SetDefaultValues(c.managerSettings.Settings)
	alert.SlackChannelName = c.slackClient.GetChannelName(ctx, c.channelID)

	if err := alert.Alert.Validate(); err != nil {
		return fmt.Errorf("alert is invalid: %w", err)
	}

	logger := c.logger.WithFields(alert.LogFields())

	if err := c.cleanAlertEscalations(ctx, alert, logger); err != nil {
		logger.Errorf("Failed to clean alert escalations: %s", err)
	}

	if issue, ok := c.issueCollection.Find(alert.Alert.CorrelationID); ok {
		if err := c.addAlertToExistingIssue(ctx, issue, alert); err != nil {
			return fmt.Errorf("failed to add alert to existing issue: %w", err)
		}
	} else {
		if err := c.createNewIssue(ctx, alert, logger); err != nil {
			return fmt.Errorf("failed to create new issue: %w", err)
		}
	}

	alertBody, err := json.Marshal(alert)
	if err != nil {
		return fmt.Errorf("failed to marshal alert body: %w", err)
	}

	if err := c.db.SaveAlert(ctx, alert.ID, alertBody); err != nil {
		return fmt.Errorf("failed to save alert to database: %w", err)
	}

	if err := alert.Ack(ctx); err != nil {
		return fmt.Errorf("failed to ack alert: %w", err)
	}

	return nil
}

func (c *channelManager) cleanAlertEscalations(ctx context.Context, alert *models.Alert, logger common.Logger) error {
	for _, escalation := range alert.Escalation {
		if escalation.MoveToChannel == "" {
			continue
		}

		validChannel, reason, err := c.slackClient.IsAlertChannel(ctx, escalation.MoveToChannel)
		if err != nil {
			return fmt.Errorf("failed to check if channel %s is a valid alert channel: %w", escalation.MoveToChannel, err)
		}

		if !validChannel {
			logger.Infof("Ignoring alert escalation move to invalid channel %s: %s", escalation.MoveToChannel, reason)
			escalation.MoveToChannel = ""
		}
	}

	return nil
}

// processCmd handles an incoming command.
func (c *channelManager) processCmd(ctx context.Context, cmd *models.Command) error {
	logger := c.logger.WithFields(cmd.LogFields())

	// Commands are attempted exactly once, so we ack regardless of any errors below.
	// Errors are logged, but otherwise ignored.
	defer func() {
		go ackCommand(ctx, cmd, logger)
	}()

	issue, err := c.FindIssueBySlackPost(ctx, cmd.SlackPostID, cmd.IncludeArchivedIssues)
	if err != nil {
		return fmt.Errorf("failed to find issue by Slack post ID: %w", err)
	}

	if issue == nil {
		logger.Info("No issue found for Slack message command")
		return nil
	}

	logger = logger.WithFields(issue.LogFields())

	cmdFunc, ok := c.cmdFuncs[cmd.Action]
	if !ok {
		return fmt.Errorf("missing handler func for cmd %v", cmd.Action)
	}

	if err := cmdFunc(ctx, issue, cmd, logger); err != nil {
		return fmt.Errorf("failed to execute command: %w", err)
	}

	if err := c.saveIssueToDB(ctx, issue); err != nil {
		return fmt.Errorf("failed to save issue to database: %w", err)
	}

	return nil
}

func ackCommand(ctx context.Context, cmd *models.Command, logger common.Logger) {
	if err := cmd.Ack(ctx); err != nil {
		logger.Errorf("Failed to ack command: %s", err)
	}
}

// processAlert handles an incoming issue, i.e. an issue moved from another channel.
func (c *channelManager) processIncomingMovedIssue(ctx context.Context, issue *models.Issue) error {
	logger := c.logger.WithFields(issue.LogFields())

	if err := c.saveIssueToDB(ctx, issue); err != nil {
		return fmt.Errorf("failed to save issue to database: %w", err)
	}

	c.issueCollection.Add(issue)

	if err := c.slackClient.UpdateSingleIssue(ctx, issue); err != nil {
		return fmt.Errorf("failed to update issue in Slack: %w", err)
	}

	logger.Info("Add issue to channel")

	return nil
}

// processActiveIssues processes all active issues. It handles escalations, archiving and Slack updates (where needed).
func (c *channelManager) processActiveIssues(ctx context.Context) error {
	if c.issueCollection.Count() == 0 {
		return nil
	}

	started := time.Now()

	c.logger.WithField("count", c.issueCollection.Count()).Debug("Issue processing started")

	archivedIssues := c.issueCollection.RegisterArchiving()
	escalationResults := c.issueCollection.RegisterEscalation()

	for _, result := range escalationResults {
		if !result.Escalated || result.MoveToChannel == "" {
			continue
		}

		if err := c.moveEscalatedIssue(ctx, result); err != nil {
			c.logger.WithFields(result.Issue.LogFields()).Infof("Failed to move issue on escalation: %s", err)
		}
	}

	allIssues := c.issueCollection.All()

	if err := c.slackClient.Update(ctx, c.channelID, allIssues); err != nil {
		return fmt.Errorf("failed to update issues in Slack: %w", err)
	}

	if err := c.saveIssuesToDB(ctx, allIssues); err != nil {
		return fmt.Errorf("failed to save issues to database: %w", err)
	}

	// Remove the archived issues (if any) from the issues collection. They remain in the database with an archived flag.
	for _, issue := range archivedIssues {
		c.issueCollection.Remove(issue)
		c.logger.WithFields(issue.LogFields()).Info("Archive issue")
	}

	c.logger.WithField("count", c.issueCollection.Count()).WithField("elapsed", fmt.Sprintf("%v", time.Since(started))).Debug("Issue processing completed")

	return nil
}

// moveEscalatedIssue moves the specified issue to the channel indicated by the escalation result.
func (c *channelManager) moveEscalatedIssue(ctx context.Context, escalationResult *models.EscalationResult) error {
	validChannel, reason, err := c.slackClient.IsAlertChannel(ctx, escalationResult.MoveToChannel)
	if err != nil {
		return fmt.Errorf("failed to check if channel %s is a valid alert channel: %w", escalationResult.MoveToChannel, err)
	}

	logger := c.logger.WithFields(escalationResult.Issue.LogFields())

	if !validChannel {
		return fmt.Errorf("failed to move issue to channel %s: %s", escalationResult.MoveToChannel, reason)
	}

	return c.moveIssue(ctx, escalationResult.Issue, escalationResult.MoveToChannel, "Issue escalation", logger)
}

// addAlertToExistingIssue adds a new alert to an existing issue, updates the issue in the database and finally updates the corresponding Slack post.
func (c *channelManager) addAlertToExistingIssue(ctx context.Context, issue *models.Issue, alert *models.Alert) error {
	updated := issue.AddAlert(alert, c.logger)

	// No point in updating db or Slack if the alert was ignored
	if !updated {
		return nil
	}

	if err := c.saveIssueToDB(ctx, issue); err != nil {
		return fmt.Errorf("failed to save issue to database: %w", err)
	}

	if err := c.slackClient.UpdateSingleIssueWithThrottling(ctx, issue, c.issueCollection.Count()); err != nil {
		return fmt.Errorf("failed to update issue in Slack: %w", err)
	}

	return nil
}

// createNewIssue creates a new issue with the specified alert, stores the issue in the database and finally creates a new Slack post.
// Some alerts may be ignored.
func (c *channelManager) createNewIssue(ctx context.Context, alert *models.Alert, logger common.Logger) error {
	// Do not create a new issue for an alert that is already resolved
	if alert.IssueFollowUpEnabled && alert.Severity == common.AlertResolved {
		logger.Info("Ignoring resolved alert for new issue")
		return nil
	}

	issue := models.NewIssue(alert, c.logger)
	logger = logger.WithFields(issue.LogFields())

	if err := c.saveIssueToDB(ctx, issue); err != nil {
		return fmt.Errorf("failed to save issue to database: %w", err)
	}

	if err := c.slackClient.UpdateSingleIssue(ctx, issue); err != nil {
		return fmt.Errorf("failed to update issue in Slack: %w", err)
	}

	c.issueCollection.Add(issue)

	logger.Info("Create issue")

	return nil
}

func (c *channelManager) saveIssueToDB(ctx context.Context, issue *models.Issue) error {
	body, err := json.Marshal(issue)
	if err != nil {
		return fmt.Errorf("failed to marshal issue body: %w", err)
	}

	cacheKey, issueHash := hashIssue(issue.ID, body)

	if existingHash, ok := c.cache.Get(ctx, cacheKey); ok && existingHash == issueHash {
		return nil
	}

	if err := c.db.CreateOrUpdateIssue(ctx, issue.ID, body); err != nil {
		return fmt.Errorf("failed to save issue to database: %w", err)
	}

	c.cache.Set(ctx, cacheKey, issueHash, 24*time.Hour)

	return nil
}

func (c *channelManager) saveIssuesToDB(ctx context.Context, issues []*models.Issue) error {
	if len(issues) == 0 {
		return nil
	}

	if len(issues) == 1 {
		return c.saveIssueToDB(ctx, issues[0])
	}

	issueBodies := make(map[string]json.RawMessage)
	issueHashes := make(map[string]string)

	for _, issue := range issues {
		body, err := json.Marshal(issue)
		if err != nil {
			return fmt.Errorf("failed to marshal issue body: %w", err)
		}

		cacheKey, issueHash := hashIssue(issue.ID, body)

		if existingHash, ok := c.cache.Get(ctx, cacheKey); ok && existingHash == issueHash {
			return nil
		}

		issueBodies[issue.ID] = body
		issueHashes[cacheKey] = issueHash
	}

	if err := c.db.UpdateIssues(ctx, issueBodies); err != nil {
		return fmt.Errorf("failed to update issues in database: %w", err)
	}

	updated := len(issueBodies)
	c.logger.WithField("count", len(issues)).WithField("updated", updated).WithField("skipped", len(issues)-updated).Debug("Updated issue collection in database")

	for cacheKey, issueHash := range issueHashes {
		c.cache.Set(ctx, cacheKey, issueHash, 24*time.Hour)
	}

	return nil
}

func hashIssue(id string, body []byte) (string, string) {
	key := "hashIssue::" + id
	return key, string(internal.HashBytes(body))
}

func (c *channelManager) terminateIssue(ctx context.Context, issue *models.Issue, cmd *models.Command, logger common.Logger) error {
	logger.Info("Cmd: terminate issue")

	issue.RegisterTerminationRequest(cmd.UserRealName)

	return c.slackClient.Delete(ctx, issue, false, nil)
}

func (c *channelManager) resolveIssue(ctx context.Context, issue *models.Issue, cmd *models.Command, logger common.Logger) error {
	logger.Info("Cmd: resolve issue")

	issue.RegisterResolveRequest(cmd.UserRealName)

	return c.slackClient.UpdateSingleIssue(ctx, issue)
}

func (c *channelManager) unresolveIssue(ctx context.Context, issue *models.Issue, _ *models.Command, logger common.Logger) error {
	logger.Info("Cmd: unresolve issue")

	issue.RegisterUnresolveRequest()

	return c.slackClient.UpdateSingleIssue(ctx, issue)
}

func (c *channelManager) investigateIssue(ctx context.Context, issue *models.Issue, cmd *models.Command, logger common.Logger) error {
	logger.Info("Cmd: investigate issue")

	issue.RegisterInvestigateRequest(cmd.UserRealName)

	return c.slackClient.UpdateSingleIssue(ctx, issue)
}

func (c *channelManager) uninvestigateIssue(ctx context.Context, issue *models.Issue, _ *models.Command, logger common.Logger) error {
	logger.Info("Cmd: uninvestigate issue")

	issue.RegisterUninvestigateRequest()

	return c.slackClient.UpdateSingleIssue(ctx, issue)
}

func (c *channelManager) muteIssue(ctx context.Context, issue *models.Issue, cmd *models.Command, logger common.Logger) error {
	logger.Info("Cmd: mute issue")

	issue.RegisterMuteRequest(cmd.UserRealName)

	return c.slackClient.UpdateSingleIssue(ctx, issue)
}

func (c *channelManager) unmuteIssue(ctx context.Context, issue *models.Issue, _ *models.Command, logger common.Logger) error {
	logger.Info("Cmd: unmute issue")

	issue.RegisterUnmuteRequest()

	return c.slackClient.UpdateSingleIssue(ctx, issue)
}

func (c *channelManager) handleMoveIssueCmd(ctx context.Context, issue *models.Issue, cmd *models.Command, logger common.Logger) error {
	logger.Info("Cmd: move issue")

	if cmd.Parameters == nil {
		return fmt.Errorf("missing command parameters in move issue command")
	}

	targetChannel, ok := cmd.Parameters["targetChannelId"]
	if !ok {
		return fmt.Errorf("missing key 'targetChannelId' in move issue command parameters")
	}

	return c.moveIssue(ctx, issue, targetChannel.(string), cmd.UserRealName, logger)
}

func (c *channelManager) moveIssue(ctx context.Context, issue *models.Issue, targetChannel, username string, logger common.Logger) error {
	// Remove the current Slack post (if any)
	if err := c.slackClient.Delete(ctx, issue, false, nil); err != nil {
		return err
	}

	// Remove the issue from the issues collection.
	// The new channel manager(s) will ensure that the issue is updated in Slack and stored in the database.
	c.issueCollection.Remove(issue)

	request := &models.MoveRequest{
		Issue:         issue,
		UserRealName:  username,
		SourceChannel: c.channelID,
		TargetChannel: targetChannel,
	}

	if err := internal.TrySend(ctx, request, c.moveRequestCh); err != nil {
		return err
	}

	logger.Info("Remove issue from channel")

	return nil
}

func (c *channelManager) handleWebhook(ctx context.Context, issue *models.Issue, cmd *models.Command, logger common.Logger) error {
	logger.Info("Handle issue webhook")

	if cmd.WebhookParameters == nil {
		return fmt.Errorf("missing command webhook parameters in post webhook command")
	}

	webhook := issue.FindWebhook(cmd.WebhookParameters.WebhookID)

	if webhook == nil {
		return fmt.Errorf("no webhook found with ID %s", cmd.WebhookParameters.WebhookID)
	}

	logger = c.logger.WithField("url", webhook.URL)

	payload, err := internal.DecryptWebhookPayload(webhook, []byte(c.cfg.EncryptionKey))
	if err != nil {
		return fmt.Errorf("failed to decrypt webhook payload: %w", err)
	}

	data := &common.WebhookCallback{
		ID:            cmd.WebhookParameters.WebhookID,
		Timestamp:     time.Now(),
		UserID:        cmd.UserID,
		UserRealName:  cmd.UserRealName,
		ChannelID:     cmd.ChannelID,
		MessageID:     cmd.SlackPostID,
		Input:         cmd.WebhookParameters.Input,
		CheckboxInput: cmd.WebhookParameters.CheckboxInput,
		Payload:       payload,
	}

	go postWebhook(ctx, c.webhookClient, webhook.URL, data, logger)

	return nil
}

func postWebhook(ctx context.Context, client *resty.Client, url string, data *common.WebhookCallback, logger common.Logger) {
	response, err := client.R().SetContext(ctx).SetBody(data).Post(url)
	if err != nil {
		logger.Errorf("Webhook POST %s failed: %w", response.Request.URL, err)
		return
	}

	logger.Debugf("Webhook POST %s %s", response.Request.URL, response.Status())

	if !response.IsSuccess() {
		logger.Errorf("Webhook POST %s failed with status code %d", response.Request.URL, response.StatusCode())
	}
}

func webhookRetryPolicy(r *resty.Response, err error) bool {
	return err == nil && r.StatusCode() >= 500
}
