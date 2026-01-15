package handler

import (
	"context"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// SocketModeHandler handles Slack Socket Mode events and interactions.
type SocketModeHandler struct {
	Client                         *socketmode.Client
	EventMap                       map[socketmode.EventType][]SocketModeHandlerFunc
	InteractionEventMap            map[slack.InteractionType][]SocketModeHandlerFunc
	InteractionBlockActionEventMap map[string][]SocketModeHandlerFunc
	EventAPIMap                    map[string][]SocketModeHandlerFunc
	SlashCommandMap                map[string][]SocketModeHandlerFunc
	Default                        SocketModeHandlerFunc
	logger                         common.Logger
}

// SocketModeHandlerFunc defines a function to handle socketmode events.
type SocketModeHandlerFunc func(context.Context, *socketmode.Event, *socketmode.Client)

// NewSocketModeHandler creates a new SocketModeHandler.
func NewSocketModeHandler(client *socketmode.Client, logger common.Logger) *SocketModeHandler {
	eventMap := make(map[socketmode.EventType][]SocketModeHandlerFunc)
	interactionEventMap := make(map[slack.InteractionType][]SocketModeHandlerFunc)
	interactionBlockActionEventMap := make(map[string][]SocketModeHandlerFunc)
	eventAPIMap := make(map[string][]SocketModeHandlerFunc)
	slackCommandMap := make(map[string][]SocketModeHandlerFunc)

	return &SocketModeHandler{
		Client:                         client,
		EventMap:                       eventMap,
		EventAPIMap:                    eventAPIMap,
		InteractionEventMap:            interactionEventMap,
		InteractionBlockActionEventMap: interactionBlockActionEventMap,
		SlashCommandMap:                slackCommandMap,
		logger:                         logger,
	}
}

// Handle registers a middleware function to use to handle an socketmode event.
func (r *SocketModeHandler) Handle(et socketmode.EventType, fs ...SocketModeHandlerFunc) {
	r.EventMap[et] = append(r.EventMap[et], fs...)
}

// HandleMultiple registers a middleware function to use to handle a list of socketmode events.
func (r *SocketModeHandler) HandleMultiple(events []socketmode.EventType, fs ...SocketModeHandlerFunc) {
	for _, et := range events {
		r.EventMap[et] = append(r.EventMap[et], fs...)
	}
}

// HandleInteraction registers a middleware function to use to handle an interaction type.
func (r *SocketModeHandler) HandleInteraction(et slack.InteractionType, f SocketModeHandlerFunc) {
	r.InteractionEventMap[et] = append(r.InteractionEventMap[et], f)
}

// HandleInteractionBlockAction registers a middleware function to use to handle an interaction block action referenced by its ActionID.
func (r *SocketModeHandler) HandleInteractionBlockAction(actionID string, f SocketModeHandlerFunc) {
	r.InteractionBlockActionEventMap[actionID] = append(r.InteractionBlockActionEventMap[actionID], f)
}

// HandleEventsAPI registers a middleware function to use to handle an EventAPI event.
func (r *SocketModeHandler) HandleEventsAPI(et string, f SocketModeHandlerFunc) {
	r.EventAPIMap[et] = append(r.EventAPIMap[et], f)
}

// HandleSlashCommand registers a middleware function to use to handle a slash command.
func (r *SocketModeHandler) HandleSlashCommand(command string, f SocketModeHandlerFunc) {
	r.SlashCommandMap[command] = append(r.SlashCommandMap[command], f)
}

// HandleDefault registers a middleware function to use as a last resort.
func (r *SocketModeHandler) HandleDefault(f SocketModeHandlerFunc) {
	r.Default = f
}

// RunEventLoop receives the event via the socket.
func (r *SocketModeHandler) RunEventLoop(ctx context.Context) error {
	go r.runEventLoop(ctx)
	return r.Client.RunContext(ctx)
}

func (r *SocketModeHandler) runEventLoop(ctx context.Context) {
	for evt := range r.Client.Events {
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

	if !ishandled && r.Default != nil {
		go r.Default(ctx, &evt, r.Client)
	}
}

// Dispatch socketmode events to the registered middleware
func (r *SocketModeHandler) socketmodeDispatcher(ctx context.Context, evt *socketmode.Event) bool {
	if handlers, ok := r.EventMap[evt.Type]; ok {
		for _, f := range handlers {
			go f(ctx, evt, r.Client)
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

	if handlers, ok := r.InteractionEventMap[interaction.Type]; ok {
		for _, f := range handlers {
			go f(ctx, evt, r.Client)
		}

		ishandled = true
	}

	blockActions := interaction.ActionCallback.BlockActions

	for _, action := range blockActions {
		if handlers, ok := r.InteractionBlockActionEventMap[action.ActionID]; ok {
			for _, f := range handlers {
				go f(ctx, evt, r.Client)
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

	if handlers, ok := r.EventAPIMap[eventsAPIEvent.InnerEvent.Type]; ok {
		for _, f := range handlers {
			go f(ctx, evt, r.Client)
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

	if handlers, ok := r.SlashCommandMap[slashCommandEvent.Command]; ok {
		for _, f := range handlers {
			go f(ctx, evt, r.Client)
		}

		ishandled = true
	}

	if !ishandled {
		ishandled = r.socketmodeDispatcher(ctx, evt)
	}

	return ishandled
}
