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
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// productNames are the products that sit, or are planned to sit, behind the
// ports of productNeutralFiles (docs/plans/requests-and-chat.md): the issue
// trackers and the chat hosts. They are matched without case anywhere in a
// line, so a type, a reason and a comment that hold one all count. The first
// is the Git host of the chart, spelled in two halves because the
// host-neutral guard beside this one counts that word in every Go file.
var productNames = []string{"git" + "ea", "github", "gitlab", "linear", "jira", "slack", "matrix"}

// importLine is a line of an import block, or a one-line import: a path in
// quotes, with or without a name before it. A path names where a package is
// kept (this module's own is on a code host), not a product behind the port.
var importLine = regexp.MustCompile(`^\s*(import\s+)?([\w.]+\s+)?"[^"\s]+"\s*$`)

// productNeutralFiles are the ports more than one product sits behind, with
// their contract suites, by their slash-separated path from the repository's
// root. None of them names a product: what one product does its own way is
// its adapter's to say.
//
// The ChatHost port, internal/ports/chathost.go, and its suite,
// internal/ports/contract/chathost.go, join the list when step 2 of
// docs/plans/requests-and-chat.md (#152) has renamed what they hold.
var productNeutralFiles = []string{
	"internal/ports/intake.go",
	"internal/ports/contract/intake.go",
	"internal/ports/contract/teammembers.go",
}

// TestProductNeutral is the guard of #151: the request intake port and its
// suites name no product, in a type, a reason or a comment.
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
// An import's path is not read.
func namedProducts(file, text string) []string {
	var msgs []string
	for i, line := range strings.Split(text, "\n") {
		if importLine.MatchString(line) {
			continue
		}
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
		{"a port that names its tracker", "// TrackerIssue is an open issue of a tracker.\ntype TrackerIssue struct{}", nil},
		{"a type", "package ports\n\ntype " + hub + "Issue struct{}",
			[]string{"p.go:3: names github;"}},
		{"a reason and its value", "\tReason" + hub + `Error = "` + hub + `Error"`,
			[]string{"p.go:1: names github;"}},
		{"a comment, in capitals", "// an issue on " + strings.ToUpper(hub) + "\n// or a ticket on JIRA",
			[]string{"p.go:1: names github;", "p.go:2: names jira;"}},
		{"two products on a line", "// " + hub + "'s teams, which " + lab + " has too",
			[]string{"p.go:1: names github and gitlab;"}},
		{"a chat host", "// a room on Matrix, a channel on Slack",
			[]string{"p.go:1: names slack and matrix;"}},
		{"an import's path, alone, named and in a block",
			"import \"" + strings.ToLower(hub) + ".com/acme/app/internal/ports\"\nimport (\n\t\"" + strings.ToLower(hub) +
				".com/acme/app/a\"\n\tb \"" + strings.ToLower(lab) + ".com/acme/app/b\"\n)", nil},
		{"a string that is no import", "\treturn \"" + strings.ToLower(hub) + ".com/acme/app\", nil",
			[]string{"p.go:1: names github;"}},
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
