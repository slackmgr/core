package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-resty/resty/v2"
	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/client"
	"github.com/peteraglen/slack-manager/core/config"
	"github.com/peteraglen/slack-manager/core/models"
	"github.com/peteraglen/slack-manager/internal"
)

const alertsTotal = "processed_alerts_total"

type cmdFunc func(ctx context.Context, issue *models.Issue, cmd *models.Command, logger common.Logger) error

type channelManager struct {
	channelID       string
	issueCollection *models.IssueCollection
	slackClient     Slack
	db              DB
	webhookClient   *resty.Client
	logger          common.Logger
	metrics         common.Metrics
	conf            *config.Config
	alertCh         chan *models.Alert
	commandCh       chan *models.Command
	addIssueCh      chan *models.Issue
	moveRequestCh   chan<- *models.MoveRequest
	cmdFuncs        map[models.CommandAction]cmdFunc
	initialized     bool
}

func newChannelManager(channelID string, slackClient Slack, db DB, moveRequestCh chan<- *models.MoveRequest, logger common.Logger, metrics common.Metrics, conf *config.Config) *channelManager {
	restyLogger := newRestyLogger(logger)
	webhookClient := resty.New().SetRetryCount(2).SetRetryWaitTime(time.Second).AddRetryCondition(webhookRetryPolicy).SetLogger(restyLogger).SetTimeout(conf.WebhookTimeout)

	c := &channelManager{
		channelID:     channelID,
		slackClient:   slackClient,
		db:            db,
		webhookClient: webhookClient,
		moveRequestCh: moveRequestCh,
		logger:        logger,
		metrics:       metrics,
		conf:          conf,
		alertCh:       make(chan *models.Alert, conf.ChannelManager.AlertChannelSize),
		commandCh:     make(chan *models.Command, conf.ChannelManager.CommandChannelSize),
		addIssueCh:    make(chan *models.Issue, 10),
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

// Init initializes the channel manager with the specified issues.
func (c *channelManager) Init(ctx context.Context, issues []*models.Issue) {
	if c.initialized {
		panic("Channel manager can only be initialized once")
	}

	c.issueCollection = models.NewIssueCollection(issues)

	currentChannelName := c.slackClient.GetChannelName(ctx, c.channelID)

	// The channel name may have changed since the issue(s) where created. We update where needed.
	updated := c.issueCollection.UpdateChannelName(currentChannelName)

	for _, issue := range updated {
		logger := c.logger.WithFields(issue.LogFields())

		body, err := json.Marshal(issue)
		if err != nil {
			logger.Errorf("Failed to marshal issue body: %s", err)
			continue
		}

		if err := c.db.CreateOrUpdateIssue(ctx, issue.ID, body); err != nil {
			c.logger.Errorf("Failed to update Slack channel name for existing issue: %s", err)
		} else {
			logger.Info("Updated Slack channel name")
		}
	}

	c.logger.Infof("Channel manager initialized with %d issue(s)", len(issues))

	c.initialized = true
}

// Run waits for and handles incoming alerts, commands and issues.
// It processes existing issues at given intervals, e.g. for archiving and escalation.
// This method blocks until the context is cancelled (all errors are logged, not returned).
func (c *channelManager) Run(ctx context.Context) error {
	c.logger.Info("Channel manager started")
	defer c.logger.Info("Channel manager exited")

	processorTimeout := time.After(time.Second)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case alert, ok := <-c.alertCh:
			if !ok {
				return nil
			}

			if err := c.processAlert(ctx, alert); err != nil {
				c.logger.Errorf("Failed to process alert: %s", err)
			}
		case cmd, ok := <-c.commandCh:
			if !ok {
				return nil
			}

			if err := c.processCmd(ctx, cmd); err != nil {
				c.logger.Errorf("Failed to process command: %s", err)
			}
		case issue, ok := <-c.addIssueCh:
			if !ok {
				return nil
			}

			if err := c.processIncomingIssue(ctx, issue); err != nil {
				c.logger.Errorf("Failed to process incoming issue: %s", err)
			}
		case <-processorTimeout:
			c.processActiveIssues(ctx)
			processorTimeout = time.After(c.conf.ProcessInterval)
		}
	}
}

// QueueAlert adds an alert to the alert queue channel.
func (c *channelManager) QueueAlert(ctx context.Context, alert *models.Alert) error {
	return internal.TrySend(ctx, alert, c.alertCh)
}

// QueueCommand adds a command to the command queue channel.
func (c *channelManager) QueueCommand(ctx context.Context, cmd *models.Command) error {
	return internal.TrySend(ctx, cmd, c.commandCh)
}

