package views

import (
	"embed"
	"encoding/json"
	"io"

	"github.com/slack-go/slack"
	"github.com/slackmgr/core/config"
)

//go:embed create_issue_modal_assets/*
var createIssueAssets embed.FS

type createIssueViewArgs struct {
	AppFriendlyName string
}

func CreateIssueModal(settings *config.ManagerSettings) (slack.Blocks, error) {
	templateArgs := createIssueViewArgs{
		AppFriendlyName: settings.AppFriendlyName,
	}

	tpl, err := renderTemplate(createIssueAssets, "create_issue_modal_assets/create_issue.json", templateArgs)
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
