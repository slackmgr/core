package models

import "time"

type MoveMapping struct {
	CorrelationID     string    `dynamodbav:"correlationId"     json:"correlationId"`
	OriginalChannelID string    `dynamodbav:"originalChannelId" json:"originalChannelId"`
	TargetChannelID   string    `dynamodbav:"targetChannelId"   json:"targetChannelId"`
	Timestamp         time.Time `dynamodbav:"timestamp"         json:"timestamp"`
}
