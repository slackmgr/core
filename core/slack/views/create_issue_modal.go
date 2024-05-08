package views

import (
	"embed"
	"encoding/json"
	"io"

	"github.com/slack-go/slack"
)

//go:embed create_issue_modal_assets/*
var createIssueAssets embed.FS

func CreateIssueModal() (slack.Blocks, error) {
	tpl, err := renderTemplate(createIssueAssets, "create_issue_modal_assets/create_issue.json", struct{}{})
	if err != nil {
		return slack.Blocks{}, err
	}

	view := slack.Msg{}

	str, err := io.ReadAll(&tpl)
	if err != nil {
		return slack.Blocks{}, err
	}

	if err := json.Unmarshal(str, &view); err != nil {
		return slack.Blocks{}, err
	}

	return view.Blocks, nil
}
