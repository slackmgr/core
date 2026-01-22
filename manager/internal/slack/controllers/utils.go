package controllers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/peteraglen/slack-manager/manager/internal/models"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

func ack(evt *socketmode.Event, clt SocketModeClient) {
	if evt.Request != nil {
		clt.Ack(*evt.Request)
	}
}

func ackWithPayload(evt *socketmode.Event, clt SocketModeClient, payload any) {
	if evt.Request != nil {
		clt.Ack(*evt.Request, payload)
	}
}

func ackWithFieldErrorMsg(evt *socketmode.Event, clt SocketModeClient, fieldName, errMsg string) {
	errors := map[string]string{fieldName: errMsg}
	ackWithPayload(evt, clt, slack.NewErrorsViewSubmissionResponse(errors))
}

func sendCommand(ctx context.Context, fifoQueue FifoQueueProducer, cmd *models.Command) error {
	body, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("failed to marshal command: %w", err)
	}

	return fifoQueue.Send(ctx, cmd.SlackChannelID, cmd.DedupID(), string(body))
}
