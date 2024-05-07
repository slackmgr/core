package slackapi

import (
	"github.com/peteraglen/slack-manager/lib/common"
)

type slackApilogger struct {
	logger common.Logger
}

func (l *slackApilogger) Output(_ int, msg string) error {
	l.logger.Debugf(msg)
	return nil
}
