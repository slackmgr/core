package issues

import "github.com/peteraglen/slack-manager/common"

type restyLogger struct {
	logger common.Logger
}

func newRestyLogger(logger common.Logger) *restyLogger {
	return &restyLogger{logger: logger}
}

// Errorf logs an internal Resty error.
// We don't log internal resty errors as errors.
// If an error persists after retries, the app using the resty client will do the logging.
func (r *restyLogger) Errorf(format string, args ...interface{}) {
	r.logger.Infof(format, args...)
}

// Warnf logs an internal Resty warning.
func (r *restyLogger) Warnf(format string, args ...interface{}) {
	r.logger.Infof(format, args...)
}

// Debugf logs an internal Resty debug message.
func (r *restyLogger) Debugf(format string, args ...interface{}) {
	r.logger.Debugf(format, args...)
}
