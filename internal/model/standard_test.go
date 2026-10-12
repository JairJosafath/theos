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
		{"gated: other protected files refused, unprotected ones the Task's",
			[]string{"Makefile", "release_test.go", ".gitea/workflows/check.yaml", "AGENTS.md"}, std, []string{"Makefile"}, true,
			ChangeSplit{Task: []string{"Makefile", "release_test.go"}, Refused: []string{".gitea/workflows/check.yaml", "AGENTS.md"}}},
		{"gated: a protected path in another case refused", []string{"Makefile", "makefile", "Agents.md"}, std,
			[]string{"Makefile"}, true, ChangeSplit{Task: []string{"Makefile"}, Refused: []string{"makefile", "Agents.md"}}},
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

func TestProposalBranchOf(t *testing.T) {
	marker := ProposalMarker("standard/orders-api-1")
	for _, tt := range []struct {
		name, body, want string
	}{
		{name: "on its own line", body: "Why\n\n" + marker + "\n", want: "standard/orders-api-1"},
		{name: "with carriage returns and spaces", body: "Why\r\n  " + marker + " \r\n", want: "standard/orders-api-1"},
		{name: "as the issue writes it", body: func() string {
			_, body := StandardChangeIssue(1, "orders-api-1", Proposal{Branch: "standard/orders-api-1",
				Paths: []string{"Makefile"}, Reason: "make dist"})
			return body
		}(), want: "standard/orders-api-1"},
		{name: "inside a line", body: "See " + marker + " here\n"},
		{name: "empty branch", body: ProposalMarker("") + "\n"},
		{name: "no marker", body: "Why\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := ProposalBranchOf(tt.body); got != tt.want {
				t.Errorf("ProposalBranchOf = %q, want %q", got, tt.want)
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

func TestStandardUpdateNames(t *testing.T) {
	for _, tt := range []struct{ name, ref, want string }{
		{"go-standard", "v1.1.1", "standard-update/go-standard-v1.1.1"},
		{"go-standard", "release/v1.1.1+build_2", "standard-update/go-standard-release-v1.1.1-build_2"},
		{"go-standard", "0a1b2c3d", "standard-update/go-standard-0a1b2c3d"},
		{"go-standard", "v1~é", "standard-update/go-standard-v1--"},
	} {
		if got := StandardUpdateBranch(tt.name, tt.ref); got != tt.want {
			t.Errorf("StandardUpdateBranch(%q, %q) = %q, want %q", tt.name, tt.ref, got, tt.want)
		}
	}
	if got := StandardUpdateTitle("go-standard", "v1.1.1"); got != "Update the Standard go-standard to v1.1.1" {
		t.Errorf("StandardUpdateTitle = %q", got)
	}
}

func TestStandardUpdateBody(t *testing.T) {
	paths := []string{"AGENTS.md", "docs/agents/implementer.md"}
	closing := "The operator merges this pull request itself once CI passes. " +
		"Close it to drop the update until the Standard's ref changes again."
	for _, tt := range []struct {
		name    string
		leftOut []string
	}{
		{name: "everything planned"},
		{name: "some files left out", leftOut: []string{"Makefile", "docs/style/go.md"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := StandardUpdateBody("go-standard", "v1.0.0", "v1.1.1", paths, tt.leftOut)
			for _, want := range []string{"`go-standard`", "`v1.0.0`", "`v1.1.1`", "## Files",
				"- `AGENTS.md`\n", "- `docs/agents/implementer.md`\n", closing} {
				if !strings.Contains(body, want) {
					t.Errorf("body lacks %q:\n%s", want, body)
				}
			}
			if got := strings.Contains(body, "## Left out"); got != (len(tt.leftOut) > 0) {
				t.Errorf("body has a Left out section = %v, want %v:\n%s", got, len(tt.leftOut) > 0, body)
			}
			for _, p := range tt.leftOut {
				if _, after, _ := strings.Cut(body, "## Left out"); !strings.Contains(after, "- `"+p+"`\n") {
					t.Errorf("Left out section lacks %q:\n%s", p, body)
				}
			}
			if !strings.HasSuffix(body, closing+"\n") {
				t.Errorf("body doesn't end with the closing line:\n%s", body)
			}
		})
	}
}

func TestStandardUpdateRefusal(t *testing.T) {
	planned := []string{"AGENTS.md", "docs/agents/implementer.md"}
	ci := map[string]string{"check / check (pull_request)": "success"}
	tests := []struct {
		name             string
		checks           string
		contexts         map[string]string
		changed, planned []string
		want             string // "" means merge
	}{
		{name: "merges", checks: "success", contexts: ci, changed: planned, planned: planned},
		{name: "part of the plan", checks: "success", contexts: ci, changed: planned[:1], planned: planned},
		{name: "a skipped push check", checks: "success", changed: planned, planned: planned,
			contexts: map[string]string{"check / check (pull_request)": "success", "check / check (push)": "skipped"}},
		{name: "CI pending", checks: "pending", contexts: ci, changed: planned, planned: planned,
			want: "CI is pending, not success"},
		{name: "CI failed", checks: "failure", contexts: ci, changed: planned, planned: planned,
			want: "CI is failure, not success"},
		{name: "no CI yet", contexts: ci, changed: planned, planned: planned,
			want: "CI is unknown reason, not success"},
		{name: "check context missing", checks: "success", changed: planned, planned: planned,
			contexts: map[string]string{"CI / check (pull_request)": "success"},
			want:     `no passing check matches "check*", which main requires`},
		{name: "a required check not passed", checks: "success", changed: planned, planned: planned,
			contexts: map[string]string{"check / check (pull_request)": "success", "check / lint (pull_request)": "pending"},
			want:     `no passing check matches "check*", which main requires`},
		{name: "empty change", checks: "success", contexts: ci, planned: planned,
			want: "the change changes nothing"},
		{name: "a path outside the plan", checks: "success", contexts: ci, planned: planned,
			changed: []string{"AGENTS.md", "README.md", "src/main.go"},
			want:    "the pull request changes README.md and src/main.go, which the update did not plan"},
		{name: "a planned directory covers nothing", checks: "success", contexts: ci, planned: []string{"docs/agents/"},
			changed: []string{"docs/agents/implementer.md"},
			want:    "the pull request changes docs/agents/implementer.md, which the update did not plan"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StandardUpdateRefusal(tt.checks, tt.contexts, tt.changed, tt.planned); got != tt.want {
				t.Errorf("StandardUpdateRefusal() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestScaffoldedRef(t *testing.T) {
	from := []string{"go-gin@v1.2.0", "go-standard-strict@v2.0.0", "go-standard@v1.0.0", "postgres@v1.1.0"}
	for _, tt := range []struct {
		name string
		from []string
		want string
	}{
		{name: "go-standard", from: from, want: "v1.0.0"},
		{name: "go-gin", from: from, want: "v1.2.0"},
		{name: "go-standard-strict", from: from, want: "v2.0.0"},
		{name: "go", from: from},
		{name: "ts-standard", from: from},
		{name: "go-standard"},
	} {
		if got := ScaffoldedRef(tt.from, tt.name); got != tt.want {
			t.Errorf("ScaffoldedRef(%v, %q) = %q, want %q", tt.from, tt.name, got, tt.want)
		}
	}
}
