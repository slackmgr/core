package common

import "io"

type Logger interface {
	Debug(msg string)
	Debugf(format string, args ...interface{})
	Info(msg string)
	Infof(format string, args ...interface{})
	Error(msg string)
	Errorf(format string, args ...interface{})
	ErrorfUnlessContextCanceled(format string, args ...interface{})
	ErrorUnlessContextCanceled(err error)
	WithField(key string, value interface{}) Logger
	WithFields(fields map[string]interface{}) Logger
	HttpLoggingHandler() io.Writer
}

type RestyLogger struct {
	logger Logger
}

func NewRestyLogger(logger Logger) *RestyLogger {
	return &RestyLogger{logger: logger}
}

// Errorf logs an internal Resty error.
// We don't log internal resty errors as errors.
// If an error persists after retries, the app using the resty client will do the logging.
func (r *RestyLogger) Errorf(format string, args ...interface{}) {
	r.logger.Infof(format, args...)
}

// Warnf logs an internal Resty warning.
func (r *RestyLogger) Warnf(format string, args ...interface{}) {
	r.logger.Infof(format, args...)
}

// Debugf logs an internal Resty debug message.
func (r *RestyLogger) Debugf(format string, args ...interface{}) {
	r.logger.Debugf(format, args...)
}
