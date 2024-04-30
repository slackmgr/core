package handler

import (
	"context"

	slackapi "github.com/slack-go/slack"
)

type SocketClient interface {
	PostEphemeral(ctx context.Context, channelID, userID string, options ...slackapi.MsgOption) (string, error)
	GetUserInfo(ctx context.Context, user string) (*slackapi.User, error)
	UserIsInGroup(ctx context.Context, groupID, userID string) bool
	SendResponse(ctx context.Context, channelID, responseURL, responseType, text string) error
	OpenModal(ctx context.Context, triggerID string, request slackapi.ModalViewRequest) error
	IsAlertChannel(ctx context.Context, channelID string) (bool, string, error)
}
