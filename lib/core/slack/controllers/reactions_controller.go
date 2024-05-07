package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/eko/gocache/lib/v4/cache"
	"github.com/eko/gocache/lib/v4/store"
	"github.com/peteraglen/slack-manager/lib/common"
	"github.com/peteraglen/slack-manager/lib/core/config"
	"github.com/peteraglen/slack-manager/lib/core/models"
	"github.com/peteraglen/slack-manager/lib/core/slack/handler"
	"github.com/peteraglen/slack-manager/lib/core/slack/views"
	"github.com/peteraglen/slack-manager/lib/internal"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// TerminationEmoji is the emoji (reaction) used to indicate that a post should be terminated/deleted
const TerminationEmoji = "firecracker"

// ResolveEmoji is the emoji (reaction) used to indicate that an issue has been resolved
const (
	ResolveEmoji    = "monitor_ok"
	ResolveEmojiAlt = "white_check_mark"
)

// InvestigateEmoji is the emoji (reaction) used to indicate that an issue is being investigated
const InvestigateEmoji = "eyes"

// MuteEmoji is the emoji (reaction) used to indicate that an issue has been muted
const MuteEmoji = "mask"

type ReactionsController struct {
	client         handler.SocketClient
	commandHandler common.FifoQueueProducer
	cache          *internal.Cache[string]
	logger         common.Logger
	conf           *config.Config
}

func NewReactionsController(eventhandler *handler.SocketModeHandler, client handler.SocketClient, commandHandler common.FifoQueueProducer, cacheStore store.StoreInterface, logger common.Logger, conf *config.Config) *ReactionsController {
	cache := internal.NewCache(cache.New[string](cacheStore), logger)

	c := &ReactionsController{
		client:         client,
		commandHandler: commandHandler,
		cache:          cache,
		logger:         logger,
		conf:           conf,
	}

	eventhandler.HandleEventsAPI(string(slackevents.ReactionAdded), c.reactionAdded)
	eventhandler.HandleEventsAPI(string(slackevents.ReactionRemoved), c.reactionRemoved)

	return c
}

func (c *ReactionsController) reactionAdded(ctx context.Context, evt *socketmode.Event, clt *socketmode.Client) {
	ack(evt, clt)

	apiEvent := evt.Data.(slackevents.EventsAPIEvent)

	reactionAddedEvent, ok := apiEvent.InnerEvent.Data.(*slackevents.ReactionAddedEvent)
	if !ok {
		c.logger.Error("Failed to cast ReactionAddedEvent")
		return
	}

	switch reactionAddedEvent.Reaction {
	case TerminationEmoji:
		c.sendReactionAddedCommand(ctx, reactionAddedEvent, models.CommandActionTerminateIssue, true)
	case ResolveEmoji, ResolveEmojiAlt:
		c.sendReactionAddedCommand(ctx, reactionAddedEvent, models.CommandActionResolveIssue, true)
	case InvestigateEmoji:
		c.sendReactionAddedCommand(ctx, reactionAddedEvent, models.CommandActionInvestigateIssue, false)
	case MuteEmoji:
		c.sendReactionAddedCommand(ctx, reactionAddedEvent, models.CommandActionMuteIssue, true)
	default:
	}
}

func (c *ReactionsController) reactionRemoved(ctx context.Context, evt *socketmode.Event, clt *socketmode.Client) {
	ack(evt, clt)

	apiEvent := evt.Data.(slackevents.EventsAPIEvent)

	reactionRemovedEvent, ok := apiEvent.InnerEvent.Data.(*slackevents.ReactionRemovedEvent)
	if !ok {
		c.logger.Error("Failed to cast ReactionRemovedEvent")
		return
	}

	switch reactionRemovedEvent.Reaction {
	case ResolveEmoji, ResolveEmojiAlt:
		c.sendReactionRemovedCommand(ctx, reactionRemovedEvent, models.CommandActionUnresolveIssue, true)
	case InvestigateEmoji:
		c.sendReactionRemovedCommand(ctx, reactionRemovedEvent, models.CommandActionUninvestigateIssue, false)
	case MuteEmoji:
		c.sendReactionRemovedCommand(ctx, reactionRemovedEvent, models.CommandActionUnmuteIssue, true)
	default:
	}
}

