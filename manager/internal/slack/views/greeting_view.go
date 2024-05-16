package views

import (
	"embed"
	"encoding/json"
	"io"

	"github.com/peteraglen/slack-manager/config"
	"github.com/slack-go/slack"
)

//go:embed greeting_view_assets/*
var greetingAssets embed.FS

func GreetingView(user string, isChannelAdmin bool, cfg *config.ManagerConfig) ([]slack.Block, error) {
	type args struct {
		User           string
		IsChannelAdmin bool
		DocsURL        string
	}

	templateArgs := args{
		User:           user,
		IsChannelAdmin: isChannelAdmin,
		DocsURL:        cfg.DocsURL,
	}

	tpl, err := renderTemplate(greetingAssets, "greeting_view_assets/greeting.json", templateArgs)
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
