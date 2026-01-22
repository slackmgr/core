package controllers

import (
	"context"
	"sync"

	"github.com/eko/gocache/lib/v4/store"
	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/peteraglen/slack-manager/internal"
	"github.com/peteraglen/slack-manager/manager/internal/models"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

// SocketModeClient defines the interface for the SocketMode client used by the handlers.
type SocketModeClient interface {
	Ack(req socketmode.Request, payload ...any)
	RunContext(ctx context.Context) error
	Events() chan socketmode.Event
}

// SocketModeHandler handles Slack Socket Mode events and interactions.
type SocketModeHandler struct {
	apiClient        SlackAPIClient
	socketModeClient SocketModeClient
	commandQueue     FifoQueueProducer
	issueFinder      IssueFinder
	cacheStore       store.StoreInterface
	cfg              *config.ManagerConfig
	managerSettings  *models.ManagerSettingsWrapper
	logger           common.Logger

	eventMap                       map[socketmode.EventType][]SocketModeHandlerFunc
	interactionEventMap            map[slack.InteractionType][]SocketModeHandlerFunc
	interactionBlockActionEventMap map[string][]SocketModeHandlerFunc
	eventAPIMap                    map[string][]SocketModeHandlerFunc
	slashCommandMap                map[string][]SocketModeHandlerFunc
	defaultHandlerFunc             SocketModeHandlerFunc

	// sem limits the number of concurrent event handlers to prevent goroutine explosion.
	sem *semaphore.Weighted
	// wg tracks in-flight handlers for graceful shutdown.
	wg sync.WaitGroup
}

// SocketModeHandlerFunc defines a function to handle socketmode events.
type SocketModeHandlerFunc func(context.Context, *socketmode.Event, SocketModeClient)

// NewSocketModeHandler creates a new SocketModeHandler.
func NewSocketModeHandler(apiClient SlackAPIClient, socketModeClient SocketModeClient, commandQueue FifoQueueProducer, issueFinder IssueFinder,
	cacheStore store.StoreInterface, cfg *config.ManagerConfig, managerSettings *models.ManagerSettingsWrapper, logger common.Logger,
) *SocketModeHandler {
	eventMap := make(map[socketmode.EventType][]SocketModeHandlerFunc)
	interactionEventMap := make(map[slack.InteractionType][]SocketModeHandlerFunc)
	interactionBlockActionEventMap := make(map[string][]SocketModeHandlerFunc)
	eventAPIMap := make(map[string][]SocketModeHandlerFunc)
	slackCommandMap := make(map[string][]SocketModeHandlerFunc)

	handler := &SocketModeHandler{
		apiClient:                      apiClient,
		socketModeClient:               socketModeClient,
		commandQueue:                   commandQueue,
		issueFinder:                    issueFinder,
		cacheStore:                     cacheStore,
		cfg:                            cfg,
		managerSettings:                managerSettings,
		logger:                         logger,
		eventMap:                       eventMap,
		eventAPIMap:                    eventAPIMap,
		interactionEventMap:            interactionEventMap,
		interactionBlockActionEventMap: interactionBlockActionEventMap,
		slashCommandMap:                slackCommandMap,
		sem:                            semaphore.NewWeighted(cfg.SocketModeMaxWorkers),
	}

	// Internal slack client events
	handler.registerInternalEventsController()

	// Interactive actions
	handler.registerInteractiveController()

	// Slash commands actions
	handler.registerSlashCommandsController()

	// Post reactions (emojis)
	handler.registerReactionsController()

	// Greeting situations (joined channel etc)
	handler.registerGreetingsController()

	// Events API
	handler.registerEventsAPIController()

	// Default controller (fallback when nothing else matches)
	handler.registerDefaultController()

	return handler
}

// RunEventLoop starts the Socket Mode event loop. It blocks until the context is cancelled or an error occurs.
// During graceful shutdown, it waits for all in-flight handlers to complete up to the configured drain timeout.
func (r *SocketModeHandler) RunEventLoop(ctx context.Context) error {
	errg, ctx := errgroup.WithContext(ctx)

	// RunContext is a blocking function that connects the Slack Socket Mode API and handles all incoming
	// requests and outgoing responses.
	errg.Go(func() error {
		return r.socketModeClient.RunContext(ctx)
	})

	// Run the event loop, which listens for incoming events and dispatches them to the appropriate handlers.
	errg.Go(func() error {
		return r.runEventLoop(ctx)
	})

	err := errg.Wait()

	// Wait for in-flight handlers to complete with timeout
	r.drainHandlers()

	return err
}

// drainHandlers waits for all in-flight handlers to complete, up to the configured drain timeout.
func (r *SocketModeHandler) drainHandlers() {
	drainCtx, cancel := context.WithTimeout(context.Background(), r.cfg.SocketModeDrainTimeout)
	defer cancel()

	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		r.logger.Debug("All socket mode handlers completed gracefully")
	case <-drainCtx.Done():
		r.logger.WithField("timeout", r.cfg.SocketModeDrainTimeout).Info("Socket mode drain timeout exceeded, some handlers may not have completed")
	}
}

