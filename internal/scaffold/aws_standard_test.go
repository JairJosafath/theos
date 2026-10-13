package scaffold

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/JairJosafath/c4factory/internal/model"
)

// aws-standard is a whole TypeScript Standard copied from ts-standard
// (plans/aws.md, retrospective rule 3): a Container has one Standard, so it
// can't sit on top of ts-standard. These tests keep the copy from drifting.
// For aws-lambda-py it carries python-standard's Python rules too, kept the
// same way.

// awsFrameworks is every Framework aws-standard renders for.
var awsFrameworks = []string{"aws-lambda-ts", "aws-lambda-py"}

// tsStandardFrameworks is every Framework ts-standard renders for, with its type.
var tsStandardFrameworks = []side{
	{"extension", "vscode-extension"},
	{"static-site", "react-vite"},
	{"cli", "node-cli"},
}

// awsStandardShared lists the files aws-standard shares with ts-standard.
// whole: its rendering is byte for byte ts-standard's for node-cli. Else
// every line that ts-standard renders the same for all its Frameworks is in
// aws-standard's rendering, in the same order: aws-standard replaces only
// what ts-standard varies per Framework.
var awsStandardShared = []struct {
	path  string
	whole bool
}{
	{".editorconfig", true},
	{".prettierrc.json", true},
	{"docs/style/typescript.md", true},
	{"eslint.config.mjs", false},
	{"docs/style/principles.md", false},
}

func TestAWSStandardKeepsTSStandardsSharedFiles(t *testing.T) {
	ts := make([]Result, 0, len(tsStandardFrameworks))
	for _, f := range tsStandardFrameworks {
		_, r := renderCombo(t, f.Side, []side{{model.SideFramework, f.Name}, {model.SideStandard, "ts-standard"}})
		ts = append(ts, r)
	}
	node := ts[len(ts)-1]
	const fix = "copy ts-standard's change into templates/aws-standard and name ts-standard's version " +
		"in its docs/style/SOURCES.md"
	for _, framework := range awsFrameworks {
		_, aws := renderCombo(t, model.TypeFunction, []side{
			{model.SideFramework, framework},
			{model.SideStandard, "aws-standard"},
		})
		for _, s := range awsStandardShared {
			t.Run(framework+"/"+s.path, func(t *testing.T) {
				got, ok := aws.Files[s.path]
				if !ok {
					t.Fatalf("aws-standard writes no %s", s.path)
				}
				if s.whole {
					if want := node.Files[s.path].Data; string(got.Data) != string(want) {
						t.Errorf("aws-standard's %s differs from ts-standard's: %s", s.path, fix)
					}
					return
				}
				renders := make([][]string, 0, len(ts))
				for _, r := range ts {
					renders = append(renders, strings.Split(string(r.Files[s.path].Data), "\n"))
				}
				lines := strings.Split(string(got.Data), "\n")
				i := 0
				for _, want := range sharedLines(renders) {
					for i < len(lines) && lines[i] != want {
						i++
					}
					if i == len(lines) {
						t.Fatalf("aws-standard's %s lacks ts-standard's line %q (or has it out of order): %s",
							s.path, want, fix)
					}
					i++
				}
			})
		}
	}
}

// sharedLines returns the non-blank lines of the first rendering, in order,
// that every rendering has the same number of times: the lines ts-standard
// doesn't vary per Framework.
func sharedLines(renders [][]string) []string {
	count := func(lines []string) map[string]int {
		m := map[string]int{}
		for _, l := range lines {
			m[l]++
		}
		return m
	}
	counts := make([]map[string]int, 0, len(renders))
	for _, r := range renders {
		counts = append(counts, count(r))
	}
	var shared []string
	for _, l := range renders[0] {
		if strings.TrimSpace(l) == "" {
			continue
		}
		same := true
		for _, c := range counts[1:] {
			if c[l] != counts[0][l] {
				same = false
			}
		}
		if same {
			shared = append(shared, l)
		}
	}
	return shared
}

