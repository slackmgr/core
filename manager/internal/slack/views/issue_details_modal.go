package views

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/slack-go/slack"
	"github.com/slackmgr/core/config"
	"github.com/slackmgr/core/manager/internal/models"
)

//go:embed issue_details_modal_assets/*
var issueDetailsAssets embed.FS

type issueDetailsArgs struct {
	CorrelationID             string
	DatabaseID                string
	SlackPostID               string
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
	IsMoved                   string
	MoveReason                string
	RouteKey                  string
}

func IssueDetailsAssets(issue *models.Issue, cfg *config.ManagerConfig) (slack.Blocks, error) {
	autoResolve := strconv.FormatBool(issue.FollowUpEnabled())

	if issue.FollowUpEnabled() {
		autoResolve += fmt.Sprintf(" (%s after last alert)", formatDuration(issue.AutoResolvePeriod))
	}

	resolved := "false"

	if issue.IsResolved() {
		resolved = fmt.Sprintf("true (%s)", formatTimestamp(issue.ResolveTime, cfg))
	}

	a := issueDetailsArgs{
		CorrelationID:             issue.CorrelationID,
		DatabaseID:                issue.ID,
		SlackPostID:               issue.SlackPostID,
		Created:                   formatTimestamp(issue.Created, cfg),
		LastUpdated:               formatTimestamp(issue.LastAlertReceived, cfg),
		AlertCount:                issue.AlertCount,
		Status:                    string(issue.LastAlert.Severity),
		AutoResolve:               autoResolve,
		AutoResolveAsInconclusive: strconv.FormatBool(issue.LastAlert.AutoResolveAsInconclusive),
		Resolved:                  resolved,
		Archived:                  strconv.FormatBool(issue.Archived),
		ArchiveDelay:              formatDuration(issue.ArchiveDelay),
		ArchiveTime:               issue.ArchiveTime.Format(time.RFC3339),
		CurrentChannel:            issue.LastAlert.SlackChannelID,
		OriginalChannel:           issue.LastAlert.OriginalSlackChannelID,
		Escalated:                 strconv.FormatBool(issue.IsEscalated),
		IsMoved:                   strconv.FormatBool(issue.IsMoved),
		MoveReason:                "-",
		RouteKey:                  issue.LastAlert.RouteKey,
	}

	if issue.IsMoved {
		switch issue.MoveReason {
		case models.MoveIssueReasonEscalation:
			a.MoveReason = "Escalation"
		case models.MoveIssueReasonUserCommand:
			a.MoveReason = fmt.Sprintf("User command (%s)", issue.MovedByUser)
		default:
			a.MoveReason = string(issue.MoveReason)
		}
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

func formatTimestamp(t time.Time, cfg *config.ManagerConfig) string {
	s := t.In(cfg.Location).Format("2006-01-02T15:04:05")

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
