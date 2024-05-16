package manager

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/eko/gocache/lib/v4/store"
	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/peteraglen/slack-manager/internal"
	"github.com/peteraglen/slack-manager/manager/internal/models"
	slack "github.com/peteraglen/slack-manager/manager/internal/slack"
	"golang.org/x/sync/errgroup"
)

type DB interface {
	SaveAlert(ctx context.Context, id string, body json.RawMessage) error
	LoadAllActiveIssues(ctx context.Context) (map[string]json.RawMessage, error)
	CreateOrUpdateIssue(ctx context.Context, id string, body json.RawMessage) error
	UpdateIssues(ctx context.Context, issues map[string]json.RawMessage) (int, error)
	FindSingleIssue(ctx context.Context, filterTerms map[string]interface{}) (string, json.RawMessage, error)
	GetMoveMappings(ctx context.Context, filterTerms map[string]interface{}) ([]json.RawMessage, error)
	SaveMoveMapping(ctx context.Context, id string, body json.RawMessage) error
}

type FifoQueue interface {
	Send(ctx context.Context, groupID, dedupID, body string) error
	Receive(ctx context.Context, sinkCh chan<- *common.FifoQueueItem) error
}

type Manager struct {
	db              DB
	slackClient     *slack.Client
	coordinator     *coordinator
	alertQueue      FifoQueue
	commandQueue    FifoQueue
	logger          common.Logger
	cfg             *config.ManagerConfig
	channelSettings *models.ChannelSettingsWrapper
}

func New(db DB, alertQueue FifoQueue, commandQueue FifoQueue, cacheStore store.StoreInterface, logger common.Logger, metrics common.Metrics, cfg *config.ManagerConfig, channelSettings *config.ChannelSettings) *Manager {
	if metrics == nil {
		metrics = &internal.NoopMetrics{}
	}

	if channelSettings == nil {
		channelSettings = &config.ChannelSettings{}
	}

	channelSettingsWrapper := &models.ChannelSettingsWrapper{Settings: channelSettings}

	slackClient := slack.New(commandQueue, cacheStore, logger, metrics, cfg, channelSettingsWrapper)
	coordinator := newCoordinator(db, alertQueue, slackClient, logger, metrics, cfg)
	slackClient.SetIssueFinder(coordinator)

	return &Manager{
		db:              db,
		slackClient:     slackClient,
		coordinator:     coordinator,
		alertQueue:      alertQueue,
		commandQueue:    commandQueue,
		logger:          logger,
		cfg:             cfg,
		channelSettings: channelSettingsWrapper,
	}
}

func (m *Manager) Run(ctx context.Context) error {
	m.logger.Debug("manager.Run started")
	defer m.logger.Debug("manager.Run exited")

	if m.db == nil {
		return fmt.Errorf("database cannot be nil")
	}

	if m.alertQueue == nil {
		return fmt.Errorf("alert queue cannot be nil")
	}

	if m.commandQueue == nil {
		return fmt.Errorf("command queue cannot be nil")
	}

	if m.logger == nil {
		return fmt.Errorf("logger cannot be nil")
	}

	if m.cfg == nil {
		return fmt.Errorf("configuration cannot be nil")
	}

	if err := m.cfg.Validate(); err != nil {
		return fmt.Errorf("failed to validate configuration: %w", err)
	}

	if err := m.channelSettings.Settings.InitAndValidate(); err != nil {
		return fmt.Errorf("failed to initialize channel settings: %w", err)
	}

	if err := m.coordinator.init(ctx); err != nil {
		return err
	}

	if err := m.slackClient.Connect(ctx); err != nil {
		return err
	}

	alertCh := make(chan models.Message, 10000)
	commandCh := make(chan models.Message, 10000)
	extenderCh := make(chan models.Message, 10000)

	errg, ctx := errgroup.WithContext(ctx)

	errg.Go(func() error {
		return queueConsumer(ctx, m.commandQueue, commandCh, models.NewCommandFromQueue, m.logger)
	})

	if !m.cfg.SkipAlertsConsumer {
		errg.Go(func() error {
			return queueConsumer(ctx, m.alertQueue, alertCh, models.NewAlert, m.logger)
		})
	}

	errg.Go(func() error {
		return processor(ctx, m.coordinator, alertCh, commandCh, extenderCh, m.logger)
	})

	errg.Go(func() error {
		return messageExtender(ctx, extenderCh, m.logger)
	})

	errg.Go(func() error {
		return m.coordinator.Run(ctx)
	})

	errg.Go(func() error {
		return m.slackClient.RunSocketMode(ctx)
	})

	return errg.Wait()
}

func (m *Manager) UpdateChannelSettings(channelSettings *config.ChannelSettings) error {
	if channelSettings == nil {
		m.channelSettings.Settings = &config.ChannelSettings{}
		return nil
	}

	if err := channelSettings.InitAndValidate(); err != nil {
		return fmt.Errorf("failed to update channel settings: %w", err)
	}

	m.channelSettings.Settings = channelSettings

	return nil
}
