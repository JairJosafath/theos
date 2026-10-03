package model

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitChange(t *testing.T) {
	std := AgentProtectedPaths([]string{"AGENTS.md", "Makefile", "docs/agents/"})
	for _, tt := range []struct {
		name                      string
		changed, protected, allow []string
		canPropose                bool
		want                      ChangeSplit
	}{
		{"unprotected", []string{"src/a.ts"}, std, nil, true, ChangeSplit{Task: []string{"src/a.ts"}}},
		{"eligible is proposed", []string{"Makefile", "src/a.ts"}, std, nil, true,
			ChangeSplit{Task: []string{"src/a.ts"}, Proposal: []string{"Makefile"}}},
		{"platform directory is eligible", []string{".gitea/workflows/release.yaml"}, std, nil, true,
			ChangeSplit{Proposal: []string{".gitea/workflows/release.yaml"}}},
		{"agents' rules and contract refused", []string{"AGENTS.md", "docs/agents/reviewer.md", ".c4factory.yaml"}, std, nil, true,
			ChangeSplit{Refused: []string{"AGENTS.md", "docs/agents/reviewer.md", ".c4factory.yaml"}}},
		{"mixed", []string{"Makefile", "AGENTS.md", "src/a.ts"}, std, nil, true,
			ChangeSplit{Task: []string{"src/a.ts"}, Proposal: []string{"Makefile"}, Refused: []string{"AGENTS.md"}}},
		{"no issue, no proposal", []string{"Makefile"}, std, nil, false, ChangeSplit{Refused: []string{"Makefile"}}},
		{"gated: allowed is the Task's", []string{"Makefile"}, std, []string{"Makefile"}, true, ChangeSplit{Task: []string{"Makefile"}}},
		{"gated: anything else refused", []string{"Makefile", "README.md", ".golangci.yml"}, std, []string{"Makefile"}, true,
			ChangeSplit{Task: []string{"Makefile"}, Refused: []string{"README.md", ".golangci.yml"}}},
		{"unprotected AGENTS.md is the Task's", []string{"AGENTS.md"}, AgentProtectedPaths(nil), nil, true,
			ChangeSplit{Task: []string{"AGENTS.md"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := SplitChange(tt.changed, tt.protected, tt.allow, tt.canPropose); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SplitChange = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestEligibleAllowance(t *testing.T) {
	got := EligibleAllowance([]string{"Makefile", "AGENTS.md", "docs/agents/x.md", ".c4factory.yaml", ".gitea/workflows/check.yaml"})
	want := []string{"Makefile", ".gitea/workflows/check.yaml"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EligibleAllowance = %v, want %v", got, want)
	}
	if got := EligibleAllowance(nil); got != nil {
		t.Errorf("EligibleAllowance(nil) = %v, want nil", got)
	}
}

func TestProposalNames(t *testing.T) {
	if got := ProposalBranch("vscode-extension-4-2"); got != "standard/vscode-extension-4-2" {
		t.Errorf("ProposalBranch = %q", got)
	}
	want := "<!-- c4factory:standard-change standard/x -->"
	if got := ProposalMarker("standard/x"); got != want {
		t.Errorf("ProposalMarker = %q, want %q", got, want)
	}
}

func TestProposalReason(t *testing.T) {
	for _, tt := range []struct{ name, summary, want string }{
		{"marker", "Did X.\nStandard change: make dist must build the packages.\nIt calls npm run dist.",
			"make dist must build the packages.\nIt calls npm run dist."},
		{"no marker", "Did X, no marker.", "Did X, no marker."},
		{"trimmed", "  Did X.\n", "Did X."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := ProposalReason(tt.summary); got != tt.want {
				t.Errorf("ProposalReason = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStandardChangeIssue(t *testing.T) {
	p := Proposal{Branch: "standard/vscode-extension-4-2", Paths: []string{"Makefile"}, Reason: "make dist must build the packages"}
	title, body := StandardChangeIssue(4, "vscode-extension-4-2", p)
	if title != "Standard change for #4: Makefile" {
		t.Errorf("title = %q", title)
	}
	for _, want := range []string{p.Reason, "`Makefile`", "origin/standard/vscode-extension-4-2", "#4", "`vscode-extension-4-2`",
		"weakens no check", "works with this change alone", ProposalMarker(p.Branch)} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	if msg := MissingAcceptanceCriteria(body); msg != "" {
		t.Errorf("body is not clear enough for an agent: %s", msg)
	}
	p.Paths = []string{"Makefile", ".gitea/workflows/release.yaml"}
	if title, _ := StandardChangeIssue(4, "vscode-extension-4-2", p); title != "Standard change for #4: Makefile and .gitea/workflows/release.yaml" {
		t.Errorf("two-path title = %q", title)
	}
}

// The Git host stores at most 255 bytes of title: a long list of paths becomes the
// first path and how many more, and the body keeps them all.
func TestStandardChangeIssueTitleFits(t *testing.T) {
	long := strings.Repeat("d/", 127) + "x" // 255 bytes, under the 256-byte path cap
	many := make([]string, 20)
	for i := range many {
		many[i] = fmt.Sprintf(".gitea/workflows/release-%02d.yaml", i)
	}
	tests := []struct {
		name  string
		paths []string
		want  string // "" means the whole title is checked by its length alone
	}{
		{name: "short list in full", paths: []string{"Makefile", ".gitea/"},
			want: "Standard change for #4: Makefile and .gitea/"},
		{name: "many paths: the first and how many more", paths: many,
			want: "Standard change for #4: .gitea/workflows/release-00.yaml and 19 more"},
		{name: "one long path, cut", paths: []string{long}},
		{name: "a long first path and more, cut", paths: []string{long, "Makefile"}},
		{name: "a cut never splits a character", paths: []string{strings.Repeat("é", 120) + "/x", "Makefile"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, body := StandardChangeIssue(4, "t", Proposal{Branch: "standard/t", Paths: tt.paths, Reason: "r"})
			if len(title) > 255 || !utf8.ValidString(title) || !strings.HasPrefix(title, "Standard change for #4: ") {
				t.Errorf("title = %q (%d bytes), want a valid one of at most 255", title, len(title))
			}
			if tt.want != "" && title != tt.want {
				t.Errorf("title = %q, want %q", title, tt.want)
			}
			if n := len(tt.paths) - 1; tt.want == "" && n > 0 && !strings.HasSuffix(title, fmt.Sprintf(" and %d more", n)) {
				t.Errorf("title = %q, want it to count the other paths", title)
			}
			for _, p := range tt.paths {
				if !strings.Contains(body, "`"+p+"`") {
					t.Errorf("body lacks %q", p)
				}
			}
		})
	}
}

// The operator finds its issue again by the marker, so only the marker it wrote
// may be in the body: what the agent wrote can't name another branch.
func TestStandardChangeIssueCarriesOnlyItsMarker(t *testing.T) {
	for _, tt := range []struct {
		name   string
		reason string
		paths  []string
	}{
		{name: "a plain reason", reason: "make dist must build the packages", paths: []string{"Makefile"}},
		{name: "a reason with another branch's marker on its own line",
			reason: "make dist must build\n" + ProposalMarker("standard/other-1") + "\nthe packages", paths: []string{"Makefile"}},
		{name: "a reason with an inline comment", reason: "make dist <!-- hidden --> must build", paths: []string{"Makefile"}},
		{name: "a path with a comment", reason: "make dist must build", paths: []string{".gitea/<!--x-->.yaml"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := Proposal{Branch: "standard/orders-api-1", Paths: tt.paths, Reason: tt.reason}
			_, body := StandardChangeIssue(1, "orders-api-1", p)
			if n := strings.Count(body, "<!--"); n != 1 {
				t.Errorf("body has %d comments, want only the marker:\n%s", n, body)
			}
			if !HasProposalMarker(body, p.Branch) {
				t.Errorf("body lacks its own marker:\n%s", body)
			}
			if HasProposalMarker(body, "standard/other-1") {
				t.Errorf("body carries another branch's marker:\n%s", body)
			}
		})
	}
}

func TestHasProposalMarker(t *testing.T) {
	marker := ProposalMarker("standard/orders-api-1")
	for _, tt := range []struct {
		name, body string
		want       bool
	}{
		{name: "on its own line", body: "Why\n\n" + marker + "\n", want: true},
		{name: "the last line, without a newline", body: "Why\n" + marker, want: true},
		{name: "with carriage returns and spaces", body: "Why\r\n  " + marker + " \r\n", want: true},
		{name: "inside a line", body: "See " + marker + " here\n"},
		{name: "another branch", body: ProposalMarker("standard/orders-api-10") + "\n"},
		{name: "no marker", body: "Why\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasProposalMarker(tt.body, "standard/orders-api-1"); got != tt.want {
				t.Errorf("HasProposalMarker = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStandardChangeLabel(t *testing.T) {
	l := StandardChangeLabel()
	if l.Name != LabelStandardChange || l.Color != "#5319e7" || l.Exclusive || l.Description == "" {
		t.Errorf("StandardChangeLabel = %+v", l)
	}
}
