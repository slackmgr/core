package controllers

import (
	"context"

	"github.com/eko/gocache/lib/v4/store"
	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/peteraglen/slack-manager/internal"
	"github.com/peteraglen/slack-manager/manager/internal/models"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// SocketModeHandler handles Slack Socket Mode events and interactions.
type SocketModeHandler struct {
	apiClient        SlackAPIClient
	socketModeClient *socketmode.Client
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
}

// SocketModeHandlerFunc defines a function to handle socketmode events.
type SocketModeHandlerFunc func(context.Context, *socketmode.Event, *socketmode.Client)

// NewSocketModeHandler creates a new SocketModeHandler.
func NewSocketModeHandler(apiClient SlackAPIClient, socketModeClient *socketmode.Client, commandQueue FifoQueueProducer, issueFinder IssueFinder,
	cacheStore store.StoreInterface, cfg *config.ManagerConfig, managerSettings *models.ManagerSettingsWrapper, logger common.Logger,
) *SocketModeHandler {
	eventMap := make(map[socketmode.EventType][]SocketModeHandlerFunc)
	interactionEventMap := make(map[slack.InteractionType][]SocketModeHandlerFunc)
	interactionBlockActionEventMap := make(map[string][]SocketModeHandlerFunc)
	eventAPIMap := make(map[string][]SocketModeHandlerFunc)
	slackCommandMap := make(map[string][]SocketModeHandlerFunc)

	return &SocketModeHandler{
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
	}
}

func (r *SocketModeHandler) RegisterDefaultController() {
	c := &defaultController{
		logger: r.logger,
	}

	r.handleDefault(c.handle)
}

func (r *SocketModeHandler) RegisterInternalEventsController() {
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

func (r *SocketModeHandler) RegisterInteractiveController() {
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

func (r *SocketModeHandler) RegisterSlashCommandsController() {
	c := &slashCommandsController{
		logger: r.logger,
	}

	r.handle(socketmode.EventTypeSlashCommand, c.handleEventTypeSlashCommands)
}

func (r *SocketModeHandler) RegisterReactionsController() {
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

func (r *SocketModeHandler) RegisterGreetingsController() {
	c := &greetingsController{
		apiClient:       r.apiClient,
		logger:          r.logger,
		cfg:             r.cfg,
		managerSettings: r.managerSettings,
	}

	r.handleEventsAPI(string(slackevents.MemberJoinedChannel), c.memberJoinedChannel)
	r.handleEventsAPI(string(slackevents.MemberLeftChannel), c.memberLeftChannel)
}

func (r *SocketModeHandler) RegisterEventsAPIController() {
	c := &eventsAPIController{
		logger: r.logger,
	}

	r.handle(socketmode.EventTypeEventsAPI, c.handleEventTypeEventsAPI)
}

// RunEventLoop receives the event via the socket.
func (r *SocketModeHandler) RunEventLoop(ctx context.Context) error {
	go r.runEventLoop(ctx)
	return r.socketModeClient.RunContext(ctx)
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

func (r *SocketModeHandler) runEventLoop(ctx context.Context) {
	for evt := range r.socketModeClient.Events {
		r.dispatcher(ctx, evt)
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
		go r.defaultHandlerFunc(ctx, &evt, r.socketModeClient)
	}
}

// Dispatch socketmode events to the registered middleware
func (r *SocketModeHandler) socketmodeDispatcher(ctx context.Context, evt *socketmode.Event) bool {
	if handlers, ok := r.eventMap[evt.Type]; ok {
		for _, f := range handlers {
			go f(ctx, evt, r.socketModeClient)
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
			go f(ctx, evt, r.socketModeClient)
		}

		ishandled = true
	}

	blockActions := interaction.ActionCallback.BlockActions

	for _, action := range blockActions {
		if handlers, ok := r.interactionBlockActionEventMap[action.ActionID]; ok {
			for _, f := range handlers {
				go f(ctx, evt, r.socketModeClient)
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
			go f(ctx, evt, r.socketModeClient)
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
			go f(ctx, evt, r.socketModeClient)
		}

		ishandled = true
	}

	if !ishandled {
		ishandled = r.socketmodeDispatcher(ctx, evt)
	}

	return ishandled
}
