package models

import (
	"context"
	"sync"
	"time"

	"github.com/peteraglen/slack-manager/common"
)

type UnmarshalFunc func(queueItem *common.QueueItem) (Message, error)

type Message interface {
	MessageID() string
	SetAckFunc(f func(ctx context.Context))
	IsAcked() bool
	SetExtendFunc(f func(ctx context.Context))
	NeedsExtension() bool
	Extend(context.Context, common.Logger)
}

type message struct {
	messageID         string
	groupID           string
	receiveTimestamp  time.Time
	visibilityTimeout time.Duration

	// ack is used to delete the SQS message, i.e. ack it and declare it as processed.
	ack func(ctx context.Context)

	// extend is used to extend the visibility timeout of the SQS message. This is to prevent the ack from failing due to the visibilty timeout expiring.
	extend      func(ctx context.Context)
	extendCount int

	// processingLock is used to prevent the ack and extend functions from being called at the same time.
	processingLock *sync.Mutex
}

func newMessage(queueItem *common.QueueItem) message {
	return message{
		messageID:         queueItem.MessageID,
		groupID:           queueItem.GroupID,
		receiveTimestamp:  queueItem.ReceiveTimestamp,
		visibilityTimeout: queueItem.VisibilityTimeout,
		processingLock:    &sync.Mutex{},
	}
}

func (m *message) MessageID() string {
	return m.messageID
}

func (m *message) SetAckFunc(f func(ctx context.Context)) {
	m.ack = f
}

func (m *message) Ack(ctx context.Context) {
	m.processingLock.Lock()
	defer m.processingLock.Unlock()

	// The ack func must be set for *all* messages, and it must only be called exactly once.
	if m.ack == nil {
		panic("Ack function has not been set, or has already been called")
	}

	m.ack(ctx)

	// Clear the ack and extend functions to prevent them from being called again.
	m.ack = nil
	m.extend = nil
}

func (m *message) IsAcked() bool {
	return m.ack == nil
}

func (m *message) SetExtendFunc(f func(ctx context.Context)) {
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

	m.extend(ctx)

	m.extendCount++
}
