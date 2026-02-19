package views

import (
	"fmt"

	"github.com/slack-go/slack"
	"github.com/slackmgr/types"
)

func WebhookInputView(webhook *types.Webhook) []slack.Block {
	blocks := []slack.Block{}

	if webhook == nil {
		return blocks
	}

	var header *slack.TextBlockObject

	if webhook.ConfirmationText != "" {
		header = slack.NewTextBlockObject(slack.MarkdownType, webhook.ConfirmationText, false, false)
	} else {
		header = slack.NewTextBlockObject(slack.PlainTextType, fmt.Sprintf("Please confirm that you wish to post the webhook to %s.", webhook.URL), false, false)
	}

	blocks = append(blocks, slack.NewSectionBlock(header, nil, nil))

	for _, input := range webhook.PlainTextInput {
		blocks = append(blocks, createStringInputBlock(input))
	}

	for _, input := range webhook.CheckboxInput {
		blocks = append(blocks, createCheckboxInputBlock(input))
	}

	return blocks
}

func createStringInputBlock(input *types.WebhookPlainTextInput) *slack.InputBlock {
	label := slack.NewTextBlockObject(slack.PlainTextType, input.Description, false, false)

	inputElement := slack.NewPlainTextInputBlockElement(nil, input.ID)
	inputElement.Multiline = input.Multiline

	if input.MinLength > 0 {
		inputElement.MinLength = input.MinLength
	}

	if input.MaxLength > 0 {
		inputElement.MaxLength = input.MaxLength
	}

	inputElement.InitialValue = input.InitialValue

	return slack.NewInputBlock(input.ID, label, nil, inputElement)
}

func createCheckboxInputBlock(input *types.WebhookCheckboxInput) *slack.InputBlock {
	label := slack.NewTextBlockObject(slack.PlainTextType, input.Label, false, false)

	checkboxGroup := slack.NewCheckboxGroupsBlockElement(input.ID)

	for _, option := range input.Options {
		optionBlock := slack.OptionBlockObject{
			Text:  slack.NewTextBlockObject(slack.PlainTextType, option.Text, false, false),
			Value: option.Value,
		}

		checkboxGroup.Options = append(checkboxGroup.Options, &optionBlock)

		if option.Selected {
			checkboxGroup.InitialOptions = append(checkboxGroup.InitialOptions, &optionBlock)
		}
	}

	block := slack.NewInputBlock(input.ID, label, nil, checkboxGroup)

	block.Optional = true

	return block
}
