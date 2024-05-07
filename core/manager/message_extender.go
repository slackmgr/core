package manager

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/peteraglen/slack-manager/common"
	"github.com/peteraglen/slack-manager/core/models"
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

	if len(inNeedOfExtension) < 2 {
		if len(inNeedOfExtension) == 1 {
			inNeedOfExtension[0].Extend(ctx, logger)
		}

		logger.WithField("duration", fmt.Sprintf("%v", time.Since(started))).Info("Completed in-flight message processing")

		return
	}

	var wg sync.WaitGroup

	for _, _msg := range inNeedOfExtension {
		msg := _msg

		wg.Add(1)

		go func() {
			defer wg.Done()
			msg.Extend(ctx, logger)
		}()
	}

	wg.Wait()

	logger.WithField("duration", fmt.Sprintf("%v", time.Since(started))).Info("Completed in-flight message processing")
}
