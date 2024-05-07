package views

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/peteraglen/slack-manager/lib/core/config"
	"github.com/peteraglen/slack-manager/lib/core/models"
	"github.com/slack-go/slack"
)

//go:embed issue_details_modal_assets/*
var issueDetailsAssets embed.FS

type issueDetailsArgs struct {
	CorrelationID             string
	DatabaseID                string
	Created                   string
	LastUpdated               string
	AlertCount                int
	Status                    string
	AutoResolve               string
	AutoResolveAsInconclusive string
	Resolved                  string
	Archived                  string
	ArchiveDelay              string
	ArchiveTime               string
	CurrentChannel            string
	OriginalChannel           string
	Escalated                 string
	Moved                     string
	RouteKey                  string
}

func IssueDetailsAssets(issue *models.Issue, conf *config.Config) (slack.Blocks, error) {
	autoResolve := fmt.Sprintf("%v", issue.FollowUpEnabled())

	if issue.FollowUpEnabled() {
		autoResolve += fmt.Sprintf(" (%s after last alert)", formatDuration(issue.AutoResolvePeriod))
	}

	resolved := "false"

	if issue.IsResolved() {
		resolved = fmt.Sprintf("true (%s)", formatTimestamp(issue.ResolveTime, conf))
	}

	a := issueDetailsArgs{
		CorrelationID:             issue.CorrelationID,
		DatabaseID:                issue.ID,
		Created:                   formatTimestamp(issue.Created, conf),
		LastUpdated:               formatTimestamp(issue.LastAlertReceived, conf),
		AlertCount:                issue.AlertCount,
		Status:                    string(issue.LastAlert.Severity),
		AutoResolve:               autoResolve,
		AutoResolveAsInconclusive: fmt.Sprintf("%v", issue.LastAlert.AutoResolveAsInconclusive),
		Resolved:                  resolved,
		Archived:                  fmt.Sprintf("%v", issue.Archived),
		ArchiveDelay:              formatDuration(issue.ArchiveDelay),
		ArchiveTime:               issue.ArchiveTime.Format(time.RFC3339),
		CurrentChannel:            issue.LastAlert.SlackChannelID,
		OriginalChannel:           issue.OriginalSlackChannelID(),
		Escalated:                 fmt.Sprintf("%v", issue.IsEscalated),
		Moved:                     fmt.Sprintf("%v", issue.IsMoved),
		RouteKey:                  issue.LastAlert.RouteKey,
	}

	if a.RouteKey == "" {
		a.RouteKey = "-"
	}

	tpl, err := renderTemplate(issueDetailsAssets, "issue_details_modal_assets/issue_details.json", a)
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

func formatTimestamp(t time.Time, conf *config.Config) string {
	s := t.In(conf.Location).Format("2006-01-02T15:04:05")

	diff := time.Since(t)

	switch {
	case diff > 48*time.Hour:
		s += fmt.Sprintf(" (%d days ago)", int(diff.Hours())/24)
	case diff > time.Hour:
		s += fmt.Sprintf(" (%d hours ago)", int(diff.Hours()))
	case diff > time.Minute:
		s += fmt.Sprintf(" (%d minutes ago)", int(diff.Minutes()))
	case diff > 5*time.Second:
		s += fmt.Sprintf(" (%d seconds ago)", int(diff.Seconds()))
	default:
		s += " (just now)"
	}

	return s
}

func formatDuration(d time.Duration) string {
	switch {
	case d > 48*time.Hour:
		return fmt.Sprintf("%d days", int(d.Hours())/24)
	case d > time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	case d == time.Hour:
		return "1 hour"
	case d > time.Minute:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	case d == time.Minute:
		return "1 minute"
	}

	return fmt.Sprintf("%d seconds", int(d.Seconds()))
}
