package model

import (
	"slices"
	"testing"
)

func TestAgentProtectedPaths(t *testing.T) {
	got := AgentProtectedPaths(giteaActions, []string{"Makefile", ".gitea/", "docs/style/"})
	want := []string{".c4factory.yaml", ".devcontainer/", ".gitea/", "Makefile", "docs/style/"}
	if !slices.Equal(got, want) {
		t.Errorf("AgentProtectedPaths() = %v, want %v", got, want)
	}
	if got := AgentProtectedPaths(giteaActions, nil); !slices.Equal(got, PlatformProtectedPaths(giteaActions)) {
		t.Errorf("AgentProtectedPaths(nil) = %v, want the platform's", got)
	}
	other := CI{Name: "other", ProtectedPaths: []string{".other-ci.yml"}}
	want = []string{".c4factory.yaml", ".devcontainer/", ".other-ci.yml", "Makefile"}
	if got := AgentProtectedPaths(other, []string{"Makefile"}); !slices.Equal(got, want) {
		t.Errorf("AgentProtectedPaths(other) = %v, want %v", got, want)
	}
}

// A cluster DNS address block (#1257) is one inside the private ranges the
// fences keep HTTPS from; a public resolver or anywhere is not.
func TestPrivateBlock(t *testing.T) {
	for _, tt := range []struct {
		cidr string
		want bool
	}{
		{"169.254.20.10/32", true}, // NodeLocal DNSCache
		{"10.96.0.10/32", true},    // a kube-dns Service address
		{"172.20.0.0/16", true},
		{"10.0.0.0/8", true},
		{"8.8.8.8/32", false},
		{"0.0.0.0/0", false},
		{"10.0.0.0/7", false}, // wider than 10/8
		{"169.254.20.10", false},
		{"fd00::10/128", false},
		{"", false},
	} {
		if got := PrivateBlock(tt.cidr); got != tt.want {
			t.Errorf("PrivateBlock(%q) = %v, want %v", tt.cidr, got, tt.want)
		}
	}
}

func TestNeverPathNamed(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"Add a note to AGENTS.md", "AGENTS.md"},
		{"Add a note to AGENTS.md.", "AGENTS.md"},
		{"- [ ] `AGENTS.md` says to run make check", "AGENTS.md"},
		{"Write ./AGENTS.md", "./AGENTS.md"},
		{"Update AGENTS.md...", "AGENTS.md"},
		{"(see AGENTS.md..)", "AGENTS.md"},
		{"- [ ] `docs/agents/reviewer.md` says so", "docs/agents/reviewer.md"},
		{"Change docs/agents/reviewer.md.", "docs/agents/reviewer.md"},
		{"A new guide in docs/agents", "docs/agents"},
		{"Set the check in .c4factory.yaml", ".c4factory.yaml"},
		{"First AGENTS.md, then .c4factory.yaml", "AGENTS.md"},
		{"Then .c4factory.yaml and AGENTS.md", ".c4factory.yaml"},
		{"Write docs/project-notes.md", ""},
		{"Add pkg/AGENTS.md for the package", ""},
		{"Keep AGENTS.md.bak", ""},
		{"Keep MY_AGENTS.md", ""},
		{"Read mydocs/agents/x.md", ""},
		{"Read docs/agents-old/x.md", ""},
		{"Write a.c4factory.yaml", ""},
		{"Agents read the notes", ""},
	} {
		if got := NeverPathNamed(tc.text); got != tc.want {
			t.Errorf("NeverPathNamed(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestWithoutNeverPaths(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{"Add CI note to `./AGENTS.md`", "Add CI note to a protected file"},
		{"Edit docs/agents/x.md and `.c4factory.yaml`.", "Edit a protected file and a protected file."},
		{"Write docs/project-notes.md", "Write docs/project-notes.md"},
	} {
		if got := withoutNeverPaths(tc.text); got != tc.want {
			t.Errorf("withoutNeverPaths(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}
