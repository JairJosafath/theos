package model

import (
	"strings"
	"testing"
)

func TestIsRequest(t *testing.T) {
	authors := []string{"gray-lenovo", "A-Colleague"}
	members := []string{"team-mate"}
	tests := []struct {
		name  string
		issue RequestIssue
		want  bool
	}{
		{"an allowed author's issue", RequestIssue{Number: 1, Author: "gray-lenovo"}, true},
		{"an author in another case", RequestIssue{Number: 2, Author: "a-colleague"}, true},
		{"a team member's issue", RequestIssue{Number: 3, Author: "Team-Mate"}, true},
		{"a stranger's issue", RequestIssue{Number: 4, Author: "stranger"}, false},
		{"an allowed author's pull request", RequestIssue{Number: 5, Author: "gray-lenovo", PullRequest: true}, false},
		{"no author (a deleted user)", RequestIssue{Number: 6}, false},
		{"an author whose login only starts like an allowed one", RequestIssue{Number: 7, Author: "gray-lenovo2"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsRequest(tc.issue, authors, members); got != tc.want {
				t.Errorf("IsRequest = %v, want %v", got, tc.want)
			}
		})
	}
	if IsRequest(RequestIssue{Number: 1, Author: "gray-lenovo"}, nil, nil) {
		t.Error("IsRequest with no authors or teams took an issue")
	}
}

func TestRequestBody(t *testing.T) {
	short := "Please add a dark theme. It hurts my eyes."
	if got := RequestBody("  " + short + "\n"); got != short {
		t.Errorf("RequestBody(short) = %q, want %q", got, short)
	}
	sentence := strings.Repeat("word ", 19) + "end. "
	long := strings.Repeat(sentence, 50)
	got := RequestBody(long)
	if len(got) > MaxRequestBody {
		t.Errorf("RequestBody kept %d bytes, want at most %d", len(got), MaxRequestBody)
	}
	if !strings.HasSuffix(got, "end.") {
		t.Errorf("RequestBody cut inside a sentence: ends %q", got[len(got)-20:])
	}
}

func TestRequestName(t *testing.T) {
	if got := RequestName("gray-lenovo/cel-lens", 12); got != "gray-lenovo/cel-lens#12" {
		t.Errorf("RequestName = %q", got)
	}
}

