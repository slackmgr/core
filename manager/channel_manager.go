package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/eko/gocache/lib/v4/store"
	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/peteraglen/slack-manager/internal"
	"github.com/peteraglen/slack-manager/manager/internal/models"
	"github.com/peteraglen/slack-manager/manager/internal/slack"
)

const alertsTotal = "processed_alerts_total"

type cmdFunc func(ctx context.Context, issue *models.Issue, cmd *models.Command, logger common.Logger) error

type channelManager struct {
	channelID string
	// issueCollection *models.IssueCollection
	slackClient     *slack.Client
	db              DB
	webhookHandlers []WebhookHandler
	// cache           *internal.Cache[string]
	locker          ChannelLocker
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
	openIssues      int
}

func newChannelManager(channelID string, slackClient *slack.Client, db DB, moveRequestCh chan<- *models.MoveRequest,
	_ store.StoreInterface, locker ChannelLocker, logger common.Logger, metrics common.Metrics, webhookHandlers []WebhookHandler, cfg *config.ManagerConfig,
	managerSettings *models.ManagerSettingsWrapper,
) *channelManager {
	// cacheKeyPrefix := cfg.CacheKeyPrefix + "channel-manager::" + channelID + "::"
	// cache := internal.NewCache[string](cacheStore, cacheKeyPrefix, logger)

	logger = logger.WithField("slack_channel_id", channelID)

	if len(webhookHandlers) == 0 {
		defaultHandler := NewHTTPWebhookHandler(logger, cfg)
		webhookHandlers = append(webhookHandlers, defaultHandler)
	}

	c := &channelManager{
		channelID:     channelID,
		slackClient:   slackClient,
		db:            db,
		moveRequestCh: moveRequestCh,
		// cache:           cache,
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
			c.logger.Info("Failed to obtain lock for channel in 10 seconds - skipping channel name update on channel initialization")
			return nil
		}

		return fmt.Errorf("failed to obtain lock for channel %s: %w", c.channelID, err)
	}

	defer c.releaseLock(ctx, lock)

	// c.issueCollection = models.NewIssueCollection(issues)

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

// queueMovedIssue adds a moved issue to the issue queue channel.
func (c *channelManager) queueMovedIssue(ctx context.Context, issue *models.Issue) error {
	if err := internal.TrySend(ctx, issue, c.movedIssueCh); err != nil {
		return fmt.Errorf("failed to queue moved issue: %w", err)
	}
	return nil
}

// findIssueBySlackPost attempts to find an existing issue by the specified Slack post ID.
// If archived issues should be included in the search, it will search the database for older issues after first searching for active issues in memory.
// If no issue is found, nil is returned.
func (c *channelManager) findIssueBySlackPost(ctx context.Context, slackPostID string, includeArchived bool) (*models.Issue, error) {
	// // Search the active issues first
	// if activeIssue, found := c.issueCollection.FindActiveIssueBySlackPost(slackPostID); found {
	// 	return activeIssue, nil
	// }

	// // No active issue found - give up if includeArchived is false
	// if !includeArchived {
	// 	return nil, nil
	// }

	issueBody, err := c.db.FindIssueBySlackPostID(ctx, c.channelID, slackPostID)
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

	if issue.Archived && !includeArchived {
		return nil, nil
	}

	return issue, nil
}

