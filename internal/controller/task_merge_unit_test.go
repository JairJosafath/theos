package controller

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// A Merged Task whose merge commit can't be read tries each minute at first,
// then each hour: a pull request that is gone mustn't cost a request a minute.
func TestMergeCommitRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	at := func(ago time.Duration) *metav1.Time { m := metav1.NewTime(now.Add(-ago)); return &m }
	for _, tc := range []struct {
		name      string
		completed *metav1.Time
		want      time.Duration
	}{
		{"no completion time", nil, time.Minute},
		{"just merged", at(time.Minute), time.Minute},
		{"merged within the hour", at(59 * time.Minute), time.Minute},
		{"merged long ago", at(48 * time.Hour), time.Hour},
	} {
		if got := mergeCommitRetryAfter(tc.completed, now); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
