package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/eko/gocache/lib/v4/store"
	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/peteraglen/slack-manager/internal"
	"github.com/peteraglen/slack-manager/manager/internal/models"
	"github.com/peteraglen/slack-manager/manager/internal/slack"
	"github.com/segmentio/ksuid"
)

type coordinator struct {
	channelManagers          map[string]*channelManager
	channelManagersWaitGroup *sync.WaitGroup
	channelManagersLock      *sync.Mutex
	db                       common.DB
	alertQueue               FifoQueue
	moveRequestQueue         FifoQueue
	slack                    *slack.Client
	cacheStore               store.StoreInterface
	locker                   ChannelLocker
	logger                   common.Logger
	metrics                  common.Metrics
	webhookHandlers          []WebhookHandler
	cfg                      *config.ManagerConfig
	managerSettings          *models.ManagerSettingsWrapper
	moveRequestCh            chan *models.MoveRequest
}

func newCoordinator(db common.DB, alertQueue FifoQueue, moveRequestQueue FifoQueue, slack *slack.Client, cacheStore store.StoreInterface,
	locker ChannelLocker, logger common.Logger, metrics common.Metrics, webhookHandlers []WebhookHandler,
	cfg *config.ManagerConfig, managerSettings *models.ManagerSettingsWrapper,
) *coordinator {
	return &coordinator{
		channelManagers:          make(map[string]*channelManager),
		channelManagersWaitGroup: &sync.WaitGroup{},
		channelManagersLock:      &sync.Mutex{},
		db:                       db,
		alertQueue:               alertQueue,
		moveRequestQueue:         moveRequestQueue,
		slack:                    slack,
		cacheStore:               cacheStore,
		locker:                   locker,
		logger:                   logger,
		metrics:                  metrics,
		cfg:                      cfg,
		webhookHandlers:          webhookHandlers,
		managerSettings:          managerSettings,
		moveRequestCh:            make(chan *models.MoveRequest, 10),
	}
}

// FindIssueBySlackPost finds an issue by its Slack post ID in the specified channel.
// It satisfies the IssueFinder interface.
func (c *coordinator) FindIssueBySlackPost(ctx context.Context, channelID string, slackPostID string, includeArchived bool) *models.Issue {
	channelManager, err := c.findOrCreateChannelManager(ctx, channelID)
	if err != nil {
		c.logger.Errorf("Failed to find issue by slack post: %s", err)
		return nil
	}

	issue, err := channelManager.findIssueBySlackPost(ctx, slackPostID, includeArchived)
	if err != nil {
		c.logger.Errorf("Failed to find issue by slack post: %s", err)
		return nil
	}

	return issue
}

func (c *coordinator) init(ctx context.Context) error {
	// Load all active issues from the database (i.e. issues that are not archived)
	issueBodies, err := c.db.LoadOpenIssues(ctx)
	if err != nil {
		return err
	}

	issues := make(map[string][]*models.Issue)

	for _, body := range issueBodies {
		issue := &models.Issue{}

		if err := json.Unmarshal(body, issue); err != nil {
			return fmt.Errorf("failed to unmarshal issue: %w", err)
		}

		// Sanity check: we asked the database for non-archived issues, but we should check that the filter actually worked
		if issue.Archived {
			return fmt.Errorf("database filter failed when loading active issues: issue %s is archived", issue.UniqueID())
		}

		issues[issue.ChannelID()] = append(issues[issue.ChannelID()], issue)
	}

	for channelID, channelIssues := range issues {
		if _, err := c.findOrCreateChannelManager(ctx, channelID, channelIssues...); err != nil {
			return fmt.Errorf("failed to find or create channel manager for channel %s: %w", channelID, err)
		}
	}

	c.logger.Infof("Coordinator initialized with %d issue(s) in %d channel(s)", len(issueBodies), len(c.channelManagers))

	return nil
}

