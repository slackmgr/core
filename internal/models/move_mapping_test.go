package models //nolint:testpackage // Internal test to access unexported fields

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setMoveMappingTestTime sets moveMappingNowFunc to return a fixed time for testing
func setMoveMappingTestTime(t time.Time) func() {
	original := moveMappingNowFunc
	moveMappingNowFunc = func() time.Time { return t }
	return func() { moveMappingNowFunc = original }
}

func TestNewMoveMapping(t *testing.T) {
	t.Run("creates move mapping with all fields set", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setMoveMappingTestTime(fixedTime)
		defer cleanup()

		mapping := NewMoveMapping("corr-123", "C11111", "C22222", MoveIssueReasonUserCommand)

		require.NotNil(t, mapping)
		assert.NotEmpty(t, mapping.ID)
		assert.Equal(t, fixedTime, mapping.Timestamp)
		assert.Equal(t, "corr-123", mapping.CorrelationID)
		assert.Equal(t, "C11111", mapping.OriginalChannelID)
		assert.Equal(t, "C22222", mapping.TargetChannelID)
		assert.Equal(t, MoveIssueReasonUserCommand, mapping.Reason)
	})

	t.Run("generates consistent ID from inputs", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setMoveMappingTestTime(fixedTime)
		defer cleanup()

		mapping1 := NewMoveMapping("corr-123", "C11111", "C22222", MoveIssueReasonUserCommand)
		mapping2 := NewMoveMapping("corr-123", "C11111", "C33333", MoveIssueReasonEscalation)

		// Same originalChannelID and correlationID should produce same ID
		assert.Equal(t, mapping1.ID, mapping2.ID)
	})

	t.Run("different inputs produce different IDs", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setMoveMappingTestTime(fixedTime)
		defer cleanup()

		mapping1 := NewMoveMapping("corr-123", "C11111", "C22222", MoveIssueReasonUserCommand)
		mapping2 := NewMoveMapping("corr-456", "C11111", "C22222", MoveIssueReasonUserCommand)

		// Different correlationID should produce different ID
		assert.NotEqual(t, mapping1.ID, mapping2.ID)
	})

	t.Run("sets timestamp in UTC", func(t *testing.T) {
		// Use a non-UTC time to test that it gets converted to UTC
		localTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.FixedZone("EST", -5*60*60))
		cleanup := setMoveMappingTestTime(localTime)
		defer cleanup()

		mapping := NewMoveMapping("corr-123", "C11111", "C22222", MoveIssueReasonUserCommand)

		assert.Equal(t, time.UTC, mapping.Timestamp.Location())
	})

	t.Run("handles all reason types", func(t *testing.T) {
		fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		cleanup := setMoveMappingTestTime(fixedTime)
		defer cleanup()

		reasons := []MoveIssueReason{
			MoveIssueReasonUserCommand,
			MoveIssueReasonEscalation,
		}

		for _, reason := range reasons {
			mapping := NewMoveMapping("corr-123", "C11111", "C22222", reason)
			assert.Equal(t, reason, mapping.Reason)
		}
	})
}

func TestMoveMapping_ChannelID(t *testing.T) {
	t.Parallel()

	mapping := &MoveMapping{
		OriginalChannelID: "C11111",
		TargetChannelID:   "C22222",
	}

	// ChannelID returns the original channel ID
	assert.Equal(t, "C11111", mapping.ChannelID())
}

func TestMoveMapping_UniqueID(t *testing.T) {
	t.Parallel()

	mapping := &MoveMapping{
		ID: "mapping-123",
	}

	assert.Equal(t, "mapping-123", mapping.UniqueID())
}

func TestMoveMapping_GetCorrelationID(t *testing.T) {
	t.Parallel()

	mapping := &MoveMapping{
		CorrelationID: "corr-123",
	}

	assert.Equal(t, "corr-123", mapping.GetCorrelationID())
}

func TestMoveMapping_MarshalJSON(t *testing.T) {
	t.Parallel()

	t.Run("marshals to JSON correctly", func(t *testing.T) {
		t.Parallel()

		timestamp := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)

		mapping := &MoveMapping{
			ID:                "mapping-123",
			Timestamp:         timestamp,
			CorrelationID:     "corr-123",
			OriginalChannelID: "C11111",
			TargetChannelID:   "C22222",
			Reason:            MoveIssueReasonUserCommand,
		}

		data, err := mapping.MarshalJSON()

		require.NoError(t, err)
		assert.Contains(t, string(data), `"id":"mapping-123"`)
		assert.Contains(t, string(data), `"correlationId":"corr-123"`)
		assert.Contains(t, string(data), `"originalChannelId":"C11111"`)
		assert.Contains(t, string(data), `"targetChannelId":"C22222"`)
		assert.Contains(t, string(data), `"reason":"USER_COMMAND"`)
	})

	t.Run("can be unmarshaled back", func(t *testing.T) {
		t.Parallel()

		timestamp := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)

		original := &MoveMapping{
			ID:                "mapping-123",
			Timestamp:         timestamp,
			CorrelationID:     "corr-123",
			OriginalChannelID: "C11111",
			TargetChannelID:   "C22222",
			Reason:            MoveIssueReasonEscalation,
		}

		data, err := json.Marshal(original)
		require.NoError(t, err)

		var decoded MoveMapping
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, original.ID, decoded.ID)
		assert.Equal(t, original.CorrelationID, decoded.CorrelationID)
		assert.Equal(t, original.OriginalChannelID, decoded.OriginalChannelID)
		assert.Equal(t, original.TargetChannelID, decoded.TargetChannelID)
		assert.Equal(t, original.Reason, decoded.Reason)
	})
}

func TestMoveIssueReasonConstants(t *testing.T) {
	t.Parallel()

	// Verify move issue reason constants have expected values
	assert.Equal(t, MoveIssueReasonUserCommand, MoveIssueReason("USER_COMMAND"))
	assert.Equal(t, MoveIssueReasonEscalation, MoveIssueReason("ESCALATION"))
}