func (r *SocketModeHandler) registerDefaultController() {
	c := &defaultController{
		logger: r.logger,
	}

	r.handleDefault(c.handle)
}

func (r *SocketModeHandler) registerInternalEventsController() {
	c := &internalEventsController{
		logger: r.logger,
	}

	r.handleMultiple(
		[]socketmode.EventType{
			socketmode.EventTypeConnecting,
			socketmode.EventTypeInvalidAuth,
			socketmode.EventTypeConnectionError,
			socketmode.EventTypeConnected,
			socketmode.EventTypeIncomingError,
			socketmode.EventTypeErrorWriteFailed,
			socketmode.EventTypeErrorBadMessage,
		}, c.handleEventTypeClientInternal)

	r.handle(socketmode.EventTypeHello, c.handleEventTypeHello)
	r.handle(socketmode.EventTypeDisconnect, c.handleEventTypeDisconnect)
}

func (r *SocketModeHandler) registerInteractiveController() {
	c := &interactiveController{
		apiClient:       r.apiClient,
		commandQueue:    r.commandQueue,
		issueFinder:     r.issueFinder,
		logger:          r.logger,
		cfg:             r.cfg,
		managerSettings: r.managerSettings,
	}

	// Global shortcuts
	r.handleInteraction(slack.InteractionTypeShortcut, c.globalShortcutHandler)

	// Message action
	r.handleInteraction(slack.InteractionTypeMessageAction, c.messageActionHandler)

	// View submission
	r.handleInteraction(slack.InteractionTypeViewSubmission, c.viewSubmissionHandler)

	// Block actions (buttons etc)
	r.handleInteraction(slack.InteractionTypeBlockActions, c.blockActionsHandler)

	// Every other interaction
	r.handle(socketmode.EventTypeInteractive, c.defaultInteractiveHandler)
}

func (r *SocketModeHandler) registerSlashCommandsController() {
	c := &slashCommandsController{
		logger: r.logger,
	}

	r.handle(socketmode.EventTypeSlashCommand, c.handleEventTypeSlashCommands)
}

func (r *SocketModeHandler) registerReactionsController() {
	cacheKeyPrefix := r.cfg.CacheKeyPrefix + "reactions-controller:"
	cache := internal.NewCache(r.cacheStore, cacheKeyPrefix, r.logger)

	c := &reactionsController{
		apiClient:       r.apiClient,
		commandQueue:    r.commandQueue,
		cache:           cache,
		logger:          r.logger,
		cfg:             r.cfg,
		managerSettings: r.managerSettings,
	}

	r.handleEventsAPI(string(slackevents.ReactionAdded), c.reactionAdded)
	r.handleEventsAPI(string(slackevents.ReactionRemoved), c.reactionRemoved)
}

func (r *SocketModeHandler) registerGreetingsController() {
	c := &greetingsController{
		apiClient:       r.apiClient,
		logger:          r.logger,
		cfg:             r.cfg,
		managerSettings: r.managerSettings,
	}

	r.handleEventsAPI(string(slackevents.MemberJoinedChannel), c.memberJoinedChannel)
	r.handleEventsAPI(string(slackevents.MemberLeftChannel), c.memberLeftChannel)
}

func (r *SocketModeHandler) registerEventsAPIController() {
	c := &eventsAPIController{
		logger: r.logger,
	}

	r.handle(socketmode.EventTypeEventsAPI, c.handleEventTypeEventsAPI)
}

// handle registers a middleware function to use to handle an socketmode event.
func (r *SocketModeHandler) handle(et socketmode.EventType, fs ...SocketModeHandlerFunc) {
	r.eventMap[et] = append(r.eventMap[et], fs...)
}

// handleMultiple registers a middleware function to use to handle a list of socketmode events.
func (r *SocketModeHandler) handleMultiple(events []socketmode.EventType, fs ...SocketModeHandlerFunc) {
	for _, et := range events {
		r.eventMap[et] = append(r.eventMap[et], fs...)
	}
}

// handleInteraction registers a middleware function to use to handle an interaction type.
func (r *SocketModeHandler) handleInteraction(et slack.InteractionType, f SocketModeHandlerFunc) {
	r.interactionEventMap[et] = append(r.interactionEventMap[et], f)
}

// HandleEventsAPI registers a middleware function to use to handle an EventAPI event.
func (r *SocketModeHandler) handleEventsAPI(et string, f SocketModeHandlerFunc) {
	r.eventAPIMap[et] = append(r.eventAPIMap[et], f)
}

