package controllers

import (
	"context"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/peteraglen/slack-manager/manager/internal/models"
	"github.com/peteraglen/slack-manager/manager/internal/slack/handler"
	"github.com/peteraglen/slack-manager/manager/internal/slack/views"
	slackapi "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

type GreetingsController struct {
	client          handler.SocketClient
	logger          common.Logger
	cfg             *config.ManagerConfig
	managerSettings *models.ManagerSettingsWrapper
}

func NewGreetingsController(eventhandler *handler.SocketModeHandler, client handler.SocketClient, logger common.Logger, cfg *config.ManagerConfig, managerSettings *models.ManagerSettingsWrapper) *GreetingsController {
	c := &GreetingsController{
		client:          client,
		logger:          logger,
		cfg:             cfg,
		managerSettings: managerSettings,
	}

	eventhandler.HandleEventsAPI(string(slackevents.MemberJoinedChannel), c.memberJoinedChannel)
	eventhandler.HandleEventsAPI(string(slackevents.MemberLeftChannel), c.memberLeftChannel)

	return c
}

func (c *GreetingsController) memberJoinedChannel(ctx context.Context, evt *socketmode.Event, clt *socketmode.Client) {
	ack(evt, clt)

	apiEvent, _ := evt.Data.(slackevents.EventsAPIEvent)

	joinedEvent, ok := apiEvent.InnerEvent.Data.(*slackevents.MemberJoinedChannelEvent)
	if !ok {
		c.logger.Error("Failed to cast MemberJoinedChannelEvent")
		return
	}

	userInfo, err := c.client.GetUserInfo(ctx, joinedEvent.User)
	if err != nil {
		c.logger.WithField("channel_id", joinedEvent.Channel).WithField("user_id", joinedEvent.User).Errorf("Failed to read Slack user info: %s", err)
		return
	}

	if userInfo.IsBot || userInfo.IsAppUser {
		return
	}

	isAlertChannel, _, err := c.client.IsAlertChannel(ctx, joinedEvent.Channel)
	if err != nil {
		c.logger.Errorf("Failed to verify if channel %s is a valid alert channel: %s", joinedEvent.Channel, err)
		return
	}

	logger := c.logger.WithField("channel_id", joinedEvent.Channel).WithField("user_name", userInfo.RealName).WithField("user_id", userInfo.ID)

	if isAlertChannel {
		logger.Info("User joined managed channel")
	} else {
		logger.Info("User joined un-managed channel")
	}

	infoChannelConfig, isInfoChannel := c.managerSettings.Settings.GetInfoChannelConfig(joinedEvent.Channel)

	if isInfoChannel {
		blocks, err := views.InfoChannelView(infoChannelConfig.TemplatePath, userInfo.RealName)
		if err != nil {
			logger.Errorf("Failed to generate view: %s", err)
			return
		}

		_, err = c.client.PostEphemeral(ctx, joinedEvent.Channel, joinedEvent.User, slackapi.MsgOptionBlocks(blocks...))
		if err != nil {
			logger.Errorf("Failed to post greeting message: %s", err)
			return
		}

		logger.WithField("reason", "Greeting message in info channel").Info("Post ephemeral message")
	} else if isAlertChannel {
		userIsChannelAdmin := c.managerSettings.Settings.UserIsChannelAdmin(ctx, joinedEvent.Channel, userInfo.ID, c.client.UserIsInGroup)

		blocks, err := views.GreetingView(userInfo.RealName, userIsChannelAdmin, c.managerSettings.Settings)
		if err != nil {
			logger.Errorf("Failed to generate view: %s", err)
			return
		}

		attachments := slackapi.MsgOptionAttachments(slackapi.Attachment{
			Blocks: slackapi.Blocks{
				BlockSet: blocks,
			},
		})

		_, err = c.client.PostEphemeral(ctx, joinedEvent.Channel, joinedEvent.User, attachments)
		if err != nil {
			logger.Errorf("Failed to post greeting message: %s", err)
			return
		}

		logger.WithField("reason", "Greeting message in managed channel").Info("Post ephemeral message")
	}
}

func (c *GreetingsController) memberLeftChannel(ctx context.Context, evt *socketmode.Event, clt *socketmode.Client) {
	ack(evt, clt)

	apiEvent, _ := evt.Data.(slackevents.EventsAPIEvent)

	leftChannelEvent, ok := apiEvent.InnerEvent.Data.(*slackevents.MemberLeftChannelEvent)
	if !ok {
		c.logger.Error("Failed to cast MemberLeftChannelEvent")
		return
	}

	userInfo, err := c.client.GetUserInfo(ctx, leftChannelEvent.User)
	if err != nil {
		c.logger.WithField("channel_id", leftChannelEvent.Channel).WithField("user_id", leftChannelEvent.User).Errorf("Failed to read Slack user info: %s", err)
		return
	}

	logger := c.logger.WithField("channel_id", leftChannelEvent.Channel).WithField("user_name", userInfo.RealName).WithField("user_id", userInfo.ID)

	if userInfo.IsBot || userInfo.IsAppUser {
		return
	}

	isAlertChannel, _, err := c.client.IsAlertChannel(ctx, leftChannelEvent.Channel)
	if err != nil {
		logger.Errorf("Failed to verify if channel %s is a valid alert channel: %s", leftChannelEvent.Channel, err)
		return
	}

	if isAlertChannel {
		logger.Info("User left managed channel")
	} else {
		logger.Debug("User left un-managed channel")
	}
}
