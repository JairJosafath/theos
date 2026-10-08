package model

import (
	"strings"
	"testing"
)

func TestAbandonedPull(t *testing.T) {
	first := IssueTry{Name: "core-1", Created: 1, Failed: true, PullRequest: 54, PullRequestURL: "https://git/pulls/54"}
	open := func(f AbandonedPullFacts) AbandonedPullFacts {
		f.Task, f.PullOpen, f.PullRequest = "core-1", true, 54
		if f.Tries == nil {
			f.Tries = []IssueTry{first}
		}
		return f
	}
	tests := []struct {
		name        string
		facts       AbandonedPullFacts
		close       bool
		ended, says string
	}{
		{"kept while a later try may continue it", open(AbandonedPullFacts{Issue: 52}), false, "", ""},
		{"merged elsewhere", AbandonedPullFacts{Task: "core-1", Issue: 52, PullMerged: true}, false,
			"merged on the Git host", ""},
		{"closed elsewhere", AbandonedPullFacts{Task: "core-1", Issue: 52, IssueClosed: true}, false,
			"closed on the Git host", ""},
		{"issue closed", open(AbandonedPullFacts{Issue: 52, IssueClosed: true}), true,
			"closed: issue #52 closed", "issue #52 is closed"},
		{"no issue", open(AbandonedPullFacts{}), true, "closed: no issue", "had no issue"},
		{"tries used up", open(AbandonedPullFacts{Issue: 52, Tries: []IssueTry{first,
			{Name: "core-2", Created: 2, Failed: true}, {Name: "core-3", Created: 3, Failed: true}}}), true,
			"closed: tries used up", "3 tries are used up"},
		{"superseded by a newer try's own pull request", open(AbandonedPullFacts{Issue: 139, Tries: []IssueTry{first,
			{Name: "core-2", Created: 2, PullRequest: 155, PullRequestURL: "https://git/pulls/155"}}}), true,
			"superseded by Task core-2", "Task `core-2`, a newer try at #139, opened https://git/pulls/155"},
		{"continued by a newer try (#607), even with the issue closed", open(AbandonedPullFacts{Issue: 139,
			IssueClosed: true, Tries: []IssueTry{first, {Name: "core-2", Created: 2, PullRequest: 54}}}), false,
			"continued by Task core-2", ""},
		{"a newer try without a pull request yet waits", open(AbandonedPullFacts{Issue: 139,
			Tries: []IssueTry{first, {Name: "core-2", Created: 2}}}), false, "", ""},
		{"an older try's pull request doesn't supersede", open(AbandonedPullFacts{Issue: 139,
			Tries: []IssueTry{{Name: "core-0", Created: 0, Failed: true, PullRequest: 50}, first}}), false, "", ""},
		{"the same second orders by name", open(AbandonedPullFacts{Issue: 139, Tries: []IssueTry{
			{Name: "core-2", Created: 1, PullRequest: 60, PullRequestURL: "https://git/pulls/60"}, first}}), true,
			"superseded by Task core-2", "opened https://git/pulls/60"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AbandonedPull(tt.facts)
			if got.Close != tt.close || got.Ended != tt.ended {
				t.Fatalf("AbandonedPull() = close %v, ended %q; want close %v, ended %q",
					got.Close, got.Ended, tt.close, tt.ended)
			}
			if tt.close && (!strings.Contains(got.Comment, tt.says) || !strings.Contains(got.Comment, "Task `core-1` failed") ||
				!strings.Contains(got.Comment, "branch stays")) {
				t.Errorf("comment %q doesn't name the Task, say %q and keep the branch", got.Comment, tt.says)
			}
			if !tt.close && got.Comment != "" {
				t.Errorf("comment %q on a pull request left as it is", got.Comment)
			}
		})
	}
}
