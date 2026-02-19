package views

import (
	"embed"
	"encoding/json"
	"io"

	"github.com/slack-go/slack"
	"github.com/slackmgr/core/config"
)

//go:embed greeting_view_assets/*
var greetingAssets embed.FS

type greetingViewArgs struct {
	User             string
	IsChannelAdmin   bool
	DocsURL          string
	DocsURLExists    bool
	AppFriendlyName  string
	TerminateEmoji   string
	ResolveEmoji     string
	MuteEmoji        string
	InvestigateEmoji string
}

func GreetingView(user string, isChannelAdmin bool, settings *config.ManagerSettings) ([]slack.Block, error) {
	templateArgs := greetingViewArgs{
		User:             user,
		IsChannelAdmin:   isChannelAdmin,
		DocsURL:          settings.DocsURL,
		DocsURLExists:    settings.DocsURL != "",
		AppFriendlyName:  settings.AppFriendlyName,
		TerminateEmoji:   settings.IssueReactions.TerminateEmojis[0],
		ResolveEmoji:     settings.IssueReactions.ResolveEmojis[0],
		MuteEmoji:        settings.IssueReactions.MuteEmojis[0],
		InvestigateEmoji: settings.IssueReactions.InvestigateEmojis[0],
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
