package model

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestFullCheckGate(t *testing.T) {
	started := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	full := func(id int64, status, conclusion string) ReleaseRun {
		return ReleaseRun{ID: id, Workflow: "check.yaml", Status: status, Conclusion: conclusion,
			URL: fmt.Sprintf("http://gitea/runs/%d", id)}
	}
	tests := []struct {
		name    string
		runs    []ReleaseRun
		after   time.Duration
		want    string
		wantRun int64
	}{
		{"the full check passed: tag", []ReleaseRun{full(4, "completed", "success")}, time.Minute, FullCheckTag, 4},
		{"it failed: file", []ReleaseRun{full(4, "completed", "failure")}, time.Minute, FullCheckFile, 4},
		{"cancelled is a failure", []ReleaseRun{full(4, "completed", "cancelled")}, time.Minute, FullCheckFile, 4},
		{"a re-run that passed counts, not the failure before it",
			[]ReleaseRun{full(5, "completed", "success"), full(4, "completed", "failure")}, time.Minute,
			FullCheckTag, 5},
		{"a failure among other workflows' runs files", []ReleaseRun{
			{ID: 6, Workflow: "lint.yaml", Status: "in_progress"}, full(4, "completed", "failure")},
			time.Minute, FullCheckFile, 4},
		{"running: wait", []ReleaseRun{full(4, "in_progress", "")}, time.Minute, FullCheckWait, 4},
		{"running past the hour still waits", []ReleaseRun{full(4, "in_progress", "")}, 3 * time.Hour,
			FullCheckWait, 4},
		{"queued for a runner still waits", []ReleaseRun{full(4, "queued", "")}, 3 * time.Hour, FullCheckWait, 4},
		{"no run yet: wait", nil, 59 * time.Minute, FullCheckWait, 0},
		{"no run within the hour: a person", nil, time.Hour, FullCheckLeave, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, run := FullCheckGate(tt.runs, started, started.Add(tt.after))
			if got != tt.want || run.ID != tt.wantRun {
				t.Errorf("FullCheckGate() = %s, run %d; want %s, run %d", got, run.ID, tt.want, tt.wantRun)
			}
		})
	}
}

func TestFullCheckIssue(t *testing.T) {
	title, body := FullCheckIssue("v0.2.0", "694db57", ReleaseRun{Workflow: "check.yaml", Conclusion: "failure",
		URL: "http://gitea/webshop/orders-api/actions/runs/9"})
	if title != "Full check fails for v0.2.0" {
		t.Errorf("title = %q", title)
	}
	for _, want := range []string{"http://gitea/webshop/orders-api/actions/runs/9", "`release-check/v0.2.0`",
		"## Acceptance criteria", "- [ ] `make check` passes"} {
		if !strings.Contains(body, want) {
			t.Errorf("body misses %q:\n%s", want, body)
		}
	}
	if commit, ok := FullCheckCommit("v0.2.0", body); !ok || commit != "694db57" {
		t.Errorf("FullCheckCommit() of its body = %q, %v; want 694db57", commit, ok)
	}
	if _, ok := FullCheckCommit("v0.3.0", body); ok {
		t.Error("FullCheckCommit() of another release's issue = true, want false")
	}
	if _, ok := FullCheckCommit("v0.2.0", "Fix the greeting"); ok {
		t.Error("FullCheckCommit() of an issue without the marker = true, want false")
	}
}

func TestFullCheckFiled(t *testing.T) {
	m := ReleaseMilestone{FullChecks: []FullCheckFix{{Issue: 9, Commit: "c3"}}}
	if !FullCheckFiled(m, "c3") || FullCheckFiled(m, "c9") {
		t.Errorf("FullCheckFiled() = %v for c3, %v for c9; want true, false", FullCheckFiled(m, "c3"),
			FullCheckFiled(m, "c9"))
	}
}

func TestNextReleaseFullCheckFix(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	merged := func(issue int, commit string, at int) MergedTask {
		return MergedTask{Name: "t-" + commit, Issue: issue, Commit: commit, At: day(at)}
	}
	release := ReleaseMilestone{ID: 2, Release: Release{Title: "v0.2.0", Due: day(12)}, Closed: []int{1, 7, 9},
		Prepared: []int{7}, FullChecks: []FullCheckFix{{Issue: 9, Commit: "c7"}}}
	work := []MergedTask{merged(1, "c1", 3), merged(7, "c7", 4)}

	got := NextRelease(ReleaseFacts{Prepare: true, Milestones: []ReleaseMilestone{release},
		Merged: append(work, merged(9, "c9", 5))})
	if want := (ReleaseStep{Tag: "v0.2.0", Commit: "c9", Milestone: 2, CreateTag: true}); !reflect.DeepEqual(got, want) {
		t.Errorf("NextRelease() with the full check's fix merged = %+v, want %+v", got, want)
	}

	// Any other merge after the prepare issue still holds it.
	release.Closed = append(release.Closed, 3)
	got = NextRelease(ReleaseFacts{Prepare: true, Milestones: []ReleaseMilestone{release},
		Merged: append(work, merged(3, "c3", 5), merged(9, "c9", 6))})
	if got.Reason != ReleaseMergedAfterPrepare || !strings.Contains(got.Held, "#3 merged after its prepare issue #7") {
		t.Errorf("NextRelease() with other work merged after the prepare issue = %+v, want %s naming #3",
			got, ReleaseMergedAfterPrepare)
	}
}

func TestFullCheckMessage(t *testing.T) {
	run := ReleaseRun{URL: "http://gitea/runs/9"}
	for _, tt := range []struct {
		reason string
		issue  int
		want   string
	}{
		{FullCheckRunning, 0, "v0.2.0 waits for its full check at c7 on release-check/v0.2.0: http://gitea/runs/9"},
		{FullCheckFailed, 12, "The full check of v0.2.0 failed at c7: http://gitea/runs/9; #12 fixes it, and v0.2.0 " +
			"is tagged once it merges and the check passes"},
		{NoFullCheck, 0, "No full check of v0.2.0 started at c7 within an hour of pushing release-check/v0.2.0; " +
			"a person checks and tags it"},
	} {
		if got := FullCheckMessage(tt.reason, "v0.2.0", "c7", run, tt.issue); got != tt.want {
			t.Errorf("FullCheckMessage(%s) = %q, want %q", tt.reason, got, tt.want)
		}
	}
}
