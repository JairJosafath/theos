package agentrunner

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/c4factory/internal/model"
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
		{"bad round limit", map[string]string{EnvMaxRounds: "-1"}, "not a round number"},
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
	implement := ImplementPrompt(job, Workspace{})
	for _, want := range []string{"Add crop", "Handle an empty image.", "round 2", "AGENTS.md", `"make check"`,
		"task/add-crop", "works fully offline"} {
		if !strings.Contains(implement, want) {
			t.Errorf("implement prompt lacks %q", want)
		}
	}
	if got, _ := JobFromEnv(env(map[string]string{EnvProtectedPaths: "Makefile, docs/style/,"})); !slices.Equal(got.ProtectedPaths, []string{"Makefile", "docs/style/"}) {
		t.Errorf("ProtectedPaths = %v", got.ProtectedPaths)
	}
	review := ReviewPrompt(job, Workspace{})
	for _, want := range []string{"Add crop", "git diff origin/main...HEAD", `"verdict"`, "do not change files",
		"Handle an empty image.", "check each point was addressed", "Non-blocking:"} {
		if !strings.Contains(review, want) {
			t.Errorf("review prompt lacks %q", want)
		}
	}
	if strings.Contains(review, "last round") {
		t.Error("review prompt says round 2 of an unknown limit is the last")
	}
	job.MaxRounds = 2
	if !strings.Contains(ReviewPrompt(job, Workspace{}), "This is the last round") {
		t.Error("review prompt of the last round doesn't say so")
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
			v, err := ParseVerdict(tt.output)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("ParseVerdict() error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil || v.Verdict != tt.verdict || v.Summary != tt.summary {
				t.Errorf("ParseVerdict() = %+v, %v", v, err)
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

// branchHead returns the head of branch in the repository at repoURL.
//
//nolint:unparam // reads any branch, though the tests use one
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
		if !strings.Contains(got.Error, ".gitea/workflows/check.yaml, which the platform and the Standard protect") {
			t.Errorf("Error = %q", got.Error)
		}
	})
	t.Run("path the Standard protects", func(t *testing.T) {
		url := bareRepo(t, map[string]string{"Makefile": "check:\n"})
		job := testJob(t, url, RoleImplement, 1)
		job.ProtectedPaths = []string{"Makefile"}
		got := Run(ctx, job, editAgent{path: "Makefile"}, t.TempDir())
		if !strings.Contains(got.Error, "touches Makefile") {
			t.Errorf("Error = %q", got.Error)
		}
		if branchHead(t, url, "task/add-crop") != "" {
			t.Error("a change to a protected path was pushed")
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

func TestJobFromEnvIssueAndRoles(t *testing.T) {
	job, err := JobFromEnv(env(map[string]string{EnvIssue: "3", EnvModel: "claude-opus-5-5", EnvHarness: HarnessStandIn,
		EnvRelated: "vscode-extension=http://gitea/cel-lens/vscode-extension.git, server=http://gitea/cel-lens/server.git"}))
	if err != nil {
		t.Fatalf("JobFromEnv() error = %v", err)
	}
	want := map[string]string{"vscode-extension": "http://gitea/cel-lens/vscode-extension.git", "server": "http://gitea/cel-lens/server.git"}
	if job.Issue != 3 || job.Model != "claude-opus-5-5" || job.Harness != HarnessStandIn || !maps.Equal(job.Related, want) {
		t.Errorf("job = issue %d, model %q, harness %q, related %v", job.Issue, job.Model, job.Harness, job.Related)
	}
	for _, bad := range []map[string]string{{EnvIssue: "three"}, {EnvRelated: "no-url"}, {EnvHarness: "copilot"}} {
		if _, err := JobFromEnv(env(bad)); err == nil {
			t.Errorf("JobFromEnv(%v) succeeded", bad)
		}
	}
}

func TestIssuePrompts(t *testing.T) {
	job, _ := JobFromEnv(env(map[string]string{EnvIssue: "3", EnvGoal: "Issue #3: Locate CEL\n\n## Acceptance criteria\n- [ ] finds rules",
		EnvProtectedPaths: "Makefile,docs/agents/"}))
	ws := Workspace{Guide: "Write the failing test first.", Related: []RelatedRepo{{Name: "vscode-extension", Dir: "/workspace/related/vscode-extension"}}}
	implement := ImplementPrompt(job, ws)
	for _, want := range []string{"issue #3", "- [ ] finds rules", "every acceptance criterion", "Write the failing test first.",
		"docs/agents/implementer.md", "/workspace/related/vscode-extension", "Makefile, docs/agents/"} {
		if !strings.Contains(implement, want) {
			t.Errorf("implement prompt lacks %q", want)
		}
	}
	ws.Guide = "Check the criteria first."
	review := ReviewPrompt(job, ws)
	for _, want := range []string{"issue #3", "each acceptance criterion", "Check the criteria first.", "docs/agents/reviewer.md",
		`"followUps"`, "/workspace/related/vscode-extension"} {
		if !strings.Contains(review, want) {
			t.Errorf("review prompt lacks %q", want)
		}
	}
	plain, _ := JobFromEnv(env(nil))
	if strings.Contains(ImplementPrompt(plain, Workspace{}), "acceptance criterion") {
		t.Error("a Task with a written goal is asked about acceptance criteria")
	}
}

func TestParseVerdictFollowUps(t *testing.T) {
	out := `{"verdict": "approve", "summary": "Good.", "followUps": [{"title": "Cache schemas", "body": "Parse once."},` +
		`{"title": " ", "body": "no title"}, {"title": "B", "body": ""}, {"title": "C"}, {"title": "D"}]}`
	v, err := ParseVerdict(out)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.FollowUp{{Title: "Cache schemas", Body: "Parse once."}, {Title: "B"}, {Title: "C"}}
	if v.Verdict != VerdictApprove || !slices.Equal(v.FollowUps, want) {
		t.Errorf("ParseVerdict() = %+v, want follow-ups %v (untitled dropped, three at most)", v, want)
	}
}

func TestResultEncodeKeepsFollowUpsShort(t *testing.T) {
	r := Result{Role: RoleReview, Verdict: VerdictApprove, Summary: strings.Repeat("s", 3000),
		FollowUps: []model.FollowUp{{Title: strings.Repeat("t", 300), Body: strings.Repeat("b", 2000)},
			{Title: "Two", Body: strings.Repeat("b", 2000)}, {Title: "Three", Body: strings.Repeat("b", 2000)}}}
	data := r.Encode()
	var back Result
	if err := json.Unmarshal(data, &back); err != nil || len(data) > MaxResultBytes {
		t.Fatalf("Encode() = %d bytes, %v", len(data), err)
	}
	if len(back.FollowUps) != 3 || back.Verdict != VerdictApprove {
		t.Errorf("Encode() lost follow-ups or the verdict: %+v", back)
	}
}

func TestRoleGuideComesFromTheBase(t *testing.T) {
	url := bareRepo(t, map[string]string{"docs/agents/reviewer.md": "Review strictly.\n", "docs/agents/implementer.md": "Test first.\n"})
	ctx := context.Background()
	// The implementer rewrites the reviewer's guide on its branch; the review must not see it.
	if got := Run(ctx, testJob(t, url, RoleImplement, 1), editAgent{path: "docs/agents/reviewer.md"}, t.TempDir()); got.Commit == "" {
		t.Fatalf("implement = %+v", got)
	}
	agent := &promptAgent{reply: `{"verdict": "approve", "summary": "ok"}`}
	Run(ctx, testJob(t, url, RoleReview, 1), agent, t.TempDir())
	if !strings.Contains(agent.prompt, "Review strictly.") || strings.Contains(agent.prompt, "changed") {
		t.Errorf("review prompt has the branch's guide, not main's:\n%s", agent.prompt)
	}
}

func TestRunClonesRelatedRepositories(t *testing.T) {
	url := bareRepo(t, map[string]string{"README.md": "server\n"})
	related := bareRepo(t, map[string]string{"package.json": "{}\n"})
	job := testJob(t, url, RoleImplement, 1)
	job.Related = map[string]string{"vscode-extension": related, "gone": "file:///nonexistent/repo.git"}
	root := t.TempDir()
	agent := &promptAgent{reply: "done", edit: "x.txt"}
	if got := Run(context.Background(), job, agent, filepath.Join(root, "repo")); got.Error != "" {
		t.Fatalf("Run() = %+v", got)
	}
	dir := filepath.Join(root, "related", "vscode-extension")
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err != nil {
		t.Errorf("related repository not cloned: %v", err)
	}
	if out, _ := exec.Command("git", "-C", dir, "remote").Output(); strings.TrimSpace(string(out)) != "" {
		t.Errorf("related clone keeps remote %q; it must not be pushable", out)
	}
	if !strings.Contains(agent.prompt, dir) || !strings.Contains(agent.prompt, "gone (could not be cloned") {
		t.Errorf("prompt doesn't list the related repositories:\n%s", agent.prompt)
	}
}

// promptAgent records the prompt of its first run and answers reply.
type promptAgent struct {
	prompt, reply, edit string
}

func (a *promptAgent) Run(_ context.Context, dir, prompt string) (string, error) {
	if a.prompt == "" {
		a.prompt = prompt
	}
	if a.edit != "" {
		return a.reply, os.WriteFile(filepath.Join(dir, a.edit), []byte("edit\n"), 0o644)
	}
	return a.reply, nil
}

func TestClaudeAgentPassesTheModel(t *testing.T) {
	// "echo" stands in for the CLI: its output shows the arguments it got.
	out, err := ClaudeAgent{Command: []string{"echo", "-p"}, Model: "claude-sonnet-5-5"}.Run(context.Background(), t.TempDir(), "")
	if err != nil || out != "-p --model claude-sonnet-5-5" {
		t.Errorf("Run() = %q, %v", out, err)
	}
}

func TestUnavailable(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name, output string
		want         bool
		retryAt      time.Time
	}{
		{"usage limit with its reset", "claude ended with an error: Claude AI usage limit reached|1790953200", true,
			time.Unix(1790953200, 0).UTC()},
		{"a limit in words", "claude ended with an error: You've hit your limit · resets 3pm (UTC)", true, time.Time{}},
		{"overloaded", "claude ended with an error: API Error: 529 {\"type\":\"overloaded_error\"}", true, time.Time{}},
		{"rate limited", "API Error: 429 {\"error\":{\"type\":\"rate_limit_error\"}}", true, time.Time{}},
		{"bad credentials", "Invalid API key · Please run /login", true, time.Time{}},
		{"an expired token", `{"type":"authentication_error","message":"OAuth token has expired"}`, true, time.Time{}},
		{"the agent's own failure", "claude ended with error_max_turns: stopped after 200 turns", false, time.Time{}},
		{"a test named after a limit", "FAIL TestRateLimiter (0.01s)", false, time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, retryAt := unavailable(tt.output, now)
			if got != tt.want || !retryAt.Equal(tt.retryAt) {
				t.Errorf("unavailable() = %v, %v; want %v, %v", got, retryAt, tt.want, tt.retryAt)
			}
		})
	}
}

func TestRunReportsAnUnavailableAgent(t *testing.T) {
	url := bareRepo(t, map[string]string{"README.md": "hi\n"})
	agent := failingAgent{err: errors.New("claude ended with an error: Claude AI usage limit reached|4102444800")}
	got := Run(context.Background(), testJob(t, url, RoleImplement, 1), agent, t.TempDir())
	if !got.Unavailable || got.RetryAt == nil || got.RetryAt.Unix() != 4102444800 {
		t.Errorf("Run() = %+v, want an unavailable agent back at its reset", got)
	}
}

type failingAgent struct{ err error }

func (a failingAgent) Run(context.Context, string, string) (string, error) { return "", a.err }

func TestUnavailableResetInThePast(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	if down, back := unavailable("usage limit reached|1759417200", now); !down || !back.IsZero() {
		t.Errorf("unavailable() = %v, %v; want unavailable, back now", down, back)
	}
}
