package manager

import (
	"context"
	"errors"
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

type Manager struct {
	db               common.DB
	slackClient      *slack.Client
	coordinator      *coordinator
	alertQueue       FifoQueue
	commandQueue     FifoQueue
	moveRequestQueue FifoQueue
	cacheStore       store.StoreInterface
	locker           ChannelLocker
	logger           common.Logger
	metrics          common.Metrics
	webhookHandlers  []WebhookHandler
	cfg              *config.ManagerConfig
	managerSettings  *models.ManagerSettingsWrapper
}

func New(db common.DB, alertQueue FifoQueue, commandQueue FifoQueue, moveRequestQueue FifoQueue, cacheStore store.StoreInterface, locker ChannelLocker, logger common.Logger, metrics common.Metrics, cfg *config.ManagerConfig, managerSettings *config.ManagerSettings) *Manager {
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

	// Add the database cache middleware, unless explicitly skipped by the configuration.
	if !cfg.SkipDatabaseCache {
		db = newDBCacheMiddleware(db, cacheStore, logger, *cfg)
	}

	return &Manager{
		db:               db,
		alertQueue:       alertQueue,
		commandQueue:     commandQueue,
		moveRequestQueue: moveRequestQueue,
		cacheStore:       cacheStore,
		locker:           locker,
		logger:           logger,
		metrics:          metrics,
		cfg:              cfg,
		managerSettings:  &models.ManagerSettingsWrapper{Settings: managerSettings},
	}
}

func (m *Manager) RegisterWebhookHandler(handler WebhookHandler) *Manager {
	m.webhookHandlers = append(m.webhookHandlers, handler)
	return m
}

func (m *Manager) Run(ctx context.Context) error {
	m.logger.Info("Manager started")
	defer m.logger.Info("Manager exited")

	if m.db == nil {
		return errors.New("database cannot be nil")
	}

	if m.alertQueue == nil {
		return errors.New("alert queue cannot be nil")
	}

	if m.commandQueue == nil {
		return errors.New("command queue cannot be nil")
	}

	if m.moveRequestQueue == nil {
		return errors.New("move request queue cannot be nil")
	}

	if m.logger == nil {
		return errors.New("logger cannot be nil")
	}

	if m.cfg == nil {
		return errors.New("configuration cannot be nil")
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

	m.coordinator = newCoordinator(m.db, m.alertQueue, m.moveRequestQueue, m.slackClient, m.cacheStore, m.locker, m.logger, m.metrics, m.webhookHandlers, m.cfg, m.managerSettings)

	if err := m.coordinator.init(ctx); err != nil {
		return fmt.Errorf("failed to initialize coordinator: %w", err)
	}

	m.slackClient.SetIssueFinder(m.coordinator)

	alertCh := make(chan models.Message, 1000)
	commandCh := make(chan models.Message, 1000)
	moveRequestCh := make(chan models.Message, 1000)
	extenderCh := make(chan models.Message, 1000)

	errg, ctx := errgroup.WithContext(ctx)

	errg.Go(func() error {
		return queueConsumer(ctx, m.commandQueue, commandCh, models.NewCommandFromQueue, m.logger)
	})

	errg.Go(func() error {
		return queueConsumer(ctx, m.alertQueue, alertCh, models.NewAlert, m.logger)
	})

	errg.Go(func() error {
		return queueConsumer(ctx, m.moveRequestQueue, moveRequestCh, models.NewMoveRequestFromQueue, m.logger)
	})

	errg.Go(func() error {
		return messageExtender(ctx, extenderCh, m.logger)
	})

	errg.Go(func() error {
		return m.coordinator.run(ctx, alertCh, commandCh, moveRequestCh, extenderCh)
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
		return fmt.Errorf("failed to initialize updated manager settings (the existing settings will continue to be used): %w", err)
	}

	m.managerSettings.Settings = settings

	return nil
}
