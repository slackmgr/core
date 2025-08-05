package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

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
	slackClient     *slack.Client
	db              common.DB
	webhookHandlers []WebhookHandler
	locker          ChannelLocker
	logger          common.Logger
	metrics         common.Metrics
	cfg             *config.ManagerConfig
	managerSettings *models.ManagerSettingsWrapper
	alertCh         chan *models.Alert
	commandCh       chan *models.Command
	movedIssueCh    chan *models.Issue
	cmdFuncs        map[models.CommandAction]cmdFunc
	initialized     bool
	openIssues      int
}

func newChannelManager(channelID string, slackClient *slack.Client, db common.DB, locker ChannelLocker, logger common.Logger, metrics common.Metrics, webhookHandlers []WebhookHandler, cfg *config.ManagerConfig,
	managerSettings *models.ManagerSettingsWrapper,
) *channelManager {
	logger = logger.WithField("slack_channel_id", channelID)

	if len(webhookHandlers) == 0 {
		defaultHandler := NewHTTPWebhookHandler(logger, cfg)
		webhookHandlers = append(webhookHandlers, defaultHandler)
	}

	c := &channelManager{
		channelID:       channelID,
		slackClient:     slackClient,
		db:              db,
		locker:          locker,
		logger:          logger,
		metrics:         metrics,
		webhookHandlers: webhookHandlers,
		cfg:             cfg,
		managerSettings: managerSettings,
		alertCh:         make(chan *models.Alert, 1000),
		commandCh:       make(chan *models.Command, 100),
		movedIssueCh:    make(chan *models.Issue, 10),
	}

	c.cmdFuncs = map[models.CommandAction]cmdFunc{
		models.CommandActionTerminateIssue:         c.terminateIssue,
		models.CommandActionResolveIssue:           c.resolveIssue,
		models.CommandActionUnresolveIssue:         c.unresolveIssue,
		models.CommandActionInvestigateIssue:       c.investigateIssue,
		models.CommandActionUninvestigateIssue:     c.uninvestigateIssue,
		models.CommandActionMuteIssue:              c.muteIssue,
		models.CommandActionUnmuteIssue:            c.unmuteIssue,
		models.CommandActionMoveIssue:              c.handleMoveIssueCmd,
		models.CommandActionShowIssueOptionButtons: c.showIssueOptionButtons,
		models.CommandActionHideIssueOptionButtons: c.hideIssueOptionButtons,
		models.CommandActionWebhook:                c.handleWebhook,
	}

	metrics.RegisterCounter(alertsTotal, "Total number of handled alerts", "channel")

	return c
}

// init initializes the channel manager with the specified issues.
func (c *channelManager) init(ctx context.Context, issues []*models.Issue) error {
	if c.initialized {
		return errors.New("channel manager can only be initialized once")
	}

	currentChannelName := c.slackClient.GetChannelName(ctx, c.channelID)

	lock, err := c.locker.Obtain(ctx, c.channelID, time.Minute, 10*time.Second)
	if err != nil {
		if errors.Is(err, ErrChannelLockUnavailable) {
			c.logger.Info("Failed to obtain lock for channel after 10 seconds - skipping channel name update on channel initialization")
			return nil
		}

		return fmt.Errorf("failed to obtain lock for channel %s: %w", c.channelID, err)
	}

	defer c.releaseLock(ctx, lock)

	// The channel name may have changed since the issue(s) where created. We update where needed.
	updated := []*models.Issue{}

	for _, issue := range issues {
		if issue.LastAlert == nil || issue.LastAlert.SlackChannelName == currentChannelName {
			continue
		}

		issue.LastAlert.SlackChannelName = currentChannelName

		updated = append(updated, issue)
	}

	if len(updated) > 0 {
		if err := c.saveIssuesToDB(ctx, updated); err != nil {
			return fmt.Errorf("failed to save issues with updated Slack channel name: %w", err)
		}

		for _, issue := range updated {
			c.logger.WithFields(issue.LogFields()).Info("Updated Slack channel name")
		}
	}

	c.logger.Infof("Channel manager initialized with %d open issue(s)", len(issues))

	c.initialized = true

	return nil
}

