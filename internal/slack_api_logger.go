package internal

import (
	common "github.com/peteraglen/slack-manager-common"
)

type slackApilogger struct {
	logger common.Logger
}

func (l *slackApilogger) Output(_ int, msg string) error {
	l.logger.Debugf(msg)
	return nil
}
