package views_test

import (
	"encoding/base64"
	"testing"

	"github.com/slackmgr/core/manager/internal/slack/views"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// minimalTemplate is a minimal valid Slack Block Kit JSON template with a
// single section block referencing the .User template variable.
const minimalTemplate = `{"blocks":[{"type":"section","text":{"type":"mrkdwn","text":"Hello {{.User}}"}}]}`

// minimalTemplateBase64 is the standard base64 encoding of minimalTemplate.
var minimalTemplateBase64 = base64.StdEncoding.EncodeToString([]byte(minimalTemplate))

func TestInfoChannelView_TemplateContent(t *testing.T) {
	t.Parallel()

	blocks, err := views.InfoChannelView(minimalTemplate, "", "Alice")
	require.NoError(t, err)
	require.Len(t, blocks, 1)
}

func TestInfoChannelView_TemplateContentTakesPrecedenceOverPath(t *testing.T) {
	t.Parallel()

	// templatePath points to a non-existent file; templateContent should win.
	blocks, err := views.InfoChannelView(minimalTemplate, "/nonexistent/path.json", "Alice")
	require.NoError(t, err)
	require.Len(t, blocks, 1)
}

func TestInfoChannelView_FallsBackToTemplatePath(t *testing.T) {
	t.Parallel()

	// No templateContent — falls back to templatePath (which does not exist here).
	_, err := views.InfoChannelView("", "/nonexistent/path.json", "Alice")
	require.Error(t, err)
}

func TestInfoChannelView_Base64TemplateContent(t *testing.T) {
	t.Parallel()

	blocks, err := views.InfoChannelView(minimalTemplateBase64, "", "Alice")
	require.NoError(t, err)
	require.Len(t, blocks, 1)
}

func TestInfoChannelView_Base64TakesPrecedenceOverPath(t *testing.T) {
	t.Parallel()

	// Base64-encoded templateContent should win over a non-existent file path.
	blocks, err := views.InfoChannelView(minimalTemplateBase64, "/nonexistent/path.json", "Alice")
	require.NoError(t, err)
	require.Len(t, blocks, 1)
}

func TestInfoChannelView_InvalidBase64TreatedAsPlain(t *testing.T) {
	t.Parallel()

	// A string that is not valid base64 must be treated as a plain template.
	// minimalTemplate contains JSON characters ({, }, :, ") outside the base64
	// alphabet, so it falls back to plain-template rendering.
	blocks, err := views.InfoChannelView(minimalTemplate, "", "Alice")
	require.NoError(t, err)
	require.Len(t, blocks, 1)
}

func TestInfoChannelView_InvalidTemplateContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "invalid template syntax",
			content: `{"blocks": [{"text": "Hello {{.Unclosed"}]}`,
		},
		{
			name:    "invalid JSON after render",
			content: `not-json`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := views.InfoChannelView(tt.content, "", "Alice")
			assert.Error(t, err)
		})
	}
}