// run waits for and handles incoming alerts, commands and issues.
// It processes existing issues at given intervals, e.g. for archiving and escalation.
// This method blocks until the context is cancelled.
// All errors are logged and not returned.
func (c *channelManager) run(ctx context.Context, waitGroup *sync.WaitGroup) {
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
		case <-processorTimeout:
			interval := c.managerSettings.Settings.IssueProcessingInterval(c.channelID)

			if err := c.processActiveIssues(ctx, interval); err != nil {
				c.logger.Errorf("Failed to process active issues: %s", err)
			}

			processorTimeout = time.After(interval)
		}
	}
}

// queueAlert adds an alert to the alert queue channel.
func (c *channelManager) queueAlert(ctx context.Context, alert *models.Alert) error {
	if err := internal.TrySend(ctx, alert, c.alertCh); err != nil {
		return fmt.Errorf("failed to queue alert: %w", err)
	}
	return nil
}

// queueCommand adds a command to the command queue channel.
func (c *channelManager) queueCommand(ctx context.Context, cmd *models.Command) error {
	if err := internal.TrySend(ctx, cmd, c.commandCh); err != nil {
		return fmt.Errorf("failed to queue command: %w", err)
	}
	return nil
}

// findIssueBySlackPost attempts to find an existing issue by the specified Slack post ID.
// If no issue is found, nil is returned.
func (c *channelManager) findIssueBySlackPost(ctx context.Context, slackPostID string, includeArchived bool) (*models.Issue, error) {
	_, issueBody, err := c.db.FindIssueBySlackPostID(ctx, c.channelID, slackPostID)
	if err != nil {
		return nil, fmt.Errorf("failed to search for issue in database: %w", err)
	}

	if issueBody == nil {
		return nil, nil
	}

	issue := &models.Issue{}

	if err := json.Unmarshal(issueBody, issue); err != nil {
		return nil, fmt.Errorf("failed to unmarshal issue body: %w", err)
	}

	// Sanity check
	if issue.LastAlert.SlackChannelID != c.channelID {
		return nil, errors.New("issue found in database is not associated with the current channel")
	}

	// Sanity check
	if issue.SlackPostID != slackPostID {
		return nil, errors.New("issue found in database does not match the specified Slack post ID")
	}

	if !includeArchived && issue.Archived {
		return nil, nil
	}

	return issue, nil
}

