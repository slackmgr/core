package common

import "io"

type Logger interface {
	Debug(msg string)
	Debugf(format string, args ...interface{})
	Info(msg string)
	Infof(format string, args ...interface{})
	Errorf(format string, args ...interface{})
	WithField(key string, value interface{}) Logger
	HttpLoggingHandler() io.Writer
}
