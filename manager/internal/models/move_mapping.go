package models

import (
	"encoding/json"
	"time"

	"github.com/peteraglen/slack-manager/internal"
)

// MoveMapping represents information about an issue moved between Slack channels.
type MoveMapping struct {
	ID                string          `json:"id"`
	Timestamp         time.Time       `json:"timestamp"`
	CorrelationID     string          `json:"correlationId"`
	OriginalChannelID string          `json:"originalChannelId"`
	TargetChannelID   string          `json:"targetChannelId"`
	Reason            MoveIssueReason `json:"reason"`
}

// NewMoveMapping creates a new MoveMapping instance.
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

// ChannelID returns the original channel ID of the move mapping, i.e., where the issue was moved from.
func (m *MoveMapping) ChannelID() string {
	return m.OriginalChannelID
}

// UniqueID returns the unique identifier of the move mapping.
func (m *MoveMapping) UniqueID() string {
	return m.ID
}

// GetCorrelationID returns the issue correlation ID associated with the move mapping.
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
