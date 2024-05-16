package views

import (
	"embed"
	"encoding/json"
	"io"

	"github.com/slack-go/slack"
)

//go:embed move_issue_modal_assets/*
var moveIssueAssets embed.FS

func MoveIssueModal() (slack.Blocks, error) {
	tpl, err := renderTemplate(moveIssueAssets, "move_issue_modal_assets/move_issue.json", struct{}{})
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