func TestRequestStage(t *testing.T) {
	tests := []struct {
		name  string
		facts RequestFacts
		want  string
	}{
		{"new in Ask", RequestFacts{}, RequestWaiting},
		{"new in On, or accepted in Ask", RequestFacts{LetThrough: true}, RequestAccepted},
		{"declined before a round", RequestFacts{Declined: true}, RequestDeclined},
		{"declined and accepted: decline wins", RequestFacts{LetThrough: true, Declined: true}, RequestDeclined},
		{"taken by a running round", RequestFacts{LetThrough: true, Round: 3}, RequestPlanning},
		{"declined after a round took it", RequestFacts{LetThrough: true, Declined: true, Round: 3}, RequestPlanning},
		{"skipped by an applied round", RequestFacts{LetThrough: true, Round: 3, Applied: true, Skipped: true},
			RequestSkipped},
		{"planned, issues open", RequestFacts{LetThrough: true, Round: 3, Applied: true, Issues: 2}, RequestPlanned},
		{"planned, issues closed, release not published",
			RequestFacts{LetThrough: true, Round: 3, Applied: true, Issues: 2, Done: true}, RequestPlanned},
		{"planned, release published, an issue open",
			RequestFacts{LetThrough: true, Round: 3, Applied: true, Issues: 2, Published: true}, RequestPlanned},
		{"issues closed and released",
			RequestFacts{LetThrough: true, Round: 3, Applied: true, Issues: 2, Done: true, Published: true},
			RequestReleased},
		{"applied with no issue", RequestFacts{LetThrough: true, Round: 3, Applied: true, Done: true, Published: true},
			RequestPlanned},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := RequestStage(tc.facts); got != tc.want {
				t.Errorf("RequestStage = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRequestCommentStage(t *testing.T) {
	want := map[string]string{
		RequestWaiting: "", RequestAccepted: "", RequestPlanning: "",
		RequestPlanned: CommentPlanned, RequestSkipped: CommentPlanned,
		RequestDeclined: CommentDeclined, RequestReleased: CommentReleased,
	}
	for stage, comment := range want {
		if got := RequestCommentStage(stage); got != comment {
			t.Errorf("RequestCommentStage(%s) = %q, want %q", stage, got, comment)
		}
	}
}

func TestRequestComments(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"planned in a release", PlannedComment("cel-lens", []string{"Add a dark theme"}, "v0.3.0"),
			"<!-- c4factory:request brief=cel-lens stage=planned -->\n" +
				"Planned as \"Add a dark theme\", in release v0.3.0.\n"},
		{"planned as several issues", PlannedComment("cel-lens",
			[]string{"Add a theme setting", "Theme the panel", "Theme the hover"}, "v0.3.0"),
			"<!-- c4factory:request brief=cel-lens stage=planned -->\n" +
				"Planned as \"Add a theme setting\", \"Theme the panel\" and \"Theme the hover\", in release v0.3.0.\n"},
		{"planned in no release", PlannedComment("cel-lens", []string{"Add a dark theme"}, ""),
			"<!-- c4factory:request brief=cel-lens stage=planned -->\nPlanned as \"Add a dark theme\".\n"},
		{"skipped", SkippedComment("cel-lens", "  The app\nalready has a dark theme. "),
			"<!-- c4factory:request brief=cel-lens stage=planned -->\nNot planned: The app already has a dark theme.\n"},
		{"skipped with no why", SkippedComment("cel-lens", ""),
			"<!-- c4factory:request brief=cel-lens stage=planned -->\nNot planned.\n"},
		{"declined", DeclinedComment("cel-lens"),
			"<!-- c4factory:request brief=cel-lens stage=declined -->\nNot taken by the app's owners.\n"},
		{"released", ReleasedComment("cel-lens", "v0.3.0", "https://github.com/gray-lenovo/cel-lens/releases/tag/v0.3.0"),
			"<!-- c4factory:request brief=cel-lens stage=released -->\n" +
				"Shipped in v0.3.0: https://github.com/gray-lenovo/cel-lens/releases/tag/v0.3.0\n"},
		{"done where nothing publishes", DoneComment("cel-lens", []string{"http://gitea/cel-lens/linter/issues/4"}),
			"<!-- c4factory:request brief=cel-lens stage=released -->\nDone in http://gitea/cel-lens/linter/issues/4.\n"},
		{"done in two issues", DoneComment("cel-lens",
			[]string{"http://gitea/cel-lens/linter/issues/4", "http://gitea/cel-lens/linter/issues/5"}),
			"<!-- c4factory:request brief=cel-lens stage=released -->\n" +
				"Done in http://gitea/cel-lens/linter/issues/4 and http://gitea/cel-lens/linter/issues/5.\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("comment =\n%s\nwant\n%s", tc.got, tc.want)
			}
		})
	}
	long := SkippedComment("cel-lens", strings.Repeat("Out of scope. ", 100))
	if n := len(strings.TrimPrefix(long, RequestMarker("cel-lens", CommentPlanned)+"\nNot planned: ")); n > MaxAnswerWhy+1 {
		t.Errorf("SkippedComment kept a why of %d bytes, want at most %d", n-1, MaxAnswerWhy)
	}
}

func TestHasRequestComment(t *testing.T) {
	planned := PlannedComment("cel-lens", []string{"Add a dark theme"}, "v0.3.0")
	tests := []struct {
		name     string
		comments []string
		brief    string
		stage    string
		want     bool
	}{
		{"its own comment", []string{"Thanks!", planned}, "cel-lens", CommentPlanned, true},
		{"another stage", []string{planned}, "cel-lens", CommentReleased, false},
		{"another Brief's", []string{planned}, "other", CommentPlanned, false},
		{"a Brief whose name starts like it", []string{PlannedComment("cel-lens-2", nil, "")}, "cel-lens",
			CommentPlanned, false},
		{"a person's quote of the text, no marker", []string{"> Planned as \"Add a dark theme\""}, "cel-lens",
			CommentPlanned, false},
		{"no comments", nil, "cel-lens", CommentPlanned, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasRequestComment(tc.comments, tc.brief, tc.stage); got != tc.want {
				t.Errorf("HasRequestComment = %v, want %v", got, tc.want)
			}
		})
	}
}
