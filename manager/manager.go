package manager

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/eko/gocache/lib/v4/store"
	gocache_store "github.com/eko/gocache/store/go_cache/v4"
	gocache "github.com/patrickmn/go-cache"
	"github.com/slackmgr/core/config"
	coreinternal "github.com/slackmgr/core/internal"
	"github.com/slackmgr/core/manager/internal/models"
	slack "github.com/slackmgr/core/manager/internal/slack"
	"github.com/slackmgr/types"
	"golang.org/x/sync/errgroup"
)

type Manager struct {
	db              types.DB
	slackClient     *slack.Client
	coordinator     *coordinator
	alertQueue      FifoQueue
	commandQueue    FifoQueue
	cacheStore      store.StoreInterface
	locker          ChannelLocker
	logger          types.Logger
	metrics         types.Metrics
	webhookHandlers []WebhookHandler
	gate            RateLimitGate
	cfg             *config.ManagerConfig
	managerSettings *models.ManagerSettingsWrapper
}

// rateLimitGateFactory is a package-private interface satisfied by ChannelLocker implementations
// that can produce a matching RateLimitGate (e.g. RedisChannelLocker → RedisRateLimitGate).
// Lockers that do not implement this interface fall back to LocalRateLimitGate.
type rateLimitGateFactory interface {
	newRateLimitGate(logger types.Logger) RateLimitGate
}

