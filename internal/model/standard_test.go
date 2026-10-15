package model

import (
	"fmt"
	"maps"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitChange(t *testing.T) {
	std := AgentProtectedPaths(giteaActions, []string{"AGENTS.md", "Makefile", "docs/agents/"})
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
		{"unprotected AGENTS.md is the Task's", []string{"AGENTS.md"}, AgentProtectedPaths(giteaActions, nil), nil, true,
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
		if got := StandardUpdateBranch(tt.name, StandardVersion{Ref: tt.ref}); got != tt.want {
			t.Errorf("StandardUpdateBranch(%q, %q) = %q, want %q", tt.name, tt.ref, got, tt.want)
		}
	}
	before := StandardVersion{Ref: "v1.1.1", Checks: ChecksBeforeRelease}
	if got, want := StandardUpdateBranch("go-standard", before),
		"standard-update/go-standard-v1.1.1-before-release"; got != want {
		t.Errorf("StandardUpdateBranch(BeforeRelease) = %q, want %q", got, want)
	}
	if got := StandardUpdateBranch("go-standard", StandardVersion{Ref: "v1.1.1", Checks: ChecksEveryChange}); got !=
		"standard-update/go-standard-v1.1.1" {
		t.Errorf("StandardUpdateBranch(EveryChange) = %q", got)
	}
	for _, tt := range []struct {
		from, to StandardVersion
		want     string
	}{
		{StandardVersion{Ref: "v1.0.0"}, StandardVersion{Ref: "v1.1.1"}, "Update the Standard go-standard to v1.1.1"},
		{StandardVersion{Ref: "v1.0.0", Checks: ChecksBeforeRelease}, before,
			"Update the Standard go-standard to v1.1.1"},
		{StandardVersion{Ref: "v1.1.1"}, before,
			"Update the Standard go-standard to v1.1.1, with the full check before release"},
		{before, StandardVersion{Ref: "v1.1.1", Checks: ChecksEveryChange},
			"Update the Standard go-standard to v1.1.1, with the full check on every change"},
	} {
		if got := StandardUpdateTitle("go-standard", tt.from, tt.to); got != tt.want {
			t.Errorf("StandardUpdateTitle(%v, %v) = %q, want %q", tt.from, tt.to, got, tt.want)
		}
	}
	if got := before.String(); got != "v1.1.1 with the full check before release" {
		t.Errorf("String() = %q", got)
	}
}

func TestStandardUpdateBody(t *testing.T) {
	paths := []string{"AGENTS.md", "docs/agents/implementer.md"}
	closing := "The operator merges this pull request itself once CI passes. " +
		"Close it to drop the update until the Standard's ref or the Container's checks change again."
	for _, tt := range []struct {
		name    string
		merged  []string
		leftOut LeftOut
	}{
		{name: "everything planned"},
		{name: "some files left out", leftOut: LeftOut{Files: []LeftOutFile{{Path: "Makefile", Ref: "v1.0.0"},
			{Path: "docs/style/go.md", Ref: "v0.9.0"}}}},
		{name: "a file merged", merged: []string{"AGENTS.md"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := StandardUpdateBody("go-standard", StandardVersion{Ref: "v1.0.0"}, StandardVersion{Ref: "v1.1.1"},
				paths, tt.merged, tt.leftOut)
			if strings.Contains(body, "full check") {
				t.Errorf("body of an update that keeps the checks names them:\n%s", body)
			}
			for _, want := range []string{"`go-standard`", "`v1.0.0`", "`v1.1.1`", "## Files",
				"- `AGENTS.md`\n", "- `docs/agents/implementer.md`\n", closing} {
				if !strings.Contains(body, want) {
					t.Errorf("body lacks %q:\n%s", want, body)
				}
			}
			if got := strings.Contains(body, "## Left out"); got != (len(tt.leftOut.Files) > 0) {
				t.Errorf("body has a Left out section = %v, want %v:\n%s", got, len(tt.leftOut.Files) > 0, body)
			}
			const mergedTitle = "## Merged with the repository's changes"
			if got := strings.Contains(body, mergedTitle); got != (len(tt.merged) > 0) {
				t.Errorf("body has a Merged section = %v, want %v:\n%s", got, len(tt.merged) > 0, body)
			}
			for _, p := range tt.merged {
				if _, after, _ := strings.Cut(body, mergedTitle); !strings.Contains(after, "- `"+p+"`\n") {
					t.Errorf("Merged section lacks %q:\n%s", p, body)
				}
			}
			for _, f := range tt.leftOut.Files {
				if _, after, _ := strings.Cut(body, "## Left out"); !strings.Contains(after, "### `"+f.Path+"`\n") {
					t.Errorf("Left out section lacks %q:\n%s", f.Path, body)
				}
			}
			if !strings.HasSuffix(body, closing+"\n") {
				t.Errorf("body doesn't end with the closing line:\n%s", body)
			}
		})
	}
}

func TestStandardUpdateBodyChangingChecks(t *testing.T) {
	body := StandardUpdateBody("go-standard", StandardVersion{Ref: "v1.1.1"},
		StandardVersion{Ref: "v1.1.1", Checks: ChecksBeforeRelease}, []string{".gitea/workflows/check.yaml"}, nil, LeftOut{})
	want := "from `v1.1.1` to `v1.1.1`, and CI from running the full check on every change to running it before release.\n"
	if !strings.Contains(body, want) {
		t.Errorf("body lacks %q:\n%s", want, body)
	}
}

func TestStandardUpdateBodyLeftOut(t *testing.T) {
	take := "kubectl -n shop annotate container orders-api c4.factory.io/standard-take=v1.2.1"
	keep := "kubectl -n shop annotate container orders-api c4.factory.io/standard-keep=v1.2.1"
	makefile := LeftOutFile{Path: "Makefile", Ref: "v1.1.6",
		Diff: []string{"@@ -1,2 +1,2 @@", " check:", "-\ttrue", "+\tmake lint test"},
		Take: TakenFile{Path: "Makefile", Dropped: [][]string{{"\tpytest\n"}}}}
	body := StandardUpdateBody("go-standard", StandardVersion{Ref: "v1.1.6"}, StandardVersion{Ref: "v1.2.1"},
		[]string{"AGENTS.md"}, nil, LeftOut{Take: take, Keep: keep, Files: []LeftOutFile{makefile,
			{Path: "logo.png", Ref: "v1.0.0", Note: "It is a binary file, so no diff is shown.",
				Take: TakenFile{Path: "logo.png", Note: "It doesn't merge line by line (binary file), so the " +
					"Standard's file replaces the repository's whole."}},
			{Path: "docs/style.md", Ref: "v1.1.6", Diff: []string{"@@ -1 +1 @@", "-a", "+b"},
				Take: TakenFile{Path: "docs/style.md"}},
		}})
	for _, want := range []string{
		"- take the Standard's version: `" + take + "`\n- or keep the repository's: `" + keep + "`\n",
		"### `Makefile`\n\nWhat the Standard changed in it from `v1.1.6` to `v1.2.1`\n\n" +
			"```diff\n--- Makefile (v1.1.6)\n+++ Makefile (v1.2.1)\n@@ -1,2 +1,2 @@\n check:\n-\ttrue\n+\tmake lint test\n```\n\n" +
			"The repository's lines taking it drops:\n\n```diff\n-\tpytest\n```\n\n",
		"### `logo.png`\n\nWhat the Standard changed in it from `v1.0.0` to `v1.2.1`: It is a binary file, so no " +
			"diff is shown.\n\nTaking it: It doesn't merge line by line (binary file), so the Standard's file " +
			"replaces the repository's whole.\n\n### ",
		"+b\n```\n\nTaking it drops none of the repository's lines.\n\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}

	t.Run("a long diff is cut", func(t *testing.T) {
		diff := make([]string, 0, 300)
		diff = append(diff, "@@ -1,300 +1,300 @@")
		for n := range 299 {
			diff = append(diff, fmt.Sprintf("+line %d", n))
		}
		body := StandardUpdateBody("go-standard", StandardVersion{Ref: "v1.1.6"}, StandardVersion{Ref: "v1.2.1"},
			nil, nil, LeftOut{Take: take, Keep: keep, Files: []LeftOutFile{{Path: "f", Ref: "v1.1.6", Diff: diff}}})
		if !strings.Contains(body, "+line 198\n```\n\nAnd 100 more lines.\n") || strings.Contains(body, "+line 199\n") {
			t.Errorf("the diff isn't cut after 200 lines and its header:\n%s", body)
		}
	})

	t.Run("a body past its bound names the rest", func(t *testing.T) {
		files := make([]LeftOutFile, 0, 40)
		for n := range 40 {
			diff := make([]string, 0, 200)
			diff = append(diff, "@@ -1,200 +1,200 @@")
			for range 199 {
				diff = append(diff, "+"+strings.Repeat("x", 20))
			}
			files = append(files, LeftOutFile{Path: fmt.Sprintf("f%d", n), Ref: "v1.1.6", Diff: diff})
		}
		body := StandardUpdateBody("go-standard", StandardVersion{Ref: "v1.1.6"}, StandardVersion{Ref: "v1.2.1"},
			nil, nil, LeftOut{Take: take, Keep: keep, Files: files})
		if len(body) > maxBodyLength {
			t.Errorf("body is %d characters, past %d", len(body), maxBodyLength)
		}
		if !strings.Contains(body, "### `f0`\n\nWhat the Standard changed") ||
			!strings.Contains(body, ", `f39`.\n\n") || strings.Contains(body, "### `f39`") {
			t.Errorf("body doesn't show the first file and name the last:\n%s", body)
		}
	})

	t.Run("a body past its bound names as many files as fit", func(t *testing.T) {
		files := make([]LeftOutFile, 0, 3000)
		for n := range 3000 {
			files = append(files, LeftOutFile{Path: fmt.Sprintf("docs/a-long-directory-name/file-%d.md", n), Ref: "v1.1.6",
				Diff: []string{"@@ -1 +1 @@", "-" + strings.Repeat("a", 400), "+" + strings.Repeat("b", 400)}})
		}
		body := StandardUpdateBody("go-standard", StandardVersion{Ref: "v1.1.6"}, StandardVersion{Ref: "v1.2.1"},
			nil, nil, LeftOut{Take: take, Keep: keep, Files: files})
		if len(body) > maxBodyLength {
			t.Errorf("body is %d characters, past %d", len(body), maxBodyLength)
		}
		if !strings.Contains(body, " more files.\n\n") {
			t.Errorf("body doesn't count the files it can't name:\n%s", body[len(body)-500:])
		}
	})

	t.Run("a body with thousands of files keeps every section within its bound", func(t *testing.T) {
		many := func(dir string) []string {
			paths := make([]string, 0, 3000)
			for n := range 3000 {
				paths = append(paths, fmt.Sprintf("%s/a-long-directory-name/file-%d.md", dir, n))
			}
			return paths
		}
		files := make([]LeftOutFile, 0, 3000)
		for _, path := range many("left") {
			files = append(files, LeftOutFile{Path: path, Ref: "v1.1.6",
				Diff: []string{"@@ -1 +1 @@", "-" + strings.Repeat("a", 400), "+" + strings.Repeat("b", 400)}})
		}
		body := StandardUpdateBody("go-standard", StandardVersion{Ref: "v1.1.6"}, StandardVersion{Ref: "v1.2.1"},
			many("files"), many("merged"), LeftOut{Take: take, Keep: keep, Files: files})
		if len(body) > maxBodyLength {
			t.Errorf("body is %d characters, past %d", len(body), maxBodyLength)
		}
		for _, want := range []string{"## Files\n\n- `files/", " more\n\n## Merged with the repository's changes\n",
			"- `merged/", " more\n\n## Left out\n", "### `left/", " more files.\n\n",
			"The operator merges this pull request itself"} {
			if !strings.Contains(body, want) {
				t.Errorf("body doesn't hold %q", want)
			}
		}
	})
}

func TestBehindChanges(t *testing.T) {
	makefile := LeftOutFile{Path: "Makefile", Ref: "v1.1.6",
		Diff: []string{"@@ -1,2 +1,2 @@", " check:", "-\ttrue", "+\tmake lint test"},
		Take: TakenFile{Path: "Makefile", Dropped: [][]string{{"\tpytest\n"}, {"\tmypy\r\n"}}}}
	logo := LeftOutFile{Path: "logo.png", Ref: "v1.0.0", Note: "It is a binary file, so no diff is shown.",
		Take: TakenFile{Path: "logo.png", Note: "the Standard's file replaces the repository's whole."}}
	style := LeftOutFile{Path: "docs/style.md", Ref: "v1.1.6", Diff: []string{"@@ -1 +1 @@", "-a", "+b"}}
	got := BehindChanges("v1.2.1", []LeftOutFile{makefile, logo, style})
	want := map[string]string{
		"Makefile": "--- Makefile (v1.1.6)\n+++ Makefile (v1.2.1)\n@@ -1,2 +1,2 @@\n check:\n-\ttrue\n" +
			"+\tmake lint test\n\nThe repository's lines taking it drops:\n-\tpytest\n@@\n-\tmypy",
		"logo.png": "It is a binary file, so no diff is shown.\n\n" +
			"Taking it: the Standard's file replaces the repository's whole.",
		"docs/style.md": "--- docs/style.md (v1.1.6)\n+++ docs/style.md (v1.2.1)\n@@ -1 +1 @@\n-a\n+b\n\n" +
			"Taking it drops none of the repository's lines.",
	}
	if !maps.Equal(got, want) {
		t.Errorf("BehindChanges = %q, want %q", got, want)
	}

	t.Run("a long change is cut at a line", func(t *testing.T) {
		diff := make([]string, 0, 300)
		diff = append(diff, "@@ -1,300 +1,300 @@")
		for n := range 299 {
			diff = append(diff, fmt.Sprintf("+line %d of the Standard's longer file", n))
		}
		got := BehindChanges("v1.2.1", []LeftOutFile{{Path: "f", Ref: "v1.1.6", Diff: diff}})["f"]
		if len(got) > maxBehindChange {
			t.Errorf("change is %d bytes, past %d", len(got), maxBehindChange)
		}
		last := got[strings.LastIndex(got, "\n")+1:]
		if !strings.HasPrefix(last, "… ") || !strings.HasSuffix(last, " more lines not shown") ||
			!strings.HasPrefix(got, "--- f (v1.1.6)\n+++ f (v1.2.1)\n@@") {
			t.Errorf("change isn't cut at a line with a count:\n%s", got)
		}
	})

	t.Run("one line past the bound is cut at a character", func(t *testing.T) {
		got := BehindChanges("v1.2.1", []LeftOutFile{{Path: "f", Ref: "v1.1.6", Note: strings.Repeat("é", 3000)}})["f"]
		if len(got) > maxBehindChange || !utf8.ValidString(got) || !strings.HasPrefix(got, "éé") ||
			!strings.HasSuffix(got, "\n… 2 more lines not shown") {
			t.Errorf("change of %d bytes isn't cut at a character:\n%s", len(got), got[len(got)-80:])
		}
	})

	t.Run("only the first files carry their change", func(t *testing.T) {
		files := make([]LeftOutFile, 0, 20)
		for n := range 20 {
			files = append(files, LeftOutFile{Path: fmt.Sprintf("f%02d", n), Ref: "v1.1.6",
				Diff: []string{"@@ -1 +1 @@", "-a", "+b"}})
		}
		got := BehindChanges("v1.2.1", files)
		if !strings.HasPrefix(got["f15"], "--- f15") {
			t.Errorf("f15 lacks its change: %q", got["f15"])
		}
		if want := "Not shown: only the first 16 of the 20 files left out carry the Standard's change here."; got["f16"] != want || got["f19"] != want {
			t.Errorf("f16 = %q, f19 = %q, want %q", got["f16"], got["f19"], want)
		}
	})
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
			if got := StandardUpdateRefusal(giteaActions, tt.checks, tt.contexts, tt.changed, tt.planned); got != tt.want {
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

func TestEffectiveChecks(t *testing.T) {
	tests := []struct {
		asked       string
		standardCan bool
		want        string
	}{
		{ChecksEveryChange, false, ChecksEveryChange},
		{ChecksEveryChange, true, ChecksEveryChange},
		{ChecksBeforeRelease, false, ChecksEveryChange},
		{ChecksBeforeRelease, true, ChecksBeforeRelease},
		// A Container read before the field had a default asks for nothing.
		{"", true, ChecksEveryChange},
	}
	for _, tt := range tests {
		if got := EffectiveChecks(tt.asked, tt.standardCan); got != tt.want {
			t.Errorf("EffectiveChecks(%q, %v) = %q, want %q", tt.asked, tt.standardCan, got, tt.want)
		}
	}
}

func TestStandardTakeBranchAndTitle(t *testing.T) {
	if got, want := StandardTakeBranch("go-standard", "v1.2.0+b/1"), "standard-take/go-standard-v1.2.0-b-1"; got != want {
		t.Errorf("StandardTakeBranch() = %q, want %q", got, want)
	}
	tests := []struct {
		paths []string
		want  string
	}{
		{[]string{"Makefile"}, "Bring Makefile up to date with go-standard v1.2.0"},
		{[]string{"Makefile", "a", "b"}, "Bring Makefile, a and b up to date with go-standard v1.2.0"},
		{[]string{"Makefile", "a", "b", "c"}, "Bring 4 files up to date with go-standard v1.2.0"},
	}
	for _, tt := range tests {
		if got := StandardTakeTitle("go-standard", "v1.2.0", tt.paths); got != tt.want {
			t.Errorf("StandardTakeTitle(%v) = %q, want %q", tt.paths, got, tt.want)
		}
	}
}

func TestStandardTakeBody(t *testing.T) {
	many := make([]string, 150)
	for i := range many {
		many[i] = fmt.Sprintf("line %d\n", i)
	}
	body := StandardTakeBody("go-standard", "v1.2.0", []TakenFile{
		{Path: ".gitea/workflows/check.yaml", Dropped: [][]string{{"    runs-on: big\r\n"}, {"a ``` fence\n"}}},
		{Path: "Makefile"},
		{Path: "logo.png", Note: "It isn't text: the Standard's file replaces the repository's whole."},
		{Path: "big.txt", Dropped: [][]string{many}},
	})
	for _, want := range []string{
		"`go-standard` left out, at `v1.2.0`",
		"## `.gitea/workflows/check.yaml`\n\nThe repository's lines it drops:\n\n````diff\n-    runs-on: big\n@@\n-a ``` fence\n````\n",
		"## `Makefile`\n\nDrops none of the repository's lines.\n",
		"## `logo.png`\n\nIt isn't text: the Standard's file replaces the repository's whole.\n\n## `big.txt`",
		"-line 99\n```\n\nAnd 50 more lines.\n",
		"Close it to keep the files as they are",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
}