// HandleDefault registers a middleware function to use as a last resort.
func (r *SocketModeHandler) handleDefault(f SocketModeHandlerFunc) {
	r.defaultHandlerFunc = f
}

// dispatchHandler safely spawns a handler goroutine with concurrency limiting and panic recovery.
// It acquires a semaphore slot before spawning the goroutine and releases it when the handler completes.
// The handler is tracked by the waitgroup for graceful shutdown.
func (r *SocketModeHandler) dispatchHandler(ctx context.Context, evt *socketmode.Event, f SocketModeHandlerFunc) {
	// Try to acquire semaphore slot, respecting context cancellation
	if err := r.sem.Acquire(ctx, 1); err != nil {
		// Context cancelled, don't start new work
		return
	}

	r.wg.Go(func() {
		defer r.sem.Release(1)
		defer func() {
			if rec := recover(); rec != nil {
				r.logger.Errorf("Panic in socket mode handler: %v", rec)
			}
		}()

		f(ctx, evt, r.socketModeClient)
	})
}

func (r *SocketModeHandler) runEventLoop(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case evt, ok := <-r.socketModeClient.Events():
			if !ok {
				// Channel closed, socket mode client shut down
				return nil
			}
			r.dispatcher(ctx, evt)
		}
	}
}

func (r *SocketModeHandler) dispatcher(ctx context.Context, evt socketmode.Event) {
	var ishandled bool

	// Some eventType can be further decomposed
	switch evt.Type { //nolint:exhaustive
	case socketmode.EventTypeInteractive:
		ishandled = r.interactionDispatcher(ctx, &evt)
	case socketmode.EventTypeEventsAPI:
		ishandled = r.eventAPIDispatcher(ctx, &evt)
	case socketmode.EventTypeSlashCommand:
		ishandled = r.slashCommandDispatcher(ctx, &evt)
	default:
		ishandled = r.socketmodeDispatcher(ctx, &evt)
	}

	if !ishandled && r.defaultHandlerFunc != nil {
		r.dispatchHandler(ctx, &evt, r.defaultHandlerFunc)
	}
}

// Dispatch socketmode events to the registered middleware
func (r *SocketModeHandler) socketmodeDispatcher(ctx context.Context, evt *socketmode.Event) bool {
	if handlers, ok := r.eventMap[evt.Type]; ok {
		for _, f := range handlers {
			r.dispatchHandler(ctx, evt, f)
		}

		return true
	}

	return false
}

// Dispatch interactions to the registered middleware
func (r *SocketModeHandler) interactionDispatcher(ctx context.Context, evt *socketmode.Event) bool {
	var ishandled bool

	interaction, ok := evt.Data.(slack.InteractionCallback)
	if !ok {
		r.logger.Infof("Ignored %+v", evt)
		return false
	}

	if handlers, ok := r.interactionEventMap[interaction.Type]; ok {
		for _, f := range handlers {
			r.dispatchHandler(ctx, evt, f)
		}

		ishandled = true
	}

	blockActions := interaction.ActionCallback.BlockActions

	for _, action := range blockActions {
		if handlers, ok := r.interactionBlockActionEventMap[action.ActionID]; ok {
			for _, f := range handlers {
				r.dispatchHandler(ctx, evt, f)
			}

			ishandled = true
		}
	}

	if !ishandled {
		ishandled = r.socketmodeDispatcher(ctx, evt)
	}

	return ishandled
}

// Dispatch eventAPI events to the registered middleware
func (r *SocketModeHandler) eventAPIDispatcher(ctx context.Context, evt *socketmode.Event) bool {
	var ishandled bool

	eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		r.logger.Infof("Ignored %+v", evt)
		return false
	}

	if handlers, ok := r.eventAPIMap[eventsAPIEvent.InnerEvent.Type]; ok {
		for _, f := range handlers {
			r.dispatchHandler(ctx, evt, f)
		}

		ishandled = true
	}

	if !ishandled {
		ishandled = r.socketmodeDispatcher(ctx, evt)
	}

	return ishandled
}

// Dispatch SlashCommands events to the registered middleware
func (r *SocketModeHandler) slashCommandDispatcher(ctx context.Context, evt *socketmode.Event) bool {
	var ishandled bool
	slashCommandEvent, ok := evt.Data.(slack.SlashCommand)
	if !ok {
		r.logger.Infof("Ignored %+v", evt)
		return false
	}

	if handlers, ok := r.slashCommandMap[slashCommandEvent.Command]; ok {
		for _, f := range handlers {
			r.dispatchHandler(ctx, evt, f)
		}

		ishandled = true
	}

	if !ishandled {
		ishandled = r.socketmodeDispatcher(ctx, evt)
	}

	return ishandled
}
