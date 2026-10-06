package agentrunner

import (
	"context"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/JairJosafath/c4factory/internal/model"
)

// planEnv is a plan job's environment: no goal, no Container.
func planEnv(overrides map[string]string) func(string) string {
	base := map[string]string{
		EnvRole: RolePlan, EnvBranch: "", EnvGoal: "", EnvContainer: "", EnvBrief: "cel-lens",
		EnvRepoURL: "http://gitea/apps/brief-cel-lens.git", EnvTask: "cel-lens-1",
	}
	maps.Copy(base, overrides)
	return env(base)
}

func TestJobFromEnvPlan(t *testing.T) {
	job, err := JobFromEnv(planEnv(map[string]string{EnvRound: "3"}))
	if err != nil {
		t.Fatalf("JobFromEnv() error = %v", err)
	}
	if job.Role != RolePlan || job.Brief != "cel-lens" || job.Branch != "plan/3" || job.Round != 3 {
		t.Errorf("job = role %q, brief %q, branch %q, round %d", job.Role, job.Brief, job.Branch, job.Round)
	}
	if job, err = JobFromEnv(planEnv(map[string]string{EnvBranch: "plan/1"})); err != nil || job.Branch != "plan/1" {
		t.Errorf("JobFromEnv(branch plan/1) = %q, %v", job.Branch, err)
	}

	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"no brief", map[string]string{EnvBrief: ""}, "missing C4_BRIEF"},
		{"another branch", map[string]string{EnvBranch: "task/add-crop"}, "a plan of round 1 goes to plan/1"},
		{"another round's branch", map[string]string{EnvBranch: "plan/2"}, "goes to plan/1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := JobFromEnv(planEnv(tt.env)); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("JobFromEnv() error = %v, want %q", err, tt.want)
			}
		})
	}
}

// briefRepo is a brief repository as the operator keeps it on main, with an
// earlier round's plan merged.
func briefRepo(t *testing.T) string {
	t.Helper()
	return bareRepo(t, map[string]string{
		model.BriefFile:               "# cel-lens\n\nA tool that explains CEL rules.\n",
		model.PlannerGuide:            "Plan the brief.\n",
		model.WorkspaceModelFile:      "---\napiVersion: c4.factory.io/v1alpha1\nkind: Project\nmetadata:\n  name: apps\n",
		model.WorkspaceCatalogFile:    "frameworks: []\n",
		model.WorkspaceContainersFile: "- name: cli\n  repository: http://gitea/apps/cli\n  openIssues: []\n",
		"plan/choices.md":             "- Round 1 chose a CLI.\n",
		"plan/issues/01-cli.md":       "---\ntitle: Round 1's issue\n---\nOld.\n",
	})
}

func planJob(t *testing.T, url string, round int) Job {
	t.Helper()
	job, err := JobFromEnv(planEnv(map[string]string{EnvRepoURL: url, EnvRound: strconv.Itoa(round)}))
	if err != nil {
		t.Fatal(err)
	}
	return job
}

// recordingAgent records the prompt, then does what next does.
type recordingAgent struct {
	prompt string
	next   Agent
}

func (a *recordingAgent) Run(ctx context.Context, dir, prompt string) (string, error) {
	a.prompt = prompt
	return a.next.Run(ctx, dir, prompt)
}

