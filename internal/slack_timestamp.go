package internal

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// FormatSlackTimestamp formats t as a Slack message timestamp, e.g. "1700000000.123456".
// Slack timestamps have a fixed number of decimals (and of seconds digits until the year 2286),
// so timestamps in this format compare correctly as strings.
func FormatSlackTimestamp(t time.Time) string {
	return fmt.Sprintf("%d.%06d", t.Unix(), t.Nanosecond()/1000)
}

// ParseSlackTimestamp parses a Slack message timestamp, e.g. "1700000000.123456". See [FormatSlackTimestamp].
// Decimals beyond microseconds are ignored, and a missing fraction is treated as zero.
func ParseSlackTimestamp(ts string) (time.Time, error) {
	secs, frac, _ := strings.Cut(ts, ".")

	sec, err := strconv.ParseInt(secs, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid Slack timestamp %q: %w", ts, err)
	}

	usec, err := strconv.ParseInt((frac + "000000")[:6], 10, 64)
	if err != nil || usec < 0 {
		return time.Time{}, fmt.Errorf("invalid Slack timestamp %q: invalid fraction", ts)
	}

	return time.Unix(sec, usec*1000).UTC(), nil
}
