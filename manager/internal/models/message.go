package models

import (
	"context"
	"fmt"
	"sync"
	"time"

	commonlib "github.com/peteraglen/slack-manager-common"
)

type UnmarshalFunc func(queueItem *commonlib.FifoQueueItem) (Message, error)

type Message interface {
	MessageID() string
	SetAckFunc(f func(ctx context.Context) error)
	IsAcked() bool
	SetExtendFunc(f func(ctx context.Context) error)
	ExtendCount() int
	NeedsExtension() bool
	Extend(context.Context) error
}

type message struct {
	messageID         string
	groupID           string
	receiveTimestamp  time.Time
	visibilityTimeout time.Duration

	// ack is used to ack the message and declare it as processed.
	ack func(ctx context.Context) error

	// extend is used to extend the visibility timeout of the message. This is to prevent the ack from failing due to the visibilty timeout expiring.
	extend      func(ctx context.Context) error
	extendCount int

	// processingLock is used to prevent the ack and extend functions from being called at the same time.
	processingLock *sync.Mutex
}

func newMessage(queueItem *commonlib.FifoQueueItem) message {
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

func (m *message) SetAckFunc(f func(ctx context.Context) error) {
	m.ack = f
}

func (m *message) Ack(ctx context.Context) error {
	m.processingLock.Lock()
	defer m.processingLock.Unlock()

	// The ack func must be set for *all* messages, and it must only be called exactly once.
	if m.ack == nil {
		return fmt.Errorf("ack function has not been set, or has already been called")
	}

	if err := m.ack(ctx); err != nil {
		return err
	}

	// Clear the ack and extend functions to prevent them from being called again.
	m.ack = nil
	m.extend = nil

	return nil
}

func (m *message) IsAcked() bool {
	return m.ack == nil
}

func (m *message) SetExtendFunc(f func(ctx context.Context) error) {
	m.extend = f
}

func (m *message) ExtendCount() int {
	return m.extendCount
}

func (m *message) NeedsExtension() bool {
	return m.extend != nil && time.Since(m.receiveTimestamp) > m.visibilityTimeout/2
}

func (m *message) Extend(ctx context.Context) error {
	m.processingLock.Lock()
	defer m.processingLock.Unlock()

	// Check that the extend function is still set. If it is not, it means that the alert has already been acked (or that we have given up).
	if m.extend == nil {
		return nil
	}

	// Reset the receive timestamp to prevent the message from being extended again too soon.
	m.receiveTimestamp = time.Now()

	if err := m.extend(ctx); err != nil {
		return err
	}

	m.extendCount++

	return nil
}