func (c *coordinator) run(ctx context.Context, alertCh <-chan models.Message, commandCh <-chan models.Message,
	moveRequestCh <-chan models.Message, extenderCh chan<- models.Message,
) error {
	c.logger.Info("Channel coordinator started")
	defer c.logger.Info("Channel coordinator exited")

messageLoop:
	for {
		select {
		case <-ctx.Done():
			break messageLoop
		case msg, ok := <-alertCh:
			if !ok {
				return nil
			}

			alert, ok := msg.(*models.Alert)
			if !ok {
				c.logger.Errorf("Invalid message type %T on alert channel", msg)
				continue
			}

			if err := c.addAlert(ctx, alert); err != nil {
				msg.MarkAsFailed()
				c.logger.WithFields(alert.LogFields()).Errorf("Failed to process alert %s: %s", alert.UniqueID(), err)
				continue
			}

			if err := internal.TrySend(ctx, msg, extenderCh); err != nil {
				return err
			}
		case msg, ok := <-commandCh:
			if !ok {
				return nil
			}

			cmd, ok := msg.(*models.Command)
			if !ok {
				c.logger.Errorf("Invalid message type %T on command channel", msg)
				continue
			}

			if err := c.addCommand(ctx, cmd); err != nil {
				msg.MarkAsFailed()
				c.logger.WithFields(cmd.LogFields()).Errorf("Failed to process %s command: %s", cmd.Action, err)
				continue
			}

			if err := internal.TrySend(ctx, msg, extenderCh); err != nil {
				return err
			}
		case msg, ok := <-moveRequestCh:
			if !ok {
				return nil
			}

			moveRequest, ok := msg.(*models.MoveRequest)
			if !ok {
				c.logger.Errorf("Invalid message type %T on move request channel", msg)
				continue
			}

			if err := c.addMoveRequest(ctx, moveRequest); err != nil {
				msg.MarkAsFailed()
				c.logger.WithFields(moveRequest.LogFields()).Errorf("Failed to process move request for issue %s: %s", moveRequest.CorrelationID, err)
				continue
			}

			if err := internal.TrySend(ctx, msg, extenderCh); err != nil {
				return err
			}
		}
	}

	c.channelManagersLock.Lock()
	defer c.channelManagersLock.Unlock()

	// Wait for all channel managers to shut down before returning
	c.channelManagersWaitGroup.Wait()

	return ctx.Err()
}

func (c *coordinator) addCommand(ctx context.Context, cmd *models.Command) error {
	if cmd.Action == models.CommandActionCreateIssue {
		if err := c.handleCreateIssueCommand(ctx, cmd); err != nil {
			return fmt.Errorf("failed to handle create issue command: %w", err)
		}
		return nil
	}

	manager, err := c.findOrCreateChannelManager(ctx, cmd.SlackChannelID)
	if err != nil {
		return fmt.Errorf("failed to find or create channel manager: %w", err)
	}

	if err := manager.queueCommand(ctx, cmd); err != nil {
		return fmt.Errorf("failed to add command to channel manager: %w", err)
	}

	return nil
}

func (c *coordinator) addAlert(ctx context.Context, alert *models.Alert) error {
	alert.OriginalSlackChannelID = alert.SlackChannelID
	alert.OriginalText = alert.Text

	if moveMapping, ok, err := c.findMoveMapping(ctx, alert.SlackChannelID, alert.CorrelationID); err != nil {
		return fmt.Errorf("failed to find move mapping: %w", err)
	} else if ok {
		alert.SlackChannelID = moveMapping.TargetChannelID
	}

	channelManager, err := c.findOrCreateChannelManager(ctx, alert.SlackChannelID)
	if err != nil {
		return fmt.Errorf("failed to find or create channel manager: %w", err)
	}

	if err := channelManager.queueAlert(ctx, alert); err != nil {
		return fmt.Errorf("failed to add alert to channel manager: %w", err)
	}

	return nil
}

func (c *coordinator) addMoveRequest(ctx context.Context, request *models.MoveRequest) error {
	_, issueBody, err := c.db.FindOpenIssueByCorrelationID(ctx, request.SourceChannel, request.CorrelationID)
	if err != nil {
		return fmt.Errorf("failed to find open issue %s in db: %w", request.CorrelationID, err)
	}

	issue := models.Issue{}

	if err := json.Unmarshal(issueBody, &issue); err != nil {
		return fmt.Errorf("failed to unmarshal issue: %w", err)
	}

	moveMapping := models.NewMoveMapping(issue.CorrelationID, issue.OriginalSlackChannelID(), request.TargetChannel)

	// Save information about the move, so that future alerts are routed correctly
	if err := c.saveMoveMapping(ctx, moveMapping); err != nil {
		return fmt.Errorf("failed to register move mapping from %s to %s: %w", moveMapping.OriginalChannelID, moveMapping.TargetChannelID, err)
	}

	// Find the Slack channel name for the new channel
	channelName := c.slack.GetChannelName(ctx, request.TargetChannel)

	// Register the move request. This will override the Slack channel ID on the last alert.
	issue.RegisterMoveRequest(request.UserRealName, request.TargetChannel, channelName)

	// Find the channel manager for the new Slack channel
	newChannelManager, err := c.findOrCreateChannelManager(ctx, request.TargetChannel)
	if err != nil {
		return fmt.Errorf("failed to find or create channel manager for target channel %s: %w", request.TargetChannel, err)
	}

	// Add the issue to the new manager
	if err := newChannelManager.queueMovedIssue(ctx, &issue); err != nil {
		return fmt.Errorf("failed to add moved issue to channel manager: %w", err)
	}

	return nil
}

