package model

import (
	"strings"
	"testing"
)

func TestNextStandardStep(t *testing.T) {
	const url = "http://gitea/webshop/orders-api/pulls/3"
	running := func() *StandardUpdateInFlight {
		return &StandardUpdateInFlight{Ref: "v2", PullRequest: 3, PullRequestURL: url,
			Branch: "standard-update/python-strict-v2", Commit: "abc", Paths: []string{"Makefile", "ruff.toml"}}
	}
	stopped := func(reason string, pr int) *StandardUpdateInFlight {
		u := running()
		u.Stopped, u.PullRequest = reason, pr
		return u
	}
	take := func(stoppedFor string) *StandardUpdateInFlight {
		u := running()
		u.Take, u.Stopped, u.Branch = true, stoppedFor, "standard-take/python-strict-v2"
		return u
	}
	open := &UpdatePullRequest{Mergeable: true, HeadSHA: "abc"}
	green := map[string]string{"check": "success"}
	// following is an update in flight at the target whose pull request, unless
	// f has one, is open and holds the operator's commit.
	following := func(f StandardUpdateFacts) StandardUpdateFacts {
		f.Name, f.Ref, f.Target, f.Update = "python-strict", "v1", "v2", running()
		if f.PR == nil {
			f.PR = open
		}
		return f
	}

	tests := []struct {
		name  string
		facts StandardUpdateFacts
		want  StandardAction
		// message and why must contain these.
		message, why string
		quiet        bool
	}{
		{name: "files at the target are up to date",
			facts: StandardUpdateFacts{Ref: "v2", Target: "v2"}, want: StandardUpToDate},
		{name: "files behind the target are planned",
			facts: StandardUpdateFacts{Ref: "v1", Target: "v2"}, want: StandardPlan},

		{name: "empty checks read as EveryChange",
			facts: StandardUpdateFacts{Ref: "v2", Target: "v2", TargetMode: ChecksEveryChange}, want: StandardUpToDate},
		{name: "files at the target ref with other checks are planned",
			facts: StandardUpdateFacts{Ref: "v2", Target: "v2", TargetMode: ChecksBeforeRelease}, want: StandardPlan},
		{name: "files back at their checks are up to date",
			facts: StandardUpdateFacts{Ref: "v2", Target: "v2", Mode: ChecksBeforeRelease, TargetMode: ChecksBeforeRelease},
			want:  StandardUpToDate},
		{name: "a stopped update the checks moved past is forgotten",
			facts: StandardUpdateFacts{Ref: "v1", Target: "v2", TargetMode: ChecksBeforeRelease,
				Update: stopped("ChecksLeftOut", 0)}, want: StandardForget},
		{name: "a stopped update at the target checks waits",
			facts: StandardUpdateFacts{Ref: "v2", Target: "v2", TargetMode: ChecksBeforeRelease,
				Update: &StandardUpdateInFlight{Ref: "v2", Mode: ChecksBeforeRelease, Stopped: "ChecksLeftOut"}},
			want: StandardNone},
		{name: "an open update the checks moved past is dropped",
			facts: StandardUpdateFacts{Ref: "v2", Target: "v2", Mode: ChecksBeforeRelease,
				Update: &StandardUpdateInFlight{Ref: "v2", Mode: ChecksBeforeRelease, PullRequest: 3}, PR: open},
			want: StandardDrop},
		{name: "an update at the target checks is followed",
			facts: StandardUpdateFacts{Ref: "v2", Target: "v2", TargetMode: ChecksBeforeRelease,
				Update: &StandardUpdateInFlight{Ref: "v2", Mode: ChecksBeforeRelease, PullRequest: 3}},
			want: StandardReadPR},

		{name: "a stopped update the Standard moved past is forgotten",
			facts: StandardUpdateFacts{Ref: "v1", Target: "v3", Update: stopped("UpdateClosed", 3)},
			want:  StandardForget},
		{name: "an update the Standard moved past is read first",
			facts: StandardUpdateFacts{Ref: "v1", Target: "v3", Update: running()}, want: StandardReadPR},
		{name: "an update the Standard moved past that merged meanwhile is recorded",
			facts: StandardUpdateFacts{Ref: "v1", Target: "v3", Update: running(),
				PR: &UpdatePullRequest{Merged: true}}, want: StandardRecordMerge},
		{name: "an open update the Standard moved past is dropped",
			facts: StandardUpdateFacts{Ref: "v1", Target: "v3", Update: running(), PR: open},
			want:  StandardDrop, message: "Closed a Standard update its Standard or checks moved past"},
		{name: "a closed update the Standard moved past is dropped",
			facts: StandardUpdateFacts{Ref: "v1", Target: "v3", Update: running(),
				PR: &UpdatePullRequest{Closed: true}}, want: StandardDrop},

		{name: "a stopped update without a pull request waits for the Standard",
			facts: StandardUpdateFacts{Ref: "v1", Target: "v2", Update: stopped("UnpinnedRef", 0)},
			want:  StandardNone},
		{name: "a stopped update's pull request is read quietly",
			facts: StandardUpdateFacts{Ref: "v1", Target: "v2", Update: stopped("UpdateRefused", 3)},
			want:  StandardReadPR, quiet: true},
		{name: "a stopped update a person merged after all is recorded",
			facts: StandardUpdateFacts{Ref: "v1", Target: "v2", Update: stopped("UpdateRefused", 3),
				PR: &UpdatePullRequest{Merged: true}}, want: StandardRecordMerge},
		{name: "a stopped update still open waits for the Standard",
			facts: StandardUpdateFacts{Ref: "v1", Target: "v2", Update: stopped("UpdateRefused", 3), PR: open},
			want:  StandardNone},

		{name: "the update's pull request is read first",
			facts: StandardUpdateFacts{Ref: "v1", Target: "v2", Update: running()}, want: StandardReadPR},
		{name: "a merged update is recorded",
			facts: following(StandardUpdateFacts{PR: &UpdatePullRequest{Merged: true}}), want: StandardRecordMerge},
		{name: "an update closed without merging stops",
			facts: StandardUpdateFacts{Name: "python-strict", Ref: "v1", Target: "v2", Update: running(),
				PR: &UpdatePullRequest{Closed: true}},
			want: StandardStopClosed, message: "Pull request " + url +
				" was closed without merging; python-strict stays at v1 until its ref or the checks change again"},
		{name: "a take closed without merging is forgotten",
			facts: StandardUpdateFacts{Ref: "v2", Target: "v2", Update: take(""), PR: &UpdatePullRequest{Closed: true}},
			want:  StandardTakeClosed},
		{name: "a stopped take closed without merging is forgotten",
			facts: StandardUpdateFacts{Ref: "v2", Target: "v2", Update: take("UpdateRefused"),
				PR: &UpdatePullRequest{Closed: true}},
			want: StandardTakeClosed},
		{name: "a merged take is recorded",
			facts: StandardUpdateFacts{Ref: "v2", Target: "v2", Update: take(""), PR: &UpdatePullRequest{Merged: true}},
			want:  StandardRecordMerge},
		{name: "a take the Standard moved past is dropped",
			facts: StandardUpdateFacts{Ref: "v2", Target: "v3", Update: take(""), PR: open},
			want:  StandardDrop},
		{name: "an update that conflicts with main is dropped",
			facts: StandardUpdateFacts{Ref: "v1", Target: "v2", Update: running(),
				PR: &UpdatePullRequest{HeadSHA: "abc"}},
			want: StandardDrop, message: "Closed a Standard update that conflicts with main"},
		{name: "an update someone else pushed to stops",
			facts: StandardUpdateFacts{Ref: "v1", Target: "v2", Update: running(),
				PR: &UpdatePullRequest{Mergeable: true, HeadSHA: "def"}},
			want:    StandardStopPushed,
			message: "Someone else pushed to standard-update/python-strict-v2; a person looks at " + url},

		{name: "the checks are read next", facts: following(StandardUpdateFacts{}), want: StandardReadChecks},
		{name: "pending checks are waited for",
			facts: following(StandardUpdateFacts{Checks: "pending"}), want: StandardWaitForChecks},
		{name: "failed checks are reported",
			facts: following(StandardUpdateFacts{Checks: "failure", ChecksURL: "http://ci/1"}),
			want:  StandardChecksFailed,
			message: "CI is failure on http://ci/1; the update merges once a re-run passes, or close " + url +
				" to drop it"},
		{name: "errored checks are reported",
			facts: following(StandardUpdateFacts{Checks: "error"}), want: StandardChecksFailed, message: "CI is error"},

		{name: "the changed paths are read next",
			facts: following(StandardUpdateFacts{Checks: "success", Contexts: green}), want: StandardReadFiles},
		{name: "a change too big to check is refused",
			facts: following(StandardUpdateFacts{Checks: "success", Contexts: green,
				Files: &UpdateFiles{Unchecked: "more than 300 files"}}),
			want: StandardRefuseMerge, why: "not every changed path can be checked: more than 300 files",
			message: "The update may not merge: not every changed path can be checked: more than 300 files; " +
				"a person looks at " + url},
		{name: "a change that misses a required check is refused",
			facts: following(StandardUpdateFacts{Checks: "success", Contexts: map[string]string{"lint": "success"},
				Files: &UpdateFiles{Changed: []string{"Makefile"}}}),
			want: StandardRefuseMerge, why: `no passing check matches "check*"`},
		{name: "a change of nothing is refused",
			facts: following(StandardUpdateFacts{Checks: "success", Contexts: green, Files: &UpdateFiles{}}),
			want:  StandardRefuseMerge, why: "the change changes nothing"},
		{name: "a change outside the plan is refused",
			facts: following(StandardUpdateFacts{Checks: "success", Contexts: green,
				Files: &UpdateFiles{Changed: []string{"Makefile", "src/app.py"}}}),
			want: StandardRefuseMerge, why: "the pull request changes src/app.py, which the update did not plan"},
		{name: "a green change of planned paths merges",
			facts: following(StandardUpdateFacts{Checks: "success", Contexts: green,
				Files: &UpdateFiles{Changed: []string{"ruff.toml"}}}),
			want: StandardForceMerge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextStandardStep(tt.facts)
			if got.Action != tt.want {
				t.Fatalf("NextStandardStep() = %s (%q), want %s", got.Action, got.Message, tt.want)
			}
			if !strings.Contains(got.Message, tt.message) {
				t.Errorf("message = %q, want it to contain %q", got.Message, tt.message)
			}
			if !strings.Contains(got.Why, tt.why) {
				t.Errorf("why = %q, want it to contain %q", got.Why, tt.why)
			}
			if got.Quiet != tt.quiet {
				t.Errorf("quiet = %v, want %v", got.Quiet, tt.quiet)
			}
		})
	}
}
