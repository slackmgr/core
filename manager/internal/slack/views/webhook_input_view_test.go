package views_test

import (
	"testing"

	"github.com/slack-go/slack"
	"github.com/slackmgr/core/manager/internal/slack/views"
	"github.com/slackmgr/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebhookInputView_NilWebhook(t *testing.T) {
	t.Parallel()

	blocks := views.WebhookInputView(nil)

	assert.Empty(t, blocks)
}

func TestWebhookInputView_BlockCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		webhook    *types.Webhook
		wantBlocks int
	}{
		{
			name:       "no inputs → header only",
			webhook:    &types.Webhook{URL: "https://example.com"},
			wantBlocks: 1,
		},
		{
			name: "one plain text input",
			webhook: &types.Webhook{
				URL: "https://example.com",
				PlainTextInput: []*types.WebhookPlainTextInput{
					{ID: "note", Description: "Note"},
				},
			},
			wantBlocks: 2,
		},
		{
			name: "one checkbox input",
			webhook: &types.Webhook{
				URL: "https://example.com",
				CheckboxInput: []*types.WebhookCheckboxInput{
					{ID: "confirm", Label: "Options"},
				},
			},
			wantBlocks: 2,
		},
		{
			name: "mixed inputs",
			webhook: &types.Webhook{
				URL: "https://example.com",
				PlainTextInput: []*types.WebhookPlainTextInput{
					{ID: "note", Description: "Note"},
					{ID: "reason", Description: "Reason"},
				},
				CheckboxInput: []*types.WebhookCheckboxInput{
					{ID: "confirm", Label: "Confirm"},
				},
			},
			wantBlocks: 4, // 1 header + 2 text + 1 checkbox
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			blocks := views.WebhookInputView(tt.webhook)
			assert.Len(t, blocks, tt.wantBlocks)
		})
	}
}

func TestWebhookInputView_ConfirmationText(t *testing.T) {
	t.Parallel()

	t.Run("custom confirmation text uses markdown", func(t *testing.T) {
		t.Parallel()
		webhook := &types.Webhook{
			URL:              "https://example.com",
			ConfirmationText: "Are you *sure*?",
		}

		blocks := views.WebhookInputView(webhook)

		require.Len(t, blocks, 1)
		header, ok := blocks[0].(*slack.SectionBlock)
		require.True(t, ok, "expected *slack.SectionBlock")
		assert.Equal(t, slack.MarkdownType, header.Text.Type)
		assert.Equal(t, "Are you *sure*?", header.Text.Text)
	})

	t.Run("no confirmation text uses plain text with URL", func(t *testing.T) {
		t.Parallel()
		webhook := &types.Webhook{URL: "https://example.com/hook"}

		blocks := views.WebhookInputView(webhook)

		require.Len(t, blocks, 1)
		header, ok := blocks[0].(*slack.SectionBlock)
		require.True(t, ok, "expected *slack.SectionBlock")
		assert.Equal(t, slack.PlainTextType, header.Text.Type)
		assert.Contains(t, header.Text.Text, "https://example.com/hook")
	})
}

func TestWebhookInputView_PlainTextInput(t *testing.T) {
	t.Parallel()

	t.Run("ID and description are set", func(t *testing.T) {
		t.Parallel()
		webhook := &types.Webhook{
			URL: "https://example.com",
			PlainTextInput: []*types.WebhookPlainTextInput{
				{ID: "my-input", Description: "Enter a value"},
			},
		}

		blocks := views.WebhookInputView(webhook)

		require.Len(t, blocks, 2)
		inputBlock, ok := blocks[1].(*slack.InputBlock)
		require.True(t, ok, "expected *slack.InputBlock")
		assert.Equal(t, "my-input", inputBlock.BlockID)
		assert.Equal(t, "Enter a value", inputBlock.Label.Text)
	})

	t.Run("min and max length propagated", func(t *testing.T) {
		t.Parallel()
		webhook := &types.Webhook{
			URL: "https://example.com",
			PlainTextInput: []*types.WebhookPlainTextInput{
				{ID: "msg", Description: "Message", MinLength: 5, MaxLength: 100},
			},
		}

		blocks := views.WebhookInputView(webhook)

		require.Len(t, blocks, 2)
		inputBlock, ok := blocks[1].(*slack.InputBlock)
		require.True(t, ok, "expected *slack.InputBlock")
		element, ok := inputBlock.Element.(*slack.PlainTextInputBlockElement)
		require.True(t, ok, "expected *slack.PlainTextInputBlockElement")
		assert.Equal(t, 5, element.MinLength)
		assert.Equal(t, 100, element.MaxLength)
	})

	t.Run("zero min/max length not propagated", func(t *testing.T) {
		t.Parallel()
		webhook := &types.Webhook{
			URL: "https://example.com",
			PlainTextInput: []*types.WebhookPlainTextInput{
				{ID: "msg", Description: "Message", MinLength: 0, MaxLength: 0},
			},
		}

		blocks := views.WebhookInputView(webhook)

		require.Len(t, blocks, 2)
		inputBlock, ok := blocks[1].(*slack.InputBlock)
		require.True(t, ok, "expected *slack.InputBlock")
		element, ok2 := inputBlock.Element.(*slack.PlainTextInputBlockElement)
		require.True(t, ok2, "expected *slack.PlainTextInputBlockElement")
		assert.Equal(t, 0, element.MinLength)
		assert.Equal(t, 0, element.MaxLength)
	})

	t.Run("multiline and initial value propagated", func(t *testing.T) {
		t.Parallel()
		webhook := &types.Webhook{
			URL: "https://example.com",
			PlainTextInput: []*types.WebhookPlainTextInput{
				{ID: "body", Description: "Body", Multiline: true, InitialValue: "default text"},
			},
		}

		blocks := views.WebhookInputView(webhook)

		require.Len(t, blocks, 2)
		inputBlock, ok := blocks[1].(*slack.InputBlock)
		require.True(t, ok, "expected *slack.InputBlock")
		element, ok2 := inputBlock.Element.(*slack.PlainTextInputBlockElement)
		require.True(t, ok2, "expected *slack.PlainTextInputBlockElement")
		assert.True(t, element.Multiline)
		assert.Equal(t, "default text", element.InitialValue)
	})
}

