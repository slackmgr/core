package views

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"unicode/utf8"

	"github.com/slack-go/slack"
)

type infoChannelViewArgs struct {
	User string
}

// InfoChannelView renders the info-channel greeting blocks. templateContent takes
// precedence over templatePath when both are non-empty.
//
// templateContent may be a plain Go template string or a standard base64-encoded
// (RFC 4648, with padding) Go template string. Base64 encoding is detected
// automatically: if the value decodes successfully to valid UTF-8, the decoded
// form is used; otherwise the raw string is treated as a plain template.
func InfoChannelView(templateContent, templatePath, user string) ([]slack.Block, error) {
	var (
		tpl bytes.Buffer
		err error
	)

	if templateContent != "" {
		tpl, err = renderTemplateFromString(resolveTemplateContent(templateContent), infoChannelViewArgs{User: user})
	} else {
		tpl, err = renderTemplateFromPath(templatePath, infoChannelViewArgs{User: user})
	}

	if err != nil {
		return nil, err
	}

	view := slack.Msg{}

	if err := json.Unmarshal(tpl.Bytes(), &view); err != nil {
		return nil, err
	}

	// We only return the block because of the way the PostEphemeral function works
	// we are going to use slack.MsgOptionBlocks in the controller
	return view.Blocks.BlockSet, nil
}

// resolveTemplateContent returns the template string ready for rendering.
// If content is valid standard base64 (RFC 4648, with padding) that decodes to
// valid UTF-8, the decoded string is returned. Otherwise the raw value is
// returned unchanged, treating it as a plain template.
//
// JSON Block Kit templates and standard base64 use disjoint character sets, so
// plain templates are never mistakenly decoded.
func resolveTemplateContent(content string) string {
	decoded, err := base64.StdEncoding.DecodeString(content)
	if err == nil && utf8.Valid(decoded) {
		return string(decoded)
	}

	return content
}
