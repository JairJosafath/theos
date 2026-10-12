package model

import "testing"

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
