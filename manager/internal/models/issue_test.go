package models

import (
	"testing"

	common "github.com/peteraglen/slack-manager-common"
	"github.com/stretchr/testify/assert"
)

func TestSlackMentionHandling(t *testing.T) {
	// Texts without mentions are OK
	issue := Issue{
		LastAlert: &Alert{
			Alert: common.Alert{
				Text: "abc everyone hei",
			},
		},
	}
	issue.sanitizeSlackMentions(false)
	assert.Equal(t, "abc everyone hei", issue.LastAlert.Text)

	// Mentions are only allowed in the text field
	issue = Issue{
		LastAlert: &Alert{
			Alert: common.Alert{
				Header: "hei <@a>",
				Author: "hei <@b>",
				Host:   "hei <@c>",
				Footer: "hei <@d>",
				Text:   "hei <@e>",
				Fields: []*common.Field{
					{
						Title: "hei <!here>",
						Value: "hei <@there>",
					},
				},
			},
		},
	}
	issue.sanitizeSlackMentions(false)
	assert.Equal(t, "hei *a*", issue.LastAlert.Header)
	assert.Equal(t, "hei *b*", issue.LastAlert.Author)
	assert.Equal(t, "hei *c*", issue.LastAlert.Host)
	assert.Equal(t, "hei *d*", issue.LastAlert.Footer)
	assert.Equal(t, "hei <@e>", issue.LastAlert.Text)
	assert.Equal(t, "hei *here*", issue.LastAlert.Fields[0].Title)
	assert.Equal(t, "hei *there*", issue.LastAlert.Fields[0].Value)

	// <!everyone> is not allowed in the text field
	issue = Issue{
		LastAlert: &Alert{
			Alert: common.Alert{
				Text: "abc <!everyone> hei <@everyone>",
			},
		},
	}
	issue.sanitizeSlackMentions(false)
	assert.Equal(t, "abc *everyone* hei *everyone*", issue.LastAlert.Text)

	// Mentions are allowed only once per hour
	issue = Issue{
		LastAlert: &Alert{
			Alert: common.Alert{
				Text: "abc <!here> hei <@bar>",
			},
		},
	}
	issue.sanitizeSlackMentions(false)
	assert.Equal(t, "abc <!here> hei <@bar>", issue.LastAlert.Text)
	issue.sanitizeSlackMentions(false)
	assert.Equal(t, "abc *here* hei *bar*", issue.LastAlert.Text)

	// Mentions are allowed more often if updated between alerts
	issue = Issue{
		LastAlert: &Alert{
			Alert: common.Alert{
				Text: "abc <!here> hei <@bar>",
			},
		},
	}
	issue.sanitizeSlackMentions(false)
	assert.Equal(t, "abc <!here> hei <@bar>", issue.LastAlert.Text)
	issue.LastAlert.Text = "abc <!channel> hei <@bar>"
	issue.sanitizeSlackMentions(false)
	assert.Equal(t, "abc <!channel> hei <@bar>", issue.LastAlert.Text)
}
