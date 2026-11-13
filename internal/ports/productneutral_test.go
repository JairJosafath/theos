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

package ports

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// productNames are the products that sit, or are planned to sit, behind the
// ports of productNeutralFiles (docs/plans/requests-and-chat.md): the issue
// trackers and the chat hosts. They are matched without case anywhere in a
// line, so a type, a reason, a string and a comment that hold one all count.
// The match is of a substring, which is why GitHubIssue is found, and why a
// plain English word that holds a name is found too: "nonlinear" holds
// linear, and "matrix" is a chat host and a word of mathematics. A writer who
// meets one of them words the line another way ("not a straight line",
// "grid"); the list gets no exception for a word. The first name is the Git
// host of the chart, spelled in two halves because the host-neutral guard
// beside this one counts that word in every Go file.
var productNames = []string{"git" + "ea", "github", "gitlab", "linear", "jira", "slack", "matrix"}

// ownImports is what the path of an import of this module starts with. Such a
// path names where a package is kept (this module's own is on a code host),
// not a product behind the port. Only these paths are not read: an import of
// a product's SDK is read, and fails the guard.
const ownImports = "github.com/JairJosafath/theos/"

// productNeutralFiles are the ports more than one product sits behind, with
// their contract suites, by their slash-separated path from the repository's
// root. None of them names a product: what one product does its own way is
// its adapter's to say. The ChatHost port and its suite joined with #152.
var productNeutralFiles = []string{
	"internal/ports/intake.go",
	"internal/ports/contract/intake.go",
	"internal/ports/contract/teammembers.go",
	"internal/ports/chathost.go",
	"internal/ports/contract/chathost.go",
}

// TestProductNeutral is the guard of #151 and #152: the request intake port,
// the chat port and their suites name no product, in a type, a reason or a
// comment.
func TestProductNeutral(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range productNeutralFiles {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			// A file that moved is no longer guarded: the list follows it.
			t.Errorf("%s: %v; productNeutralFiles names the file where it is", file, err)
			continue
		}
		for _, msg := range namedProducts(file, string(b)) {
			t.Error(msg)
		}
	}
}

// namedProducts returns one message per line of text that names a product,
// in the order of the lines, each starting with file and the line's number.
// The paths of imports of this module are not read (ownImports); any other
// text of a line is, a string included. text must be a Go file: one that does
// not parse is one message, and the guard fails on it.
func namedProducts(file, text string) []string {
	// ImportsOnly stops at the first declaration; the imports are all that is
	// needed of the syntax.
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, text, parser.ImportsOnly)
	if err != nil {
		return []string{fmt.Sprintf("%s: does not parse, so the guard can't tell an import of this module from a product's name: %v", file, err)}
	}
	// Blank the paths of this module's imports, in place, so that every line
	// keeps its number.
	b := []byte(text)
	for _, spec := range f.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || !strings.HasPrefix(path, ownImports) {
			continue
		}
		for i := fset.Position(spec.Path.Pos()).Offset; i < fset.Position(spec.Path.End()).Offset; i++ {
			b[i] = ' '
		}
	}
	var msgs []string
	for i, line := range strings.Split(string(b), "\n") {
		lower := strings.ToLower(line)
		var found []string
		for _, product := range productNames {
			if strings.Contains(lower, product) {
				found = append(found, product)
			}
		}
		if len(found) > 0 {
			msgs = append(msgs, fmt.Sprintf("%s:%d: names %s; a port with more than one product behind it names none: "+
				"say tracker, host or credential here, and what one product does in its adapter under internal/adapters",
				file, i+1, strings.Join(found, " and ")))
		}
	}
	return msgs
}

// TestNamedProducts covers #151: the guard finds a product's name in a
// type, a reason and a comment, whatever its case, and nothing in a port
// that says tracker.
func TestNamedProducts(t *testing.T) {
	// The names are put together here, so that this file can't trip a guard
	// that counts one of them.
	hub, lab := "Git"+"Hub", "Git"+"Lab"
	tests := []struct {
		name string
		text string
		want []string
	}{
		{"a port that names its tracker", "package p\n\n// TrackerIssue is an open issue of a tracker.\ntype TrackerIssue struct{}", nil},
		{"a type", "package ports\n\ntype " + hub + "Issue struct{}",
			[]string{"p.go:3: names github;"}},
		{"a reason and its value", "package p\n\nconst Reason" + hub + `Error = "` + hub + `Error"`,
			[]string{"p.go:3: names github;"}},
		{"a comment, in capitals", "package p\n\n// an issue on " + strings.ToUpper(hub) + "\n// or a ticket on JIRA",
			[]string{"p.go:3: names github;", "p.go:4: names jira;"}},
		{"two products on a line", "package p\n\n// " + hub + "'s teams, which " + lab + " has too",
			[]string{"p.go:3: names github and gitlab;"}},
		{"a chat host", "package p\n\n// a room on Matrix, a channel on Slack",
			[]string{"p.go:3: names slack and matrix;"}},
		{"this module's imports, alone, named and in a block", "package p\n\nimport \"" + ownImports + "internal/ports\"\n" +
			"import (\n\t\"" + ownImports + "a\"\n\tb \"" + ownImports + "b\"\n)", nil},
		{"a returned string, #151", "package p\n\nfunc f() string {\n\treturn \"" + hub + "Error\"\n}",
			[]string{"p.go:4: names github;"}},
		{"a string alone on a continuation line, #151", "package p\n\nconst x = \"a\" +\n\t\"" + hub + "Error\"",
			[]string{"p.go:4: names github;"}},
		{"a field's tag, #151", "package p\n\ntype T struct {\n\tports.IssueTracker \"jira\"\n}",
			[]string{"p.go:4: names jira;"}},
		{"a product's SDK imported, #151", "package p\n\nimport \"" + strings.ToLower(hub) + ".com/andygrunwald/go-jira\"",
			[]string{"p.go:3: names github and jira;"}},
		{"a file that does not parse", "an import's path", []string{"p.go: does not parse"}},
		{"a string that is no import", "package p\n\nvar x = \"" + strings.ToLower(hub) + ".com/acme/app\"",
			[]string{"p.go:3: names github;"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := namedProducts("p.go", tt.text)
			if len(got) != len(tt.want) {
				t.Fatalf("namedProducts() = %q, want %d messages", got, len(tt.want))
			}
			for i, prefix := range tt.want {
				if !strings.HasPrefix(got[i], prefix) {
					t.Errorf("message %d = %q, want it to start %q", i, got[i], prefix)
				}
			}
		})
	}
}
