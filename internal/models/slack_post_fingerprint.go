package models

// SlackPostFingerprint identifies the content of a Slack post, split by how important a change is to the reader.
// It is used to decide how soon a post update must be sent (see the Slack client update throttling).
//
// The alert statistics in the post footer (last alert time and alert count) are part of neither hash, since they
// change with every alert. A post update where both hashes are unchanged only refreshes those statistics.
type SlackPostFingerprint struct {
	// Significant is a hash of the post parts where a change must be shown immediately: the status (action and
	// severity), header, buttons, webhook post-click status, user actions (resolved, investigated, muted) and the
	// sender name and icon.
	Significant string `json:"significant,omitempty"`

	// Content is a hash of the alert content: text, fields, author, host, link, footer and fallback text.
	Content string `json:"content,omitempty"`
}

// IsZero reports whether the fingerprint is unknown, e.g. for a post created before fingerprints were introduced,
// or an adopted post. An unknown fingerprint never matches another fingerprint.
func (f SlackPostFingerprint) IsZero() bool {
	return f.Significant == "" || f.Content == ""
}
