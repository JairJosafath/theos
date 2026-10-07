package model

import "testing"

func TestBriefParallel(t *testing.T) {
	for _, tc := range []struct {
		name         string
		spec, status int32
		want         int32
	}{
		{"nothing says: the default", 0, 0, BriefParallelDefault},
		{"spec.parallel", 2, 0, 2},
		{"a prompt's number", 0, 4, 4},
		{"spec.parallel wins over a prompt", 1, 4, 1},
	} {
		if got := BriefParallel(tc.spec, tc.status); got != tc.want {
			t.Errorf("%s: BriefParallel(%d, %d) = %d, want %d", tc.name, tc.spec, tc.status, got, tc.want)
		}
	}
}
