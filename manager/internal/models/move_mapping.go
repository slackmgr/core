package models

import (
	"time"

	"github.com/peteraglen/slack-manager/internal"
)

type MoveMapping struct {
	ID                string    `json:"id"`
	CorrelationID     string    `json:"correlationId"`
	OriginalChannelID string    `json:"originalChannelId"`
	TargetChannelID   string    `json:"targetChannelId"`
	Timestamp         time.Time `json:"timestamp"`
}

func NewMoveMapping(correlationID, originalChannelID, targetChannelID string) *MoveMapping {
	return &MoveMapping{
		ID:                internal.Hash(originalChannelID, correlationID),
		CorrelationID:     correlationID,
		OriginalChannelID: originalChannelID,
		TargetChannelID:   targetChannelID,
		Timestamp:         time.Now(),
	}
}
