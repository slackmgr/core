package internal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatSlackTimestamp(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "1700000000.123456", FormatSlackTimestamp(time.Unix(1700000000, 123456789)))
	assert.Equal(t, "1700000000.000001", FormatSlackTimestamp(time.Unix(1700000000, 1000)))
	assert.Equal(t, "1700000000.000000", FormatSlackTimestamp(time.Unix(1700000000, 0)))

	// Timestamps compare correctly as strings
	assert.Less(t, FormatSlackTimestamp(time.Unix(1700000000, 999999000)), FormatSlackTimestamp(time.Unix(1700000001, 0)))
}

func TestParseSlackTimestamp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ts      string
		want    time.Time
		wantErr bool
	}{
		{ts: "1700000000.123456", want: time.Unix(1700000000, 123456000).UTC()},
		{ts: "1700000000.1234567", want: time.Unix(1700000000, 123456000).UTC()},
		{ts: "1700000000.1", want: time.Unix(1700000000, 100000000).UTC()},
		{ts: "1700000000", want: time.Unix(1700000000, 0).UTC()},
		{ts: "invalid", wantErr: true},
		{ts: "", wantErr: true},
		{ts: "1700000000.abc", wantErr: true},
		{ts: "1700000000.-12345", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.ts, func(t *testing.T) {
			t.Parallel()

			got, err := ParseSlackTimestamp(tt.ts)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	// Round trip
	now := time.Now()
	got, err := ParseSlackTimestamp(FormatSlackTimestamp(now))
	require.NoError(t, err)
	assert.Equal(t, now.Truncate(time.Microsecond).UTC(), got)
}
