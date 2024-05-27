package internal

import (
	"github.com/slack-go/slack"
)

type ChannelSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Create a new ChannelSummary from a slack.Channel object.
// We map only the fields we need, and ignore the rest.
func NewChannelSummary(channel slack.Channel) *ChannelSummary {
	return &ChannelSummary{
		ID:   channel.ID,
		Name: channel.Name,
	}
}
