package manager

import (
	"context"
	"encoding/json"
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

// DB is an interface for interacting with the database.
type DB interface {
	// SaveAlert saves an alert to the database (for auditing purposes).
	// The same alert may be saved multiple times, in case of errors and retries.
	//
	// A database implementation can choose to skip saving the alerts, since they are never read by the manager.
	//
	// id is the unique identifier for the alert, and body is the json formatted alert.
	SaveAlert(ctx context.Context, channelID string, alert *models.Alert) error

	// CreateOrUpdateIssue creates or updates a single issue in the database.
	//
	// id is the unique identifier for the issue, and body is the json formatted issue.
	CreateOrUpdateIssue(ctx context.Context, channelID string, issue *models.Issue) error

	// UpdateIssues updates multiple existing issues in the database.
	//
	// issues is a map of issue IDs to json formatted issue bodies.
	UpdateIssues(ctx context.Context, channelID string, issues ...*models.Issue) error

	// FindIssueBySlackPostID finds a single issue in the database, based on the provided channel ID and Slack post ID.
	//
	// The database implementation should return an error if the query matches multiple issues, and ["", nil, nil] if no issue is found.
	FindIssueBySlackPostID(ctx context.Context, channelID, postID string) (string, json.RawMessage, error)

	// LoadOpenIssues loads all open (non-archived) issues from the database, across all channels.
	LoadOpenIssues(ctx context.Context) (map[string]json.RawMessage, error)

	// LoadMoveMappings returns all move mappings from the database, for the specified channel ID.
	LoadMoveMappings(ctx context.Context, channelID string) ([]json.RawMessage, error)

	// SaveMoveMapping saves a move mapping to the database.
	SaveMoveMapping(ctx context.Context, channelID string, moveMapping *models.MoveMapping) error
}

// FifoQueue is an interface for interacting with a fifo queue.
type FifoQueue interface {
	// Name returns the name of the queue.
	Name() string

	// Send sends a single message to the queue.
	//
	// slackChannelID is the Slack channel to which the message belongs.
	// A queue implementation should use this value to partition the queue (i.e. group ID in an AWS SQS Fifo queue),
	// but it is not required.
	//
	// dedupID is a unique identifier for the message.
	// A queue implementation should use this value to deduplicate messages, but it is not required.
	//
	// body is the json formatted message body.
	Send(ctx context.Context, slackChannelID, dedupID, body string) error

	// Receive receives messages from the queue, until the context is cancelled.
	// Messages are sent to the provided channel.
	// The channel must be closed when Receive returns.
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
		return fmt.Errorf("failed to initialize updated manager settings (the existing settings will continue to be used): %w", err)
	}

	m.managerSettings.Settings = settings

	return nil
}
