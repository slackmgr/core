package models

import (
	"context"
	"errors"
	"sync"
	"time"

	commonlib "github.com/peteraglen/slack-manager-common"
)

type UnmarshalFunc func(queueItem *commonlib.FifoQueueItem) (Message, error)

type Message interface {
	MessageID() string
	SetAckFunc(f func(ctx context.Context) error)
	IsAcked() bool
	IsFailed() bool
	MarkAsFailed()
	SetExtendVisibilityFunc(f func(ctx context.Context) error)
	IsExtendable() bool
	ExtendCount() int
	NeedsExtensionNow() bool
	ExtendVisibility(ctx context.Context) error
}

type message struct {
	messageID         string
	receiveTimestamp  time.Time
	visibilityTimeout time.Duration

	// ackFunc is used to acknowledge the message and declare it as processed.
	// The ack function must be set for *all* messages.
	ackFunc func(ctx context.Context) error

	// extendVisibilityFunc is used to extend the visibility timeout of the message. This is to prevent the ack from failing due to the visibilty timeout expiring.
	// The extend function is optional, and is only set if the message can in fact be extended (such as for SQS messages).
	extendVisibilityFunc func(ctx context.Context) error
	extendCount          int

	// isFailed is used to indicate that the message has failed processing.
	isFailed bool

	// processingLock is used to prevent the Ack and Extend functions from being called at the same time.
	processingLock *sync.Mutex
}

func newMessage(queueItem *commonlib.FifoQueueItem) message {
	return message{
		messageID:         queueItem.MessageID,
		receiveTimestamp:  queueItem.ReceiveTimestamp,
		visibilityTimeout: queueItem.VisibilityTimeout,
		processingLock:    &sync.Mutex{},
	}
}

func (m *message) MessageID() string {
	return m.messageID
}

func (m *message) SetAckFunc(f func(ctx context.Context) error) {
	m.ackFunc = f
}

func (m *message) Ack(ctx context.Context) error {
	m.processingLock.Lock()
	defer m.processingLock.Unlock()

	// The ack func must be set for *all* messages, and it can be called at most once.
	if m.ackFunc == nil {
		return errors.New("ack function has not been set, or has already been called")
	}

	if err := m.ackFunc(ctx); err != nil {
		return err
	}

	// Clear the ack and extend functions to prevent them from being called again.
	m.ackFunc = nil
	m.extendVisibilityFunc = nil

	return nil
}

func (m *message) IsAcked() bool {
	return m.ackFunc == nil
}

func (m *message) IsFailed() bool {
	return m.isFailed
}

func (m *message) MarkAsFailed() {
	m.isFailed = true
}

func (m *message) SetExtendVisibilityFunc(f func(ctx context.Context) error) {
	m.extendVisibilityFunc = f
}

func (m *message) IsExtendable() bool {
	return m.extendVisibilityFunc != nil
}

func (m *message) ExtendCount() int {
	return m.extendCount
}

func (m *message) NeedsExtensionNow() bool {
	return m.extendVisibilityFunc != nil && time.Since(m.receiveTimestamp) > m.visibilityTimeout/2
}

func (m *message) ExtendVisibility(ctx context.Context) error {
	m.processingLock.Lock()
	defer m.processingLock.Unlock()

	// Check that the extend function is still set after we acquired the lock.
	// If it is not, it means that the alert has already been acked (or that we have given up).
	if m.extendVisibilityFunc == nil {
		return nil
	}

	// Reset the receive timestamp to prevent the message from being extended again too soon.
	m.receiveTimestamp = time.Now()

	if err := m.extendVisibilityFunc(ctx); err != nil {
		return err
	}

	m.extendCount++

	return nil
}
