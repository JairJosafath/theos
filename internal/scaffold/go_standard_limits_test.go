package scaffold

import (
	"slices"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/JairJosafath/theos/internal/model"
)

// goLintConfig is the part of a .golangci.yml the readability limits live in.
type goLintConfig struct {
	Linters struct {
		Enable   []string `yaml:"enable"`
		Settings struct {
			Cyclop struct {
				MaxComplexity int `yaml:"max-complexity"`
			} `yaml:"cyclop"`
			Gocognit struct {
				MinComplexity int `yaml:"min-complexity"`
			} `yaml:"gocognit"`
			Nestif struct {
				MinComplexity int `yaml:"min-complexity"`
			} `yaml:"nestif"`
			Funlen struct {
				Lines      int `yaml:"lines"`
				Statements int `yaml:"statements"`
			} `yaml:"funlen"`
			Lll struct {
				LineLength int `yaml:"line-length"`
			} `yaml:"lll"`
			Revive struct {
				Rules []struct {
					Name      string `yaml:"name"`
					Arguments []any  `yaml:"arguments"`
				} `yaml:"rules"`
			} `yaml:"revive"`
		} `yaml:"settings"`
	} `yaml:"linters"`
}

// TestGoStandardLimitsComplexityAndSize keeps the readability limits in the
// Go lint config agents are checked by: a function with few paths, easy to
// follow and short, and a file of at most 300 lines of code. Agents write
// long, branched functions and big files unless the lint stops them.
// aws-standard copies the file (TestAWSStandardKeepsGoStandardsFilesForGo).
func TestGoStandardLimitsComplexityAndSize(t *testing.T) {
	_, res := renderCombo(t, "cli", []side{{model.SideFramework, "go-cli"}, {model.SideStandard, "go-standard"}})
	file, ok := res.Files[".golangci.yml"]
	if !ok {
		t.Fatal("go-standard writes no .golangci.yml")
	}
	var cfg goLintConfig
	if err := yaml.Unmarshal(file.Data, &cfg); err != nil {
		t.Fatalf("go-standard's .golangci.yml: %v", err)
	}
	for _, linter := range []string{"cyclop", "funlen", "gocognit", "lll", "maintidx", "nestif", "revive"} {
		if !slices.Contains(cfg.Linters.Enable, linter) {
			t.Errorf("go-standard does not enable %s", linter)
		}
	}
	s := cfg.Linters.Settings
	for name, c := range map[string]struct{ got, want int }{
		"cyclop max-complexity":   {s.Cyclop.MaxComplexity, 10},
		"gocognit min-complexity": {s.Gocognit.MinComplexity, 15},
		"nestif min-complexity":   {s.Nestif.MinComplexity, 4},
		"funlen lines":            {s.Funlen.Lines, 60},
		"funlen statements":       {s.Funlen.Statements, 40},
		"lll line-length":         {s.Lll.LineLength, 120},
	} {
		if c.got != c.want {
			t.Errorf("go-standard's %s is %d, want %d", name, c.got, c.want)
		}
	}
	rules := map[string]bool{}
	for _, r := range s.Revive.Rules {
		rules[r.Name] = len(r.Arguments) > 0
	}
	for _, rule := range []string{"file-length-limit", "argument-limit", "function-result-limit"} {
		if !rules[rule] {
			t.Errorf("go-standard's revive has no %s with a limit", rule)
		}
	}
}