func New(db types.DB, alertQueue FifoQueue, commandQueue FifoQueue, cacheStore store.StoreInterface, locker ChannelLocker, logger types.Logger, metrics types.Metrics, cfg *config.ManagerConfig, managerSettings *config.ManagerSettings) *Manager {
	if cacheStore == nil {
		gocacheClient := gocache.New(5*time.Minute, time.Minute)
		cacheStore = gocache_store.NewGoCache(gocacheClient)
	}

	if metrics == nil {
		metrics = &types.NoopMetrics{}
	}

	if managerSettings == nil {
		managerSettings = &config.ManagerSettings{}
	}

	// If the locker implements rateLimitGateFactory, use it to create a matching RateLimitGate.
	// This allows RedisChannelLocker to provide a RedisRateLimitGate that uses the same Redis client and configuration.
	// If the locker does not implement rateLimitGateFactory, fall back to a LocalRateLimitGate that operates in-memory.
	var gate RateLimitGate
	if factory, ok := locker.(rateLimitGateFactory); ok {
		gate = factory.newRateLimitGate(logger)
	} else {
		gate = NewLocalRateLimitGate(logger, 0)
	}

	return &Manager{
		db:              db,
		alertQueue:      alertQueue,
		commandQueue:    commandQueue,
		cacheStore:      cacheStore,
		locker:          locker,
		logger:          logger,
		metrics:         metrics,
		gate:            gate,
		cfg:             cfg,
		managerSettings: models.NewManagerSettingsWrapper(managerSettings),
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

	// We could create a default in-memory locker, but this decision needs to be explicit.
	// Running the manager in a distributed environment without a proper channel locker will end badly.
	if m.locker == nil {
		return errors.New("channel locker cannot be nil")
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

	if m.cfg.EncryptionKey == "" {
		m.logger.Error("No encryption key configured. The manager will start, but alerts with webhook payloads will be dropped.")
	}

	if err := m.managerSettings.GetSettings().InitAndValidate(); err != nil {
		return fmt.Errorf("failed to initialize channel settings: %w", err)
	}

	if m.alertQueue.Name() == m.commandQueue.Name() {
		return errors.New("alert queue and command queue must have different names")
	}

	if !m.cfg.SkipDatabaseCache {
		if err := validateCacheStoreIsDistributed(m.cacheStore); err != nil {
			return fmt.Errorf("invalid cache store for multi-instance deployment: %w", err)
		}
		m.db = newDBCacheMiddleware(m.db, m.cacheStore, m.logger, m.cfg)
	}

	m.slackClient = slack.New(m.commandQueue, m.cacheStore, m.logger, m.metrics, m.cfg, m.managerSettings, coreinternal.WithRateLimitGate(m.gate))

	if err := m.slackClient.Connect(ctx); err != nil {
		return fmt.Errorf("failed to connect Slack client: %w", err)
	}

	m.gate.SetReadyCheck(m.slackClient.SocketModeQuiet)

	m.coordinator = newCoordinator(m.db, m.alertQueue, m.slackClient, m.cacheStore, m.locker, m.logger, m.metrics, m.webhookHandlers, m.gate, m.cfg, m.managerSettings)

	if err := m.coordinator.init(ctx); err != nil {
		return fmt.Errorf("failed to initialize coordinator: %w", err)
	}

	m.slackClient.SetIssueFinder(m.coordinator)

	alertCh := make(chan models.InFlightMessage, 1000)
	commandCh := make(chan models.InFlightMessage, 1000)

	errg, ctx := errgroup.WithContext(ctx)

	errg.Go(func() error {
		return queueConsumer(ctx, m.commandQueue, commandCh, models.NewCommandFromQueueItem, m.logger)
	})

	errg.Go(func() error {
		return queueConsumer(ctx, m.alertQueue, alertCh, models.NewAlertFromQueueItem, m.logger)
	})

	errg.Go(func() error {
		return m.coordinator.run(ctx, alertCh, commandCh)
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

	m.managerSettings.SetSettings(settings)

	m.logger.Infof("Manager settings updated")

	return nil
}

// stripMajorVersion removes a Go module major-version suffix (/vN where N >= 2) from
// the end of a package path, so that "pkg/v4" and "pkg/v5" both normalise to "pkg".
// Non-version suffixes (e.g. a path component that happens to start with "v" but is
// followed by non-numeric characters) are left untouched.
func stripMajorVersion(pkgPath string) string {
	if i := strings.LastIndex(pkgPath, "/v"); i >= 0 {
		if n, err := strconv.Atoi(pkgPath[i+2:]); err == nil && n >= 2 {
			return pkgPath[:i]
		}
	}
	return pkgPath
}

// validateCacheStoreIsDistributed returns an error if the provided store is not a known
// distributed (non-process-local) gocache store implementation. Uses a whitelist so that
// unrecognised store types are rejected by default.
// Only needs to be called when the DB cache middleware is active (SkipDatabaseCache=false).
func validateCacheStoreIsDistributed(cacheStore store.StoreInterface) error {
	t := reflect.TypeOf(cacheStore)

	if t == nil {
		return errors.New("cache store must not be nil when SkipDatabaseCache is false")
	}

	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	// Compare against unversioned base paths so the check stays valid across major versions.
	// stripMajorVersion normalises "github.com/eko/gocache/store/redis/v4" →
	// "github.com/eko/gocache/store/redis", matching v5, v6, … automatically.
	allowedPkgPaths := map[string]bool{
		"github.com/eko/gocache/store/redis":        true,
		"github.com/eko/gocache/store/rediscluster": true,
		"github.com/eko/gocache/store/rueidis":      true,
		"github.com/eko/gocache/store/valkey":       true,
		"github.com/eko/gocache/store/memcache":     true,
		"github.com/eko/gocache/store/hazelcast":    true,
	}

	if !allowedPkgPaths[stripMajorVersion(t.PkgPath())] {
		return fmt.Errorf("cache store %q (%s) is not a known distributed store; "+
			"use one of the approved gocache store implementations (redis, rediscluster, "+
			"rueidis, valkey, memcache, hazelcast) to ensure correctness in multi-instance "+
			"deployments; to use a custom or in-memory store, set SkipDatabaseCache=true",
			t.Name(), t.PkgPath())
	}

	return nil
}

func isCtxCanceledErr(err error) bool {
	if err == nil {
		return false
	}

	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
