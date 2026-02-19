package views

import (
	"embed"
	"encoding/json"
	"io"

	"github.com/slack-go/slack"
	"github.com/slackmgr/core/config"
)

//go:embed not_admin_view_assets/*
var notAdminAssets embed.FS

type notAdminViewArgs struct {
	User            string
	DocsURL         string
	DocsURLExists   bool
	AppFriendlyName string
}

func NotAdminView(user string, settings *config.ManagerSettings) ([]slack.Block, error) {
	templateArgs := notAdminViewArgs{
		User:            user,
		DocsURL:         settings.DocsURL,
		DocsURLExists:   settings.DocsURL != "",
		AppFriendlyName: settings.AppFriendlyName,
	}

	tpl, err := renderTemplate(notAdminAssets, "not_admin_view_assets/not_admin.json", templateArgs)
	if err != nil {
		return nil, err
	}

	view := slack.Msg{}

	str, err := io.ReadAll(&tpl)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(str, &view); err != nil {
		return nil, err
	}

	// We only return the block because of the way the PostEphemeral function works
	// we are going to use slack.MsgOptionBlocks in the controller
	return view.Blocks.BlockSet, nil
}
