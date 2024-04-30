package client

type Logger interface {
	Debugf(format string, args ...interface{})
	Infof(format string, args ...interface{})
	Errorf(format string, args ...interface{})
}

type restyLogger struct {
	logger Logger
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