// processAlert handles an incoming alert, by adding it to an existing issue or creating a new issue.
// The alert may be ignored in certain situations.
//
// This method must obtain a lock for the channel before processing the alert.
func (c *channelManager) processAlert(ctx context.Context, alert *models.Alert) error {
	lock, err := c.locker.Obtain(ctx, c.channelID, 5*time.Minute, 30*time.Second)
	if err != nil {
		return fmt.Errorf("failed to obtain lock for channel %s after 30 seconds: %w", c.channelID, err)
	}

	defer c.releaseLock(ctx, lock)

	c.metrics.AddToCounter(alertsTotal, float64(1), c.channelID)

	alert.SetDefaultValues(c.managerSettings.Settings)
	alert.SlackChannelName = c.slackClient.GetChannelName(ctx, c.channelID)

	if err := alert.Validate(); err != nil {
		return fmt.Errorf("alert is invalid: %w", err)
	}

	logger := c.logger.WithFields(alert.LogFields())

	if err := c.cleanAlertEscalations(ctx, alert, logger); err != nil {
		logger.Errorf("Failed to clean alert escalations: %s", err)
	}

	_, issueBody, err := c.db.FindOpenIssueByCorrelationID(ctx, c.channelID, alert.CorrelationID)
	if err != nil {
		return fmt.Errorf("failed to find open issue by correlation ID: %w", err)
	}

	if issueBody != nil {
		issue := &models.Issue{}

		if err := json.Unmarshal(issueBody, issue); err != nil {
			return fmt.Errorf("failed to unmarshal issue body: %w", err)
		}

		if err := c.addAlertToExistingIssue(ctx, issue, alert); err != nil {
			return fmt.Errorf("failed to add alert to existing issue: %w", err)
		}
	} else {
		if err := c.createNewIssue(ctx, alert, logger); err != nil {
			return fmt.Errorf("failed to create new issue: %w", err)
		}
	}

	if err := c.db.SaveAlert(ctx, &alert.Alert); err != nil {
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
//
// This method must obtain a lock for the channel before processing the command.
func (c *channelManager) processCmd(ctx context.Context, cmd *models.Command) error {
	lock, err := c.locker.Obtain(ctx, c.channelID, 5*time.Minute, 30*time.Second)
	if err != nil {
		return fmt.Errorf("failed to obtain lock for channel %s after 30 seconds: %w", c.channelID, err)
	}

	defer c.releaseLock(ctx, lock)

	logger := c.logger.WithFields(cmd.LogFields())

	// Commands are attempted exactly once, so we ack regardless of any errors below.
	// Errors are logged, but otherwise ignored.
	defer func() {
		go ackCommand(ctx, cmd, logger)
	}()

	issue, err := c.findIssueBySlackPost(ctx, cmd.SlackPostID, cmd.IncludeArchivedIssues)
	if err != nil {
		return fmt.Errorf("failed to find issue by Slack post ID: %w", err)
	}

	if issue == nil && !cmd.ExecuteWhenNoIssueFound {
		logger.Info("No issue found for Slack message command")
		return nil
	}

	if issue != nil {
		logger = logger.WithFields(issue.LogFields())
	}

	cmdFunc, ok := c.cmdFuncs[cmd.Action]
	if !ok {
		return fmt.Errorf("missing handler func for cmd %v", cmd.Action)
	}

	if err := cmdFunc(ctx, issue, cmd, logger); err != nil {
		return fmt.Errorf("failed to execute command: %w", err)
	}

	if issue != nil {
		if err := c.saveIssueToDB(ctx, issue); err != nil {
			return fmt.Errorf("failed to save issue to database: %w", err)
		}
	}

	return nil
}

func ackCommand(ctx context.Context, cmd *models.Command, logger common.Logger) {
	if err := cmd.Ack(ctx); err != nil {
		logger.Errorf("Failed to ack command: %s", err)
	}
}

// processActiveIssues processes all active issues. It handles escalations, archiving and Slack updates (where needed).
// It skips processing if the last processing time is within the specified interval. This may happen if there are
// multiple managers running in parallel, e.g. in a Kubernetes cluster.
//
// This method must obtain a lock for the channel before processing the issues.
func (c *channelManager) processActiveIssues(ctx context.Context, minInterval time.Duration) error {
	lock, err := c.locker.Obtain(ctx, c.channelID, 5*time.Minute, 2*minInterval)
	if err != nil {
		if errors.Is(err, ErrChannelLockUnavailable) {
			c.logger.Errorf("Failed to obtain lock for channel after %d seconds - skipping channel processing", int(2*minInterval.Seconds()))
			return nil
		}
		return fmt.Errorf("failed to obtain lock for channel %s: %w", c.channelID, err)
	}

	defer c.releaseLock(ctx, lock)

	processingState, err := c.db.FindChannelProcessingState(ctx, c.channelID)
	if err != nil {
		return fmt.Errorf("failed to find channel processing state: %w", err)
	}

	if processingState == nil {
		processingState = common.NewChannelProcessingState(c.channelID)
	}

	// Skip processing if the last processing time is within the min interval.
	if time.Since(processingState.LastProcessed) < minInterval {
		c.logger.WithField("last_processed", processingState.LastProcessed).Debug("Skipping issue processing due to recent processing by other manager")
		return nil
	}

	processingState.LastProcessed = time.Now()

	if err := c.db.SaveChannelProcessingState(ctx, processingState); err != nil {
		return fmt.Errorf("failed to save channel processing state: %w", err)
	}

	issueBodies, err := c.db.LoadOpenIssuesInChannel(ctx, c.channelID)
	if err != nil {
		return fmt.Errorf("failed to load open issues from database: %w", err)
	}

	c.openIssues = len(issueBodies)

	if len(issueBodies) == 0 {
		return nil
	}

	issues := make(map[string]*models.Issue)

	for _, body := range issueBodies {
		issue := &models.Issue{}

		if err := json.Unmarshal(body, issue); err != nil {
			return fmt.Errorf("failed to unmarshal issue body: %w", err)
		}

		issues[issue.ID] = issue
	}

	started := time.Now()

	c.logger.WithField("count", len(issues)).Debug("Issue processing started")

	escalationResults := []*models.EscalationResult{}
	openIssues := 0

	for _, issue := range issues {
		if issue.IsReadyForArchiving() {
			issue.RegisterArchiving()
		} else {
			openIssues++
			escalationResults = append(escalationResults, issue.ApplyEscalationRules())
		}
	}

	movedIssues := make(map[string]struct{})

	for _, result := range escalationResults {
		if !result.Escalated || result.MoveToChannel == "" {
			continue
		}

		if err := c.moveEscalatedIssue(ctx, result); err != nil {
			c.logger.WithFields(result.Issue.LogFields()).Errorf("Failed to move issue on escalation: %s", err)
			continue
		}

		movedIssues[result.Issue.ID] = struct{}{}
		openIssues--
	}

	issuesToUpdate := []*models.Issue{}

	// If the issue was moved, we do not update it in Slack or the database.
	// The move process has already updated the issue in Slack and the database.
	for _, issue := range issues {
		if _, ok := movedIssues[issue.ID]; ok {
			c.logger.WithFields(issue.LogFields()).Info("Issue was moved, skipping update in Slack and database")
			continue
		}

		issuesToUpdate = append(issuesToUpdate, issue)
	}

	// Update the issues in Slack.
	// Errors from the Slack client are logged, but not returned.
	if err := c.slackClient.Update(ctx, c.channelID, issuesToUpdate); err != nil {
		c.logger.Errorf("Failed to update issues in Slack: %s", err)
	}

	// Saved the issues to the database.
	if err := c.saveIssuesToDB(ctx, issuesToUpdate); err != nil {
		return fmt.Errorf("failed to save issues to database: %w", err)
	}

	c.logger.WithField("count", issuesToUpdate).WithField("moved_count", len(movedIssues)).WithField("elapsed", fmt.Sprintf("%v", time.Since(started))).Debug("Issue processing completed")

	c.openIssues = openIssues

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

// addAlertToExistingIssue adds a new alert to an existing issue, updates the corresponding Slack post, and finally updates the issue in the database.
func (c *channelManager) addAlertToExistingIssue(ctx context.Context, issue *models.Issue, alert *models.Alert) error {
	updated := issue.AddAlert(alert, c.logger)

	// No point in updating db or Slack if the alert was ignored by the existing issue
	if !updated {
		return nil
	}

	slackErr := c.slackClient.UpdateSingleIssueWithThrottling(ctx, issue, "alert added to existing issue", c.openIssues)

	if err := c.saveIssueToDB(ctx, issue); err != nil {
		return fmt.Errorf("failed to save issue to database: %w", err)
	}

	if slackErr != nil {
		return fmt.Errorf("failed to update issue in Slack: %w", slackErr)
	}

	return nil
}

// createNewIssue creates a new issue with the specified alert, creates a new Slack post, and finally stores the issue in the database.
// Some alerts may be ignored, for various reasons.
func (c *channelManager) createNewIssue(ctx context.Context, alert *models.Alert, logger common.Logger) error {
	// Do not create a new issue for an alert that is already resolved
	if alert.IssueFollowUpEnabled && alert.Severity == common.AlertResolved {
		logger.Info("Ignoring resolved alert for new issue")
		return nil
	}

	issue := models.NewIssue(alert, c.logger)
	logger = logger.WithFields(issue.LogFields())

	// Slack errors are eventually returned, but we first need to store the issue in the database
	slackErr := c.slackClient.UpdateSingleIssue(ctx, issue, "new issue created")

	// Save the issue to the database, regardless of any Slack errors.
	// This ensures that the issue is available in the database, even if the Slack update fails.
	if err := c.saveIssueToDB(ctx, issue); err != nil {
		return fmt.Errorf("failed to save issue to database: %w", err)
	}

	logger.Info("Create issue")

	// No we can return a possible error from the Slack client, after the issue has been saved to the database.
	if slackErr != nil {
		return fmt.Errorf("failed to update issue in Slack: %w", slackErr)
	}

	return nil
}

func (c *channelManager) saveIssueToDB(ctx context.Context, issue *models.Issue) error {
	if issue == nil {
		return nil
	}

	if err := c.db.SaveIssue(ctx, issue); err != nil {
		return fmt.Errorf("failed to save issue to database: %w", err)
	}

	return nil
}

func (c *channelManager) saveIssuesToDB(ctx context.Context, issues []*models.Issue) error {
	if len(issues) == 0 {
		return nil
	}

	// Convert issues to common.Issue slice for db.SaveIssues
	issuesToUpdate := make([]common.Issue, len(issues))

	for i, issue := range issues {
		issuesToUpdate[i] = issue
	}

	if err := c.db.SaveIssues(ctx, issuesToUpdate...); err != nil {
		return fmt.Errorf("failed to update issues in database: %w", err)
	}

	c.logger.WithField("count", len(issues)).Debug("Updated issue collection in database")

	return nil
}

func (c *channelManager) terminateIssue(ctx context.Context, issue *models.Issue, cmd *models.Command, logger common.Logger) error {
	logger.Info("Cmd: terminate issue")

	if issue != nil {
		issue.RegisterTerminationRequest(cmd.UserRealName)
		return c.slackClient.Delete(ctx, issue, "terminate issue request", false, nil)
	}

	return c.slackClient.DeletePost(ctx, cmd.SlackChannelID, cmd.SlackPostID)
}

func (c *channelManager) resolveIssue(ctx context.Context, issue *models.Issue, cmd *models.Command, logger common.Logger) error {
	logger.Info("Cmd: resolve issue")

	issue.RegisterResolveRequest(cmd.UserRealName)

	return c.slackClient.UpdateSingleIssue(ctx, issue, "resolve issue request")
}

func (c *channelManager) unresolveIssue(ctx context.Context, issue *models.Issue, _ *models.Command, logger common.Logger) error {
	logger.Info("Cmd: unresolve issue")

	issue.RegisterUnresolveRequest()

	return c.slackClient.UpdateSingleIssue(ctx, issue, "unresolve issue request")
}

func (c *channelManager) investigateIssue(ctx context.Context, issue *models.Issue, cmd *models.Command, logger common.Logger) error {
	logger.Info("Cmd: investigate issue")

	issue.RegisterInvestigateRequest(cmd.UserRealName)

	return c.slackClient.UpdateSingleIssue(ctx, issue, "investigate issue request")
}

func (c *channelManager) uninvestigateIssue(ctx context.Context, issue *models.Issue, _ *models.Command, logger common.Logger) error {
	logger.Info("Cmd: uninvestigate issue")

	issue.RegisterUninvestigateRequest()

	return c.slackClient.UpdateSingleIssue(ctx, issue, "uninvestigate issue request")
}

func (c *channelManager) muteIssue(ctx context.Context, issue *models.Issue, cmd *models.Command, logger common.Logger) error {
	logger.Info("Cmd: mute issue")

	issue.RegisterMuteRequest(cmd.UserRealName)

	return c.slackClient.UpdateSingleIssue(ctx, issue, "mute issue request")
}

func (c *channelManager) unmuteIssue(ctx context.Context, issue *models.Issue, _ *models.Command, logger common.Logger) error {
	logger.Info("Cmd: unmute issue")

	issue.RegisterUnmuteRequest()

	return c.slackClient.UpdateSingleIssue(ctx, issue, "unmute issue request")
}

func (c *channelManager) handleMoveIssueCmd(ctx context.Context, issue *models.Issue, cmd *models.Command, logger common.Logger) error {
	logger.Info("Cmd: move issue")

	if cmd.Parameters == nil {
		return errors.New("missing command parameters in move issue command")
	}

	targetChannel, ok := cmd.Parameters["targetChannelId"]
	if !ok {
		return errors.New("missing key 'targetChannelId' in move issue command parameters")
	}

	targetChannelStr, ok := targetChannel.(string)
	if !ok {
		return errors.New("target channel ID is not a string")
	}

	return c.moveIssue(ctx, issue, targetChannelStr, cmd.UserRealName, logger)
}

func (c *channelManager) showIssueOptionButtons(ctx context.Context, issue *models.Issue, _ *models.Command, logger common.Logger) error {
	logger.Info("Cmd: show issue option buttons")

	issue.RegisterShowOptionButtonsRequest()

	return c.slackClient.UpdateSingleIssue(ctx, issue, "show issue option buttons request")
}

func (c *channelManager) hideIssueOptionButtons(ctx context.Context, issue *models.Issue, _ *models.Command, logger common.Logger) error {
	logger.Info("Cmd: hide issue option buttons")

	issue.RegisterHideOptionButtonsRequest()

	return c.slackClient.UpdateSingleIssue(ctx, issue, "hide issue option buttons request")
}

func (c *channelManager) moveIssue(ctx context.Context, issue *models.Issue, targetChannel, username string, logger common.Logger) error {
	// Obtain a lock for the *target channel*
	targetChannelLock, err := c.locker.Obtain(ctx, targetChannel, 30*time.Second, time.Minute)
	if err != nil {
		return fmt.Errorf("failed to obtain lock for target channel %s after 5 minutes: %w", c.channelID, err)
	}

	defer c.releaseLock(ctx, targetChannelLock)

	// Create a new move mapping to track the move of the issue.
	moveMapping := models.NewMoveMapping(issue.CorrelationID, issue.ChannelID(), targetChannel)

	// Save information about the move, so that future alerts are routed correctly.
	if err := c.db.SaveMoveMapping(ctx, moveMapping); err != nil {
		return err
	}

	logger = logger.WithField("target_slack_channel_id", targetChannel).WithField("correlation_id", issue.CorrelationID)

	logger.Info("Saved move mapping")

	// Remove the current Slack post (if any).
	if err := c.slackClient.Delete(ctx, issue, "Issue moved between channels", false, nil); err != nil {
		return err
	}

	// Find the Slack channel name for the new channel.
	channelName := c.slackClient.GetChannelName(ctx, targetChannel)

	// Register the move request on the issue. This will override the Slack channel ID on the last alert.
	issue.RegisterMoveRequest(username, targetChannel, channelName)

	// Create a new Slack post for the issue in the target channel.
	// Errors from the Slack client are logged, but not returned.
	// If the Slack post is not created from some reason, it will be created on the nest issue processing cycle.
	if err := c.slackClient.UpdateSingleIssue(ctx, issue, "incoming moved issue"); err != nil {
		logger.Errorf("Failed to update moved issue in Slack: %s", err)
	}

	// Save the issue to the database, regardless of any Slack errors above.
	// This ensures that the issue is available in the database, even if the Slack update have failed.
	if err := c.saveIssueToDB(ctx, issue); err != nil {
		return fmt.Errorf("failed to save issue to database: %w", err)
	}

	logger.Info("Issue moved to target channel")

	return nil

	// request := models.NewMoveRequest(issue.CorrelationID, c.channelID, targetChannel, username)

	// body, err := json.Marshal(request)
	// if err != nil {
	// 	return fmt.Errorf("failed to marshal move request: %w", err)
	// }

	// if err := c.moveRequestQueue.Send(ctx, c.channelID, request.DedupID(), string(body)); err != nil {
	// 	return fmt.Errorf("failed to send move request to queue: %w", err)
	// }

	// logger.Info("Remove issue from channel")
}

func (c *channelManager) handleWebhook(ctx context.Context, issue *models.Issue, cmd *models.Command, logger common.Logger) error {
	logger.Info("Handle issue webhook")

	if cmd.WebhookParameters == nil {
		return errors.New("missing command webhook parameters in post webhook command")
	}

	webhook := issue.FindWebhook(cmd.WebhookParameters.WebhookID)

	if webhook == nil {
		return fmt.Errorf("no webhook found with ID %s", cmd.WebhookParameters.WebhookID)
	}

	logger = c.logger.WithField("webhook_target", webhook.URL)

	payload, err := internal.DecryptWebhookPayload(webhook, []byte(c.cfg.EncryptionKey))
	if err != nil {
		return fmt.Errorf("failed to decrypt webhook payload: %w", err)
	}

	var handler WebhookHandler

	for _, h := range c.webhookHandlers {
		if h.ShouldHandleWebhook(ctx, webhook.URL) {
			handler = h
			break
		}
	}

	if handler == nil {
		return fmt.Errorf("no webhook handler found for target %s", webhook.URL)
	}

	data := &common.WebhookCallback{
		ID:            cmd.WebhookParameters.WebhookID,
		Timestamp:     time.Now(),
		UserID:        cmd.UserID,
		UserRealName:  cmd.UserRealName,
		ChannelID:     cmd.SlackChannelID,
		MessageID:     cmd.SlackPostID,
		Input:         cmd.WebhookParameters.Input,
		CheckboxInput: cmd.WebhookParameters.CheckboxInput,
		Payload:       payload,
	}

	// We do this in a separate goroutine to avoid blocking the command processing.
	// The webhook may fail, in which case we log the error and continue.
	go postWebhook(ctx, handler, webhook.URL, data, logger)

	return nil
}

func (c *channelManager) releaseLock(ctx context.Context, lock ChannelLock) {
	if err := lock.Release(ctx); err != nil {
		c.logger.Errorf("Failed to release lock for channel %s: %s", c.channelID, err)
	}
}

func postWebhook(ctx context.Context, handler WebhookHandler, target string, data *common.WebhookCallback, logger common.Logger) {
	if err := handler.HandleWebhook(ctx, target, data, logger); err != nil {
		logger.Errorf("Failed to handle webhook: %s", err)
	}
}
