package client

type RequestLogger interface {
	Errorf(format string, v ...interface{})
	Warnf(format string, v ...interface{})
	Debugf(format string, v ...interface{})
}

type NoopLogger struct{}

func (l *NoopLogger) Errorf(format string, v ...interface{}) {}
func (l *NoopLogger) Warnf(format string, v ...interface{})  {}
func (l *NoopLogger) Debugf(format string, v ...interface{}) {}
