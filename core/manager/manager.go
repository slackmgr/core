package manager

import (
	"context"

	commonlib "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/common"
	"github.com/peteraglen/slack-manager/core/config"
	"github.com/peteraglen/slack-manager/core/models"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

type Slack interface {
	RunSocketMode(ctx context.Context) error
	GetChannelName(ctx context.Context, channelID string) string
	IsAlertChannel(ctx context.Context, channelID string) (bool, string, error)
	UpdateSingleIssue(ctx context.Context, issue *models.Issue) error
	Update(ctx context.Context, channelID string, issues []*models.Issue) error
	UpdateSingleIssueWithThrottling(ctx context.Context, issue *models.Issue, issuesInChannel int) error
	Delete(ctx context.Context, issue *models.Issue, updateIfMessageHasReplies bool, sem *semaphore.Weighted) error
}

type FifoQueue interface {
	Send(ctx context.Context, groupID, dedupID, body string) error
	Receive(ctx context.Context, sinkCh chan<- *commonlib.QueueItem) error
}

type App struct {
	slack        Slack
	coordinator  *coordinator
	alertQueue   FifoQueue
	commandQueue FifoQueue
	logger       common.Logger
	conf         *config.Config
}

func New(db DB, slack Slack, alertQueue FifoQueue, commandQueue FifoQueue, logger common.Logger, metrics common.Metrics, conf *config.Config) *App {
	coordinator := newCoordinator(db, alertQueue, slack, logger, metrics, conf)

	return &App{
		slack:        slack,
		coordinator:  coordinator,
		alertQueue:   alertQueue,
		commandQueue: commandQueue,
		logger:       logger,
		conf:         conf,
	}
}

func (app *App) Run(ctx context.Context) error {
	app.logger.Debug("manager.Run started")
	defer app.logger.Debug("manager.Run exited")

	alertCh := make(chan models.Message, 10000)
	commandCh := make(chan models.Message, 10000)
	extenderCh := make(chan models.Message, 10000)

	errg, ctx := errgroup.WithContext(ctx)

	if err := app.coordinator.init(ctx); err != nil {
		return err
	}

	errg.Go(func() error {
		return queueConsumer(ctx, app.commandQueue, commandCh, models.NewCommandFromQueue, app.logger)
	})

	if !app.conf.SkipAlertsConsumer {
		errg.Go(func() error {
			return queueConsumer(ctx, app.alertQueue, alertCh, models.NewAlert, app.logger)
		})
	}

	errg.Go(func() error {
		return processor(ctx, app.coordinator, alertCh, commandCh, extenderCh, app.logger)
	})

	errg.Go(func() error {
		return messageExtender(ctx, extenderCh, app.logger)
	})

	errg.Go(func() error {
		return app.coordinator.Run(ctx)
	})

	errg.Go(func() error {
		return app.slack.RunSocketMode(ctx)
	})

	return errg.Wait()
}
