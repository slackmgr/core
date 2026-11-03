package models_test

import (
	"testing"

	"github.com/slackmgr/core/internal/models"
	"github.com/stretchr/testify/assert"
)

func TestSlackPostFingerprint_IsZero(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		fingerprint models.SlackPostFingerprint
		want        bool
	}{
		{name: "empty", fingerprint: models.SlackPostFingerprint{}, want: true},
		{name: "significant only", fingerprint: models.SlackPostFingerprint{Significant: "s"}, want: true},
		{name: "content only", fingerprint: models.SlackPostFingerprint{Content: "c"}, want: true},
		{name: "complete", fingerprint: models.SlackPostFingerprint{Significant: "s", Content: "c"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.fingerprint.IsZero())
		})
	}
}
