package internal

import (
	"maps"
	"sync"

	"github.com/slackmgr/types"
)

// UniqueFieldsLogger is a [types.Logger] wrapper that guarantees unique field keys in each log line.
//
// Logger implementations such as zerolog append fields, so adding the same key twice (e.g. a channel logger
// that already has channel_id, extended with the fields of an issue) results in duplicate JSON keys.
// UniqueFieldsLogger collects the fields itself, where a later value replaces an earlier one with the same key,
// and passes them to the wrapped logger once, when the first log line is written.
type UniqueFieldsLogger struct {
	base     types.Logger
	fields   map[string]any
	once     sync.Once
	resolved types.Logger
}

// NewUniqueFieldsLogger wraps logger in a [UniqueFieldsLogger]. It returns nil if logger is nil, and logger itself
// if it is already a [UniqueFieldsLogger].
func NewUniqueFieldsLogger(logger types.Logger) types.Logger { //nolint:ireturn
	if logger == nil {
		return nil
	}

	if l, ok := logger.(*UniqueFieldsLogger); ok {
		return l
	}

	return &UniqueFieldsLogger{base: logger}
}

// Debug logs a message at debug level.
func (l *UniqueFieldsLogger) Debug(msg string) {
	l.logger().Debug(msg)
}

// Debugf logs a formatted message at debug level.
func (l *UniqueFieldsLogger) Debugf(format string, args ...any) {
	l.logger().Debugf(format, args...)
}

// Info logs a message at info level.
func (l *UniqueFieldsLogger) Info(msg string) {
	l.logger().Info(msg)
}

// Infof logs a formatted message at info level.
func (l *UniqueFieldsLogger) Infof(format string, args ...any) {
	l.logger().Infof(format, args...)
}

// Error logs a message at error level.
func (l *UniqueFieldsLogger) Error(msg string) {
	l.logger().Error(msg)
}

// Errorf logs a formatted message at error level.
func (l *UniqueFieldsLogger) Errorf(format string, args ...any) {
	l.logger().Errorf(format, args...)
}

// WithField returns a new logger with the field added. An existing field with the same key is replaced.
func (l *UniqueFieldsLogger) WithField(key string, value any) types.Logger { //nolint:ireturn
	fields := make(map[string]any, len(l.fields)+1)
	maps.Copy(fields, l.fields)
	fields[key] = value

	return &UniqueFieldsLogger{base: l.base, fields: fields}
}

// WithFields returns a new logger with the fields added. Existing fields with the same keys are replaced.
func (l *UniqueFieldsLogger) WithFields(fields map[string]any) types.Logger { //nolint:ireturn
	if len(fields) == 0 {
		return l
	}

	merged := make(map[string]any, len(l.fields)+len(fields))
	maps.Copy(merged, l.fields)
	maps.Copy(merged, fields)

	return &UniqueFieldsLogger{base: l.base, fields: merged}
}

// logger returns the wrapped logger with all fields applied. It is created once, on first use.
func (l *UniqueFieldsLogger) logger() types.Logger { //nolint:ireturn
	l.once.Do(func() {
		if len(l.fields) == 0 {
			l.resolved = l.base
		} else {
			l.resolved = l.base.WithFields(l.fields)
		}
	})

	return l.resolved
}
