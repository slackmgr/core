package manager

import (
	"context"
	"fmt"
	"sync"
	"time"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/peteraglen/slack-manager/config"
	"github.com/peteraglen/slack-manager/manager/internal/models"
	"golang.org/x/sync/semaphore"
)

func messageExtender(ctx context.Context, sourceCh <-chan models.Message, logger common.Logger, cfg *config.ManagerConfig) error {
	logger.Debug("messageExtender started")
	defer logger.Debug("messageExtender exited")

	interval := cfg.MessageExtensionProcessInterval
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

			// Ignore messages that can't be extended.
			if !msg.IsExtendable() {
				continue
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
		if !msg.IsExtendable() {
			logger.WithField("message_id", msg.MessageID()).Debug("Removing non-extendable message from list of in-flight messages")
			delete(messages, msg.MessageID())
			continue
		}

		if msg.IsAcked() {
			logger.WithField("message_id", msg.MessageID()).Debug("Removing acked message from list of in-flight messages")
			delete(messages, msg.MessageID())
			continue
		}

		if msg.IsFailed() {
			logger.WithField("message_id", msg.MessageID()).Debug("Removing failed message from list of in-flight messages")
			delete(messages, msg.MessageID())
			continue
		}

		if msg.NeedsExtensionNow() {
			inNeedOfExtension = append(inNeedOfExtension, msg)
		}
	}

	logger = logger.WithField("count", len(messages)).WithField("extended_count", len(inNeedOfExtension))

	if len(inNeedOfExtension) == 0 {
		logger.WithField("duration", fmt.Sprintf("%v", time.Since(started))).Info("Completed in-flight message processing")
		return
	}

	// If less than 4 messages need to be extended, do it sequentially.
	// Otherwise, do it concurrently with at most 3 tasks running at the same time.
	if len(inNeedOfExtension) < 4 {
		for _, msg := range inNeedOfExtension {
			extend(ctx, msg, logger)
		}
	} else {
		wg := sync.WaitGroup{}
		sem := semaphore.NewWeighted(3)

		for _, _msg := range inNeedOfExtension {
			msg := _msg
			wg.Add(1)

			go func() {
				defer wg.Done()

				if err := sem.Acquire(ctx, 1); err != nil {
					return
				}
				defer sem.Release(1)

				extend(ctx, msg, logger)
			}()
		}

		wg.Wait()
	}

	logger.WithField("duration", fmt.Sprintf("%v", time.Since(started))).Info("Completed in-flight message processing")
}

func extend(ctx context.Context, msg models.Message, logger common.Logger) {
	if msg.ExtendCount() >= 5 {
		logger.Errorf("Message %s has been extended at least 5 times - giving up", msg.MessageID())
		msg.SetExtendVisibilityFunc(nil)
		return
	}

	if err := msg.ExtendVisibility(ctx); err != nil {
		logger.Errorf("Failed to extend visibility for message %s", msg.MessageID())
	}
}
