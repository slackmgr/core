package slackapi

import (
	"time"

	"github.com/slack-go/slack"
)

type ChannelSummary struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	IsPrivate bool      `json:"is_private"`
	Created   time.Time `json:"created"`
	Topic     string    `json:"topic"`
	Purpose   string    `json:"purpose"`
}

func NewChannelSummary(channel slack.Channel) *ChannelSummary {
	return &ChannelSummary{
		ID:        channel.ID,
		Name:      channel.Name,
		IsPrivate: channel.IsPrivate,
		Created:   channel.Created.Time(),
		Topic:     channel.Topic.Value,
		Purpose:   channel.Purpose.Value,
	}
}
