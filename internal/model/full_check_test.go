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
		URL: "http://gitea/webshop/orders-api/actions/runs/9"}, false, 0)
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

func TestFullCheckCutOff(t *testing.T) {
	const ended = "2026-10-12T09:00:00.1234567Z 🏁  Job failed"
	const cut = "2026-10-12T09:00:00.1234567Z go test ./..."
	tests := []struct {
		name       string
		conclusion string
		jobs       []FullCheckJob
		want       bool
	}{
		{"#2028: a run cancelled at the time limit is cut off", "cancelled", nil, true},
		{"#2028: a failed job with no log lost its runner", "failure", []FullCheckJob{{Conclusion: "failure", NoLog: true}}, true},
		{"#2028: a failed job whose log stops before its end lost its runner", "failure",
			[]FullCheckJob{{Conclusion: "success", Log: ended}, {Conclusion: "failure", Log: cut}}, true},
		{"a job that failed its check is no cut", "failure", []FullCheckJob{{Conclusion: "failure", Log: cut + "\n" + ended}}, false},
		{"a job whose log can't be read is no cut", "failure", []FullCheckJob{{Conclusion: "failure"}}, false},
		{"a passing job's missing log doesn't count", "failure",
			[]FullCheckJob{{Conclusion: "success", NoLog: true}, {Conclusion: "failure", Log: cut + "\n" + ended}}, false},
		{"no jobs read", "failure", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FullCheckCutOff(ReleaseRun{Conclusion: tt.conclusion}, tt.jobs); got != tt.want {
				t.Errorf("FullCheckCutOff() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFullCheckFailure(t *testing.T) {
	for _, tt := range []struct {
		name   string
		cutOff bool
		reruns int
		want   string
	}{
		{"#2028: cut off, it runs again once", true, 0, FullCheckRerun},
		{"#2028: cut off again after a re-run, the issue is filed", true, 1, FullCheckFile},
		{"a failure that wasn't cut off files at once", false, 0, FullCheckFile},
	} {
		if got := FullCheckFailure(tt.cutOff, tt.reruns); got != tt.want {
			t.Errorf("%s: FullCheckFailure(%v, %d) = %s, want %s", tt.name, tt.cutOff, tt.reruns, got, tt.want)
		}
	}
}

func TestFullCheckIssueCutOff(t *testing.T) {
	run := ReleaseRun{Workflow: "check.yaml", Conclusion: "cancelled", URL: "http://gitea/runs/9"}
	_, body := FullCheckIssue("v0.2.0", "694db57", run, true, 1)
	for _, want := range []string{"was cut off at 694db57", "check.yaml ended cancelled, http://gitea/runs/9",
		"stopped at the runner's time limit, or its runner was lost", "It ran again once and was cut off again",
		"`theos.factory.io/rerun-release`", "- [ ] `make check` passes and ends within the runner's time limit"} {
		if !strings.Contains(body, want) {
			t.Errorf("#2028: the cut-off issue's body misses %q:\n%s", want, body)
		}
	}
	if commit, ok := FullCheckCommit("v0.2.0", body); !ok || commit != "694db57" {
		t.Errorf("FullCheckCommit() of the cut-off issue = %q, %v; want 694db57", commit, ok)
	}
	_, body = FullCheckIssue("v0.2.0", "694db57", run, true, 0)
	if strings.Contains(body, "ran again") {
		t.Errorf("a cut-off issue filed with no re-run says it ran again:\n%s", body)
	}
	_, body = FullCheckIssue("v0.2.0", "694db57", run, false, 1)
	if strings.Contains(body, "cut off") {
		t.Errorf("an issue of a check that failed says it was cut off:\n%s", body)
	}
}

func TestNextReleaseFullCheckRerun(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	release := ReleaseMilestone{ID: 2, Release: Release{Title: "v0.2.0", Due: day(12)}, Open: []int{9},
		Closed: []int{1}, FullChecks: []FullCheckFix{{Issue: 9, Commit: "c1"}}}
	merged := []MergedTask{{Name: "t-1", Issue: 1, Commit: "c1", At: day(3)}}
	facts := ReleaseFacts{Milestones: []ReleaseMilestone{release}, Merged: merged, Active: []int{9}}
	if got := NextRelease(facts); !reflect.DeepEqual(got, ReleaseStep{}) {
		t.Errorf("NextRelease() with the full check's issue open = %+v, want nothing", got)
	}
	facts.FullCheckRerun = &FullCheckAt{Tag: "v0.2.0", Commit: "c1"}
	want := ReleaseStep{Tag: "v0.2.0", Commit: "c1", Milestone: 2, CreateTag: true, Closes: []int{9}}
	if got := NextRelease(facts); !reflect.DeepEqual(got, want) {
		t.Errorf("#2028: NextRelease() while a person re-runs the full check = %+v, want %+v", got, want)
	}
	if len(facts.Milestones[0].Open) != 1 || len(facts.Active) != 1 {
		t.Errorf("NextRelease() changed the facts given: %+v", facts)
	}
	facts.FullCheckRerun = &FullCheckAt{Tag: "v0.2.0", Commit: "c0"}
	if got := NextRelease(facts); !reflect.DeepEqual(got, ReleaseStep{}) {
		t.Errorf("NextRelease() re-running another commit's check = %+v, want nothing", got)
	}
}
