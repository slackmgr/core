package slack

import (
	"strings"
	"testing"
	"unicode/utf8"

	slackapi "github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
)

func TestNewHeaderBlock(t *testing.T) {
	t.Parallel()

	text := newPlainTextTextBlock("foo")
	foo := slackapi.NewHeaderBlock(text)
	assert.NotNil(t, foo)
}

func TestFindCutPoint(t *testing.T) {
	t.Parallel()

	// base returns a string of exactly n ASCII bytes.
	base := func(n int) string { return strings.Repeat("a", n) }

	t.Run("double newline in window", func(t *testing.T) {
		t.Parallel()
		// Place "\n\n" at byte 2600 (within [2500,2700]).
		s := base(2600) + "\n\n" + base(200)
		cut := findCutPoint(s)
		assert.Equal(t, 2602, cut) // after the two newline bytes
		assert.True(t, utf8.Valid([]byte(s[:cut])))
	})

	t.Run("single newline in window, no double newline", func(t *testing.T) {
		t.Parallel()
		s := base(2600) + "\n" + base(200)
		cut := findCutPoint(s)
		assert.Equal(t, 2601, cut)
	})

	t.Run("space in window, no newlines", func(t *testing.T) {
		t.Parallel()
		s := base(2600) + " " + base(200)
		cut := findCutPoint(s)
		assert.Equal(t, 2601, cut)
	})

	t.Run("double newline preferred over single newline", func(t *testing.T) {
		t.Parallel()
		// Single newline at 2550, double newline at 2600.
		s := base(2550) + "\n" + base(49) + "\n\n" + base(200)
		cut := findCutPoint(s)
		assert.Equal(t, 2602, cut) // double newline wins
	})

	t.Run("no natural break falls back to 2700", func(t *testing.T) {
		t.Parallel()
		s := base(3000)
		cut := findCutPoint(s)
		assert.Equal(t, 2700, cut)
	})

	t.Run("multi-byte UTF-8 straddles 2700, no breaks", func(t *testing.T) {
		t.Parallel()
		// Build a string where a 3-byte rune straddles byte 2700.
		// '€' is 3 bytes (0xE2 0x82 0xAC). Place it so its first byte is at 2699.
		prefix := base(2699)
		s := prefix + "€" + base(100) // '€' occupies bytes 2699,2700,2701
		cut := findCutPoint(s)
		// Cut must be at a valid UTF-8 boundary, so it should walk back to 2699.
		assert.Equal(t, 2699, cut)
		assert.True(t, utf8.Valid([]byte(s[:cut])))
	})
}
