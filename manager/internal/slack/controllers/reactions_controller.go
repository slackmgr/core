package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/eko/gocache/lib/v4/store"
	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/peteraglen/slack-manager/internal"
	"github.com/peteraglen/slack-manager/manager/internal/models"
	"github.com/peteraglen/slack-manager/manager/internal/slack/handler"
	"github.com/peteraglen/slack-manager/manager/internal/slack/views"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

var (
	errUnmanagedChannel      = errors.New("channel is not managed by the manager")
	errUserIsNotChannelAdmin = errors.New("user is not channel admin")
)

type ReactionsController struct {
	client          handler.SocketClient
	commandHandler  handler.FifoQueueProducer
	cache           *internal.Cache
	logger          common.Logger
	cfg             *config.ManagerConfig
	managerSettings *models.ManagerSettingsWrapper
}

func NewReactionsController(eventhandler *handler.SocketModeHandler, client handler.SocketClient, commandHandler handler.FifoQueueProducer, cacheStore store.StoreInterface, logger common.Logger, cfg *config.ManagerConfig, managerSettings *models.ManagerSettingsWrapper) *ReactionsController {
	cacheKeyPrefix := cfg.CacheKeyPrefix + "reactions-controller:"
	cache := internal.NewCache(cacheStore, cacheKeyPrefix, logger)

	c := &ReactionsController{
		client:          client,
		commandHandler:  commandHandler,
		cache:           cache,
		logger:          logger,
		cfg:             cfg,
		managerSettings: managerSettings,
	}

	eventhandler.HandleEventsAPI(string(slackevents.ReactionAdded), c.reactionAdded)
	eventhandler.HandleEventsAPI(string(slackevents.ReactionRemoved), c.reactionRemoved)

	return c
}

func (c *ReactionsController) reactionAdded(ctx context.Context, evt *socketmode.Event, clt *socketmode.Client) {
	ack(evt, clt)

	apiEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		// This should never happen
		c.logger.Error("Failed to cast EventsAPIEvent")
		return
	}

	reactionAddedEvent, ok := apiEvent.InnerEvent.Data.(*slackevents.ReactionAddedEvent)
	if !ok {
		c.logger.Error("Failed to cast ReactionAddedEvent")
		return
	}

	reaction := c.managerSettings.Settings.MapSlackPostReaction(reactionAddedEvent.Reaction)

	c.logger.Debugf("Registered added reaction '%s', mapped to '%s'", reactionAddedEvent.Reaction, reaction)

	switch reaction {
	case config.IssueReactionTerminate:
		c.sendReactionAddedCommand(ctx, reactionAddedEvent, models.CommandActionTerminateIssue, true)
	case config.IssueReactionResolve:
		c.sendReactionAddedCommand(ctx, reactionAddedEvent, models.CommandActionResolveIssue, true)
	case config.IssueReactionInvestigate:
		c.sendReactionAddedCommand(ctx, reactionAddedEvent, models.CommandActionInvestigateIssue, false)
	case config.IssueReactionMute:
		c.sendReactionAddedCommand(ctx, reactionAddedEvent, models.CommandActionMuteIssue, true)
	case config.IssueReactionShowOptionButtons:
		c.sendReactionAddedCommand(ctx, reactionAddedEvent, models.CommandActionShowIssueOptionButtons, true)
	default:
	}
}

func (c *ReactionsController) reactionRemoved(ctx context.Context, evt *socketmode.Event, clt *socketmode.Client) {
	ack(evt, clt)

	apiEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		// This should never happen
		c.logger.Error("Failed to cast EventsAPIEvent")
		return
	}

	reactionRemovedEvent, ok := apiEvent.InnerEvent.Data.(*slackevents.ReactionRemovedEvent)
	if !ok {
		c.logger.Error("Failed to cast ReactionRemovedEvent")
		return
	}

	reaction := c.managerSettings.Settings.MapSlackPostReaction(reactionRemovedEvent.Reaction)

	c.logger.Debugf("Registered removed reaction '%s', mapped to '%s'", reactionRemovedEvent.Reaction, reaction)

	switch reaction {
	case config.IssueReactionResolve:
		c.sendReactionRemovedCommand(ctx, reactionRemovedEvent, models.CommandActionUnresolveIssue, true)
	case config.IssueReactionInvestigate:
		c.sendReactionRemovedCommand(ctx, reactionRemovedEvent, models.CommandActionUninvestigateIssue, false)
	case config.IssueReactionMute:
		c.sendReactionRemovedCommand(ctx, reactionRemovedEvent, models.CommandActionUnmuteIssue, true)
	case config.IssueReactionShowOptionButtons:
		c.sendReactionRemovedCommand(ctx, reactionRemovedEvent, models.CommandActionHideIssueOptionButtons, true)
	case config.IssueReactionTerminate:
		// This can't really happen (we can't un-terminate an issue)
	default:
	}
}

