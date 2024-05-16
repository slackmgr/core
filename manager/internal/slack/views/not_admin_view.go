package views

import (
	"embed"
	"encoding/json"
	"io"

	"github.com/peteraglen/slack-manager/config"
	"github.com/slack-go/slack"
)

//go:embed not_admin_view_assets/*
var notAdminAssets embed.FS

func NotAdminView(user string, conf *config.ManagerConfig) ([]slack.Block, error) {
	type args struct {
		User    string
		DocsURL string
	}

	tpl, err := renderTemplate(notAdminAssets, "not_admin_view_assets/not_admin.json", args{User: user, DocsURL: conf.DocsURL})
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
