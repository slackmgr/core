package manager

import (
	"context"
	"fmt"
	"sync"
	"time"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/manager/internal/models"
)

func messageExtender(ctx context.Context, sourceCh <-chan models.Message, logger common.Logger) error {
	logger.Debug("messageExtender started")
	defer logger.Debug("messageExtender exited")

	interval := 10 * time.Second
	inFlightMessages := make(map[string]models.Message)
	timeout := time.After(interval)

	logger.Infof("Starting message extender with interval %v", interval)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			processInFlightMessages(ctx, inFlightMessages, logger)
			timeout = time.After(interval)
		case msg, ok := <-sourceCh:
			if !ok {
				return nil
			}

			inFlightMessages[msg.MessageID()] = msg
		}
	}
}

func processInFlightMessages(ctx context.Context, messages map[string]models.Message, logger common.Logger) {
	logger.WithField("count", len(messages)).Debug("Starting in-flight message processing")

	started := time.Now()
	inNeedOfExtension := []models.Message{}

	for _, msg := range messages {
		if msg.IsAcked() {
			logger.WithField("message_id", msg.MessageID()).Debug("Removing acked message from list of in-flight messages")
			delete(messages, msg.MessageID())
			continue
		}

		if msg.NeedsExtension() {
			inNeedOfExtension = append(inNeedOfExtension, msg)
		}
	}

	logger = logger.WithField("count", len(messages)).WithField("extended_count", len(inNeedOfExtension))

	if len(inNeedOfExtension) == 0 {
		logger.WithField("duration", fmt.Sprintf("%v", time.Since(started))).Info("Completed in-flight message processing")
		return
	}

	var wg sync.WaitGroup

	for _, _msg := range inNeedOfExtension {
		msg := _msg

		wg.Add(1)

		go func() {
			defer wg.Done()

			if msg.ExtendCount() >= 5 {
				logger.Errorf("Message %s has been extended at least 5 times - giving up", msg.MessageID())
				msg.SetExtendFunc(nil)
			}

			if err := msg.Extend(ctx); err != nil {
				logger.Errorf("Failed to extend message %s", msg.MessageID())
				return
			}
		}()
	}

	wg.Wait()

	logger.WithField("duration", fmt.Sprintf("%v", time.Since(started))).Info("Completed in-flight message processing")
}