// planFiles returns every file of branch, by path.
func planFiles(t *testing.T, url, branch string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "clone", "--quiet", "--branch", branch, url, dir).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v: %s", err, out)
	}
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && d.Name() == ".git" {
				return filepath.SkipDir
			}
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		data, err := os.ReadFile(path)
		files[rel] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestRunPlanPushesOnlyThePlan(t *testing.T) {
	url := briefRepo(t)
	agent := &recordingAgent{next: filesAgent{summary: "One CLI and one issue.", files: map[string]string{
		model.PlanChoicesFile:   "- A CLI, since the idea is a tool.\n",
		model.PlanModelFile:     "apiVersion: c4.factory.io/v1alpha1\nkind: Project\nmetadata:\n  name: apps\n",
		"plan/issues/01-cli.md": "---\ntitle: Explain a rule\n---\nBody.\n",
		model.BriefFile:         "The planner may not rewrite the brief.\n",
		"notes.md":              "Nor add files outside plan/.\n",
	}}}

	result := Run(context.Background(), planJob(t, url, 2), agent, t.TempDir())
	if result.Error != "" || result.Role != RolePlan || result.Summary != "One CLI and one issue." {
		t.Fatalf("Run() = %+v", result)
	}
	if got := branchHead(t, url, "plan/2"); got != result.Commit {
		t.Errorf("plan/2 head = %q, want the result's commit %q", got, result.Commit)
	}
	files := planFiles(t, url, "plan/2")
	if files[model.BriefFile] != "# cel-lens\n\nA tool that explains CEL rules.\n" {
		t.Errorf("brief.md = %q, want main's", files[model.BriefFile])
	}
	if _, ok := files["notes.md"]; ok {
		t.Error("notes.md, outside plan/, was pushed")
	}
	if files["plan/issues/01-cli.md"] != "---\ntitle: Explain a rule\n---\nBody.\n" ||
		files[model.PlanChoicesFile] != "- A CLI, since the idea is a tool.\n" {
		t.Errorf("plan = %v, want this round's", files)
	}
	for _, want := range []string{"Plan the brief.", "A tool that explains CEL rules.", "brief \"cel-lens\"", "plan/2"} {
		if !strings.Contains(agent.prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, agent.prompt)
		}
	}

	// A rerun of the round replaces its branch, and drops what an earlier run left.
	again := Run(context.Background(), planJob(t, url, 2), filesAgent{files: map[string]string{
		model.PlanNeedsInputFile: "Which Git host should the CLI's releases go to, and why?\n",
	}}, t.TempDir())
	if again.Error != "" {
		t.Fatalf("rerun = %+v", again)
	}
	files = planFiles(t, url, "plan/2")
	if _, ok := files["plan/issues/01-cli.md"]; ok || files[model.PlanNeedsInputFile] == "" {
		t.Errorf("rerun plan = %v, want only needs-input.md", files)
	}
}

func TestRunPlanStandIn(t *testing.T) {
	workspace, err := model.BriefWorkspaceFiles(model.BriefWorkspace{
		Brief: "notes", Idea: "Notes for a team.", GitProvider: "gitea",
		Catalog: []model.CatalogEntry{{Side: model.SideFramework, Name: "go-cli", SupportedTypes: []string{model.TypeCLI}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	workspace["plan/issues/01-cli.md"] = "---\ntitle: Round 1's issue\n---\nOld.\n"
	url := bareRepo(t, workspace)

	result := Run(context.Background(), planJob(t, url, 2), FakeAgent{Role: RolePlan, Round: 1}, t.TempDir())
	if result.Error != "" {
		t.Fatalf("Run() = %+v", result)
	}
	files := planFiles(t, url, "plan/2")
	plan, err := model.ParsePlan(files)
	if err != nil {
		t.Fatalf("the stand-in's plan doesn't parse: %v", err)
	}
	if len(plan.Objects) != 2 || len(plan.Issues) != 1 || plan.Issues[0].Container != "notes" {
		t.Errorf("stand-in plan = %+v, want a SoftwareSystem and Container notes, with one issue", plan)
	}
	if _, ok := files["plan/issues/01-cli.md"]; ok {
		t.Error("the earlier round's issue is still in the plan")
	}
}

func TestRunPlanFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("no plan", func(t *testing.T) {
		url := briefRepo(t)
		result := Run(ctx, planJob(t, url, 1), filesAgent{files: map[string]string{"notes.md": "x"}}, t.TempDir())
		if !strings.Contains(result.Error, "the planner wrote no plan") || branchHead(t, url, "plan/1") != "" {
			t.Errorf("Run() = %+v, want no plan and no branch", result)
		}
	})
	t.Run("no guide", func(t *testing.T) {
		url := bareRepo(t, map[string]string{model.BriefFile: "An idea.\n"})
		result := Run(ctx, planJob(t, url, 1), FakeAgent{Role: RolePlan, Round: 1}, t.TempDir())
		if !strings.Contains(result.Error, "no PLANNER.md on main") {
			t.Errorf("Run() = %+v", result)
		}
	})
	t.Run("agent fails", func(t *testing.T) {
		url := briefRepo(t)
		result := Run(ctx, planJob(t, url, 1), failingAgent{err: errors.New("claude ended with an error")}, t.TempDir())
		if !strings.Contains(result.Error, "claude ended with an error") || result.Commit != "" {
			t.Errorf("Run() = %+v", result)
		}
	})
}

func TestPlanPrompt(t *testing.T) {
	job := Job{Role: RolePlan, Brief: "cel-lens", Project: "apps", Branch: "plan/3", Round: 3, MaxRounds: 3,
		Feedback: "Container/cli: catalog entry go-cli does not exist"}
	prompt := PlanPrompt(job, "The guide.", "The idea.")
	for _, want := range []string{"The guide.", "The idea.", "round 3", "go-cli does not exist", "last round",
		model.PlanNeedsInputFile, "plan/3"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("PlanPrompt() lacks %q:\n%s", want, prompt)
		}
	}
}
