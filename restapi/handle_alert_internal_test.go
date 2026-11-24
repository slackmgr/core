package restapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAlertInputParser(t *testing.T) {
	t.Parallel()

	t.Run("array input", func(t *testing.T) {
		t.Parallel()
		input := `[{"header":"foo"}, {"header":"bar"}]`
		alerts, err := parseAlertInput([]byte(input))
		require.NoError(t, err)
		assert.Len(t, alerts, 2)
		assert.Equal(t, "foo", alerts[0].Header)
		assert.Equal(t, "bar", alerts[1].Header)
	})

	t.Run("array input with invalid json should return an error", func(t *testing.T) {
		t.Parallel()
		input := `[{"header":"foo"`
		_, err := parseAlertInput([]byte(input))
		require.Error(t, err)
	})

	t.Run("object input with all fields at root", func(t *testing.T) {
		t.Parallel()
		input := `{"header":"foo", "footer":"bar"}`
		alerts, err := parseAlertInput([]byte(input))
		require.NoError(t, err)
		assert.Len(t, alerts, 1)
		assert.Equal(t, "foo", alerts[0].Header)
		assert.Equal(t, "bar", alerts[0].Footer)
	})

	t.Run("object input with invalid json should return an error", func(t *testing.T) {
		t.Parallel()
		input := `{"header":"foo", `
		_, err := parseAlertInput([]byte(input))
		require.Error(t, err)
	})

	t.Run("object input with array of alerts", func(t *testing.T) {
		t.Parallel()
		input := `{"alerts":[{"header":"foo"}, {"header":"bar"}]}`
		alerts, err := parseAlertInput([]byte(input))
		require.NoError(t, err)
		assert.Len(t, alerts, 2)
		assert.Equal(t, "foo", alerts[0].Header)
		assert.Equal(t, "bar", alerts[1].Header)
	})

	t.Run("null alerts should return an error", func(t *testing.T) {
		t.Parallel()

		for _, input := range []string{`[null]`, `[{"header":"foo"}, null]`, `{"alerts":[null]}`, `{"alerts":[{"header":"foo"}, null]}`} {
			alerts, err := parseAlertInput([]byte(input))
			require.Error(t, err, input)
			assert.Contains(t, err.Error(), "cannot be null", input)
			assert.Nil(t, alerts, input)
		}
	})

	t.Run("null body is a single empty alert", func(t *testing.T) {
		t.Parallel()
		alerts, err := parseAlertInput([]byte(`null`))
		require.NoError(t, err)
		require.Len(t, alerts, 1)
		assert.NotNil(t, alerts[0])
	})
}