// QueueMovedIssue adds a moved issue to the issue queue channel.
func (c *channelManager) QueueMovedIssue(ctx context.Context, issue *models.Issue) error {
	return internal.TrySend(ctx, issue, c.addIssueCh)
}

// FindIssueBySlackPost attempts to find an existing issue by the specified Slack post ID.
// If archived issues should be included in the search, it will search the database for older issues after first searching for active issues in memory.
func (c *channelManager) FindIssueBySlackPost(ctx context.Context, slackPostID string, includeArchived bool) *models.Issue {
	// Search the active issues first
	if activeIssue, found := c.issueCollection.FindActiveIssueBySlackPost(slackPostID); found {
		return activeIssue
	}

	// No active issue found - give up if includeArchived is false
	if !includeArchived {
		return nil
	}

	// Search the database for archived issues
	filterTerms := map[string]interface{}{
		"lastAlert.slackChannelId": c.channelID,
		"slackPostId":              slackPostID,
	}

	id, issueBody, err := c.db.FindSingleIssue(ctx, filterTerms)
	if err != nil {
		c.logger.Errorf("Failed to search for issue in database: %s", err)
		return nil
	}

	issue := &models.Issue{}

	if err := json.Unmarshal(issueBody, issue); err != nil {
		c.logger.Errorf("Failed to unmarshal issue body: %s", err)
		return nil
	}

	// Backwards compatibility: Set the ID if it is missing
	issue.ID = id

	return issue
}

