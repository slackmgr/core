package manager

import (
	"context"
	"time"

	commonlib "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/common"
	"github.com/peteraglen/slack-manager/core/config"
	"github.com/peteraglen/slack-manager/core/models"
	"golang.org/x/sync/errgroup"
)

type Coordinator interface {
	Init(ctx context.Context, shutdownTimeout time.Duration) error
	Run(ctx context.Context) error
	AddAlert(ctx context.Context, alert *models.Alert)
	AddCommand(ctx context.Context, alert *models.Command)
}

type Slack interface {
	RunSocketMode(ctx context.Context) error
}

type FifoQueueConsumer interface {
	Receive(ctx context.Context, sinkCh chan<- *commonlib.QueueItem) error
}

type App struct {
	slack        Slack
	coordinator  Coordinator
	alertQueue   FifoQueueConsumer
	commandQueue FifoQueueConsumer
	logger       common.Logger
	conf         *config.Config
}

func New(slack Slack, coordinator Coordinator, alertQueue FifoQueueConsumer, commandQueue FifoQueueConsumer, logger common.Logger, conf *config.Config) *App {
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

	if err := app.coordinator.Init(ctx, 10*time.Second); err != nil {
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
