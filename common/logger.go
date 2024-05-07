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
