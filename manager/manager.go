package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/eko/gocache/lib/v4/store"
	gocache_store "github.com/eko/gocache/store/go_cache/v4"
	gocache "github.com/patrickmn/go-cache"
	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/peteraglen/slack-manager/manager/internal/models"
	slack "github.com/peteraglen/slack-manager/manager/internal/slack"
	"golang.org/x/sync/errgroup"
)

type DB interface {
	SaveAlert(ctx context.Context, id string, body json.RawMessage) error
	CreateOrUpdateIssue(ctx context.Context, id string, body json.RawMessage) error
	UpdateIssues(ctx context.Context, issues map[string]json.RawMessage) error
	FindSingleIssue(ctx context.Context, opts ...common.FindOption) (string, json.RawMessage, error)
	LoadIssues(ctx context.Context, opts ...common.FindOption) (map[string]json.RawMessage, error)
	GetMoveMappings(ctx context.Context, opts ...common.FindOption) ([]json.RawMessage, error)
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
	cacheStore      store.StoreInterface
	logger          common.Logger
	metrics         common.Metrics
	cfg             *config.ManagerConfig
	managerSettings *models.ManagerSettingsWrapper
}

func New(db DB, alertQueue FifoQueue, commandQueue FifoQueue, cacheStore store.StoreInterface, logger common.Logger, metrics common.Metrics, cfg *config.ManagerConfig, managerSettings *config.ManagerSettings) *Manager {
	if cacheStore == nil {
		gocacheClient := gocache.New(5*time.Minute, time.Minute)
		cacheStore = gocache_store.NewGoCache(gocacheClient)
	}

	if metrics == nil {
		metrics = &common.NoopMetrics{}
	}

	if managerSettings == nil {
		managerSettings = &config.ManagerSettings{}
	}

	return &Manager{
		db:              db,
		alertQueue:      alertQueue,
		commandQueue:    commandQueue,
		cacheStore:      cacheStore,
		logger:          logger,
		metrics:         metrics,
		cfg:             cfg,
		managerSettings: &models.ManagerSettingsWrapper{Settings: managerSettings},
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

	if err := m.managerSettings.Settings.InitAndValidate(); err != nil {
		return fmt.Errorf("failed to initialize channel settings: %w", err)
	}

	m.slackClient = slack.New(m.commandQueue, m.cacheStore, m.logger, m.metrics, m.cfg, m.managerSettings)

	if err := m.slackClient.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect Slack client: %w", err)
	}

	m.coordinator = newCoordinator(m.db, m.alertQueue, m.slackClient, m.cacheStore, m.logger, m.metrics, m.cfg, m.managerSettings)

	if err := m.coordinator.init(ctx); err != nil {
		return fmt.Errorf("failed to initialize coordinator: %w", err)
	}

	m.slackClient.SetIssueFinder(m.coordinator)

	alertCh := make(chan models.Message, 10000)
	commandCh := make(chan models.Message, 10000)
	extenderCh := make(chan models.Message, 10000)

	errg, ctx := errgroup.WithContext(ctx)

	errg.Go(func() error {
		return queueConsumer(ctx, m.commandQueue, commandCh, models.NewCommandFromQueue, m.logger)
	})

	errg.Go(func() error {
		return queueConsumer(ctx, m.alertQueue, alertCh, models.NewAlert, m.logger)
	})

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

func (m *Manager) UpdateSettings(settings *config.ManagerSettings) error {
	if settings == nil {
		settings = &config.ManagerSettings{}
	}

	if err := settings.InitAndValidate(); err != nil {
		return fmt.Errorf("failed to update manager settings (the existing settings will continue to be used): %w", err)
	}

	m.managerSettings.Settings = settings

	return nil
}
