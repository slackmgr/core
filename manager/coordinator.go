package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/peteraglen/slack-manager/internal"
	"github.com/peteraglen/slack-manager/manager/internal/models"
	"github.com/peteraglen/slack-manager/manager/internal/slack"
	"github.com/segmentio/ksuid"
	"golang.org/x/sync/errgroup"
)

type coordinator struct {
	channelManagers          map[string]*channelManager
	channelManagersWaitGroup *errgroup.Group
	channelManagersWaitCtx   context.Context //nolint:containedctx
	db                       DB
	alertQueue               FifoQueue
	slack                    *slack.Client
	logger                   common.Logger
	metrics                  common.Metrics
	cfg                      *config.ManagerConfig
	managerLock              *sync.Mutex
	mappingsLock             *sync.Mutex
	moveRequestCh            chan *models.MoveRequest
	moveMappings             map[string]map[string]*models.MoveMapping
}

func newCoordinator(db DB, alertQueue FifoQueue, slack *slack.Client, logger common.Logger, metrics common.Metrics, cfg *config.ManagerConfig) *coordinator {
	return &coordinator{
		channelManagers: make(map[string]*channelManager),
		db:              db,
		alertQueue:      alertQueue,
		slack:           slack,
		logger:          logger,
		metrics:         metrics,
		cfg:             cfg,
		managerLock:     &sync.Mutex{},
		mappingsLock:    &sync.Mutex{},
		moveRequestCh:   make(chan *models.MoveRequest, 10),
		moveMappings:    make(map[string]map[string]*models.MoveMapping),
	}
}

func (c *coordinator) init(ctx context.Context) error {
	errg, gctx := errgroup.WithContext(ctx)

	c.channelManagersWaitGroup = errg
	c.channelManagersWaitCtx = gctx

	issueBodies, err := c.db.LoadAllActiveIssues(ctx)
	if err != nil {
		return err
	}

	issues := make(map[string][]*models.Issue)

	for id, body := range issueBodies {
		issue := &models.Issue{}

		if err := json.Unmarshal(body, issue); err != nil {
			return fmt.Errorf("failed to unmarshal issue: %w", err)
		}

		// Backwards compatibility for issues without populated ID
		issue.ID = id

		issues[issue.SlackChannelID()] = append(issues[issue.SlackChannelID()], issue)
	}

	for channelID, channelIssues := range issues {
		c.findOrCreateChannelManager(channelID, channelIssues...)
	}

	c.logger.Infof("Coordinator initialized with %d issue(s) in %d channel(s)", len(issueBodies), len(c.channelManagers))

	return nil
}

func (c *coordinator) Run(ctx context.Context) error {
	c.logger.Info("Channel coordinator started")
	defer c.logger.Info("Channel coordinator exited")

run:
	for {
		select {
		case <-ctx.Done():
			break run
		case request, ok := <-c.moveRequestCh:
			if !ok {
				break run
			}

			c.handleMoveRequest(ctx, request)
		}
	}

	// Wait for the channel managers to shut down before returning
	if err := c.channelManagersWaitGroup.Wait(); err != nil {
		c.logger.Errorf("Channel manager failed during shutdown: %s", err)
	}

	return ctx.Err()
}

func (c *coordinator) AddCommand(ctx context.Context, cmd *models.Command) {
	if cmd.Action == models.CommandActionCreateIssue {
		if err := c.handleCreateIssueCommand(ctx, cmd); err != nil {
			c.logger.Errorf("Failed to handle create issue command: %s", err)
		}
		return
	}

	manager := c.findOrCreateChannelManager(cmd.ChannelID)

	if err := manager.QueueCommand(ctx, cmd); err != nil {
		c.logger.Errorf("Failed to add command to channel manager: %s", err)
	}
}

func (c *coordinator) AddAlert(ctx context.Context, alert *models.Alert) {
	alert.OriginalSlackChannelID = alert.SlackChannelID
	alert.OriginalText = alert.Text

	if moveMapping, found := c.findMoveMapping(ctx, alert.SlackChannelID, alert.CorrelationID); found {
		alert.SlackChannelID = moveMapping.TargetChannelID
	}

	manager := c.findOrCreateChannelManager(alert.SlackChannelID)

	if err := manager.QueueAlert(ctx, alert); err != nil {
		c.logger.Errorf("Failed to add alert to channel manager: %s", err)
	}
}

func (c *coordinator) FindIssueBySlackPost(ctx context.Context, channelID string, slackPostID string, includeArchived bool) *models.Issue {
	manager := c.findOrCreateChannelManager(channelID)

	return manager.FindIssueBySlackPost(ctx, slackPostID, includeArchived)
}

