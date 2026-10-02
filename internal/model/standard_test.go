package model

import (
	"reflect"
	"strings"
	"testing"
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

func TestStandardChangeLabel(t *testing.T) {
	l := StandardChangeLabel()
	if l.Name != LabelStandardChange || l.Color != "#5319e7" || l.Exclusive || l.Description == "" {
		t.Errorf("StandardChangeLabel = %+v", l)
	}
}
