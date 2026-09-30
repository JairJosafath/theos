package agentrunner

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func env(overrides map[string]string) func(string) string {
	base := map[string]string{
		EnvRole: RoleImplement, EnvRepoURL: "http://gitea/toolbox/photo-editor.git", EnvBranch: "task/add-crop",
		EnvGoal: "Add crop", EnvContainer: "photo-editor", EnvProject: "toolbox", EnvGitUsername: "bot",
		EnvGitToken: "t0k", EnvTask: "add-crop",
	}
	maps.Copy(base, overrides)
	return func(k string) string { return base[k] }
}

func TestJobFromEnv(t *testing.T) {
	job, err := JobFromEnv(env(nil))
	if err != nil {
		t.Fatalf("JobFromEnv() error = %v", err)
	}
	if job.Base != "main" || job.Round != 1 || strings.Join(job.CheckCommand, " ") != "make check" {
		t.Errorf("defaults = base %q, round %d, check %v", job.Base, job.Round, job.CheckCommand)
	}

	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"missing values", map[string]string{EnvGoal: "", EnvGitToken: ""}, "missing C4_GIT_TOKEN, C4_GOAL"},
		{"unknown role", map[string]string{EnvRole: "deploy"}, `C4_ROLE="deploy"`},
		{"bad round", map[string]string{EnvRound: "zero"}, "not a round number"},
		{"branch is the base", map[string]string{EnvBranch: "main"}, "must not be the base"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := JobFromEnv(env(tt.env))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("JobFromEnv() error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestPrompts(t *testing.T) {
	job, _ := JobFromEnv(env(map[string]string{EnvFeedback: "Handle an empty image.", EnvRound: "2",
		EnvContext: "Pocket Tools: works fully offline"}))
	implement := ImplementPrompt(job)
	for _, want := range []string{"Add crop", "Handle an empty image.", "round 2", "AGENTS.md", `"make check"`,
		"task/add-crop", "works fully offline"} {
		if !strings.Contains(implement, want) {
			t.Errorf("implement prompt lacks %q", want)
		}
	}
	review := ReviewPrompt(job)
	for _, want := range []string{"Add crop", "git diff origin/main...HEAD", `"verdict"`, "do not change files"} {
		if !strings.Contains(review, want) {
			t.Errorf("review prompt lacks %q", want)
		}
	}
	if strings.Contains(review, "t0k") || strings.Contains(implement, "t0k") {
		t.Error("a prompt contains the Git token")
	}
}

func TestParseVerdict(t *testing.T) {
	tests := []struct {
		name, output, verdict, summary, err string
	}{
		{"last line", "All good.\n{\"verdict\": \"approve\", \"summary\": \"Clean.\"}", VerdictApprove, "Clean.", ""},
		{"code braces before it", "func x() { return }\n{\"verdict\":\"changes\",\"summary\":\"Test empty input.\"}\n",
			VerdictChanges, "Test empty input.", ""},
		{"upper case", `{"verdict": "APPROVE", "summary": ""}`, VerdictApprove, "", ""},
		{"no verdict", "Looks fine to me.", "", "", "no JSON verdict"},
		{"unknown verdict", `{"verdict": "maybe"}`, "", "", `unknown verdict "maybe"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verdict, summary, err := ParseVerdict(tt.output)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("ParseVerdict() error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil || verdict != tt.verdict || summary != tt.summary {
				t.Errorf("ParseVerdict() = %q, %q, %v", verdict, summary, err)
			}
		})
	}
}

func TestResultEncodeFitsTerminationMessage(t *testing.T) {
	r := Result{Role: RoleImplement, Commit: strings.Repeat("a", 40),
		Summary: strings.Repeat("é", 5000), Error: strings.Repeat("x", 3000)}
	data := r.Encode()
	if len(data) > MaxResultBytes {
		t.Fatalf("Encode() = %d bytes, want at most %d", len(data), MaxResultBytes)
	}
	var back Result
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("Encode() is not JSON: %v", err)
	}
	if back.Commit != r.Commit {
		t.Error("Encode() lost the commit")
	}
}

// bareRepo makes a bare repository with one commit on main and returns its URL.
func bareRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	bare, work := filepath.Join(root, "origin.git"), filepath.Join(root, "work")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run(root, "init", "--quiet", "--bare", "--initial-branch=main", bare)
	run(root, "init", "--quiet", "--initial-branch=main", work)
	for name, data := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(work, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(work, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run(work, "add", "--all")
	run(work, "commit", "--quiet", "-m", "Scaffold")
	run(work, "push", "--quiet", bare, "main")
	return "file://" + bare
}

func branchHead(t *testing.T, repoURL, branch string) string {
	t.Helper()
	out, err := exec.Command("git", "ls-remote", repoURL, "refs/heads/"+branch).Output()
	if err != nil {
		t.Fatal(err)
	}
	head, _, _ := strings.Cut(string(out), "\t")
	return head
}

func testJob(t *testing.T, repoURL, role string, round int, check ...string) Job {
	t.Helper()
	job, err := JobFromEnv(env(map[string]string{EnvRepoURL: repoURL, EnvRole: role}))
	if err != nil {
		t.Fatal(err)
	}
	job.Round = round
	job.CheckCommand = check
	if len(check) == 0 {
		job.CheckCommand = []string{"true"}
	}
	return job
}

func TestRunImplementPushesTheBranch(t *testing.T) {
	url := bareRepo(t, map[string]string{"README.md": "hi\n"})
	ctx := context.Background()

	first := Run(ctx, testJob(t, url, RoleImplement, 1), FakeAgent{Role: RoleImplement, Round: 1}, t.TempDir())
	if first.Error != "" || first.Commit == "" {
		t.Fatalf("round 1 = %+v", first)
	}
	if got := branchHead(t, url, "task/add-crop"); got != first.Commit {
		t.Errorf("branch head = %q, want %q", got, first.Commit)
	}

	// Round 2 continues from the pushed branch.
	second := Run(ctx, testJob(t, url, RoleImplement, 2), FakeAgent{Role: RoleImplement, Round: 2}, t.TempDir())
	if second.Error != "" || second.Commit == first.Commit {
		t.Fatalf("round 2 = %+v", second)
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "clone", "--quiet", "--branch", "task/add-crop", url, dir).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v: %s", err, out)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "STAND-IN.md"))
	if string(data) != "Round 1 of the stand-in agent.\nRound 2 of the stand-in agent.\n" {
		t.Errorf("STAND-IN.md = %q", data)
	}
}

func TestRunImplementFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("no change", func(t *testing.T) {
		url := bareRepo(t, map[string]string{"README.md": "hi\n"})
		got := Run(ctx, testJob(t, url, RoleImplement, 1), FakeAgent{Role: RoleImplement, NoChange: true}, t.TempDir())
		if got.Error != "the agent made no changes" {
			t.Errorf("Error = %q", got.Error)
		}
		if branchHead(t, url, "task/add-crop") != "" {
			t.Error("an empty change was pushed")
		}
	})
	t.Run("protected path", func(t *testing.T) {
		url := bareRepo(t, map[string]string{".gitea/workflows/check.yaml": "on: push\n"})
		got := Run(ctx, testJob(t, url, RoleImplement, 1), editAgent{path: ".gitea/workflows/check.yaml"}, t.TempDir())
		if !strings.Contains(got.Error, ".gitea/workflows/check.yaml, which belong to the platform") {
			t.Errorf("Error = %q", got.Error)
		}
	})
	t.Run("check keeps failing", func(t *testing.T) {
		url := bareRepo(t, map[string]string{"README.md": "hi\n"})
		agent := &countingAgent{}
		got := Run(ctx, testJob(t, url, RoleImplement, 1, "sh", "-c", "echo lint: bad; exit 1"), agent, t.TempDir())
		if !strings.Contains(got.Error, "still fails after 2 attempts") || !strings.Contains(got.Error, "lint: bad") {
			t.Errorf("Error = %q", got.Error)
		}
		if agent.calls != 3 {
			t.Errorf("agent ran %d times, want the work plus 2 fix attempts", agent.calls)
		}
		if got.Commit == "" || branchHead(t, url, "task/add-crop") != got.Commit {
			t.Error("the work was not pushed for the next round")
		}
	})
	t.Run("clone fails", func(t *testing.T) {
		got := Run(ctx, testJob(t, "file:///nonexistent/repo.git", RoleImplement, 1), FakeAgent{}, t.TempDir())
		if !strings.HasPrefix(got.Error, "git clone") {
			t.Errorf("Error = %q", got.Error)
		}
	})
}

func TestRunReview(t *testing.T) {
	url := bareRepo(t, map[string]string{"README.md": "hi\n"})
	ctx := context.Background()
	implemented := Run(ctx, testJob(t, url, RoleImplement, 1), FakeAgent{Role: RoleImplement, Round: 1}, t.TempDir())

	first := Run(ctx, testJob(t, url, RoleReview, 1), FakeAgent{Role: RoleReview, Round: 1, ChangesFirst: true}, t.TempDir())
	if first.Verdict != VerdictChanges || first.Commit != implemented.Commit || first.Error != "" {
		t.Errorf("round 1 review = %+v", first)
	}
	second := Run(ctx, testJob(t, url, RoleReview, 2), FakeAgent{Role: RoleReview, Round: 2, ChangesFirst: true}, t.TempDir())
	if second.Verdict != VerdictApprove {
		t.Errorf("round 2 review = %+v", second)
	}
}

// editAgent changes one file.
type editAgent struct{ path string }

func (a editAgent) Run(_ context.Context, dir, _ string) (string, error) {
	return "edited", os.WriteFile(filepath.Join(dir, a.path), []byte("changed\n"), 0o644)
}

// countingAgent changes a file on every call.
type countingAgent struct{ calls int }

func (a *countingAgent) Run(_ context.Context, dir, _ string) (string, error) {
	a.calls++
	return "tried", os.WriteFile(filepath.Join(dir, "attempt.txt"), []byte(strings.Repeat("x", a.calls)), 0o644)
}
