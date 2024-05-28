package models

import (
	"time"

	"github.com/peteraglen/slack-manager/internal"
)

type MoveMapping struct {
	ID                string    `json:"id"`
	Timestamp         time.Time `json:"timestamp"`
	CorrelationID     string    `json:"correlationId"`
	OriginalChannelID string    `json:"originalChannelId"`
	TargetChannelID   string    `json:"targetChannelId"`
}

func NewMoveMapping(correlationID, originalChannelID, targetChannelID string) *MoveMapping {
	return &MoveMapping{
		ID:                internal.Hash(originalChannelID, correlationID),
		Timestamp:         time.Now(),
		CorrelationID:     correlationID,
		OriginalChannelID: originalChannelID,
		TargetChannelID:   targetChannelID,
	}
}
