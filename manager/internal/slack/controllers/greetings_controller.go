package controllers

import (
	"context"

	slackapi "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
	"github.com/slackmgr/core/manager/internal/models"
	"github.com/slackmgr/core/manager/internal/slack/views"
	"github.com/slackmgr/types"
)

type greetingsController struct {
	clt             SocketModeClient
	apiClient       SlackAPIClient
	logger          types.Logger
	managerSettings *models.ManagerSettingsWrapper
}

func (c *greetingsController) memberJoinedChannel(ctx context.Context, evt *socketmode.Event) {
	c.clt.Ack(ctx, evt.Request)

	apiEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		c.logger.WithField("operation", "slack").WithField("envelope_id", evt.Request.EnvelopeID).Errorf("Failed to cast EventsAPIEvent, got: %T", evt.Data)
		return
	}

	joinedEvent, ok := apiEvent.InnerEvent.Data.(*slackevents.MemberJoinedChannelEvent)
	if !ok {
		c.logger.Errorf("Failed to cast event data to MemberJoinedChannelEvent, got: %T", apiEvent.InnerEvent.Data)
		return
	}

	logger := c.logger.WithField("channel_id", joinedEvent.Channel)

	userInfo, err := c.apiClient.GetUserInfo(ctx, joinedEvent.User)
	if err != nil {
		logger.WithField("channel_id", joinedEvent.Channel).WithField("user_id", joinedEvent.User).Errorf("Failed to read Slack user info: %s", err)
		return
	}

	if userInfo.IsBot || userInfo.IsAppUser {
		return
	}

	logger = logger.WithField("user_name", userInfo.RealName).WithField("user_id", userInfo.ID)

	isAlertChannel, _, err := c.apiClient.IsAlertChannel(ctx, joinedEvent.Channel)
	if err != nil {
		logger.Errorf("Failed to verify if channel %s is a valid alert channel: %s", joinedEvent.Channel, err)
		return
	}

	if !isAlertChannel {
		logger.Debug("User joined un-managed channel")
		return
	}

	logger.Info("User joined managed alert channel")

	userIsChannelAdmin := c.managerSettings.GetSettings().UserIsChannelAdmin(ctx, joinedEvent.Channel, userInfo.ID, c.apiClient.UserIsInGroup)

	blocks, err := views.GreetingView(userInfo.RealName, userIsChannelAdmin, c.managerSettings.GetSettings())
	if err != nil {
		logger.Errorf("Failed to generate view: %s", err)
		return
	}

	attachments := slackapi.MsgOptionAttachments(slackapi.Attachment{
		Blocks: slackapi.Blocks{
			BlockSet: blocks,
		},
	})

	_, err = c.apiClient.PostEphemeral(ctx, joinedEvent.Channel, joinedEvent.User, attachments)
	if err != nil {
		logger.Errorf("Failed to post greeting message: %s", err)
		return
	}

	logger.WithField("reason", "Greeting message in managed channel").Info("Post ephemeral message")
}

func (c *greetingsController) memberLeftChannel(ctx context.Context, evt *socketmode.Event) {
	c.clt.Ack(ctx, evt.Request)

	apiEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		c.logger.WithField("operation", "slack").WithField("envelope_id", evt.Request.EnvelopeID).Errorf("Failed to cast EventsAPIEvent, got: %T", evt.Data)
		return
	}

	leftChannelEvent, ok := apiEvent.InnerEvent.Data.(*slackevents.MemberLeftChannelEvent)
	if !ok {
		c.logger.Errorf("Failed to cast event data to MemberLeftChannelEvent, got: %T", apiEvent.InnerEvent.Data)
		return
	}

	userInfo, err := c.apiClient.GetUserInfo(ctx, leftChannelEvent.User)
	if err != nil {
		c.logger.WithField("channel_id", leftChannelEvent.Channel).WithField("user_id", leftChannelEvent.User).Errorf("Failed to read Slack user info: %s", err)
		return
	}

	logger := c.logger.WithField("channel_id", leftChannelEvent.Channel).WithField("user_name", userInfo.RealName).WithField("user_id", userInfo.ID)

	if userInfo.IsBot || userInfo.IsAppUser {
		return
	}

	isAlertChannel, _, err := c.apiClient.IsAlertChannel(ctx, leftChannelEvent.Channel)
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