func TestWebhookInputView_CheckboxInput(t *testing.T) {
	t.Parallel()

	t.Run("checkbox block is optional", func(t *testing.T) {
		t.Parallel()
		webhook := &types.Webhook{
			URL: "https://example.com",
			CheckboxInput: []*types.WebhookCheckboxInput{
				{ID: "opts", Label: "Options"},
			},
		}

		blocks := views.WebhookInputView(webhook)

		require.Len(t, blocks, 2)
		inputBlock, ok := blocks[1].(*slack.InputBlock)
		require.True(t, ok, "expected *slack.InputBlock")
		assert.True(t, inputBlock.Optional)
	})

	t.Run("all options added to checkbox group", func(t *testing.T) {
		t.Parallel()
		webhook := &types.Webhook{
			URL: "https://example.com",
			CheckboxInput: []*types.WebhookCheckboxInput{
				{
					ID:    "flags",
					Label: "Flags",
					Options: []*types.WebhookCheckboxOption{
						{Value: "a", Text: "Option A"},
						{Value: "b", Text: "Option B"},
						{Value: "c", Text: "Option C"},
					},
				},
			},
		}

		blocks := views.WebhookInputView(webhook)

		require.Len(t, blocks, 2)
		inputBlock, ok := blocks[1].(*slack.InputBlock)
		require.True(t, ok, "expected *slack.InputBlock")
		element, ok2 := inputBlock.Element.(*slack.CheckboxGroupsBlockElement)
		require.True(t, ok2, "expected *slack.CheckboxGroupsBlockElement")
		assert.Len(t, element.Options, 3)
	})

	t.Run("only selected options become initial options", func(t *testing.T) {
		t.Parallel()
		webhook := &types.Webhook{
			URL: "https://example.com",
			CheckboxInput: []*types.WebhookCheckboxInput{
				{
					ID:    "flags",
					Label: "Flags",
					Options: []*types.WebhookCheckboxOption{
						{Value: "a", Text: "Option A", Selected: true},
						{Value: "b", Text: "Option B", Selected: false},
						{Value: "c", Text: "Option C", Selected: true},
					},
				},
			},
		}

		blocks := views.WebhookInputView(webhook)

		require.Len(t, blocks, 2)
		inputBlock, ok := blocks[1].(*slack.InputBlock)
		require.True(t, ok, "expected *slack.InputBlock")
		element, ok2 := inputBlock.Element.(*slack.CheckboxGroupsBlockElement)
		require.True(t, ok2, "expected *slack.CheckboxGroupsBlockElement")
		assert.Len(t, element.Options, 3)
		require.Len(t, element.InitialOptions, 2)
		assert.Equal(t, "a", element.InitialOptions[0].Value)
		assert.Equal(t, "c", element.InitialOptions[1].Value)
	})

	t.Run("no selected options → empty initial options", func(t *testing.T) {
		t.Parallel()
		webhook := &types.Webhook{
			URL: "https://example.com",
			CheckboxInput: []*types.WebhookCheckboxInput{
				{
					ID:    "flags",
					Label: "Flags",
					Options: []*types.WebhookCheckboxOption{
						{Value: "a", Text: "Option A", Selected: false},
					},
				},
			},
		}

		blocks := views.WebhookInputView(webhook)

		require.Len(t, blocks, 2)
		inputBlock, ok := blocks[1].(*slack.InputBlock)
		require.True(t, ok, "expected *slack.InputBlock")
		element, ok2 := inputBlock.Element.(*slack.CheckboxGroupsBlockElement)
		require.True(t, ok2, "expected *slack.CheckboxGroupsBlockElement")
		assert.Empty(t, element.InitialOptions)
	})
}
