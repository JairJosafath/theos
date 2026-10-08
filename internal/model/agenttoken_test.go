package model

import (
	"testing"
	"time"
)

func TestOldTokensUnused(t *testing.T) {
	since := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		sandboxes []time.Time
		want      bool
	}{
		{"no sandbox", nil, true},
		{"all started after", []time.Time{since.Add(time.Second), since.Add(time.Hour)}, true},
		{"one started before", []time.Time{since.Add(time.Hour), since.Add(-time.Minute)}, false},
		{"same second", []time.Time{since}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := OldTokensUnused(since, tt.sandboxes); got != tt.want {
				t.Errorf("OldTokensUnused() = %v, want %v", got, tt.want)
			}
		})
	}
}
