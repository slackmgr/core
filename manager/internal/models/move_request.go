package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	commonlib "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/internal"
)

type MoveRequest struct {
	message

	Timestamp     time.Time `json:"timestamp"`
	CorrelationID string    `json:"correlationId"`
	SourceChannel string    `json:"sourceChannel"`
	TargetChannel string    `json:"targetChannel"`
	UserRealName  string    `json:"userRealName"`
}

func NewMoveRequest(correlationID, sourceChannel, targetChannel, userRealName string) *MoveRequest {
	return &MoveRequest{
		Timestamp:     time.Now().UTC(),
		CorrelationID: correlationID,
		SourceChannel: sourceChannel,
		TargetChannel: targetChannel,
		UserRealName:  userRealName,
	}
}

func NewMoveRequestFromQueue(queueItem *commonlib.FifoQueueItem) (Message, error) { //nolint:ireturn
	if len(queueItem.Body) == 0 {
		return nil, errors.New("move request body is empty")
	}

	var moveRequest MoveRequest

	if err := json.Unmarshal([]byte(queueItem.Body), &moveRequest); err != nil {
		return nil, fmt.Errorf("failed to unmarshal move request body: %w", err)
	}

	moveRequest.message = newMessage(queueItem)

	return &moveRequest, nil
}

func (m *MoveRequest) DedupID() string {
	return fmt.Sprintf("moveRequest::%s::%s", internal.Hash(m.CorrelationID), m.Timestamp.Format(time.RFC3339Nano))
}

func (m *MoveRequest) LogFields() map[string]any {
	if m == nil {
		return nil
	}

	fields := map[string]any{
		"correlation_id": m.CorrelationID,
		"source_channel": m.SourceChannel,
		"target_channel": m.TargetChannel,
		"user_name":      m.UserRealName,
	}

	return fields
}
