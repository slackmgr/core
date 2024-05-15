package issues

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/peteraglen/slack-manager/client"
	"github.com/peteraglen/slack-manager/common"
	"github.com/peteraglen/slack-manager/core/config"
	"github.com/peteraglen/slack-manager/core/models"
	"github.com/peteraglen/slack-manager/internal"
	"github.com/segmentio/ksuid"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

type Slack interface {
	GetChannelName(ctx context.Context, channelID string) string
	IsAlertChannel(ctx context.Context, channelID string) (bool, string, error)
	UpdateSingleIssue(ctx context.Context, issue *models.Issue) error
	Update(ctx context.Context, channelID string, issues []*models.Issue) error
	UpdateSingleIssueWithThrottling(ctx context.Context, issue *models.Issue, issuesInChannel int) error
	Delete(ctx context.Context, issue *models.Issue, updateIfMessageHasReplies bool, sem *semaphore.Weighted) error
}

type DB interface {
	SaveAlert(ctx context.Context, id string, body json.RawMessage) error
	LoadAllActiveIssues(ctx context.Context) (map[string]json.RawMessage, error)
	CreateOrUpdateIssue(ctx context.Context, id string, body json.RawMessage) error
	UpdateIssues(ctx context.Context, issues map[string]json.RawMessage) (int, error)
	FindSingleIssue(ctx context.Context, filterTerms map[string]interface{}) (string, json.RawMessage, error)
}

type Settings interface {
	GetMoveMappings(ctx context.Context, channelID string) (map[string]*models.MoveMapping, error)
	SaveMoveMapping(ctx context.Context, mapping *models.MoveMapping) error
}

type FifoQueueProducer interface {
	Send(ctx context.Context, groupID, dedupID, body string) error
}

type Coordinator struct {
	channelManagers          map[string]*channelManager
	channelManagersWaitGroup *errgroup.Group
	channelManagersWaitCtx   context.Context //nolint:containedctx
	db                       DB
	queue                    FifoQueueProducer
	settings                 Settings
	slack                    Slack
	logger                   common.Logger
	metrics                  common.Metrics
	conf                     *config.Config
	managerLock              *sync.Mutex
	mappingsLock             *sync.Mutex
	moveRequestCh            chan *moveRequest
	moveMappings             map[string]map[string]*models.MoveMapping
	initialized              bool
}

func NewCoordinator(db DB, queue FifoQueueProducer, settings Settings, slack Slack, logger common.Logger, metrics common.Metrics, conf *config.Config) *Coordinator {
	return &Coordinator{
		channelManagers: make(map[string]*channelManager),
		db:              db,
		queue:           queue,
		settings:        settings,
		slack:           slack,
		logger:          logger,
		metrics:         metrics,
		conf:            conf,
		managerLock:     &sync.Mutex{},
		mappingsLock:    &sync.Mutex{},
		moveRequestCh:   make(chan *moveRequest, 10),
		moveMappings:    make(map[string]map[string]*models.MoveMapping),
	}
}

func (c *Coordinator) Init(ctx context.Context) error {
	if c.initialized {
		return errors.New("Coordinator can only be initialized once")
	}

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

	c.initialized = true

	c.logger.Infof("Coordinator initialized with %d issue(s) in %d channel(s)", len(issueBodies), len(c.channelManagers))

	return nil
}

func (c *Coordinator) Run(ctx context.Context) error {
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
		c.logger.ErrorfUnlessContextCanceled("Channel manager failed during shutdown: %s", err)
	}

	return ctx.Err()
}

func (c *Coordinator) AddCommand(ctx context.Context, cmd *models.Command) {
	if cmd.Action == models.CommandActionCreateIssue {
		if err := c.handleCreateIssueCommand(ctx, cmd); err != nil {
			c.logger.ErrorfUnlessContextCanceled("Failed to handle create issue command: %s", err)
		}
		return
	}

	manager := c.findOrCreateChannelManager(cmd.ChannelID)

	if err := manager.QueueCommand(ctx, cmd); err != nil {
		c.logger.ErrorfUnlessContextCanceled("Failed to add command to channel manager: %s", err)
	}
}

func (c *Coordinator) AddAlert(ctx context.Context, alert *models.Alert) {
	alert.OriginalSlackChannelID = alert.SlackChannelID
	alert.OriginalText = alert.Text

	if moveMapping, found := c.findMoveMapping(ctx, alert.SlackChannelID, alert.CorrelationID); found {
		alert.SlackChannelID = moveMapping.TargetChannelID
	}

	manager := c.findOrCreateChannelManager(alert.SlackChannelID)

	if err := manager.QueueAlert(ctx, alert); err != nil {
		c.logger.ErrorfUnlessContextCanceled("Failed to add alert to channel manager: %s", err)
	}
}