func (c *ReactionsController) sendReactionAddedCommand(ctx context.Context, evt *slackevents.ReactionAddedEvent, action models.CommandAction, requireChannelAdmin bool) {
	logger := c.logger.WithField("slack_channel_id", evt.Item.Channel).WithField("user_id", evt.User).WithField("action_type", "added").
		WithField("reaction", evt.Reaction).WithField("slack_post_id", evt.Item.Timestamp)

	userInfo, err := c.getUserInfo(ctx, evt.Item.Channel, evt.User, requireChannelAdmin, logger)
	if err != nil {
		logger.ErrorfUnlessContextCanceled("Failed to get user info: %w", err)
		return
	}

	if userInfo == nil {
		return
	}

	cmd := models.NewCommand(evt.Item.Channel, evt.Item.Timestamp, evt.Reaction, userInfo.ID, userInfo.RealName, action, nil)

	if action == models.CommandActionTerminateIssue {
		cmd.IncludeArchivedIssues = true
	}

	if err := sendCommand(ctx, c.commandHandler, cmd); err != nil {
		logger.ErrorfUnlessContextCanceled("Failed to send command '%s': %w", action, err)
	}
}

func (c *ReactionsController) sendReactionRemovedCommand(ctx context.Context, evt *slackevents.ReactionRemovedEvent, action models.CommandAction, requireChannelAdmin bool) {
	logger := c.logger.WithField("slack_channel_id", evt.Item.Channel).WithField("user_id", evt.User).WithField("action_type", "removed").
		WithField("reaction", evt.Reaction).WithField("slack_post_id", evt.Item.Timestamp)

	userInfo, err := c.getUserInfo(ctx, evt.Item.Channel, evt.User, requireChannelAdmin, logger)
	if err != nil {
		logger.ErrorfUnlessContextCanceled("Failed to get user info: %w", err)
		return
	}

	if userInfo == nil {
		return
	}

	cmd := models.NewCommand(evt.Item.Channel, evt.Item.Timestamp, evt.Reaction, userInfo.ID, userInfo.RealName, action, nil)

	if err := sendCommand(ctx, c.commandHandler, cmd); err != nil {
		logger.ErrorfUnlessContextCanceled("Failed to send command '%s': %w", action, err)
	}
}

func (c *ReactionsController) getUserInfo(ctx context.Context, channel, userID string, requireChannelAdmin bool, logger common.Logger) (*slack.User, error) {
	managedChannel, _, err := c.client.IsAlertChannel(ctx, channel)
	if err != nil {
		return nil, fmt.Errorf("failed to verify if Slack manager is in channel: %w", err)
	}

	if !managedChannel {
		logger.Debug("User reaction to message in un-managed channel")
		return nil, nil
	}

	userInfo, err := c.client.GetUserInfo(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to read user info for %s: %w", userID, err)
	}

	logger = logger.WithField("user_name", userInfo.RealName)

	logger.Info("User reaction to message in managed channel")

	if requireChannelAdmin {
		userIsChannelAdmin := c.conf.UserIsChannelAdmin(ctx, channel, userInfo.ID, c.client.UserIsInGroup)

		if !userIsChannelAdmin {
			logger.Info("User is not admin in channel")
			c.postNotAdminAlert(ctx, channel, userInfo.ID, userInfo.RealName, logger)
			return nil, nil
		}
	}

	return userInfo, nil
}

func (c *ReactionsController) postNotAdminAlert(ctx context.Context, channel, userID, userRealName string, logger common.Logger) {
	cacheKey := fmt.Sprintf("ReactionsController::userNotAdmin::%s::%s", channel, userID)

	if _, ok := c.cache.Get(ctx, cacheKey); ok {
		return
	}

	blocks, err := views.NotAdminView(userRealName, c.conf)
	if err != nil {
		logger.ErrorfUnlessContextCanceled("Failed to generate view: %s", err)
		return
	}

	_, err = c.client.PostEphemeral(ctx, channel, userID, slack.MsgOptionBlocks(blocks...))
	if err != nil {
		logger.ErrorfUnlessContextCanceled("Failed to post ephemeral message: %s", err)
		return
	}

	logger.WithField("reason", "User is not admin in channel").Info("Post ephemeral message")

	c.cache.Set(ctx, cacheKey, "", 30*time.Minute)
}
