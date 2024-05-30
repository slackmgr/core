package slack

import (
	"testing"

	slackapi "github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
)

func TestNewHeaderBlock(t *testing.T) {
	t.Parallel()

	text := newPlainTextTextBlock("foo")
	foo := slackapi.NewHeaderBlock(text)
	assert.NotNil(t, foo)
}
