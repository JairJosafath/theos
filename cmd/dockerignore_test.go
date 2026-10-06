package main

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
)

// goBuild finds a go build command in a Dockerfile, after its line
// continuations are joined.
var goBuild = regexp.MustCompile(`\bgo build\b([^;&|\n]*)`)

// TestDockerContextHasEmbeddedFiles fails when a Dockerfile's ignore file
// drops a file a //go:embed directive of the packages it builds needs: such a
// file is missing from the image build while go build outside Docker still
// passes. It checks every Dockerfile that builds this module (it copies
// go.mod) from the repository root, against its Dockerfile.dockerignore or
// else the root .dockerignore, and matches with the library Docker itself uses.
func TestDockerContextHasEmbeddedFiles(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	var dockerfiles []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".claude", ".dev", "bin", "node_modules", "templates", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "Dockerfile" {
			dockerfiles = append(dockerfiles, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	checked := map[string]bool{}
	embedded := 0
	for _, dockerfile := range dockerfiles {
		rel, _ := filepath.Rel(root, dockerfile)
		data, err := os.ReadFile(dockerfile)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ReplaceAll(string(data), "\\\n", " ")
		if !regexp.MustCompile(`(?m)^COPY go\.mod\b`).MatchString(text) {
			continue
		}
		var targets []string
		for _, m := range goBuild.FindAllStringSubmatch(text, -1) {
			targets = append(targets, buildTargets(m[1])...)
		}
		if len(targets) == 0 {
			continue
		}
		t.Run(filepath.ToSlash(rel), func(t *testing.T) {
			ignore := dockerfile + ".dockerignore"
			if _, err := os.Stat(ignore); err != nil {
				ignore = filepath.Join(root, ".dockerignore")
			}
			ignoreRel, _ := filepath.Rel(root, ignore)
			pm := readIgnore(t, ignore)
			for _, file := range embedFiles(t, root, targets) {
				embedded++
				excluded, err := pm.MatchesOrParentMatches(file)
				if err != nil {
					t.Fatal(err)
				}
				if excluded {
					t.Errorf("%s is embedded (//go:embed) in %s but %s leaves it out of the image build; re-include it there",
						file, strings.Join(targets, " "), filepath.ToSlash(ignoreRel))
				}
			}
		})
		checked[filepath.ToSlash(rel)] = true
	}
	if embedded == 0 {
		t.Fatal("go list found no embedded files; the test no longer sees the //go:embed directives")
	}
	for _, want := range []string{"Dockerfile", "hack/agent/Dockerfile", "hack/session/Dockerfile"} {
		if !checked[want] {
			t.Errorf("%s was not checked; the test no longer finds its go build", want)
		}
	}
}

// buildTargets returns the packages or files a go build command line names,
// skipping its flags and their values.
func buildTargets(args string) []string {
	var targets []string
	fields := strings.Fields(args)
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		switch {
		case f == "-o" || f == "-tags" || f == "-ldflags" || f == "-gcflags":
			i++
		case strings.HasPrefix(f, "-"):
		default:
			if !strings.HasPrefix(f, "./") && !strings.HasSuffix(f, ".go") {
				f = "./" + f
			}
			targets = append(targets, f)
		}
	}
	return targets
}

func readIgnore(t *testing.T, path string) *patternmatcher.PatternMatcher {
	t.Helper()
	f, err := os.Open(path)
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
	return pm
}

// embedFiles returns the files, relative to root, that the //go:embed
// directives of this module's packages the targets compile embed.
func embedFiles(t *testing.T, root string, targets []string) []string {
	t.Helper()
	args := append([]string{"list", "-deps",
		"-f", "{{if .Module}}{{if .Module.Main}}{{.Dir}}{{range .EmbedFiles}}\t{{.}}{{end}}{{end}}{{end}}"}, targets...)
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list %v: %v", targets, err)
	}
	var files []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, "\t")
		for _, file := range fields[1:] {
			rel, err := filepath.Rel(root, filepath.Join(fields[0], file))
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, filepath.ToSlash(rel))
		}
	}
	return files
}
