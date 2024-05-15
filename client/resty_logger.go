package client

import common "github.com/peteraglen/slack-manager-common"

type restyLogger struct {
	logger common.Logger
}

func (l *restyLogger) Debugf(format string, args ...interface{}) {
	l.logger.Debugf(format, args...)
}

func (l *restyLogger) Warnf(format string, args ...interface{}) {
	l.logger.Infof(format, args...)
}

// Errorf is called by resty when a request fails. We log as info, since the client using resty decides when errors should be logged as such.
func (l *restyLogger) Errorf(format string, args ...interface{}) {
	l.logger.Infof(format, args...)
}
