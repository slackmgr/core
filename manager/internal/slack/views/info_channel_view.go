package views

import (
	"encoding/json"
	"io"

	"github.com/slack-go/slack"
)

func InfoChannelView(templatePath, user string) ([]slack.Block, error) {
	type args struct {
		User string
	}

	tpl, err := renderTemplateFromPath(templatePath, args{User: user})
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
