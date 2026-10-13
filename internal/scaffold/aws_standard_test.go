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

// tsStandardFrameworks is every Framework ts-standard renders for, with its type.
var tsStandardFrameworks = []side{
	{"extension", "vscode-extension"},
	{"static-site", "react-vite"},
	{"cli", "node-cli"},
}

// awsStandardShared lists the files aws-standard shares with ts-standard.
// whole: its rendering for aws-lambda-ts is byte for byte ts-standard's for
// node-cli. Else, and for every other AWS Framework, every line that
// ts-standard renders the same for all its Frameworks is in aws-standard's
// rendering, in the same order: aws-standard replaces only what ts-standard
// varies per Framework, and adds what an AWS Framework needs.
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

// awsFrameworks is every Framework aws-standard renders for, with its type.
var awsFrameworks = []side{
	{model.TypeFunction, "aws-lambda-ts"},
	{model.TypeFunction, "aws-lambda-go"},
	{model.TypeService, "aws-fargate-ts"},
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
	for _, f := range awsFrameworks {
		framework := f.Name
		_, aws := renderCombo(t, f.Side, []side{
			{model.SideFramework, framework},
			{model.SideStandard, "aws-standard"},
		})
		for _, s := range awsStandardShared {
			t.Run(framework+"/"+s.path, func(t *testing.T) {
				got, ok := aws.Files[s.path]
				if !ok {
					t.Fatalf("aws-standard writes no %s", s.path)
				}
				if s.whole && framework != "aws-lambda-go" {
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

// awsStandardGoShared lists the files aws-standard copies from go-standard,
// byte for byte, for aws-lambda-go, and writes for no other Framework.
var awsStandardGoShared = []string{".golangci.yml", "docs/style/go.md"}

func TestAWSStandardKeepsGoStandardsFilesForGo(t *testing.T) {
	_, goStd := renderCombo(t, "cli", []side{{model.SideFramework, "go-cli"}, {model.SideStandard, "go-standard"}})
	_, goAWS := renderCombo(t, model.TypeFunction, []side{
		{model.SideFramework, "aws-lambda-go"},
		{model.SideStandard, "aws-standard"},
	})
	_, tsAWS := renderCombo(t, model.TypeFunction, []side{
		{model.SideFramework, "aws-lambda-ts"},
		{model.SideStandard, "aws-standard"},
	})
	for _, path := range awsStandardGoShared {
		want, ok := goStd.Files[path]
		if !ok {
			t.Fatalf("go-standard writes no %s", path)
		}
		got, ok := goAWS.Files[path]
		if !ok {
			t.Errorf("aws-standard writes no %s for aws-lambda-go", path)
		} else if string(got.Data) != string(want.Data) {
			t.Errorf("aws-standard's %s differs from go-standard's: copy go-standard's change into "+
				"templates/aws-standard and name go-standard's version in its docs/style/SOURCES.md", path)
		}
		if _, ok := tsAWS.Files[path]; ok {
			t.Errorf("aws-standard writes %s for aws-lambda-ts, which has no Go", path)
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
// SOURCES.md names the ts-standard version its shared files were copied from,
// and the go-standard version its Go files were.
func TestAWSStandardNamesTheTSStandardItCopies(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(templatesDir, "aws-standard", "docs", "style", "SOURCES.md.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, standard := range []string{"ts-standard", "go-standard"} {
		if !regexp.MustCompile(`copied\s+from\s+` + standard + `\s+v\d+\.\d+\.\d+`).Match(src) {
			t.Errorf("aws-standard's SOURCES.md doesn't say which %s version it was copied from", standard)
		}
	}
}
