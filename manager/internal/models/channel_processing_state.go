package models

import (
	"encoding/json"
	"time"
)

type ChannelProcessingState struct {
	ID             string    `json:"id"`
	SlackChannelID string    `json:"slackChannelId"`
	Created        time.Time `json:"created"`
	LastProcessed  time.Time `json:"lastProcessed"`
}

func NewChannelProcessingState(channelID string) *ChannelProcessingState {
	return &ChannelProcessingState{
		ID:             channelID,
		SlackChannelID: channelID,
		Created:        time.Now(),
		LastProcessed:  time.Time{},
	}
}

func (s *ChannelProcessingState) ChannelID() string {
	return s.SlackChannelID
}

func (s *ChannelProcessingState) UniqueID() string {
	return s.ID
}

func (s *ChannelProcessingState) MarshalJSON() ([]byte, error) {
	type Alias ChannelProcessingState

	return json.Marshal(&struct {
		*Alias
	}{
		Alias: (*Alias)(s),
	})
}
