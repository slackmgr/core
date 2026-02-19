package internal

import (
	"github.com/slackmgr/types"
)

type slackApilogger struct {
	logger types.Logger
}

func (l *slackApilogger) Output(_ int, msg string) error {
	l.logger.Debugf(msg)
	return nil
}
