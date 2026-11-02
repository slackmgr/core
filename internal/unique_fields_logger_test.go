package internal_test

import (
	"sync"
	"testing"

	"github.com/slackmgr/core/internal"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fieldsLogger is a types.Logger that appends fields like zerolog does, and records the fields of each log line.
type fieldsLogger struct {
	fields [][2]any
	lines  *[]logLine
	mu     *sync.Mutex
}

type logLine struct {
	level  string
	msg    string
	fields [][2]any
}

func newFieldsLogger() *fieldsLogger {
	return &fieldsLogger{lines: &[]logLine{}, mu: &sync.Mutex{}}
}

func (l *fieldsLogger) Debug(msg string)               { l.log("debug", msg) }
func (l *fieldsLogger) Debugf(format string, _ ...any) { l.log("debug", format) }
func (l *fieldsLogger) Info(msg string)                { l.log("info", msg) }
func (l *fieldsLogger) Infof(format string, _ ...any)  { l.log("info", format) }
func (l *fieldsLogger) Error(msg string)               { l.log("error", msg) }
func (l *fieldsLogger) Errorf(format string, _ ...any) { l.log("error", format) }
func (l *fieldsLogger) WithField(k string, v any) types.Logger { //nolint:ireturn
	return &fieldsLogger{fields: append(append([][2]any{}, l.fields...), [2]any{k, v}), lines: l.lines, mu: l.mu}
}

func (l *fieldsLogger) WithFields(fields map[string]any) types.Logger { //nolint:ireturn
	out := append([][2]any{}, l.fields...)
	for k, v := range fields {
		out = append(out, [2]any{k, v})
	}

	return &fieldsLogger{fields: out, lines: l.lines, mu: l.mu}
}

func (l *fieldsLogger) log(level, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	*l.lines = append(*l.lines, logLine{level: level, msg: msg, fields: l.fields})
}

func (l *fieldsLogger) lastLine(t *testing.T) logLine {
	t.Helper()

	l.mu.Lock()
	defer l.mu.Unlock()

	require.NotEmpty(t, *l.lines)

	return (*l.lines)[len(*l.lines)-1]
}

func fieldMap(t *testing.T, fields [][2]any) map[string]any {
	t.Helper()

	m := make(map[string]any, len(fields))

	for _, f := range fields {
		key, ok := f[0].(string)
		require.True(t, ok)
		_, dup := m[key]
		require.False(t, dup, "duplicate field key %q", key)
		m[key] = f[1]
	}

	return m
}

func TestNewUniqueFieldsLogger(t *testing.T) {
	t.Parallel()

	assert.Nil(t, internal.NewUniqueFieldsLogger(nil))

	logger := internal.NewUniqueFieldsLogger(newFieldsLogger())
	assert.Same(t, logger, internal.NewUniqueFieldsLogger(logger), "wrapping twice must return the same logger")
}

func TestUniqueFieldsLogger_NoDuplicateKeys(t *testing.T) {
	t.Parallel()

	base := newFieldsLogger()

	logger := internal.NewUniqueFieldsLogger(base).
		WithField("channel_id", "C1").
		WithFields(map[string]any{"channel_id": "C2", "channel_name": "alerts", "correlation_id": "a"}).
		WithFields(map[string]any{"channel_id": "C3", "correlation_id": "b", "slack_post_id": "123"}).
		WithField("slack_post_id", "456")

	logger.Info("Issue created")

	line := base.lastLine(t)
	assert.Equal(t, "info", line.level)
	assert.Equal(t, map[string]any{
		"channel_id":     "C3",
		"channel_name":   "alerts",
		"correlation_id": "b",
		"slack_post_id":  "456",
	}, fieldMap(t, line.fields))
}

func TestUniqueFieldsLogger_ParentUnchanged(t *testing.T) {
	t.Parallel()

	base := newFieldsLogger()
	parent := internal.NewUniqueFieldsLogger(base).WithField("a", 1)
	input := map[string]any{"b": 2}

	child := parent.WithFields(input)
	input["c"] = 3

	parent.Info("parent")
	assert.Equal(t, map[string]any{"a": 1}, fieldMap(t, base.lastLine(t).fields))

	child.Info("child")
	assert.Equal(t, map[string]any{"a": 1, "b": 2}, fieldMap(t, base.lastLine(t).fields))

	assert.Same(t, parent, parent.WithFields(nil))
}

func TestUniqueFieldsLogger_Levels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		log       func(types.Logger)
		wantLevel string
	}{
		{"debug", func(l types.Logger) { l.Debug("m") }, "debug"},
		{"debugf", func(l types.Logger) { l.Debugf("m") }, "debug"},
		{"info", func(l types.Logger) { l.Info("m") }, "info"},
		{"infof", func(l types.Logger) { l.Infof("m") }, "info"},
		{"error", func(l types.Logger) { l.Error("m") }, "error"},
		{"errorf", func(l types.Logger) { l.Errorf("m") }, "error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base := newFieldsLogger()
			tt.log(internal.NewUniqueFieldsLogger(base))

			line := base.lastLine(t)
			assert.Equal(t, tt.wantLevel, line.level)
			assert.Equal(t, "m", line.msg)
			assert.Empty(t, line.fields)
		})
	}
}

func TestUniqueFieldsLogger_ConcurrentUse(t *testing.T) {
	t.Parallel()

	base := newFieldsLogger()
	logger := internal.NewUniqueFieldsLogger(base).WithField("a", 1)

	var wg sync.WaitGroup

	for range 10 {
		wg.Go(func() {
			logger.Info("m")
			logger.WithField("a", 2).Info("m")
		})
	}

	wg.Wait()

	base.mu.Lock()
	defer base.mu.Unlock()

	assert.Len(t, *base.lines, 20)

	for _, line := range *base.lines {
		assert.Len(t, fieldMap(t, line.fields), 1)
	}
}