// TestAWSStandardNamesTheTSStandardItCopies checks that aws-standard's
// SOURCES.md names the ts-standard version its shared files were copied from.
func TestAWSStandardNamesTheTSStandardItCopies(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(templatesDir, "aws-standard", "docs", "style", "SOURCES.md.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`copied from ts-standard v\d+\.\d+\.\d+`).Match(src) {
		t.Error("aws-standard's SOURCES.md doesn't say which ts-standard version it was copied from")
	}
}

// The part of python-standard's python.md that
// aws-standard's carries for aws-lambda-py: its general sections, from the
// first up to the Framework-specific ones.
const (
	pythonStandardFirstSection = "## Layout and formatting"
	pythonStandardEndSection   = "## FastAPI (applies to FastAPI services)"
)

// TestAWSStandardKeepsPythonStandardsRules checks that aws-standard, for
// aws-lambda-py, writes python-standard's ruff.toml and mypy.ini byte for
// byte and its python.md's general sections, and writes none of them for
// aws-lambda-ts.
func TestAWSStandardKeepsPythonStandardsRules(t *testing.T) {
	_, py := renderCombo(t, "cli", []side{{model.SideFramework, "python-cli"}, {model.SideStandard, "python-standard"}})
	_, aws := renderCombo(t, model.TypeFunction, []side{
		{model.SideFramework, "aws-lambda-py"},
		{model.SideStandard, "aws-standard"},
	})
	_, awsTS := renderCombo(t, model.TypeFunction, []side{
		{model.SideFramework, "aws-lambda-ts"},
		{model.SideStandard, "aws-standard"},
	})
	const fix = "copy python-standard's change into templates/aws-standard and name python-standard's " +
		"version in its docs/style/SOURCES.md"
	for _, path := range []string{"ruff.toml", "mypy.ini", "docs/style/python.md"} {
		if _, ok := awsTS.Files[path]; ok {
			t.Errorf("aws-standard writes %s for aws-lambda-ts", path)
		}
		if _, ok := aws.Files[path]; !ok {
			t.Errorf("aws-standard writes no %s for aws-lambda-py", path)
		}
	}
	for _, path := range []string{"ruff.toml", "mypy.ini"} {
		if string(aws.Files[path].Data) != string(py.Files[path].Data) {
			t.Errorf("aws-standard's %s differs from python-standard's: %s", path, fix)
		}
	}
	want := string(py.Files["docs/style/python.md"].Data)
	first, end := strings.Index(want, pythonStandardFirstSection), strings.Index(want, pythonStandardEndSection)
	if first < 0 || end < first {
		t.Fatalf("python-standard's python.md has no %q before %q: update this test", pythonStandardFirstSection,
			pythonStandardEndSection)
	}
	if !strings.Contains(string(aws.Files["docs/style/python.md"].Data), strings.TrimRight(want[first:end], "\n")) {
		t.Errorf("aws-standard's python.md lacks python-standard's general sections, byte for byte: %s", fix)
	}
}

// TestAWSStandardNamesThePythonStandardItCopies checks that aws-standard's
// SOURCES.md names the python-standard version its Python rules came from:
// the one templates/versions.txt pins.
func TestAWSStandardNamesThePythonStandardItCopies(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(templatesDir, "aws-standard", "docs", "style", "SOURCES.md.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	versions, err := os.ReadFile(filepath.Join(templatesDir, "versions.txt"))
	if err != nil {
		t.Fatal(err)
	}
	pinned := regexp.MustCompile(`(?m)^python-standard (v\d+\.\d+\.\d+)$`).FindSubmatch(versions)
	if pinned == nil {
		t.Fatal("templates/versions.txt pins no python-standard")
	}
	// The rules are copied from the pinned version, which the drift test
	// compares against.
	if !strings.Contains(string(src), "are python-standard "+string(pinned[1])+"'s") {
		t.Errorf("aws-standard's SOURCES.md doesn't say its Python rules are python-standard %s's", pinned[1])
	}
}
