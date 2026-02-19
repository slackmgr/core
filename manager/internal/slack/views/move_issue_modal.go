package views

import (
	"embed"
	"encoding/json"
	"io"

	"github.com/slack-go/slack"
	"github.com/slackmgr/core/config"
)

//go:embed move_issue_modal_assets/*
var moveIssueAssets embed.FS

type moveIssueViewArgs struct {
	AppFriendlyName string
}

func MoveIssueModal(settings *config.ManagerSettings) (slack.Blocks, error) {
	templateArgs := moveIssueViewArgs{
		AppFriendlyName: settings.AppFriendlyName,
	}

	tpl, err := renderTemplate(moveIssueAssets, "move_issue_modal_assets/move_issue.json", templateArgs)
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
