package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/eko/gocache/lib/v4/store"
	"github.com/segmentio/ksuid"
	"github.com/slackmgr/core/config"
	"github.com/slackmgr/core/manager/internal/models"
	"github.com/slackmgr/types"
)

type coordinator struct {
	// channelManagers holds the active channel managers, keyed by Slack channel ID.
	// No locking is required, since all access happen via the synchronous coordinator run loop.
	channelManagers map[string]*channelManager

	// channelManagersWaitGroup is used to wait for all channel managers to exit when the coordinator is shutting down.
	channelManagersWaitGroup *sync.WaitGroup

	db              types.DB
	alertQueue      FifoQueue
	slack           SlackClient
	cacheStore      store.StoreInterface
	locker          ChannelLocker
	logger          types.Logger
	metrics         types.Metrics
	webhookHandlers []WebhookHandler
	cfg             *config.ManagerConfig
	managerSettings *models.ManagerSettingsWrapper
}

func newCoordinator(db types.DB, alertQueue FifoQueue, slack SlackClient, cacheStore store.StoreInterface,
	locker ChannelLocker, logger types.Logger, metrics types.Metrics, webhookHandlers []WebhookHandler,
	cfg *config.ManagerConfig, managerSettings *models.ManagerSettingsWrapper,
) *coordinator {
	return &coordinator{
		channelManagers:          make(map[string]*channelManager),
		channelManagersWaitGroup: &sync.WaitGroup{},
		db:                       db,
		alertQueue:               alertQueue,
		slack:                    slack,
		cacheStore:               cacheStore,
		locker:                   locker,
		logger:                   logger,
		metrics:                  metrics,
		cfg:                      cfg,
		webhookHandlers:          webhookHandlers,
		managerSettings:          managerSettings,
	}
}

// FindIssueBySlackPost finds an issue by its Slack post ID in the specified channel.
// It satisfies the IssueFinder interface.
func (c *coordinator) FindIssueBySlackPost(ctx context.Context, channelID string, slackPostID string, includeArchived bool) *models.Issue {
	_, issueBody, err := c.db.FindIssueBySlackPostID(ctx, channelID, slackPostID)
	if err != nil {
		c.logger.Errorf("Failed to find issue by slack post: %s", err)
		return nil
	}

	if issueBody == nil {
		return nil
	}

	issue := &models.Issue{}

	if err := json.Unmarshal(issueBody, issue); err != nil {
		c.logger.Errorf("Failed to unmarshal issue body: %s", err)
		return nil
	}

	if issue.LastAlert == nil {
		c.logger.Errorf("Issue has nil LastAlert after unmarshal")
		return nil
	}

	// Sanity check
	if issue.LastAlert.SlackChannelID != channelID {
		c.logger.Errorf("Issue found in database is not associated with the current channel")
		return nil
	}

	// Sanity check
	if issue.SlackPostID != slackPostID {
		c.logger.Errorf("Issue found in database does not match the specified Slack post ID")
		return nil
	}

	if !includeArchived && issue.Archived {
		return nil
	}

	return issue
}

func (c *coordinator) init(ctx context.Context) error {
	if err := c.refreshChannelManagers(ctx); err != nil {
		return fmt.Errorf("failed to start channel managers: %w", err)
	}

	c.logger.Infof("Coordinator initialized with %d active channel(s)", len(c.channelManagers))

	return nil
}

func (c *coordinator) run(ctx context.Context, alertCh <-chan models.InFlightMessage, commandCh <-chan models.InFlightMessage) error {
	c.logger.Info("Channel coordinator started")
	defer c.logger.Info("Channel coordinator exited")

	channelRefreshInterval := 30 * time.Second
	channelRefreshTimeout := time.After(channelRefreshInterval)

messageLoop:
	for {
		select {
		case <-ctx.Done():
			break messageLoop
		case <-channelRefreshTimeout:
			if err := c.refreshChannelManagers(ctx); err != nil {
				return fmt.Errorf("failed to refresh channel managers: %w", err)
			}

			channelRefreshTimeout = time.After(channelRefreshInterval)
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
				msg.Nack()
				c.logger.WithFields(alert.LogFields()).Errorf("Failed to process alert %s: %s", alert.UniqueID(), err)
				continue
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
				msg.Nack()
				c.logger.WithFields(cmd.LogFields()).Errorf("Failed to process %s command: %s", cmd.Action, err)
				continue
			}
		}
	}

	// When we get here, the context has been cancelled.
	// Drain remaining messages from both channels before waiting for channel managers.
	c.drainChannels(alertCh, commandCh)

	// Wait for all channel managers to shut down in an orderly fashion.
	c.channelManagersWaitGroup.Wait()

	return ctx.Err()
}