func (c *ReactionsController) sendReactionAddedCommand(ctx context.Context, evt *slackevents.ReactionAddedEvent, action models.CommandAction, requireChannelAdmin bool) {
	logger := c.logger.WithField("channel_id", evt.Item.Channel).WithField("user_id", evt.User).WithField("action_type", "added").
		WithField("reaction", evt.Reaction).WithField("slack_post_id", evt.Item.Timestamp)

	userInfo, err := c.getUserInfo(ctx, evt.Item.Channel, evt.User, requireChannelAdmin, logger)
	if err != nil {
		if !errors.Is(err, errUnmanagedChannel) && !errors.Is(err, errUserIsNotChannelAdmin) {
			logger.Errorf("Failed to get user info: %w", err)
		}
		return
	}

	if userInfo == nil {
		return
	}

	cmd := models.NewCommand(evt.Item.Channel, evt.Item.Timestamp, evt.Reaction, userInfo.ID, userInfo.RealName, action, nil)

	if action == models.CommandActionTerminateIssue {
		cmd.IncludeArchivedIssues = true
		cmd.ExecuteWhenNoIssueFound = true
	}

	if err := sendCommand(ctx, c.commandHandler, cmd); err != nil {
		logger.Errorf("Failed to send command '%s': %w", action, err)
	}
}

func (c *ReactionsController) sendReactionRemovedCommand(ctx context.Context, evt *slackevents.ReactionRemovedEvent, action models.CommandAction, requireChannelAdmin bool) {
	logger := c.logger.WithField("channel_id", evt.Item.Channel).WithField("user_id", evt.User).WithField("action_type", "removed").
		WithField("reaction", evt.Reaction).WithField("slack_post_id", evt.Item.Timestamp)

	userInfo, err := c.getUserInfo(ctx, evt.Item.Channel, evt.User, requireChannelAdmin, logger)
	if err != nil {
		if !errors.Is(err, errUnmanagedChannel) && !errors.Is(err, errUserIsNotChannelAdmin) {
			logger.Errorf("Failed to get user info: %w", err)
		}
		return
	}

	if userInfo == nil {
		return
	}

	cmd := models.NewCommand(evt.Item.Channel, evt.Item.Timestamp, evt.Reaction, userInfo.ID, userInfo.RealName, action, nil)

	if err := sendCommand(ctx, c.commandHandler, cmd); err != nil {
		logger.Errorf("Failed to send command '%s': %w", action, err)
	}
}

func (c *ReactionsController) getUserInfo(ctx context.Context, channel, userID string, requireChannelAdmin bool, logger common.Logger) (*slack.User, error) {
	isAlertChannel, _, err := c.client.IsAlertChannel(ctx, channel)
	if err != nil {
		return nil, fmt.Errorf("failed to verify if channel %s is a valid alert channel: %w", channel, err)
	}

	if !isAlertChannel {
		logger.Debug("User reaction to message in un-managed channel")
		return nil, errUnmanagedChannel
	}

	userInfo, err := c.client.GetUserInfo(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to read user info for %s: %w", userID, err)
	}

	logger = logger.WithField("user_name", userInfo.RealName)

	logger.Info("User reaction to message in managed channel")

	if requireChannelAdmin {
		userIsChannelAdmin := c.managerSettings.Settings.UserIsChannelAdmin(ctx, channel, userInfo.ID, c.client.UserIsInGroup)

		if !userIsChannelAdmin {
			logger.Info("User is not admin in channel")
			c.postNotAdminAlert(ctx, channel, userInfo.ID, userInfo.RealName, logger)
			return nil, errUserIsNotChannelAdmin
		}
	}

	return userInfo, nil
}

func (c *ReactionsController) postNotAdminAlert(ctx context.Context, channel, userID, userRealName string, logger common.Logger) {
	cacheKey := fmt.Sprintf("ReactionsController:userNotAdmin:%s:%s", channel, userID)

	if _, ok := c.cache.Get(ctx, cacheKey); ok {
		return
	}

	blocks, err := views.NotAdminView(userRealName, c.managerSettings.Settings)
	if err != nil {
		logger.Errorf("Failed to generate view: %s", err)
		return
	}

	_, err = c.client.PostEphemeral(ctx, channel, userID, slack.MsgOptionBlocks(blocks...))
	if err != nil {
		logger.Errorf("Failed to post ephemeral message: %s", err)
		return
	}

	logger.WithField("reason", "User is not admin in channel").Info("Post ephemeral message")

	c.cache.Set(ctx, cacheKey, "", 30*time.Minute)
}
