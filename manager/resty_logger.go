package manager

import "github.com/slackmgr/types"

type restyLogger struct {
	logger types.Logger
}

func newRestyLogger(logger types.Logger) *restyLogger {
	return &restyLogger{logger: logger}
}

// Errorf logs an internal Resty error.
// We don't log internal resty errors as errors.
// If an error persists after retries, the app using the resty client will do the logging.
func (r *restyLogger) Errorf(format string, args ...any) {
	r.logger.Infof(format, args...)
}

// Warnf logs an internal Resty warning.
func (r *restyLogger) Warnf(format string, args ...any) {
	r.logger.Infof(format, args...)
}

// Debugf logs an internal Resty debug message.
func (r *restyLogger) Debugf(format string, args ...any) {
	r.logger.Debugf(format, args...)
}
