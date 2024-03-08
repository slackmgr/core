package slackapi

type Logger interface {
	Debugf(format string, args ...interface{})
	Infof(format string, args ...interface{})
	Errorf(format string, args ...interface{})
	WithField(key string, value interface{}) Logger
}

type slackApilogger struct {
	logger Logger
}

func (l *slackApilogger) Output(_ int, msg string) error {
	l.logger.Debugf(msg)
	return nil
}