// drainChannels nacks all remaining messages in the alert and command channels.
// This ensures messages are returned to the queue for reprocessing by another instance.
func (c *coordinator) drainChannels(alertCh <-chan models.InFlightMessage, commandCh <-chan models.InFlightMessage) {
	drainCtx, cancel := context.WithTimeout(context.Background(), c.cfg.CoordinatorDrainTimeout)
	defer cancel()

	drained := 0

	for {
		select {
		case msg, ok := <-alertCh:
			if !ok {
				// Channel closed - set to nil so select skips it (prevents busy loop)
				alertCh = nil
				continue
			}
			msg.Nack()
			drained++
		case msg, ok := <-commandCh:
			if !ok {
				// Channel closed - set to nil so select skips it (prevents busy loop)
				commandCh = nil
				continue
			}
			msg.Nack()
			drained++
		case <-drainCtx.Done():
			c.logger.Errorf("Coordinator drain timeout reached after draining %d messages", drained)
			return
		default:
			// No more messages available (channels empty or closed)
			if drained > 0 {
				c.logger.Infof("Drained %d messages from coordinator channels", drained)
			}
			return
		}
	}
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

// refreshChannelManagers ensures that we have a running channel manager for each channel that has open issues.
func (c *coordinator) refreshChannelManagers(ctx context.Context) error {
	activeChannels, err := c.db.FindActiveChannels(ctx)
	if err != nil {
		return err
	}

	// Iterate over all channel managers and ensure they are running for each channel that has open issues.
	// Send a keep-alive signal to existing channel managers, to ensure that the message processing loop is running.
	for _, channelID := range activeChannels {
		if manager, ok := c.channelManagers[channelID]; ok {
			if err := manager.keepAlive(ctx); err != nil {
				return err
			}
		} else {
			if _, err := c.createChannelManager(ctx, channelID); err != nil {
				return fmt.Errorf("failed to create channel manager for channel %s: %w", channelID, err)
			}
		}
	}

	return nil
}

// findOrCreateChannelManager finds an existing channel manager, or creates a new.
// If a new channel manager is created, it starts running in a separate goroutine.
func (c *coordinator) findOrCreateChannelManager(ctx context.Context, channelID string) (*channelManager, error) {
	if manager, ok := c.channelManagers[channelID]; ok {
		return manager, nil
	}

	// If no channel manager exists for the channel, create a new one.
	return c.createChannelManager(ctx, channelID)
}

func (c *coordinator) createChannelManager(ctx context.Context, channelID string) (*channelManager, error) {
	channelManager := newChannelManager(channelID, c.slack, c.db, c.locker, c.logger, c.metrics, c.webhookHandlers, c.cfg, c.managerSettings)

	// Add one to the wait group to ensure we wait for this channel manager to finish.
	// This is important to ensure that we don't exit the coordinator while channel managers are still running.
	c.channelManagersWaitGroup.Add(1)

	// Add the new channel manager to the map.
	// There is no need to ever delete it, as channel managers live for the lifetime of the coordinator, i.e until the context is cancelled.
	c.channelManagers[channelID] = channelManager

	// Start the channel manager in a separate goroutine.
	go c.runChannelManagerAsync(ctx, channelManager)

	return channelManager, nil
}

// runChannelManagerAsync runs the channel manager in a separate goroutine and waits for it to finish.
func (c *coordinator) runChannelManagerAsync(ctx context.Context, channelManager *channelManager) {
	// Make sure to signal that this goroutine is done when it exits.
	defer c.channelManagersWaitGroup.Done()

	// Run the channel manager. This will block the current thread until the channel manager exits.
	channelManager.run(ctx)
}

func (c *coordinator) handleCreateIssueCommand(ctx context.Context, cmd *models.Command) error {
	// Commands are attempted exactly once, so we ack regardless of any errors below.
	defer cmd.Ack()

	alert := types.NewAlert(types.AlertSeverity(cmd.ParamAsString("severity")))

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
