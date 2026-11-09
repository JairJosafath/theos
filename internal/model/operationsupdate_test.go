/*
Copyright 2026 The theos Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package model

import (
	"strings"
	"testing"
)

func TestNextOperationsStep(t *testing.T) {
	const url = "http://gitea/webshop/orders-api/pulls/3"
	scaffolded := ResolveOperations(OperationsChoices{})
	changed := ResolveOperations(OperationsChoices{RPO: "5m"})
	again := ResolveOperations(OperationsChoices{RPO: "15m"})
	running := func(o Operations) *OperationsUpdateInFlight {
		return &OperationsUpdateInFlight{Operations: o, PullRequest: 3, PullRequestURL: url,
			Branch: OperationsUpdateBranch(o), Commit: "abc"}
	}
	stopped := func(reason string, pr int) *OperationsUpdateInFlight {
		u := running(changed)
		u.Stopped, u.PullRequest = reason, pr
		return u
	}
	merged := func(o Operations) *OperationsUpdateInFlight {
		u := running(o)
		u.Merged = true
		return u
	}
	open := &UpdatePullRequest{Mergeable: true, HeadSHA: "abc"}
	green := map[string]string{"check": "success"}
	// following is an update in flight at the target whose pull request, unless
	// f has one, is open and holds the operator's commit.
	following := func(f OperationsUpdateFacts) OperationsUpdateFacts {
		f.Target, f.Recorded, f.Update = changed, scaffolded, running(changed)
		f.CI = giteaActions
		if f.PR == nil {
			f.PR = open
		}
		return f
	}
	ready := func(f OperationsUpdateFacts) OperationsUpdateFacts {
		f = following(f)
		if f.Checks == "" {
			f.Checks, f.Contexts = "success", green
		}
		return f
	}

	tests := []struct {
		name  string
		facts OperationsUpdateFacts
		want  OperationsAction
		// message and why must contain these.
		message, why string
		quiet        bool
	}{
		{name: "the recorded block is up to date",
			facts: OperationsUpdateFacts{Target: scaffolded, Recorded: scaffolded}, want: OperationsUpToDate},
		{name: "a changed block is planned",
			facts: OperationsUpdateFacts{Target: changed, Recorded: scaffolded}, want: OperationsPlan},
		{name: "a merged update at the target is up to date",
			facts: OperationsUpdateFacts{Target: changed, Recorded: changed, Update: merged(changed)},
			want:  OperationsUpToDate},
		{name: "a block changed after a merged update is planned",
			facts: OperationsUpdateFacts{Target: again, Recorded: changed, Update: merged(changed)},
			want:  OperationsPlan},

		{name: "a stopped update the block moved past is forgotten",
			facts: OperationsUpdateFacts{Target: again, Recorded: scaffolded, Update: stopped("UpdateClosed", 3)},
			want:  OperationsForget},
		{name: "a stopped update undone back to the recorded block is forgotten",
			facts: OperationsUpdateFacts{Target: scaffolded, Recorded: scaffolded, Update: stopped("Failed", 0)},
			want:  OperationsForget},
		{name: "an update the block moved past is read first",
			facts: OperationsUpdateFacts{Target: again, Recorded: scaffolded, Update: running(changed)},
			want:  OperationsReadPR},
		{name: "an update the block moved past is dropped",
			facts: OperationsUpdateFacts{Target: again, Recorded: scaffolded, Update: running(changed), PR: open},
			want:  OperationsDrop, message: "moved past"},
		{name: "an update the block moved past that merged is recorded",
			facts: OperationsUpdateFacts{Target: again, Recorded: scaffolded, Update: running(changed),
				PR: &UpdatePullRequest{Merged: true}},
			want: OperationsRecordMerge},

		{name: "a stopped update without a pull request waits",
			facts: OperationsUpdateFacts{Target: changed, Recorded: scaffolded, Update: stopped("ChangedAfterScaffold", 0)},
			want:  OperationsNone},
		{name: "a stopped update's pull request is read quietly",
			facts: OperationsUpdateFacts{Target: changed, Recorded: scaffolded, Update: stopped("UpdateRefused", 3)},
			want:  OperationsReadPR, quiet: true},
		{name: "a stopped update a person merged is recorded",
			facts: OperationsUpdateFacts{Target: changed, Recorded: scaffolded, Update: stopped("UpdateClosed", 3),
				PR: &UpdatePullRequest{Merged: true}},
			want: OperationsRecordMerge},
		{name: "a stopped update still open waits",
			facts: OperationsUpdateFacts{Target: changed, Recorded: scaffolded, Update: stopped("UpdateRefused", 3),
				PR: open},
			want: OperationsNone},

		{name: "an update in flight reads its pull request",
			facts: OperationsUpdateFacts{Target: changed, Recorded: scaffolded, Update: running(changed)},
			want:  OperationsReadPR},
		{name: "a merged pull request is recorded",
			facts: following(OperationsUpdateFacts{PR: &UpdatePullRequest{Merged: true, Closed: true}}),
			want:  OperationsRecordMerge},
		{name: "a closed pull request stops the update",
			facts: following(OperationsUpdateFacts{PR: &UpdatePullRequest{Closed: true, HeadSHA: "abc"}}),
			want:  OperationsStopClosed, message: url + " was closed without merging"},
		{name: "a pull request that conflicts with main is dropped",
			facts: following(OperationsUpdateFacts{PR: &UpdatePullRequest{HeadSHA: "abc"}}),
			want:  OperationsDrop, message: "conflicts with main"},
		{name: "a pull request someone else pushed to stops the update",
			facts: following(OperationsUpdateFacts{PR: &UpdatePullRequest{Mergeable: true, HeadSHA: "def"}}),
			want:  OperationsStopPushed, message: "Someone else pushed to operations-update/"},
		{name: "the checks are read",
			facts: following(OperationsUpdateFacts{}), want: OperationsReadChecks},
		{name: "pending checks are waited for",
			facts: following(OperationsUpdateFacts{Checks: "pending"}), want: OperationsWaitForChecks},
		{name: "failed checks say where, and the pull request",
			facts: following(OperationsUpdateFacts{Checks: "failure", ChecksURL: "http://gitea/runs/1"}),
			want:  OperationsChecksFailed, message: "CI is failure on http://gitea/runs/1; the update merges once " +
				"a re-run passes, or close " + url + " to drop it"},
		{name: "errored checks fail too",
			facts: following(OperationsUpdateFacts{Checks: "error"}), want: OperationsChecksFailed},
		{name: "green checks read the changed files",
			facts: ready(OperationsUpdateFacts{}), want: OperationsReadFiles},
		{name: "files that can't all be read refuse the merge",
			facts: ready(OperationsUpdateFacts{Files: &UpdateFiles{Unchecked: "too many files"}}),
			want:  OperationsRefuseMerge, why: "not every changed path can be checked"},
		{name: "a file besides infra/operations.ts refuses the merge",
			facts: ready(OperationsUpdateFacts{Files: &UpdateFiles{Changed: []string{OperationsFile, "Makefile"}}}),
			want:  OperationsRefuseMerge, why: "Makefile"},
		{name: "a required check that didn't pass refuses the merge",
			facts: ready(OperationsUpdateFacts{Contexts: map[string]string{}, Checks: "success",
				Files: &UpdateFiles{Changed: []string{OperationsFile}}}),
			want: OperationsRefuseMerge, why: "main requires"},
		{name: "only infra/operations.ts, green, merges",
			facts: ready(OperationsUpdateFacts{Files: &UpdateFiles{Changed: []string{OperationsFile}}}),
			want:  OperationsForceMerge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextOperationsStep(tt.facts)
			if got.Action != tt.want {
				t.Fatalf("action = %s (%q), want %s", got.Action, got.Message, tt.want)
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

func TestOperationsUpdateBranch(t *testing.T) {
	a, b := ResolveOperations(OperationsChoices{}), ResolveOperations(OperationsChoices{RPO: "5m"})
	if OperationsUpdateBranch(a) == OperationsUpdateBranch(b) {
		t.Errorf("two blocks share the branch %s", OperationsUpdateBranch(a))
	}
	if got := OperationsUpdateBranch(a); got != OperationsUpdateBranch(a) || !strings.HasPrefix(got,
		"operations-update/") || len(got) != len("operations-update/")+12 {
		t.Errorf("OperationsUpdateBranch() = %q, want operations-update/ and 12 hex digits, the same each time", got)
	}
}

func TestOperationsUpdateBody(t *testing.T) {
	from := ResolveOperations(OperationsChoices{})
	to := ResolveOperations(OperationsChoices{RPO: "5m", Email: "ops@example.com"})
	body := OperationsUpdateBody(from, to)
	for _, want := range []string{"`infra/operations.ts`", "changes no other file",
		"- from: backups.retentionDays 35, backups.copyToRegion none, recovery.rpo 1h, recovery.rto 4h, " +
			"alerts.email none\n",
		"- to: backups.retentionDays 35, backups.copyToRegion none, recovery.rpo 5m, recovery.rto 4h, " +
			"alerts.email ops@example.com\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("body = %q, want it to contain %q", body, want)
		}
	}
}