// processAlert handles an incoming alert, by adding it to an existing issue or creating a new issue.
// The alert may be ignored in certain situations.
func (c *channelManager) processAlert(ctx context.Context, alert *models.Alert) error {
	c.metrics.AddToCounter(alertsTotal, float64(1), c.channelID)

	alert.SetDefaultValues(c.conf.DefaultArchivingDelay)
	alert.SlackChannelName = c.slackClient.GetChannelName(ctx, c.channelID)

	logger := c.logger.WithField("correlation_id", alert.Alert.CorrelationID)

	if err := alert.Alert.Validate(); err != nil {
		logger.Errorf("Ignoring invalid alert: %s", err)
		return nil
	}

	for _, escalation := range alert.Escalation {
		if escalation.MoveToChannel == "" {
			continue
		}

		validChannel, reason, err := c.slackClient.IsAlertChannel(ctx, escalation.MoveToChannel)
		if err != nil {
			return fmt.Errorf("failed to check if Slack manager is in channel %s: %w", escalation.MoveToChannel, err)
		}

		if !validChannel {
			logger.Infof("Ignoring alert escalation move to channel %s: %s", escalation.MoveToChannel, reason)
			escalation.MoveToChannel = ""
		}
	}

	issue, found := c.issueCollection.Find(alert.Alert.CorrelationID)

	if found {
		if err := c.addAlertToExistingIssue(ctx, issue, alert); err != nil {
			return fmt.Errorf("failed to update issue: %w", err)
		}
	} else {
		if err := c.createNewIssue(ctx, alert, logger); err != nil {
			return fmt.Errorf("failed to create new issue: %w", err)
		}
	}

	alertBody, err := json.Marshal(alert.Alert)
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

// processCmd handles an incoming command.
func (c *channelManager) processCmd(ctx context.Context, cmd *models.Command) error {
	logger := c.logger.WithFields(cmd.LogFields())

	// Commands are attempted exactly once, so we ack regardless of any errors below.
	// Errors are logged, but otherwise ignored.
	defer func() {
		go ackCommand(ctx, cmd, logger)
	}()

	issue := c.FindIssueBySlackPost(ctx, cmd.SlackPostID, cmd.IncludeArchivedIssues)

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
		c.logger.Errorf("Failed to process command: %s", err)
	}

	body, err := json.Marshal(issue)
	if err != nil {
		return fmt.Errorf("failed to marshal issue body: %w", err)
	}

	if err := c.db.CreateOrUpdateIssue(ctx, issue.ID, body); err != nil {
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
func (c *channelManager) processIncomingIssue(ctx context.Context, issue *models.Issue) error {
	logger := c.logger.WithFields(issue.LogFields())

	body, err := json.Marshal(issue)
	if err != nil {
		return fmt.Errorf("failed to marshal issue body: %w", err)
	}

	if err := c.db.CreateOrUpdateIssue(ctx, issue.ID, body); err != nil {
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
func (c *channelManager) processActiveIssues(ctx context.Context) {
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
		c.logger.Errorf("Failed to update issues in Slack: %s", err)
	}

	issueBodies := make(map[string]json.RawMessage, len(allIssues))

	for _, issue := range allIssues {
		body, err := json.Marshal(issue)
		if err != nil {
			c.logger.WithFields(issue.LogFields()).Errorf("Failed to marshal issue body: %s", err)
			continue
		}

		issueBodies[issue.ID] = body
	}

	updated, err := c.db.UpdateIssues(ctx, issueBodies)
	if err != nil {
		c.logger.Errorf("Failed to update issues in database: %s", err)
	} else {
		c.logger.WithField("count", len(allIssues)).WithField("updated", updated).WithField("skipped", len(allIssues)-updated).Debug("Updated issue collection in database")
	}

	// Remove the archived issues (if any) from the issues collection. They remain in the database with an archived flag.
	for _, issue := range archivedIssues {
		c.issueCollection.Remove(issue)
		c.logger.WithFields(issue.LogFields()).Info("Archive issue")
	}

	c.logger.WithField("count", c.issueCollection.Count()).WithField("elapsed", fmt.Sprintf("%v", time.Since(started))).Debug("Issue processing completed")
}

// moveEscalatedIssue moves the specified issue to the channel indicated by the escalation result.
// The method returns an error if the target channel is not valid (e.g. the Slack Manager is not a channel member).
func (c *channelManager) moveEscalatedIssue(ctx context.Context, escalationResult *models.EscalationResult) error {
	validChannel, reason, err := c.slackClient.IsAlertChannel(ctx, escalationResult.MoveToChannel)
	if err != nil {
		return fmt.Errorf("failed to check if Slack manager is in channel %s: %w", escalationResult.MoveToChannel, err)
	}

	logger := c.logger.WithFields(escalationResult.Issue.LogFields())

	if !validChannel {
		return fmt.Errorf("failed to move issue to channel %s: %s", escalationResult.MoveToChannel, reason)
	}

	return c.moveIssue(ctx, escalationResult.Issue, escalationResult.MoveToChannel, "SUDO Slack Manager Escalation", logger)
}

// addAlertToExistingIssue adds a new alert to an existing issue, updates the issue in the database and finally updates the corresponding Slack post.
func (c *channelManager) addAlertToExistingIssue(ctx context.Context, issue *models.Issue, alert *models.Alert) error {
	updated := issue.AddAlert(alert, c.logger)

	// No point in updating db or Slack if the alert was ignored
	if !updated {
		return nil
	}

	body, err := json.Marshal(issue)
	if err != nil {
		return fmt.Errorf("failed to marshal issue body: %w", err)
	}

	if err := c.db.CreateOrUpdateIssue(ctx, issue.ID, body); err != nil {
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
	if alert.IssueFollowUpEnabled && alert.Severity == client.AlertResolved {
		logger.Info("Ignoring resolved alert for new issue")
		return nil
	}

	issue := models.NewIssue(alert, c.logger)
	logger = logger.WithFields(issue.LogFields())

	body, err := json.Marshal(issue)
	if err != nil {
		return fmt.Errorf("failed to marshal issue body: %w", err)
	}

	if err := c.db.CreateOrUpdateIssue(ctx, issue.ID, body); err != nil {
		return fmt.Errorf("failed to save issue to database: %w", err)
	}

	if err := c.slackClient.UpdateSingleIssue(ctx, issue); err != nil {
		return fmt.Errorf("failed to update issue in Slack: %w", err)
	}

	c.issueCollection.Add(issue)

	logger.Info("Create issue")

	return nil
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

	payload, err := webhook.DecryptPayload([]byte(c.conf.EncryptionKey))
	if err != nil {
		return fmt.Errorf("failed to decrypt webhook payload: %w", err)
	}

	data := &client.WebhookCallback{
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

	go c.postWebhook(ctx, webhook.URL, data, logger)

	return nil
}

func (c *channelManager) postWebhook(ctx context.Context, url string, data *client.WebhookCallback, logger common.Logger) {
	response, err := c.webhookClient.R().SetContext(ctx).SetBody(data).Post(url)
	if err != nil {
		c.logger.Errorf("POST %s failed: %w", response.Request.URL, err)
		return
	}

	logger.Debugf("POST %s %s", response.Request.URL, response.Status())

	if !response.IsSuccess() {
		c.logger.Errorf("POST %s failed with status code %d", response.Request.URL, response.StatusCode())
	}
}

func webhookRetryPolicy(r *resty.Response, err error) bool {
	return err == nil && r.StatusCode() >= 500
}
