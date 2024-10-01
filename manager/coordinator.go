package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/eko/gocache/lib/v4/store"
	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/peteraglen/slack-manager/manager/internal/models"
	"github.com/peteraglen/slack-manager/manager/internal/slack"
	"github.com/segmentio/ksuid"
)

type coordinator struct {
	channelManagers          map[string]*channelManager
	channelManagersWaitGroup *sync.WaitGroup
	channelManagersLock      *sync.Mutex
	db                       DB
	alertQueue               FifoQueue
	slack                    *slack.Client
	cacheStore               store.StoreInterface
	logger                   common.Logger
	metrics                  common.Metrics
	cfg                      *config.ManagerConfig
	managerSettings          *models.ManagerSettingsWrapper
	mappingsLock             *sync.Mutex
	moveRequestCh            chan *models.MoveRequest
	moveMappings             map[string]map[string]*models.MoveMapping
}

func newCoordinator(db DB, alertQueue FifoQueue, slack *slack.Client, cacheStore store.StoreInterface, logger common.Logger, metrics common.Metrics, cfg *config.ManagerConfig, managerSettings *models.ManagerSettingsWrapper) *coordinator {
	return &coordinator{
		channelManagers:          make(map[string]*channelManager),
		channelManagersWaitGroup: &sync.WaitGroup{},
		channelManagersLock:      &sync.Mutex{},
		db:                       db,
		alertQueue:               alertQueue,
		slack:                    slack,
		cacheStore:               cacheStore,
		logger:                   logger,
		metrics:                  metrics,
		cfg:                      cfg,
		managerSettings:          managerSettings,
		mappingsLock:             &sync.Mutex{},
		moveRequestCh:            make(chan *models.MoveRequest, 10),
		moveMappings:             make(map[string]map[string]*models.MoveMapping),
	}
}

func (c *coordinator) init(ctx context.Context) error {
	// Load all active issues from the database (i.e. issues that are not archived)
	issueBodies, err := c.db.LoadOpenIssues(ctx)
	if err != nil {
		return err
	}

	issues := make(map[string][]*models.Issue)

	for id, body := range issueBodies {
		issue := &models.Issue{}

		if err := json.Unmarshal(body, issue); err != nil {
			return fmt.Errorf("failed to unmarshal issue %s: %w", id, err)
		}

		// Sanity check: we asked the database for non-archived issues, but we should check that the filter actually worked
		if issue.Archived {
			return fmt.Errorf("database filter failed when loading active issues: issue %s is archived", id)
		}

		issues[issue.SlackChannelID()] = append(issues[issue.SlackChannelID()], issue)
	}

	for channelID, channelIssues := range issues {
		if _, err := c.findOrCreateChannelManager(ctx, channelID, channelIssues...); err != nil {
			return fmt.Errorf("failed to find or create channel manager for channel %s: %w", channelID, err)
		}
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

			if err := c.handleMoveRequest(ctx, request); err != nil {
				c.logger.WithFields(request.Issue.LogFields()).Errorf("Failed to handle move request: %s", err)
			}
		}
	}

	c.channelManagersLock.Lock()
	defer c.channelManagersLock.Unlock()

	// Wait for all channel managers to shut down before returning
	c.channelManagersWaitGroup.Wait()

	return ctx.Err()
}

func (c *coordinator) AddCommand(ctx context.Context, cmd *models.Command) error {
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

func (c *coordinator) AddAlert(ctx context.Context, alert *models.Alert) error {
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

func (c *coordinator) handleMoveRequest(ctx context.Context, request *models.MoveRequest) error {
	issue := request.Issue

	moveMapping := models.NewMoveMapping(issue.CorrelationID, issue.OriginalSlackChannelID(), request.TargetChannel)

	// Save information about the move, so that future alerts are routed correctly
	if err := c.addMoveMapping(ctx, moveMapping); err != nil {
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
	if err := newChannelManager.queueMovedIssue(ctx, issue); err != nil {
		return fmt.Errorf("failed to add moved issue to channel manager: %w", err)
	}

	return nil
}

// findMoveMapping finds a move mapping for the specified channel and correlation ID, if it exists.
func (c *coordinator) findMoveMapping(ctx context.Context, channelID, correlationID string) (*models.MoveMapping, bool, error) {
	c.mappingsLock.Lock()
	defer c.mappingsLock.Unlock()

	moveMappingsForChannel, err := c.getOrCreateMoveMappingsForChannel(ctx, channelID)
	if err != nil {
		return nil, false, fmt.Errorf("failed to get move mappings for channel %s: %w", channelID, err)
	}

	if mapping, ok := moveMappingsForChannel[correlationID]; ok {
		return mapping, true, nil
	}

	return nil, false, nil
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

	if err := c.db.SaveMoveMapping(ctx, mapping.OriginalChannelID, mapping); err != nil {
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

	mappingsList, err := c.db.LoadMoveMappings(ctx, channelID)
	if err != nil {
		return nil, err
	}

	mappings = make(map[string]*models.MoveMapping, len(mappingsList))

	for _, body := range mappingsList {
		mapping := &models.MoveMapping{}

		if err := json.Unmarshal(body, mapping); err != nil {
			return nil, fmt.Errorf("failed to unmarshal move mapping: %w", err)
		}

		// Sanity check
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

	manager := newChannelManager(channelID, c.slack, c.db, c.moveRequestCh, c.cacheStore, c.logger, c.metrics, c.cfg, c.managerSettings)

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

	return c.alertQueue.Send(ctx, alert.SlackChannelID, alert.DedupID(), string(body))
}
