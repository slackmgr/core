package internal

import (
	"testing"

	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type debugRecordingLogger struct {
	types.NoopLogger

	debugMessages []string
}

func (l *debugRecordingLogger) Debug(msg string) {
	l.debugMessages = append(l.debugMessages, msg)
}

func (l *debugRecordingLogger) Debugf(_ string, _ ...any) {
	panic("Debugf must not be used: Slack SDK messages are not format strings")
}

func TestSlackAPILogger_Output(t *testing.T) {
	t.Parallel()

	logger := &debugRecordingLogger{}
	l := &slackApilogger{logger: logger}

	msg := "rate limited, retry in 100%s %d"

	require.NoError(t, l.Output(2, msg))
	assert.Equal(t, []string{msg}, logger.debugMessages)
}
