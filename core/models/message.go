package models

import (
	"context"
	"sync"
	"time"

	"github.com/peteraglen/slack-manager/common"
)

type UnmarshalFunc func(messageID, groupID, receiptHandle string, receiveTimestamp time.Time, visibilityTimeout time.Duration, body string) (Message, error)

type Message interface {
	MessageID() string
	ReceiptHandle() string
	SetAckFunc(f func(ctx context.Context, messageID, groupID, receiptHandle string))
	IsAcked() bool
	SetExtendFunc(f func(ctx context.Context, messageID, groupID, receiptHandle string))
	NeedsExtension() bool
	Extend(context.Context, common.Logger)
}

type message struct {
	messageID         string
	groupID           string
	receiptHandle     string
	receiveTimestamp  time.Time
	visibilityTimeout time.Duration

	// ack is used to delete the SQS message, i.e. ack it and declare it as processed.
	ack func(ctx context.Context, messageID, groupID, receiptHandle string)

	// extend is used to extend the visibility timeout of the SQS message. This is to prevent the ack from failing due to the visibilty timeout expiring.
	extend      func(ctx context.Context, messageID, groupID, receiptHandle string)
	extendCount int

	// processingLock is used to prevent the ack and extend functions from being called at the same time.
	processingLock *sync.Mutex
}

func newMessage(messageID, groupID, receiptHandle string, receiveTimestamp time.Time, visibilityTimeout time.Duration) message {
	return message{
		messageID:         messageID,
		groupID:           groupID,
		receiptHandle:     receiptHandle,
		receiveTimestamp:  receiveTimestamp,
		visibilityTimeout: visibilityTimeout,
		processingLock:    &sync.Mutex{},
	}
}

func (m *message) MessageID() string {
	return m.messageID
}

func (m *message) ReceiptHandle() string {
	return m.receiptHandle
}

func (m *message) SetAckFunc(f func(ctx context.Context, messageID, groupID, receiptHandle string)) {
	m.ack = f
}

func (m *message) Ack(ctx context.Context) {
	m.processingLock.Lock()
	defer m.processingLock.Unlock()

	// The ack func must be set for *all* messages, and it must only be called exactly once.
	if m.ack == nil {
		panic("Ack function has not been set, or has already been called")
	}

	m.ack(ctx, m.messageID, m.groupID, m.receiptHandle)

	// Clear the ack and extend functions to prevent them from being called again.
	m.ack = nil
	m.extend = nil
}

func (m *message) IsAcked() bool {
	return m.ack == nil
}

func (m *message) SetExtendFunc(f func(ctx context.Context, messageID, groupID, receiptHandle string)) {
	m.extend = f
}

func (m *message) NeedsExtension() bool {
	return m.extend != nil && time.Since(m.receiveTimestamp) > m.visibilityTimeout/2
}

func (m *message) Extend(ctx context.Context, logger common.Logger) {
	m.processingLock.Lock()
	defer m.processingLock.Unlock()

	// Check that the extend function is still set. If it is not, it means that the alert has already been acked (or that we have given up).
	if m.extend == nil {
		return
	}

	if m.extendCount == 5 {
		logger.Errorf("SQS message %s has been extended 5 times - giving up", m.messageID)
		m.extend = nil
		return
	}

	// Reset the receive timestamp to prevent the message from being extended again too soon.
	m.receiveTimestamp = time.Now()

	m.extend(ctx, m.messageID, m.groupID, m.receiptHandle)

	m.extendCount++
}
