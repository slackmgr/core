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

// Manager processes alerts from an alert queue and manages the lifecycle of Slack
// issues. It connects to Slack via Socket Mode, coordinates per-channel workers,
// and persists issue state through the injected database.
//
// Create a Manager with [New] and start it with [Manager.Run]. Settings can be
// updated at runtime via [Manager.UpdateSettings] without restarting the service.
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
	hooks           Hooks
}

// rateLimitGateFactory is a package-private interface satisfied by ChannelLocker implementations
// that can produce a matching RateLimitGate (e.g. RedisChannelLocker → RedisRateLimitGate).
// Lockers that do not implement this interface fall back to LocalRateLimitGate.
type rateLimitGateFactory interface {
	newRateLimitGate(logger types.Logger) RateLimitGate
}

// New creates a [Manager] with the five required dependencies.
//
// Optional dependencies can be configured via method chaining before calling [Manager.Run]:
//   - [Manager.WithCacheStore] — sets the cache store (defaults to in-process go-cache, unsuitable for multi-instance)
//   - [Manager.WithLocker] — sets the distributed channel locker and matching [RateLimitGate]
//   - [Manager.WithMetrics] — sets the metrics implementation (defaults to no-op)
//   - [Manager.WithSettings] — sets the initial manager settings (defaults to zero-value)
func New(db types.DB, alertQueue FifoQueue, commandQueue FifoQueue, logger types.Logger, cfg *config.ManagerConfig) *Manager {
	return &Manager{
		db:              db,
		alertQueue:      alertQueue,
		commandQueue:    commandQueue,
		logger:          logger,
		cfg:             cfg,
		metrics:         &types.NoopMetrics{},
		gate:            NewLocalRateLimitGate(logger, 0),
		managerSettings: models.NewManagerSettingsWrapper(&config.ManagerSettings{}),
	}
}

// RegisterWebhookHandler adds a [WebhookHandler] that is consulted whenever an
// issue has a webhook callback configured. Handlers are tried in registration
// order; the first one whose ShouldHandleWebhook returns true handles the call.
// Returns the [Manager] to allow method chaining.
func (m *Manager) RegisterWebhookHandler(handler WebhookHandler) *Manager {
	m.webhookHandlers = append(m.webhookHandlers, handler)
	return m
}

// WithCacheStore sets the cache store. If not called, [Manager.Run] defaults to an
// in-process go-cache instance (unsuitable for multi-instance deployments unless
// SkipDatabaseCache is true in the config). Passing nil is a no-op.
func (m *Manager) WithCacheStore(cacheStore store.StoreInterface) *Manager {
	if cacheStore == nil {
		return m
	}
	m.cacheStore = cacheStore
	return m
}

// WithLocker sets the [ChannelLocker] and recalculates the [RateLimitGate].
// [RedisChannelLocker] produces a [RedisRateLimitGate]; all other lockers fall
// back to [LocalRateLimitGate]. Passing nil is a no-op.
func (m *Manager) WithLocker(locker ChannelLocker) *Manager {
	if locker == nil {
		return m
	}
	m.locker = locker
	if factory, ok := locker.(rateLimitGateFactory); ok {
		m.gate = factory.newRateLimitGate(m.logger)
	} else {
		m.gate = NewLocalRateLimitGate(m.logger, 0)
	}
	return m
}

// WithMetrics sets the metrics implementation. If not called, a no-op
// implementation is used. Passing nil is a no-op.
func (m *Manager) WithMetrics(metrics types.Metrics) *Manager {
	if metrics == nil {
		return m
	}
	m.metrics = metrics
	return m
}

// WithSettings sets the initial manager settings. If not called, zero-value
// settings are used. For runtime updates after [Manager.Run] is started, use
// [Manager.UpdateSettings] instead. Passing nil is a no-op.
func (m *Manager) WithSettings(settings *config.ManagerSettings) *Manager {
	if settings == nil {
		return m
	}
	m.managerSettings = models.NewManagerSettingsWrapper(settings)
	return m
}

// WithHooks registers optional lifecycle callbacks for startup and shutdown
// probe support. See [Hooks] for the available hooks and when they fire.
func (m *Manager) WithHooks(hooks Hooks) *Manager {
	m.hooks = hooks
	return m
}

// Run starts the Manager and blocks until ctx is cancelled or a fatal error occurs.
// It validates configuration, connects to Slack via Socket Mode, initialises the
// coordinator and per-channel managers, and starts the alert and command queue
// consumers. All goroutines are managed with an errgroup; any single fatal error
// triggers a shutdown of the entire group.
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

	if m.logger == nil {
		return errors.New("logger cannot be nil")
	}

	if m.cfg == nil {
		return errors.New("configuration cannot be nil")
	}

	if err := m.cfg.Validate(); err != nil {
		return fmt.Errorf("failed to validate configuration: %w", err)
	}

	if m.cfg.IsSingleInstanceDeployment {
		m.logger.Info("Running in single-instance deployment mode. Multi-instance safeguards are disabled.")
	}

	if m.locker == nil {
		if !m.cfg.IsSingleInstanceDeployment {
			// Require an explicit locker in multi-instance deployments to prevent race conditions.
			return errors.New("channel locker cannot be nil unless IsSingleInstanceDeployment is true")
		}
		m.locker = &NoopChannelLocker{}
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

	if m.cacheStore == nil {
		gocacheClient := gocache.New(5*time.Minute, time.Minute)
		m.cacheStore = gocache_store.NewGoCache(gocacheClient)
	}

	// Wrap the metrics implementation with the configured prefix so all internal metric
	// names are automatically namespaced (e.g. "slackmgr_slack_api_call").
	m.metrics = coreinternal.NewPrefixedMetrics(m.metrics, m.cfg.MetricsPrefix)

	// Wire metrics into the rate limit gate (if it supports it), so gate-level counters
	// are emitted with the same prefix as all other manager metrics.
	type metricsConnector interface {
		connectMetrics(m types.Metrics)
	}
	if mc, ok := m.gate.(metricsConnector); ok {
		mc.connectMetrics(m.metrics)
	}

	// Add the DB cache middleware if not skipped by config.
	// In multi-instance deployments, validate that the provided cache store is a known distributed implementation
	// to prevent subtle and hard-to-debug cache consistency issues.
	if !m.cfg.SkipDatabaseCache {
		if !m.cfg.IsSingleInstanceDeployment {
			if err := validateCacheStoreIsDistributed(m.cacheStore); err != nil {
				return fmt.Errorf("invalid cache store for multi-instance deployment: %w", err)
			}
		}
		m.db = newDBCacheMiddleware(m.db, m.cacheStore, m.logger, m.metrics, m.cfg)
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

	alertCh := make(chan models.InFlightMessage, 50)
	commandCh := make(chan models.InFlightMessage, 50)

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

	if m.hooks.OnStartup != nil {
		m.hooks.OnStartup()
	}

	if m.hooks.OnShutdown != nil {
		defer m.hooks.OnShutdown()
	}

	return errg.Wait()
}

// UpdateSettings hot-reloads manager settings without restarting. The new settings
// are validated before being applied; if validation fails the existing settings remain
// active and an error is returned. Passing nil replaces the current settings with
// zero-value defaults.
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