func (c *Coordinator) FindIssueBySlackPost(ctx context.Context, channelID string, slackPostID string, includeArchived bool) *models.Issue {
	manager := c.findOrCreateChannelManager(channelID)

	return manager.FindIssueBySlackPost(ctx, slackPostID, includeArchived)
}

func (c *Coordinator) handleMoveRequest(ctx context.Context, request *moveRequest) {
	issue := request.Issue

	moveMapping := &models.MoveMapping{
		OriginalChannelID: request.Issue.OriginalSlackChannelID(),
		TargetChannelID:   request.TargetChannel,
		CorrelationID:     issue.CorrelationID,
		Timestamp:         time.Now(),
	}

	logger := c.logger.WithFields(issue.LogFields())

	// Save information about the move, so that future alerts are routed correctly
	if err := c.addMoveMapping(ctx, moveMapping); err != nil {
		logger.ErrorfUnlessContextCanceled("Failed to register move mapping from %s to %s: %s", moveMapping.OriginalChannelID, moveMapping.TargetChannelID, err)
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
		logger.ErrorfUnlessContextCanceled("Failed to queue moved issue: %s", err)
	}
}

// findMoveMapping finds a move mapping for the specified channel and correlation ID, if it exists.
func (c *Coordinator) findMoveMapping(ctx context.Context, channelID, correlationID string) (*models.MoveMapping, bool) {
	c.mappingsLock.Lock()
	defer c.mappingsLock.Unlock()

	moveMappingsForChannel, err := c.getOrCreateMoveMappingsForChannel(ctx, channelID)
	if err != nil {
		c.logger.ErrorfUnlessContextCanceled("Failed to get move mappings for channel %s: %s", channelID, err)
		return nil, false
	}

	if mapping, found := moveMappingsForChannel[correlationID]; found {
		return mapping, true
	}

	return nil, false
}

// addMoveMapping adds a new move mapping to the settings database (and the local cache).
func (c *Coordinator) addMoveMapping(ctx context.Context, mapping *models.MoveMapping) error {
	c.mappingsLock.Lock()
	defer c.mappingsLock.Unlock()

	moveMappingsForChannel, err := c.getOrCreateMoveMappingsForChannel(ctx, mapping.OriginalChannelID)
	if err != nil {
		return err
	}

	moveMappingsForChannel[mapping.CorrelationID] = mapping

	if err := c.settings.SaveMoveMapping(ctx, mapping); err != nil {
		return err
	}

	c.logger.WithField("slack_channel_id", mapping.OriginalChannelID).WithField("target_slack_channel_id", mapping.TargetChannelID).WithField("correlation_id", mapping.CorrelationID).Info("Saved move mapping")

	return nil
}

// getOrCreateMoveMappingsForChannel gets the move mappings for the specified channel, either from the local cache or from the settings database.
// The caller of this function must lock c.mappingsLock!
func (c *Coordinator) getOrCreateMoveMappingsForChannel(ctx context.Context, channelID string) (map[string]*models.MoveMapping, error) {
	mappings, found := c.moveMappings[channelID]

	if found {
		return mappings, nil
	}

	mappings, err := c.settings.GetMoveMappings(ctx, channelID)
	if err != nil {
		return nil, err
	}

	c.moveMappings[channelID] = mappings

	c.logger.WithField("slack_channel_id", channelID).WithField("count", len(mappings)).Info("Loaded move mappings")

	return mappings, err
}

// findOrCreateChannelManager finds an existing channel manager, or creates a new.
func (c *Coordinator) findOrCreateChannelManager(channelID string, existingIssues ...*models.Issue) *channelManager {
	c.managerLock.Lock()
	defer c.managerLock.Unlock()

	logger := c.logger.WithField("slack_channel_id", channelID)

	if manager, ok := c.channelManagers[channelID]; ok {
		return manager
	}

	manager := newChannelManager(channelID, c.slack, c.db, c.moveRequestCh, logger, c.metrics, c.conf)

	manager.Init(c.channelManagersWaitCtx, existingIssues)

	c.channelManagersWaitGroup.Go(func() error {
		return manager.Run(c.channelManagersWaitCtx)
	})

	c.channelManagers[channelID] = manager

	return manager
}

func (c *Coordinator) handleCreateIssueCommand(ctx context.Context, cmd *models.Command) error {
	logger := c.logger.WithFields(cmd.LogFields())

	// Commands are attempted exactly once, so we ack regardless of any errors below.
	// Errors are logged, but otherwise ignored.
	defer func() {
		go ackCommand(ctx, cmd, logger)
	}()

	alert := client.NewAlert(client.AlertSeverity(cmd.ParamAsString("severity")))

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

	return c.queue.Send(ctx, groupID, dedupID, string(body))
}