// processAlert handles an incoming alert, by adding it to an existing issue or creating a new issue.
// The alert may be ignored in certain situations.
//
// This method must obtain a lock for the channel before processing the alert.
func (c *channelManager) processAlert(ctx context.Context, alert *models.Alert) error {
	lock, err := c.locker.Obtain(ctx, c.channelID, 5*time.Minute, 10*time.Second)
	if err != nil {
		return fmt.Errorf("failed to obtain lock for channel %s after 10 seconds: %w", c.channelID, err)
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

	issueBody, err := c.db.FindOpenIssueByCorrelationID(ctx, c.channelID, alert.CorrelationID)
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

	if err := c.db.SaveAlert(ctx, c.channelID, &alert.Alert); err != nil {
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
	lock, err := c.locker.Obtain(ctx, c.channelID, 5*time.Minute, 10*time.Second)
	if err != nil {
		return fmt.Errorf("failed to obtain lock for channel %s after 10 seconds: %w", c.channelID, err)
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

// processAlert handles an incoming issue, i.e. an issue moved from another channel.
//
// This method must obtain a lock for the channel before processing the issue.
func (c *channelManager) processIncomingMovedIssue(ctx context.Context, issue *models.Issue) error {
	lock, err := c.locker.Obtain(ctx, c.channelID, 5*time.Minute, time.Minute)
	if err != nil {
		return fmt.Errorf("failed to obtain lock for channel %s after 10 seconds: %w", c.channelID, err)
	}

	defer c.releaseLock(ctx, lock)

	logger := c.logger.WithFields(issue.LogFields())

	// c.issueCollection.Add(issue)

	// Slack errors are eventually returned, but we first need to store the issue in the database
	slackErr := c.slackClient.UpdateSingleIssue(ctx, issue, "incoming moved issue")

	// Save the issue to the database, regardless of any Slack errors.
	// This ensures that the issue is available in the database, even if the Slack update fails.
	if err := c.saveIssueToDB(ctx, issue); err != nil {
		return fmt.Errorf("failed to save issue to database: %w", err)
	}

	logger.Info("Add issue to channel")

	// No we can return a possible error from the Slack client, after the issue has been saved to the database.
	if slackErr != nil {
		return fmt.Errorf("failed to update issue in Slack: %w", slackErr)
	}

	return nil
}

// processActiveIssues processes all active issues. It handles escalations, archiving and Slack updates (where needed).
//
// This method must obtain a lock for the channel before processing the issues.
func (c *channelManager) processActiveIssues(ctx context.Context) error {
	lock, err := c.locker.Obtain(ctx, c.channelID, 5*time.Minute, 10*time.Second)
	if err != nil {
		return fmt.Errorf("failed to obtain lock for channel %s after 10 seconds: %w", c.channelID, err)
	}

	defer c.releaseLock(ctx, lock)

	// if c.issueCollection.Count() == 0 {
	// 	return nil
	// }

	issueBodies, err := c.db.LoadOpenIssuesInChannel(ctx, c.channelID)
	if err != nil {
		return fmt.Errorf("failed to load open issues from database: %w", err)
	}

	if len(issueBodies) == 0 {
		return nil
	}

	issues := make([]*models.Issue, 0, len(issueBodies))

	for _, body := range issueBodies {
		issue := &models.Issue{}

		if err := json.Unmarshal(body, issue); err != nil {
			return fmt.Errorf("failed to unmarshal issue body: %w", err)
		}

		issues = append(issues, issue)
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

	// archivedIssues := c.issueCollection.RegisterArchiving()
	// escalationResults := c.issueCollection.RegisterEscalation()

	for _, result := range escalationResults {
		if !result.Escalated || result.MoveToChannel == "" {
			continue
		}

		if err := c.moveEscalatedIssue(ctx, result); err != nil {
			c.logger.WithFields(result.Issue.LogFields()).Infof("Failed to move issue on escalation: %s", err)
		}
	}

	if err := c.slackClient.Update(ctx, c.channelID, issues); err != nil {
		return fmt.Errorf("failed to update issues in Slack: %w", err)
	}

	if err := c.saveIssuesToDB(ctx, issues); err != nil {
		return fmt.Errorf("failed to save issues to database: %w", err)
	}

	// // Remove the archived issues (if any) from the issues collection. They remain in the database with an archived flag.
	// for _, issue := range archivedIssues {
	// 	c.issueCollection.Remove(issue)
	// 	c.logger.WithFields(issue.LogFields()).Info("Archive issue")
	// }

	c.logger.WithField("count", openIssues).WithField("elapsed", fmt.Sprintf("%v", time.Since(started))).Debug("Issue processing completed")

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

	// c.issueCollection.Add(issue)

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

	// body, err := json.Marshal(issue)
	// if err != nil {
	// 	return fmt.Errorf("failed to marshal issue body: %w", err)
	// }

	// cacheKey, issueHash := hashIssue(issue.ID, body)

	// // No point in updating db if the issue has not changed
	// if existingHash, ok := c.cache.Get(ctx, cacheKey); ok && existingHash == issueHash {
	// 	return nil
	// }

	if err := c.db.CreateOrUpdateIssue(ctx, c.channelID, issue); err != nil {
		return fmt.Errorf("failed to save issue to database: %w", err)
	}

	// c.cache.Set(ctx, cacheKey, issueHash, 24*time.Hour)

	return nil
}

func (c *channelManager) saveIssuesToDB(ctx context.Context, issues []*models.Issue) error {
	if len(issues) == 0 {
		return nil
	}

	issuesToUpdate := []common.Issue{}
	// issueHashes := make(map[string]string)

	for _, issue := range issues {
		// body, err := json.Marshal(issue)
		// if err != nil {
		// 	return fmt.Errorf("failed to marshal issue body: %w", err)
		// }

		// cacheKey, issueHash := hashIssue(issue.ID, body)

		// // No point in updating db if the issue has not changed
		// if existingHash, ok := c.cache.Get(ctx, cacheKey); ok && existingHash == issueHash {
		// 	continue
		// }

		issuesToUpdate = append(issuesToUpdate, issue)
		// issueHashes[cacheKey] = issueHash
	}

	// No changed issues found
	if len(issuesToUpdate) == 0 {
		return nil
	}

	// Batch update all changed issues
	if err := c.db.UpdateIssues(ctx, c.channelID, issuesToUpdate...); err != nil {
		return fmt.Errorf("failed to update issues in database: %w", err)
	}

	updated := len(issuesToUpdate)

	c.logger.WithField("count", len(issues)).WithField("updated", updated).WithField("skipped", len(issues)-updated).Debug("Updated issue collection in database")

	// for cacheKey, issueHash := range issueHashes {
	// 	c.cache.Set(ctx, cacheKey, issueHash, 24*time.Hour)
	// }

	return nil
}

// func hashIssue(id string, body []byte) (string, string) {
// 	key := "hashIssue::" + id
// 	return key, string(internal.HashBytes(body))
// }

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
	// Remove the current Slack post (if any)
	if err := c.slackClient.Delete(ctx, issue, "Issue moved between channels", false, nil); err != nil {
		return err
	}

	// // Remove the issue from the issues collection.
	// // The new channel manager(s) will ensure that the issue is updated in Slack and stored in the database.
	// c.issueCollection.Remove(issue)

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
