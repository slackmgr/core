package models

import (
	"encoding/json"
	"time"

	"github.com/peteraglen/slack-manager/internal"
)

type MoveMapping struct {
	ID                string          `json:"id"`
	Timestamp         time.Time       `json:"timestamp"`
	CorrelationID     string          `json:"correlationId"`
	OriginalChannelID string          `json:"originalChannelId"`
	TargetChannelID   string          `json:"targetChannelId"`
	Reason            MoveIssueReason `json:"reason"`
}

func NewMoveMapping(correlationID, originalChannelID, targetChannelID string, reason MoveIssueReason) *MoveMapping {
	return &MoveMapping{
		ID:                internal.Hash(originalChannelID, correlationID),
		Timestamp:         time.Now(),
		CorrelationID:     correlationID,
		OriginalChannelID: originalChannelID,
		TargetChannelID:   targetChannelID,
		Reason:            reason,
	}
}

func (m *MoveMapping) ChannelID() string {
	return m.OriginalChannelID
}

func (m *MoveMapping) UniqueID() string {
	return m.ID
}

func (m *MoveMapping) GetCorrelationID() string {
	return m.CorrelationID
}

func (m *MoveMapping) MarshalJSON() ([]byte, error) {
	type Alias MoveMapping

	return json.Marshal(&struct {
		*Alias
	}{
		Alias: (*Alias)(m),
	})
}