// findMoveMapping finds a move mapping for the specified channel and correlation ID, if it exists.
func (c *coordinator) findMoveMapping(ctx context.Context, channelID, correlationID string) (*models.MoveMapping, bool, error) {
	moveMappingBody, err := c.db.FindMoveMapping(ctx, channelID, correlationID)
	if err != nil {
		return nil, false, fmt.Errorf("failed to find move mapping for channel %s and correlation ID %s: %w", channelID, correlationID, err)
	}

	if moveMappingBody == nil {
		return nil, false, nil
	}

	moveMapping := &models.MoveMapping{}

	if err := json.Unmarshal(moveMappingBody, moveMapping); err != nil {
		return nil, false, fmt.Errorf("failed to unmarshal move mapping for channel %s and correlation ID %s: %w", channelID, correlationID, err)
	}

	return moveMapping, true, nil
}

// saveMoveMapping saves a move mapping to the database.
func (c *coordinator) saveMoveMapping(ctx context.Context, mapping *models.MoveMapping) error {
	if err := c.db.SaveMoveMapping(ctx, mapping); err != nil {
		return err
	}

	c.logger.WithField("slack_channel_id", mapping.OriginalChannelID).WithField("target_slack_channel_id", mapping.TargetChannelID).WithField("correlation_id", mapping.CorrelationID).Info("Saved move mapping")

	return nil
}

// findOrCreateChannelManager finds an existing channel manager, or creates a new.
func (c *coordinator) findOrCreateChannelManager(ctx context.Context, channelID string, existingIssues ...*models.Issue) (*channelManager, error) {
	c.channelManagersLock.Lock()
	defer c.channelManagersLock.Unlock()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if manager, ok := c.channelManagers[channelID]; ok {
		return manager, nil
	}

	manager := newChannelManager(channelID, c.slack, c.db, c.moveRequestQueue, c.locker, c.logger, c.metrics, c.webhookHandlers, c.cfg, c.managerSettings)

	if err := manager.init(ctx, existingIssues); err != nil {
		return nil, fmt.Errorf("failed to initialize channel manager: %w", err)
	}

	c.channelManagersWaitGroup.Add(1)

	go manager.run(ctx, c.channelManagersWaitGroup)

	c.channelManagers[channelID] = manager

	return manager, nil
}

func (c *coordinator) handleCreateIssueCommand(ctx context.Context, cmd *models.Command) error {
	logger := c.logger.WithFields(cmd.LogFields())

	// Commands are attempted exactly once, so we ack regardless of any errors below.
	// Errors are logged, but otherwise ignored.
	defer func() {
		go ackCommand(ctx, cmd, logger)
	}()

	alert := common.NewAlert(common.AlertSeverity(cmd.ParamAsString("severity")))

	alert.CorrelationID = ksuid.New().String()
	alert.SlackChannelID = cmd.SlackChannelID
	alert.Header = cmd.ParamAsString("header")
	alert.Text = cmd.ParamAsString("text")
	alert.IssueFollowUpEnabled = cmd.ParamAsBool("followUpEnabled")
	alert.AutoResolveSeconds = cmd.ParamAsInt("autoResolveSeconds")
	alert.IconEmoji = cmd.ParamAsString("iconEmoji")
	alert.Username = cmd.UserRealName

	if err := alert.ValidateIcon(); err != nil {
		alert.IconEmoji = ""
	}

	alert.Clean()

	if err := alert.Validate(); err != nil {
		return err
	}

	body, err := json.Marshal(alert)
	if err != nil {
		return fmt.Errorf("failed to marshal alert: %w", err)
	}

	return c.alertQueue.Send(ctx, alert.SlackChannelID, alert.UniqueID(), string(body))
}