func (c *coordinator) handleMoveRequest(ctx context.Context, request *models.MoveRequest) {
	issue := request.Issue

	moveMapping := models.NewMoveMapping(issue.CorrelationID, issue.OriginalSlackChannelID(), request.TargetChannel)

	logger := c.logger.WithFields(issue.LogFields())

	// Save information about the move, so that future alerts are routed correctly
	if err := c.addMoveMapping(ctx, moveMapping); err != nil {
		logger.Errorf("Failed to register move mapping from %s to %s: %s", moveMapping.OriginalChannelID, moveMapping.TargetChannelID, err)
		return
	}

	// Find the Slack channel name for the new channel
	channelName := c.slack.GetChannelName(ctx, request.TargetChannel)

	// Register the move request. This will override the Slack channel ID on the last alert.
	issue.RegisterMoveRequest(request.UserRealName, request.TargetChannel, channelName)

	// Find the newManager for the new Slack channel
	newManager := c.findOrCreateChannelManager(request.TargetChannel)

	// Add the issue to the new manager
	if err := newManager.QueueMovedIssue(ctx, issue); err != nil {
		logger.Errorf("Failed to queue moved issue: %s", err)
	}
}

// findMoveMapping finds a move mapping for the specified channel and correlation ID, if it exists.
func (c *coordinator) findMoveMapping(ctx context.Context, channelID, correlationID string) (*models.MoveMapping, bool) {
	c.mappingsLock.Lock()
	defer c.mappingsLock.Unlock()

	moveMappingsForChannel, err := c.getOrCreateMoveMappingsForChannel(ctx, channelID)
	if err != nil {
		c.logger.Errorf("Failed to get move mappings for channel %s: %s", channelID, err)
		return nil, false
	}

	if mapping, found := moveMappingsForChannel[correlationID]; found {
		return mapping, true
	}

	return nil, false
}

// addMoveMapping adds a new move mapping to the settings database (and the local cache).
func (c *coordinator) addMoveMapping(ctx context.Context, mapping *models.MoveMapping) error {
	c.mappingsLock.Lock()
	defer c.mappingsLock.Unlock()

	moveMappingsForChannel, err := c.getOrCreateMoveMappingsForChannel(ctx, mapping.OriginalChannelID)
	if err != nil {
		return err
	}

	moveMappingsForChannel[mapping.CorrelationID] = mapping

	body, err := json.Marshal(mapping)
	if err != nil {
		return fmt.Errorf("failed to marshal move mapping: %w", err)
	}

	if err := c.db.SaveMoveMapping(ctx, mapping.ID, body); err != nil {
		return err
	}

	c.logger.WithField("slack_channel_id", mapping.OriginalChannelID).WithField("target_slack_channel_id", mapping.TargetChannelID).WithField("correlation_id", mapping.CorrelationID).Info("Saved move mapping")

	return nil
}

// getOrCreateMoveMappingsForChannel gets the move mappings for the specified channel, either from the local cache or from the settings database.
// The caller of this function must lock c.mappingsLock!
func (c *coordinator) getOrCreateMoveMappingsForChannel(ctx context.Context, channelID string) (map[string]*models.MoveMapping, error) {
	mappings, found := c.moveMappings[channelID]

	if found {
		return mappings, nil
	}

	filterTerms := map[string]interface{}{
		"originalChannelId": channelID,
	}

	mappingsList, err := c.db.GetMoveMappings(ctx, filterTerms)
	if err != nil {
		return nil, err
	}

	mappings = make(map[string]*models.MoveMapping, len(mappingsList))

	for _, body := range mappingsList {
		mapping := &models.MoveMapping{}

		if err := json.Unmarshal(body, mapping); err != nil {
			return nil, fmt.Errorf("failed to unmarshal move mapping: %w", err)
		}

		if mapping.OriginalChannelID != channelID {
			return nil, fmt.Errorf("move mapping for channel %s has incorrect original channel ID %s", channelID, mapping.OriginalChannelID)
		}

		if mapping.CorrelationID == "" {
			return nil, fmt.Errorf("move mapping for channel %s has empty correlation ID", channelID)
		}

		if mapping.TargetChannelID == "" {
			return nil, fmt.Errorf("move mapping for channel %s has empty target channel ID", channelID)
		}

		mappings[mapping.CorrelationID] = mapping
	}

	c.moveMappings[channelID] = mappings

	c.logger.WithField("slack_channel_id", channelID).WithField("count", len(mappings)).Info("Loaded move mappings")

	return mappings, err
}

// findOrCreateChannelManager finds an existing channel manager, or creates a new.
func (c *coordinator) findOrCreateChannelManager(channelID string, existingIssues ...*models.Issue) *channelManager {
	c.managerLock.Lock()
	defer c.managerLock.Unlock()

	logger := c.logger.WithField("slack_channel_id", channelID)

	if manager, ok := c.channelManagers[channelID]; ok {
		return manager
	}

	manager := newChannelManager(channelID, c.slack, c.db, c.moveRequestCh, logger, c.metrics, c.cfg)

	manager.Init(c.channelManagersWaitCtx, existingIssues)

	c.channelManagersWaitGroup.Go(func() error {
		return manager.Run(c.channelManagersWaitCtx)
	})

	c.channelManagers[channelID] = manager

	return manager
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
	alert.SlackChannelID = cmd.ChannelID
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

	groupID := alert.SlackChannelID
	dedupID := internal.Hash("alert", alert.SlackChannelID, alert.CorrelationID, alert.Timestamp.Format(time.RFC3339Nano))

	return c.alertQueue.Send(ctx, groupID, dedupID, string(body))
}
