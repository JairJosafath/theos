package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
)

// TestDockerContextHasEmbeddedFiles fails when .dockerignore drops a file a
// //go:embed directive needs: the Dockerfile builds from the repository root,
// so such a file is missing from the image build while go build outside Docker
// still passes. It matches with the library Docker itself uses.
func TestDockerContextHasEmbeddedFiles(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(root, ".dockerignore"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	patterns, err := ignorefile.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	pm, err := patternmatcher.New(patterns)
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "list", "-f", "{{.Dir}}{{range .EmbedFiles}}\t{{.}}{{end}}", "./...")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	embedded := 0
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, "\t")
		for _, file := range fields[1:] {
			rel, err := filepath.Rel(root, filepath.Join(fields[0], file))
			if err != nil {
				t.Fatal(err)
			}
			rel = filepath.ToSlash(rel)
			embedded++
			excluded, err := pm.MatchesOrParentMatches(rel)
			if err != nil {
				t.Fatal(err)
			}
			if excluded {
				t.Errorf("%s is embedded (//go:embed) but .dockerignore leaves it out of the image build; re-include it there", rel)
			}
		}
	}
	if embedded == 0 {
		t.Fatal("go list found no embedded files; the test no longer sees the //go:embed directives")
	}
}
