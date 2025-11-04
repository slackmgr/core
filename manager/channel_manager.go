package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/peteraglen/slack-manager/internal"
	"github.com/peteraglen/slack-manager/manager/internal/models"
	"github.com/peteraglen/slack-manager/manager/internal/slack"
)

const alertsTotal = "processed_alerts_total"

type cmdFunc func(ctx context.Context, issue *models.Issue, cmd *models.Command, logger common.Logger) (bool, error)

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
	keepAliveCh     chan struct{}
	cmdFuncs        map[models.CommandAction]cmdFunc
}

func newChannelManager(channelID string, slackClient *slack.Client, db common.DB, locker ChannelLocker, logger common.Logger, metrics common.Metrics, webhookHandlers []WebhookHandler, cfg *config.ManagerConfig,
	managerSettings *models.ManagerSettingsWrapper,
) *channelManager {
	logger = logger.WithField("channel_id", channelID)

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
		keepAliveCh:     make(chan struct{}, 10),
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

// run waits for and handles incoming alerts, commands and issues.
// It processes existing issues at given intervals, e.g. for archiving and escalation.
// This method blocks until the context is cancelled.
// All errors are logged and not returned.
func (c *channelManager) run(ctx context.Context) {
	c.logger.Info("Channel manager started")
	defer c.logger.Info("Channel manager exited")

	processorTimeout := time.After(time.Second)
	processorIsRunning := true

	for {
		interval := c.managerSettings.Settings.IssueProcessingInterval(c.channelID)

		select {
		case <-ctx.Done():
			return
		case alert, ok := <-c.alertCh:
			if !ok {
				return
			}

			if err := c.processAlert(ctx, alert); err != nil {
				alert.Nack(ctx)
				c.logger.WithFields(alert.LogFields()).Errorf("Failed to process alert: %s", err)
			} else {
				alert.Ack(ctx)
			}

			// Make sure the issue processor is running after an alert is received.
			if !processorIsRunning {
				processorTimeout = time.After(interval)
				processorIsRunning = true
			}
		case cmd, ok := <-c.commandCh:
			if !ok {
				return
			}

			if err := c.processCmd(ctx, cmd); err != nil {
				cmd.Nack(ctx)
				c.logger.WithFields(cmd.LogFields()).Errorf("Failed to process command: %s", err)
			} else {
				cmd.Ack(ctx)
			}

			// Make sure the issue processor is running after a command is received.
			if !processorIsRunning {
				processorTimeout = time.After(interval)
				processorIsRunning = true
			}
		case <-processorTimeout:
			openIssues, err := c.processActiveIssues(ctx, interval)
			if err != nil {
				c.logger.Errorf("Failed to process active issues: %s", err)
			}

			// If no error occurred AND no open issues exists, stop the processing loop (i.e. don't create a new timeout).
			// It will be started again when a new alert or command is received, or when a keep-alive signal is received from the coordinator.
			if err == nil && openIssues == 0 {
				processorIsRunning = false
				c.logger.Info("Issue processing stopped - no activity in channel")
				continue
			}

			// If we reach this point, it means that either an error occurred or there are still open issues in the channel.
			// Either way, we need to continue processing open issues.
			processorTimeout = time.After(interval)
			processorIsRunning = true
		case <-c.keepAliveCh:
			// The coordinator has sent a keep-alive signal, indicating that there are active issues in the channel.
			// Restart the processing loop if it was stopped.
			if !processorIsRunning {
				processorTimeout = time.After(interval)
				processorIsRunning = true
				c.logger.Info("Issue processing restarted - received keep-alive signal")
			}
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

// keepAlive sends a keep-alive signal to the channel manager, indicating that open issues exist.
// This is used by the coordinator to ensure processing of active issues in the channel, even if no new alerts or commands are received.
func (c *channelManager) keepAlive(ctx context.Context) error {
	if err := internal.TrySend(ctx, struct{}{}, c.keepAliveCh); err != nil {
		return fmt.Errorf("failed to queue keep-alive signal: %w", err)
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
	lock, err := c.obtainLock(ctx, c.channelID, 5*time.Minute, 30*time.Second)
	if err != nil {
		return fmt.Errorf("failed to obtain lock for channel %s after 30 seconds: %w", c.channelID, err)
	}

	defer c.releaseLock(lock) //nolint:contextcheck

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
	lock, err := c.obtainLock(ctx, c.channelID, 5*time.Minute, 30*time.Second)
	if err != nil {
		return fmt.Errorf("failed to obtain lock for channel %s after 30 seconds: %w", c.channelID, err)
	}

	defer c.releaseLock(lock) //nolint:contextcheck

	logger := c.logger.WithFields(cmd.LogFields())

	// Commands are attempted exactly once, so we ack regardless of any errors below.
	// Errors are logged, but otherwise ignored.
	defer func() {
		go ackCommand(ctx, cmd)
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

	dbUpdateNeeded, err := cmdFunc(ctx, issue, cmd, logger)
	if err != nil {
		return fmt.Errorf("failed to execute command: %w", err)
	}

	if issue != nil && dbUpdateNeeded {
		if err := c.db.SaveIssue(ctx, issue); err != nil {
			return fmt.Errorf("failed to save issue to database: %w", err)
		}
	}

	return nil
}

func ackCommand(ctx context.Context, cmd *models.Command) {
	cmd.Ack(ctx)
}

// processActiveIssues processes all active issues. It handles escalations, archiving and Slack updates (where needed).
// It skips processing if the last processing time is within the specified interval. This may happen if there are
// multiple managers running in parallel, e.g. in a Kubernetes cluster.
//
// This method must obtain a lock for the channel before processing the issues.
func (c *channelManager) processActiveIssues(ctx context.Context, minInterval time.Duration) (int, error) {
	lock, err := c.obtainLock(ctx, c.channelID, 5*time.Minute, 2*minInterval)
	if err != nil {
		return 0, fmt.Errorf("failed to obtain lock for channel %s after 2 minutes: %w", c.channelID, err)
	}

	defer c.releaseLock(lock) //nolint:contextcheck

	processingState, err := c.db.FindChannelProcessingState(ctx, c.channelID)
	if err != nil {
		return 0, fmt.Errorf("failed to find channel processing state: %w", err)
	}

	// No processing state found means this is the first time we are processing this channel.
	// Create a new processing state and proceed.
	if processingState == nil {
		processingState = common.NewChannelProcessingState(c.channelID)
		c.logger.Debug("No channel processing state found in database, creating a new one")
	}

	timeSinceLastProcessed := time.Since(processingState.LastProcessed)

	// If the processing state indicates that the channel was processed recently, skip processing.
	// This can happen if there are multiple managers running in parallel, e.g. in a Kubernetes cluster.
	if timeSinceLastProcessed < minInterval {
		c.logger.WithField("last_processed", timeSinceLastProcessed).WithField("min_interval", minInterval).Debug("Skipping issue processing due to recent processing by other manager")
		return processingState.OpenIssues, nil
	}

	c.logger.WithField("last_processed", timeSinceLastProcessed).WithField("min_interval", minInterval).Debug("Processing issues in channel")

	issueBodies, err := c.db.LoadOpenIssuesInChannel(ctx, c.channelID)
	if err != nil {
		return 0, fmt.Errorf("failed to load open issues from database: %w", err)
	}

	now := time.Now().UTC()

	// Update the processing state with the current time. This is used to ensure that the processing is not repeated too often (by multiple managers).
	processingState.LastProcessed = now

	// Update the processing state with the number of open issues in the channel.
	processingState.OpenIssues = len(issueBodies)

	// We update the LastChannelActivity only if there are open issues.
	if processingState.OpenIssues > 0 {
		processingState.LastChannelActivity = now
	}

	// Save the updated processing state to the database.
	if err := c.db.SaveChannelProcessingState(ctx, processingState); err != nil {
		return 0, fmt.Errorf("failed to save channel processing state: %w", err)
	}

	// Stop here if there are no open issues in the channel.
	if processingState.OpenIssues == 0 {
		return 0, nil
	}

	issues := make(map[string]*models.Issue)

	for _, body := range issueBodies {
		issue := &models.Issue{}

		if err := json.Unmarshal(body, issue); err != nil {
			return 0, fmt.Errorf("failed to unmarshal issue body: %w", err)
		}

		issues[issue.ID] = issue
	}

	started := time.Now()

	c.logger.WithField("count", len(issues)).Debug("Issue processing started")

	escalationResults := []*models.EscalationResult{}
	openIssues := 0

	currentChannelName := c.slackClient.GetChannelName(ctx, c.channelID)

	for _, issue := range issues {
		if issue.IsReadyForArchiving() {
			if err := c.registerIssueAsArchived(ctx, issue); err != nil {
				c.logger.WithFields(issue.LogFields()).Errorf("Failed to archive issue: %s", err)
			}
			continue
		}

		openIssues++

		// The channel name may have changed since the issue was created. Update it if needed.
		issue.LastAlert.SlackChannelName = currentChannelName

		escalationResults = append(escalationResults, issue.ApplyEscalationRules())
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

	// If the issue was moved, we do not update it in Slack or the database. The move process has already taken care of that.
	for _, issue := range issues {
		if _, ok := movedIssues[issue.ID]; ok {
			c.logger.WithFields(issue.LogFields()).Debug("Issue was moved, skipping update in Slack and database")
			continue
		}

		issuesToUpdate = append(issuesToUpdate, issue)
	}

	// Update the issues in Slack.
	// Errors from the Slack client are logged, but not returned.
	if err := c.slackClient.Update(ctx, c.channelID, issuesToUpdate); err != nil {
		c.logger.Errorf("Failed to update issues in Slack: %s", err)
	}

	// Saved the issues to the database. This includes archived issues, which are marked as such in the database.
	if err := c.saveIssuesToDB(ctx, issuesToUpdate); err != nil {
		return 0, fmt.Errorf("failed to save issues to database: %w", err)
	}

	c.logger.WithField("count", len(issuesToUpdate)).WithField("moved_count", len(movedIssues)).WithField("elapsed", time.Since(started)).Debug("Issue processing completed")

	return processingState.OpenIssues, nil
}

// registerIssueAsArchived marks the issue as archived (but does not update it in the database).
// It also deletes any escalation move mappings related to the issue, if applicable.
func (c *channelManager) registerIssueAsArchived(ctx context.Context, issue *models.Issue) error {
	issue.RegisterArchiving()

	c.logger.WithField("correlation_id", issue.CorrelationID).Info("Issue archived")

	moveMappingBody, err := c.db.FindMoveMapping(ctx, issue.LastAlert.OriginalSlackChannelID, issue.CorrelationID)
	if err != nil {
		return fmt.Errorf("failed to find move mapping for issue %s in channel %s: %w", issue.CorrelationID, issue.LastAlert.OriginalSlackChannelID, err)
	}

	if moveMappingBody != nil {
		var moveMapping *models.MoveMapping

		if err := json.Unmarshal(moveMappingBody, &moveMapping); err != nil {
			return fmt.Errorf("failed to unmarshal move mapping body: %w", err)
		}

		if moveMapping.Reason == models.MoveIssueReasonEscalation {
			if err := c.db.DeleteMoveMapping(ctx, moveMapping.OriginalChannelID, moveMapping.CorrelationID); err != nil {
				return fmt.Errorf("failed to delete escalation move mapping for issue %s in channel %s: %w", moveMapping.CorrelationID, moveMapping.OriginalChannelID, err)
			}

			c.logger.WithField("correlation_id", moveMapping.CorrelationID).Info("Escalation move mapping deleted for archived issue")
		}
	}

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

	return c.moveIssue(ctx, escalationResult.Issue, escalationResult.MoveToChannel, models.MoveIssueReasonEscalation, "N/A", logger)
}

// addAlertToExistingIssue adds a new alert to an existing issue, updates the corresponding Slack post, and finally updates the issue in the database.
func (c *channelManager) addAlertToExistingIssue(ctx context.Context, issue *models.Issue, alert *models.Alert) error {
	updated := issue.AddAlert(alert, c.logger)

	// No point in updating db or Slack if the alert was ignored by the existing issue
	if !updated {
		return nil
	}

	openIssues := 0

	processingState, err := c.db.FindChannelProcessingState(ctx, c.channelID)
	if err != nil {
		c.logger.Errorf("Failed to find channel processing state: %s", err)
	} else if processingState != nil {
		openIssues = processingState.OpenIssues
	}

	// Update the issue in Slack.
	// Errors from the Slack client are logged, but not returned.
	if err := c.slackClient.UpdateSingleIssueWithThrottling(ctx, issue, "alert added to existing issue", openIssues); err != nil {
		c.logger.Errorf("Failed to update issue in Slack: %s", err)
	}

	if err := c.db.SaveIssue(ctx, issue); err != nil {
		return fmt.Errorf("failed to save issue to database: %w", err)
	}

	return nil
}

// createNewIssue creates a new issue with the specified alert, creates a new Slack post, and finally stores the issue in the database.
// Some alerts may be ignored, for various reasons.
func (c *channelManager) createNewIssue(ctx context.Context, alert *models.Alert, logger common.Logger) error {
	// Do not create a new issue for an alert that is already resolved
	if alert.IssueFollowUpEnabled && alert.Severity == common.AlertResolved {
		logger.Info("Resolved alert ignored for new issue")
		return nil
	}

	issue := models.NewIssue(alert, c.logger)
	logger = logger.WithFields(issue.LogFields())

	// Create the issue post in Slack.
	// Errors from the Slack client are logged, but not returned.
	if err := c.slackClient.UpdateSingleIssue(ctx, issue, "new issue created"); err != nil {
		c.logger.Errorf("Failed to update new issue in Slack: %s", err)
	}

	// Save the issue to the database, regardless of any Slack errors.
	// This ensures that the issue is available in the database, even if the Slack update fails.
	if err := c.db.SaveIssue(ctx, issue); err != nil {
		return fmt.Errorf("failed to save issue to database: %w", err)
	}

	logger.Info("Issue created")

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

	c.logger.WithField("count", len(issues)).Debug("Issues saved to database")

	return nil
}

func (c *channelManager) terminateIssue(ctx context.Context, issue *models.Issue, cmd *models.Command, logger common.Logger) (bool, error) {
	logger.Info("Cmd: terminate issue")

	if issue != nil {
		issue.RegisterTerminationRequest(cmd.UserRealName)
		return true, c.slackClient.Delete(ctx, issue, "terminate issue request", false, nil)
	}

	return false, c.slackClient.DeletePost(ctx, cmd.SlackChannelID, cmd.SlackPostID)
}

func (c *channelManager) resolveIssue(ctx context.Context, issue *models.Issue, cmd *models.Command, logger common.Logger) (bool, error) {
	logger.Info("Cmd: resolve issue")

	issue.RegisterResolveRequest(cmd.UserRealName)

	return true, c.slackClient.UpdateSingleIssue(ctx, issue, "resolve issue request")
}

func (c *channelManager) unresolveIssue(ctx context.Context, issue *models.Issue, _ *models.Command, logger common.Logger) (bool, error) {
	logger.Info("Cmd: unresolve issue")

	issue.RegisterUnresolveRequest()

	return true, c.slackClient.UpdateSingleIssue(ctx, issue, "unresolve issue request")
}

func (c *channelManager) investigateIssue(ctx context.Context, issue *models.Issue, cmd *models.Command, logger common.Logger) (bool, error) {
	logger.Info("Cmd: investigate issue")

	issue.RegisterInvestigateRequest(cmd.UserRealName)

	return true, c.slackClient.UpdateSingleIssue(ctx, issue, "investigate issue request")
}

func (c *channelManager) uninvestigateIssue(ctx context.Context, issue *models.Issue, _ *models.Command, logger common.Logger) (bool, error) {
	logger.Info("Cmd: uninvestigate issue")

	issue.RegisterUninvestigateRequest()

	return true, c.slackClient.UpdateSingleIssue(ctx, issue, "uninvestigate issue request")
}

func (c *channelManager) muteIssue(ctx context.Context, issue *models.Issue, cmd *models.Command, logger common.Logger) (bool, error) {
	logger.Info("Cmd: mute issue")

	issue.RegisterMuteRequest(cmd.UserRealName)

	return true, c.slackClient.UpdateSingleIssue(ctx, issue, "mute issue request")
}

func (c *channelManager) unmuteIssue(ctx context.Context, issue *models.Issue, _ *models.Command, logger common.Logger) (bool, error) {
	logger.Info("Cmd: unmute issue")

	issue.RegisterUnmuteRequest()

	return true, c.slackClient.UpdateSingleIssue(ctx, issue, "unmute issue request")
}

func (c *channelManager) handleMoveIssueCmd(ctx context.Context, issue *models.Issue, cmd *models.Command, logger common.Logger) (bool, error) {
	logger.Info("Cmd: move issue")

	if cmd.Parameters == nil {
		return false, errors.New("missing command parameters in move issue command")
	}

	targetChannel, ok := cmd.Parameters["targetChannelId"]
	if !ok {
		return false, errors.New("missing key 'targetChannelId' in move issue command parameters")
	}

	targetChannelStr, ok := targetChannel.(string)
	if !ok {
		return false, errors.New("target channel ID is not a string")
	}

	// We return false, indicating that the issue does not need to be saved to the database.
	// The moveIssue method will handle saving the issue to the database.
	return false, c.moveIssue(ctx, issue, targetChannelStr, models.MoveIssueReasonUserCommand, cmd.UserRealName, logger)
}

func (c *channelManager) showIssueOptionButtons(ctx context.Context, issue *models.Issue, _ *models.Command, logger common.Logger) (bool, error) {
	logger.Info("Cmd: show issue option buttons")

	issue.RegisterShowOptionButtonsRequest()

	return true, c.slackClient.UpdateSingleIssue(ctx, issue, "show issue option buttons request")
}

func (c *channelManager) hideIssueOptionButtons(ctx context.Context, issue *models.Issue, _ *models.Command, logger common.Logger) (bool, error) {
	logger.Info("Cmd: hide issue option buttons")

	issue.RegisterHideOptionButtonsRequest()

	return true, c.slackClient.UpdateSingleIssue(ctx, issue, "hide issue option buttons request")
}

func (c *channelManager) moveIssue(ctx context.Context, issue *models.Issue, targetChannel string, reason models.MoveIssueReason, username string, logger common.Logger) error {
	if issue.LastAlert.SlackChannelID == targetChannel {
		logger.Errorf("Cannot move issue %s to the same channel", issue.CorrelationID)
		return nil
	}

	// Obtain a lock for the *target channel*
	targetChannelLock, err := c.obtainLock(ctx, targetChannel, 30*time.Second, time.Minute)
	if err != nil {
		return fmt.Errorf("failed to obtain lock for target channel %s after 1 minute: %w", targetChannel, err)
	}

	defer c.releaseLock(targetChannelLock) //nolint:contextcheck

	// The original channel is the channel where the issue was created. If this is the same as the target channel, we need to delete the move mapping.
	originalChannel := issue.LastAlert.OriginalSlackChannelID

	// The source channel is the channel where the issue is currently located (i.e. the channel managed by this channel manager).
	// It may or may not be the same as the original channel, depending on how many times the issue has been moved.
	sourceChannel := c.channelID

	if originalChannel != targetChannel {
		// Create a new move mapping to track the move of the issue. The mapping must be created for the original channel,
		// not the current source channel, since the issue may have been moved multiple times.
		moveMapping := models.NewMoveMapping(issue.CorrelationID, originalChannel, targetChannel, reason)

		// Save information about the move, so that future alerts are routed correctly.
		// If a previous move mappings exists for the current combination of channel and correlation ID, it will be overwritten.
		if err := c.db.SaveMoveMapping(ctx, moveMapping); err != nil {
			return err
		}

		logger.WithField("target_slack_channel_id", targetChannel).WithField("correlation_id", issue.CorrelationID).Info("Move mapping saved")
	} else {
		// If the original and target channels are the same, we remove any existing move mapping for the issue.
		if err := c.db.DeleteMoveMapping(ctx, originalChannel, issue.CorrelationID); err != nil {
			return fmt.Errorf("failed to delete move mapping for issue %s in channel %s: %w", issue.CorrelationID, originalChannel, err)
		}

		logger.WithField("correlation_id", issue.CorrelationID).Info("Move mapping deleted")
	}

	// Remove the current Slack post (if any).
	if err := c.slackClient.Delete(ctx, issue, "Issue moved between channels", false, nil); err != nil {
		return err
	}

	// Find the Slack channel name for the new channel.
	channelName := c.slackClient.GetChannelName(ctx, targetChannel)

	// Register the move request on the issue. This will override the Slack channel ID on the last alert.
	issue.RegisterMoveRequest(reason, username, targetChannel, channelName)

	// Create a new Slack post for the issue in the target channel.
	// Errors from the Slack client are logged, but not returned.
	// If the Slack post is not created from some reason, it will be created on the next issue processing cycle.
	if err := c.slackClient.UpdateSingleIssue(ctx, issue, "incoming moved issue"); err != nil {
		logger.Errorf("Failed to update moved issue in Slack: %s", err)
	}

	// Save the issue to the database, regardless of any Slack errors above.
	// This ensures that the issue is available in the database, even if the Slack update have failed.
	if err := c.db.MoveIssue(ctx, issue, sourceChannel, targetChannel); err != nil {
		return fmt.Errorf("failed to move issue in the database: %w", err)
	}

	logger.Info("Issue moved to target channel")

	return nil
}

func (c *channelManager) handleWebhook(ctx context.Context, issue *models.Issue, cmd *models.Command, logger common.Logger) (bool, error) {
	logger.Info("Handle issue webhook")

	if cmd.WebhookParameters == nil {
		return false, errors.New("missing command webhook parameters in post webhook command")
	}

	webhook := issue.FindWebhook(cmd.WebhookParameters.WebhookID)

	if webhook == nil {
		return false, fmt.Errorf("no webhook found with ID %s", cmd.WebhookParameters.WebhookID)
	}

	logger = c.logger.WithField("webhook_target", webhook.URL)

	payload, err := internal.DecryptWebhookPayload(webhook, []byte(c.cfg.EncryptionKey))
	if err != nil {
		return false, fmt.Errorf("failed to decrypt webhook payload: %w", err)
	}

	var handler WebhookHandler

	for _, h := range c.webhookHandlers {
		if h.ShouldHandleWebhook(ctx, webhook.URL) {
			handler = h
			break
		}
	}

	if handler == nil {
		return false, fmt.Errorf("no webhook handler found for target %s", webhook.URL)
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

	// We return false, indicating that the issue does not need to be saved to the database (since we did not modify it).
	return false, nil
}

func (c *channelManager) obtainLock(ctx context.Context, channelID string, ttl, maxWait time.Duration) (ChannelLock, error) { //nolint:ireturn
	lock, err := c.locker.Obtain(ctx, channelID, ttl, maxWait)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain lock for channel %s after %d seconds: %w", c.channelID, int(maxWait.Seconds()), err)
	}

	c.logger.WithField("lock_key", channelID).Debug("Channel lock obtained")

	return lock, nil
}

// releaseLock releases the lock for the specified channel.
// We can't use the regular context here, since it's vital that the lock is released even if the context is cancelled.
func (c *channelManager) releaseLock(lock ChannelLock) {
	key := lock.Key()

	if err := lock.Release(context.Background()); err != nil {
		c.logger.WithField("lock_key", key).Errorf("Failed to release lock: %s", err)
	} else {
		c.logger.WithField("lock_key", key).Debug("Channel lock released")
	}
}

func postWebhook(ctx context.Context, handler WebhookHandler, target string, data *common.WebhookCallback, logger common.Logger) {
	if err := handler.HandleWebhook(ctx, target, data, logger); err != nil {
		logger.Errorf("Failed to handle webhook: %s", err)
	}
}
