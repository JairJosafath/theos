package agentrunner

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/c4factory/internal/model"
)

func TestMain(m *testing.M) {
	// Failed runs wait this long for a stop that never comes.
	stopGrace = 200 * time.Millisecond
	os.Exit(m.Run())
}

// gitToken is the Git token of every test job.
var gitToken = rand.Text()

func env(overrides map[string]string) func(string) string {
	base := map[string]string{
		EnvRole: RoleImplement, EnvRepoURL: "http://gitea/apps/photo-editor.git", EnvBranch: "task/add-crop",
		EnvGoal: "Add crop", EnvContainer: "photo-editor", EnvProject: "apps", EnvGitUsername: "bot",
		EnvGitToken: gitToken, EnvTask: "add-crop",
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
		{"bad resumed", map[string]string{EnvResumed: "maybe"}, "C4_RESUMED"},
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

func TestJobFromEnvStandardChange(t *testing.T) {
	got, err := JobFromEnv(env(map[string]string{EnvAllowedPaths: "Makefile, .gitea/", EnvResumed: "true"}))
	if err != nil || !slices.Equal(got.AllowedPaths, []string{"Makefile", ".gitea/"}) || !got.Resumed {
		t.Errorf("JobFromEnv() = %v, %v, %v", got.AllowedPaths, got.Resumed, err)
	}
	got, err = JobFromEnv(env(nil))
	if err != nil || got.AllowedPaths != nil || got.Resumed {
		t.Errorf("defaults = %v, %v, %v", got.AllowedPaths, got.Resumed, err)
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
	if strings.Contains(review, gitToken) || strings.Contains(implement, gitToken) {
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

	followUp := Run(ctx, testJob(t, url, RoleReview, 1), FakeAgent{Role: RoleReview, Round: 1, FollowUp: true}, t.TempDir())
	if followUp.Verdict != VerdictApprove || len(followUp.FollowUps) != 1 || len(followUp.FollowUps[0].Criteria) == 0 {
		t.Errorf("review with a follow-up = %+v", followUp)
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
		EnvRelated: "vscode-extension=http://gitea/tools/vscode-extension.git, server=http://gitea/tools/server.git"}))
	if err != nil {
		t.Fatalf("JobFromEnv() error = %v", err)
	}
	want := map[string]string{"vscode-extension": "http://gitea/tools/vscode-extension.git", "server": "http://gitea/tools/server.git"}
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
		`"followUps"`, `"criteria"`, "at most 200 characters", "/workspace/related/vscode-extension"} {
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
	out := `{"verdict": "approve", "summary": "Good.", "followUps": [{"title": "Cache schemas", "body": "Parse once.",` +
		` "criteria": [" Parsed once per file ", "", "A test counts parses", "Three", "Four"]},` +
		`{"title": " ", "body": "no title"}, {"title": "B", "body": ""}, {"title": "C"}, {"title": "D"}]}`
	v, err := ParseVerdict(out)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.FollowUp{{Title: "Cache schemas", Body: "Parse once.",
		Criteria: []string{"Parsed once per file", "A test counts parses", "Three"}}, {Title: "B"}, {Title: "C"}}
	if v.Verdict != VerdictApprove || !reflect.DeepEqual(v.FollowUps, want) {
		t.Errorf("ParseVerdict() = %+v, want follow-ups %v (untitled dropped, three at most, blank criteria dropped)", v, want)
	}
}

func TestResultEncodeKeepsFollowUpsShort(t *testing.T) {
	r := Result{Role: RoleReview, Verdict: VerdictApprove, Summary: strings.Repeat("s", 3000),
		FollowUps: []model.FollowUp{{Title: strings.Repeat("t", 300), Body: strings.Repeat("b", 2000),
			Criteria: []string{strings.Repeat("c", 500), "Short"}},
			{Title: "Two", Body: strings.Repeat("b", 2000)}, {Title: "Three", Body: strings.Repeat("b", 2000)}}}
	data := r.Encode()
	var back Result
	if err := json.Unmarshal(data, &back); err != nil || len(data) > MaxResultBytes {
		t.Fatalf("Encode() = %d bytes, %v", len(data), err)
	}
	if len(back.FollowUps) != 3 || back.Verdict != VerdictApprove {
		t.Errorf("Encode() lost follow-ups or the verdict: %+v", back)
	}
	if c := back.FollowUps[0].Criteria; len(c) != 2 || len(c[0]) > maxFollowUpCriterion || c[1] != "Short" {
		t.Errorf("Encode() criteria = %q, want both, the long one shortened", c)
	}
	if len(r.FollowUps[0].Criteria[0]) != 500 {
		t.Error("Encode() changed the caller's criteria")
	}
}

// A suggested issue's criteria are the checklist its work is judged by (#378):
// one the reviewer kept within bounds reaches the issue whole, and a longer one
// ends at a whole word with a marker, never mid-word.
func TestFollowUpCriteriaReachTheIssueWhole(t *testing.T) {
	whole := "sprout plant with text containing a newline or an ESC character either fails with exit 2 " +
		"and a clear message, or the text is stored with the control characters escaped"
	long := strings.Repeat("checks every flag of the plant command ", 10)
	out := `{"verdict": "approve", "summary": "Good.", "followUps": [{"title": "Reject control characters", ` +
		`"body": "Todo text may hold control characters.", "criteria": [` + strconv.Quote(whole) + `, ` + strconv.Quote(long) + `]}]}`
	v, err := ParseVerdict(out)
	if err != nil {
		t.Fatal(err)
	}
	var back Result
	if err := json.Unmarshal(Result{Role: RoleReview, Verdict: v.Verdict, Summary: v.Summary, FollowUps: v.FollowUps}.Encode(), &back); err != nil {
		t.Fatal(err)
	}
	body := model.FollowUpIssue(back.FollowUps[0], "reviewer", "PR 7", 5, "task-5")
	if !strings.Contains(body, "- [ ] "+whole+"\n") {
		t.Errorf("the issue lost part of a criterion within bounds:\n%s", body)
	}
	got := back.FollowUps[0].Criteria[1]
	words := strings.Fields(strings.TrimSuffix(got, "…"))
	if !strings.HasSuffix(got, "…") || len(got) > maxFollowUpCriterion || !strings.HasPrefix(long, strings.Join(words, " ")+" ") ||
		!slices.Contains(strings.Fields(long), words[len(words)-1]) {
		t.Errorf("a long criterion = %q, want it cut at a whole word with a marker, at most %d bytes", got, maxFollowUpCriterion)
	}
}

func TestShortenWords(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"fails with exit 2, or the text is stored", 22, "fails with exit 2…"},
		{"fails with exit 2, or the text is stored", 28, "fails with exit 2, or the…"},
		{"one" + strings.Repeat("o", 40), 10, "oneoooo…"},
		{"héé héé héé", 9, "héé…"},
	} {
		if got := shortenWords(tc.in, tc.n); got != tc.want || len(got) > max(tc.n, len(tc.in)) {
			t.Errorf("shortenWords(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

func TestResultEncodeDropsFollowUpsBeforeAskedChanges(t *testing.T) {
	long := strings.Repeat("c", 120)
	f := model.FollowUp{Title: strings.Repeat("t", 100), Body: strings.Repeat("b", 400), Criteria: []string{long, long, long}}
	summary := strings.Repeat("s", 2500)
	changes := Result{Role: RoleReview, Verdict: VerdictChanges, Summary: summary, FollowUps: []model.FollowUp{f, f, f}}
	var back Result
	if err := json.Unmarshal(changes.Encode(), &back); err != nil || back.Summary != summary || back.FollowUps != nil {
		t.Errorf("Encode() of changes kept follow-ups (%d) over the summary (%d bytes), %v", len(back.FollowUps), len(back.Summary), err)
	}
	approve := changes
	approve.Verdict, approve.Summary = VerdictApprove, "Good."
	if err := json.Unmarshal(approve.Encode(), &back); err != nil || len(back.FollowUps) != 3 {
		t.Errorf("Encode() of an approval with full follow-ups kept %d, want 3, %v", len(back.FollowUps), err)
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

func TestClaudeAgentReportsTheCost(t *testing.T) {
	// A shell script stands in for the CLI: it prints what the CLI would.
	tests := []struct {
		name, output string
		want         Cost
		wantErr      bool
	}{
		{"a reported cost", `{"result": "done", "total_cost_usd": 0.31234}`, Cost{MilliUSD: 312, Reported: true}, false},
		{"a reported cost of nothing", `{"result": "done", "total_cost_usd": 0}`, Cost{Reported: true}, false},
		{"a failed call's cost", `{"result": "stopped", "is_error": true, "subtype": "error_max_turns", "total_cost_usd": 1.5}`,
			Cost{MilliUSD: 1500, Reported: true}, true},
		{"no cost in the output", `{"result": "done"}`, Cost{}, false},
		{"plain text", `done`, Cost{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := ClaudeAgent{Command: []string{"sh", "-c", "cat >/dev/null; printf '%s' '" + tt.output + "'"}}
			answer, cost, err := agent.RunReportingCost(context.Background(), t.TempDir(), "plan it")
			if cost != tt.want || (err != nil) != tt.wantErr || answer == "" {
				t.Errorf("RunReportingCost() = %q, %+v, %v; want cost %+v", answer, cost, err, tt.want)
			}
		})
	}
}

// costAgent approves, and says each call cost milliUSD.
type costAgent struct{ milliUSD int64 }

func (a costAgent) Run(ctx context.Context, dir, prompt string) (string, error) {
	answer, _, err := a.RunReportingCost(ctx, dir, prompt)
	return answer, err
}

func (a costAgent) RunReportingCost(context.Context, string, string) (string, Cost, error) {
	return `{"verdict": "approve", "summary": "fine"}`, Cost{MilliUSD: a.milliUSD, Reported: true}, nil
}

func TestRunReportsTheCost(t *testing.T) {
	url := bareRepo(t, map[string]string{"README.md": "hi\n"})
	ctx := context.Background()
	Run(ctx, testJob(t, url, RoleImplement, 1), FakeAgent{Role: RoleImplement, Round: 1}, t.TempDir())

	reported := Run(ctx, testJob(t, url, RoleReview, 1), costAgent{milliUSD: 42}, t.TempDir())
	if reported.CostMilliUSD == nil || *reported.CostMilliUSD != 42 {
		t.Errorf("a reporting agent's run = %+v, want a cost of 42", reported)
	}
	var decoded Result
	if err := json.Unmarshal(reported.Encode(), &decoded); err != nil || decoded.CostMilliUSD == nil ||
		*decoded.CostMilliUSD != 42 {
		t.Errorf("decoded = %+v, %v; want the cost kept", decoded, err)
	}

	unreported := Run(ctx, testJob(t, url, RoleReview, 1), FakeAgent{Role: RoleReview, Round: 1}, t.TempDir())
	if unreported.Verdict != VerdictApprove || unreported.CostMilliUSD != nil {
		t.Errorf("a run whose agent doesn't report its cost = %+v, want no cost", unreported)
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

func TestRunReportsAnInterruptedRun(t *testing.T) {
	url := bareRepo(t, map[string]string{"README.md": "hi\n"})
	ctx, cancel := context.WithCancel(context.Background())
	got := Run(ctx, testJob(t, url, RoleImplement, 1), killedAgent{stop: cancel}, t.TempDir())
	if !got.Interrupted || got.Unavailable || got.Error != "claude failed: signal: killed" {
		t.Errorf("Run() = %+v, want an interrupted run", got)
	}
}

func TestRunKilledBeforeTheStopIsInterrupted(t *testing.T) {
	// A node restart: every process of the pod gets SIGTERM at once, and the
	// agent dies before c4agent's own signal cancels ctx.
	url := bareRepo(t, map[string]string{"README.md": "hi\n"})
	ctx, cancel := context.WithCancel(context.Background())
	agent := killedAgent{stop: func() { time.AfterFunc(50*time.Millisecond, cancel) }}
	got := Run(ctx, testJob(t, url, RoleImplement, 1), agent, t.TempDir())
	if !got.Interrupted || got.Error != "claude failed: signal: killed" {
		t.Errorf("Run() = %+v, want an interrupted run", got)
	}
}

func TestRunKilledWithoutStopIsTheAgentsFailure(t *testing.T) {
	// An out-of-memory kill: the agent dies, but nobody told c4agent to stop.
	url := bareRepo(t, map[string]string{"README.md": "hi\n"})
	got := Run(context.Background(), testJob(t, url, RoleImplement, 1),
		failingAgent{err: errors.New("claude failed: signal: killed")}, t.TempDir())
	if got.Interrupted || got.Error == "" {
		t.Errorf("Run() = %+v, want a failed run that is not interrupted", got)
	}
}

// killedAgent is an agent killed because c4agent was told to stop.
type killedAgent struct{ stop context.CancelFunc }

func (a killedAgent) Run(context.Context, string, string) (string, error) {
	a.stop()
	return "", errors.New("claude failed: signal: killed")
}

type failingAgent struct{ err error }

func (a failingAgent) Run(context.Context, string, string) (string, error) { return "", a.err }

func TestUnavailableResetInThePast(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	if down, back := unavailable("usage limit reached|1759417200", now); !down || !back.IsZero() {
		t.Errorf("unavailable() = %v, %v; want unavailable, back now", down, back)
	}
}

// pushToMain commits a file straight to main of the repository at repoURL, as a
// merge of another change would.
func pushToMain(t *testing.T, repoURL, path, data string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("clone", "--quiet", repoURL, ".")
	if err := os.WriteFile(filepath.Join(dir, path), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "--all")
	run("commit", "--quiet", "-m", "Another change")
	run("push", "--quiet", "origin", "main")
}

func TestRunImplementMergesMain(t *testing.T) {
	ctx := context.Background()
	t.Run("cleanly", func(t *testing.T) {
		url := bareRepo(t, map[string]string{"README.md": "hi\n"})
		Run(ctx, testJob(t, url, RoleImplement, 1), FakeAgent{Role: RoleImplement, Round: 1}, t.TempDir())
		pushToMain(t, url, "OTHER.md", "merged first\n")
		got := Run(ctx, testJob(t, url, RoleImplement, 2), FakeAgent{Role: RoleImplement, Round: 2}, t.TempDir())
		if got.Error != "" {
			t.Fatalf("round 2 = %+v", got)
		}
		dir := t.TempDir()
		if out, err := exec.Command("git", "clone", "--quiet", "--branch", "task/add-crop", url, dir).CombinedOutput(); err != nil {
			t.Fatalf("clone: %v: %s", err, out)
		}
		if _, err := os.Stat(filepath.Join(dir, "OTHER.md")); err != nil {
			t.Errorf("the branch lacks main's change: %v", err)
		}
	})
	t.Run("with conflicts the agent resolves", func(t *testing.T) {
		url := bareRepo(t, map[string]string{"README.md": "hi\n"})
		Run(ctx, testJob(t, url, RoleImplement, 1), editAgent{path: "README.md"}, t.TempDir())
		pushToMain(t, url, "README.md", "main's line\n")
		agent := &promptAgent{reply: "resolved", edit: "README.md"}
		got := Run(ctx, testJob(t, url, RoleImplement, 2), agent, t.TempDir())
		if got.Error != "" || got.Commit == "" {
			t.Fatalf("round 2 = %+v", got)
		}
		if !strings.Contains(agent.prompt, "conflicts in README.md") {
			t.Errorf("prompt doesn't name the conflicts:\n%s", agent.prompt)
		}
		out, err := exec.Command("git", "--git-dir", strings.TrimPrefix(url, "file://"), "log", "--format=%P", "-1", "task/add-crop").Output()
		if err != nil || len(strings.Fields(string(out))) != 2 {
			t.Errorf("the branch head is not a merge commit: %q, %v", out, err)
		}
	})
}

// filesAgent writes each file and returns summary.
type filesAgent struct {
	files   map[string]string
	remove  []string
	summary string
}

func (a filesAgent) Run(_ context.Context, dir, _ string) (string, error) {
	for _, name := range a.remove {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return "", err
		}
	}
	for name, data := range a.files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			return "", err
		}
	}
	return a.summary, nil
}

// cloneTask clones the Task branch of the repository and returns the directory.
func cloneTask(t *testing.T, url string) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "clone", "--quiet", "--branch", "task/add-crop", url, dir).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v: %s", err, out)
	}
	return dir
}

func fileIn(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return string(data)
}

func standardJob(t *testing.T, url string, check ...string) Job {
	t.Helper()
	job := testJob(t, url, RoleImplement, 1, check...)
	job.Issue = 4
	job.ProtectedPaths = []string{"Makefile"}
	return job
}

func standardRepo(t *testing.T) string {
	t.Helper()
	return bareRepo(t, map[string]string{"Makefile": "check:\n", "README.md": "hi\n"})
}

func TestRunImplementProposesAStandardChange(t *testing.T) {
	ctx := context.Background()
	summary := "Done.\nStandard change: make dist must build the packages"
	t.Run("with other work", func(t *testing.T) {
		url := standardRepo(t)
		agent := filesAgent{files: map[string]string{"Makefile": "check:\n\techo more\n", "src.txt": "x\n"}, summary: summary}
		got := Run(ctx, standardJob(t, url), agent, t.TempDir())
		want := model.Proposal{Branch: "standard/add-crop", Paths: []string{"Makefile"}, Reason: "make dist must build the packages"}
		if got.Error != "" || got.Proposal == nil || !slices.Equal(got.Proposal.Paths, want.Paths) ||
			got.Proposal.Branch != want.Branch || got.Proposal.Reason != want.Reason {
			t.Fatalf("Run() = %+v, proposal %+v", got, got.Proposal)
		}
		out, err := exec.Command("git", "--git-dir", strings.TrimPrefix(url, "file://"), "diff", "--name-only", "main", "standard/add-crop").Output()
		if err != nil || string(out) != "Makefile\n" {
			t.Errorf("proposal diff = %q, %v", out, err)
		}
		dir := cloneTask(t, url)
		if fileIn(t, dir, "src.txt") != "x\n" || fileIn(t, dir, "Makefile") != "check:\n" {
			t.Errorf("task branch: src.txt = %q, Makefile = %q", fileIn(t, dir, "src.txt"), fileIn(t, dir, "Makefile"))
		}
		if got.Commit != branchHead(t, url, "task/add-crop") {
			t.Errorf("Commit = %q, want the task branch head", got.Commit)
		}
	})
	t.Run("nothing else to do", func(t *testing.T) {
		url := standardRepo(t)
		agent := filesAgent{files: map[string]string{"Makefile": "check:\n\techo more\n"}, summary: summary}
		got := Run(ctx, standardJob(t, url), agent, t.TempDir())
		if got.Error != "" || got.Proposal == nil || got.Commit != branchHead(t, url, "task/add-crop") {
			t.Fatalf("Run() = %+v", got)
		}
		if fileIn(t, cloneTask(t, url), "Makefile") != "check:\n" {
			t.Error("the task branch carries the protected edit")
		}
	})
}

// A round that adds one protected file and deletes another: the proposal adds and
// removes them as the work head has them, and the task branch restores both as
// main has them (removing the added one).
func TestRunImplementProposesAddedAndDeletedFiles(t *testing.T) {
	const added, deleted = ".gitea/workflows/release.yaml", ".gitea/workflows/old.yaml"
	url := bareRepo(t, map[string]string{"Makefile": "check:\n", ".gitea/workflows/check.yaml": "on: push\n",
		deleted: "on: schedule\n"})
	job := standardJob(t, url)
	job.ProtectedPaths = []string{"Makefile", ".gitea/"}
	agent := filesAgent{files: map[string]string{added: "on:\n  push:\n    tags: [v*]\n", "src.txt": "x\n"},
		remove: []string{deleted}, summary: "Done.\nStandard change: releases need a workflow"}
	got := Run(context.Background(), job, agent, t.TempDir())
	if got.Error != "" || got.Proposal == nil || !slices.Equal(got.Proposal.Paths, []string{deleted, added}) {
		t.Fatalf("Run() = %+v, proposal %+v", got, got.Proposal)
	}
	out, err := exec.Command("git", "--git-dir", strings.TrimPrefix(url, "file://"), "diff", "--no-renames",
		"--name-status", "main", "standard/add-crop").Output()
	if want := "D\t" + deleted + "\nA\t" + added + "\n"; err != nil || string(out) != want {
		t.Errorf("proposal diff = %q, %v; want %q", out, err, want)
	}
	dir := cloneTask(t, url)
	if _, err := os.Stat(filepath.Join(dir, added)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the task branch keeps the added %s (%v)", added, err)
	}
	if fileIn(t, dir, deleted) != "on: schedule\n" || fileIn(t, dir, "src.txt") != "x\n" {
		t.Errorf("task branch: %s = %q, src.txt = %q", deleted, fileIn(t, dir, deleted), fileIn(t, dir, "src.txt"))
	}
	if got.Commit != branchHead(t, url, "task/add-crop") {
		t.Errorf("Commit = %q, want the task branch head", got.Commit)
	}
}

func TestRunImplementRefusesTheAgentsRules(t *testing.T) {
	url := bareRepo(t, map[string]string{"AGENTS.md": "rules\n", "README.md": "hi\n"})
	job := standardJob(t, url)
	job.ProtectedPaths = []string{"AGENTS.md"}
	got := Run(context.Background(), job, filesAgent{files: map[string]string{"AGENTS.md": "mine\n"}, summary: "Standard change: x"}, t.TempDir())
	if !strings.Contains(got.Error, "AGENTS.md") || got.Proposal != nil {
		t.Errorf("Run() = %+v", got)
	}
	if branchHead(t, url, "task/add-crop") != "" || branchHead(t, url, "standard/add-crop") != "" {
		t.Error("something was pushed")
	}
}

func TestRunImplementGated(t *testing.T) {
	ctx := context.Background()
	t.Run("inside the allowance", func(t *testing.T) {
		url := standardRepo(t)
		job := standardJob(t, url)
		job.AllowedPaths = []string{"Makefile"}
		got := Run(ctx, job, filesAgent{files: map[string]string{"Makefile": "check:\n\tgo test\n"}, summary: "Done."}, t.TempDir())
		if got.Error != "" || got.Proposal != nil {
			t.Fatalf("Run() = %+v", got)
		}
		if fileIn(t, cloneTask(t, url), "Makefile") != "check:\n\tgo test\n" {
			t.Error("the allowed change is missing from the task branch")
		}
		if branchHead(t, url, "standard/add-crop") != "" {
			t.Error("a proposal branch was pushed")
		}
	})
	t.Run("outside the allowance", func(t *testing.T) {
		url := standardRepo(t)
		job := standardJob(t, url)
		job.AllowedPaths = []string{"Makefile"}
		agent := filesAgent{files: map[string]string{"Makefile": "check:\n\tgo test\n", "README.md": "changed\n"}, summary: "Done."}
		got := Run(ctx, job, agent, t.TempDir())
		if !strings.Contains(got.Error, "this Task may change only Makefile") {
			t.Errorf("Error = %q", got.Error)
		}
		if branchHead(t, url, "task/add-crop") != "" {
			t.Error("a refused change was pushed")
		}
	})
}

func TestRunImplementKeepsNoProtectedEditWhenTheCheckFails(t *testing.T) {
	url := standardRepo(t)
	agent := filesAgent{files: map[string]string{"Makefile": "check:\n\techo more\n", "src.txt": "x\n"}, summary: "Standard change: needed"}
	got := Run(context.Background(), standardJob(t, url, "sh", "-c", "exit 1"), agent, t.TempDir())
	if !strings.Contains(got.Error, "still fails") || !strings.Contains(got.Error, "was not kept") || got.Proposal != nil {
		t.Errorf("Run() = %+v", got)
	}
	if branchHead(t, url, "standard/add-crop") != "" {
		t.Error("a proposal was pushed although the check failed")
	}
	dir := cloneTask(t, url)
	if fileIn(t, dir, "Makefile") != "check:\n" || fileIn(t, dir, "src.txt") != "x\n" {
		t.Errorf("task branch: Makefile = %q, src.txt = %q", fileIn(t, dir, "Makefile"), fileIn(t, dir, "src.txt"))
	}
}

func TestRunImplementRefusesAProposalThatDoesNotFit(t *testing.T) {
	url := standardRepo(t)
	job := standardJob(t, url)
	long := strings.Repeat("d", 200) + "/" + strings.Repeat("f", 100)
	job.ProtectedPaths = []string{strings.Repeat("d", 200) + "/"}
	got := Run(context.Background(), job, filesAgent{files: map[string]string{long: "x\n"}, summary: "Standard change: x"}, t.TempDir())
	if !strings.Contains(got.Error, "at most 256") || got.Proposal != nil {
		t.Errorf("Run() = %+v", got)
	}
	if branchHead(t, url, "task/add-crop") != "" {
		t.Error("a refused proposal was pushed")
	}
}

func TestResultEncodeKeepsTheProposal(t *testing.T) {
	paths := make([]string, 0, 20)
	for i := range 20 {
		paths = append(paths, "docs/protected/file-"+strings.Repeat("a", i)+".md")
	}
	r := Result{Role: RoleImplement, Commit: strings.Repeat("a", 40), Summary: strings.Repeat("s", 10<<10),
		Proposal: &model.Proposal{Branch: "standard/add-crop", Paths: paths, Reason: strings.Repeat("r", 10<<10)}}
	short := Result{Role: RoleImplement, Summary: strings.Repeat("s", 5000),
		Proposal: &model.Proposal{Branch: "standard/add-crop", Paths: []string{"Makefile"}, Reason: strings.Repeat("r", 500)}}
	var kept Result
	if err := json.Unmarshal(short.Encode(), &kept); err != nil || kept.Proposal == nil || kept.Proposal.Reason != strings.Repeat("r", 500) {
		t.Errorf("a 500-byte Reason beside a 5000-byte summary = %+v, %v", kept.Proposal, err)
	}
	data := r.Encode()
	if len(data) > MaxResultBytes {
		t.Fatalf("Encode() = %d bytes, want at most %d", len(data), MaxResultBytes)
	}
	var back Result
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Proposal == nil || back.Proposal.Branch != "standard/add-crop" || !slices.Equal(back.Proposal.Paths, paths) {
		t.Errorf("decoded proposal = %+v", back.Proposal)
	}
	if len(back.Proposal.Reason) < 500 {
		t.Errorf("decoded Reason = %d bytes, want it kept next to a long summary", len(back.Proposal.Reason))
	}
	if len(r.Proposal.Reason) != 10<<10 {
		t.Error("Encode() changed the caller's proposal")
	}
}

func TestResultEncodeTerminatesWhenTheProposalCannotFit(t *testing.T) {
	r := Result{Role: RoleImplement, Proposal: &model.Proposal{Branch: "b", Paths: []string{strings.Repeat("p", 5000)}}}
	if len(r.Encode()) == 0 {
		t.Error("Encode() returned nothing")
	}
}

type renameAgent struct{ from, to string }

func (a renameAgent) Run(_ context.Context, dir, _ string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, a.to)), 0o755); err != nil {
		return "", err
	}
	return "Standard change: move the build", os.Rename(filepath.Join(dir, a.from), filepath.Join(dir, a.to))
}

func TestRunImplementSeesARenamedProtectedFile(t *testing.T) {
	ctx := context.Background()
	t.Run("proposed", func(t *testing.T) {
		url := standardRepo(t)
		got := Run(ctx, standardJob(t, url), renameAgent{"Makefile", "src/build.mk"}, t.TempDir())
		if got.Error != "" || got.Proposal == nil || !slices.Equal(got.Proposal.Paths, []string{"Makefile"}) {
			t.Fatalf("Run() = %+v, proposal %+v", got, got.Proposal)
		}
		if fileIn(t, cloneTask(t, url), "Makefile") != "check:\n" {
			t.Error("the task branch lost the protected file")
		}
	})
	t.Run("refused without an issue", func(t *testing.T) {
		url := standardRepo(t)
		job := standardJob(t, url)
		job.Issue = 0
		got := Run(ctx, job, renameAgent{"Makefile", "src/build.mk"}, t.TempDir())
		if !strings.Contains(got.Error, "touches Makefile") || branchHead(t, url, "task/add-crop") != "" {
			t.Errorf("Run() = %+v", got)
		}
	})
}

func TestRunImplementResumed(t *testing.T) {
	ctx := context.Background()
	roundOne := func(t *testing.T, url string) {
		first := Run(ctx, testJob(t, url, RoleImplement, 1), FakeAgent{Role: RoleImplement, Round: 1}, t.TempDir())
		if first.Error != "" || first.Commit == "" {
			t.Fatalf("round 1 = %+v", first)
		}
	}
	t.Run("branch equals main", func(t *testing.T) {
		url := bareRepo(t, map[string]string{"README.md": "hi\n"})
		roundOne(t, url)
		pushToMain(t, url, "STAND-IN.md", "Round 1 of the stand-in agent.\n")
		job := testJob(t, url, RoleImplement, 1)
		job.Resumed = true
		got := Run(ctx, job, FakeAgent{NoChange: true}, t.TempDir())
		if got.Error != "" || !got.Empty || got.Commit != "" {
			t.Errorf("Run() = %+v, want Empty with no error and no commit", got)
		}
	})
	t.Run("work left, nothing new", func(t *testing.T) {
		url := bareRepo(t, map[string]string{"README.md": "hi\n"})
		roundOne(t, url)
		job := testJob(t, url, RoleImplement, 2)
		job.Resumed = true
		got := Run(ctx, job, FakeAgent{NoChange: true}, t.TempDir())
		if got.Error != "" || got.Empty || got.Commit != branchHead(t, url, "task/add-crop") {
			t.Errorf("Run() = %+v, want the branch head and no Empty", got)
		}
	})
}

func TestStandardChangePrompts(t *testing.T) {
	issue := Job{Container: "c", Project: "p", Issue: 4, Goal: "Issue #4", Branch: "task/x", Base: "main",
		ProtectedPaths: []string{"AGENTS.md", "Makefile"}, CheckCommand: []string{"make", "check"}}
	goal := issue
	goal.Issue = 0
	gated := issue
	gated.AllowedPaths = []string{"Makefile"}
	cases := []struct {
		name   string
		prompt string
		want   []string
		not    []string
	}{
		{"implement issue", ImplementPrompt(issue, Workspace{}), []string{"Standard change:",
			"The protected change merges first, so it must work on main without the rest of your work",
			"may rely on it", "use a hook", "npm run dist --if-present", "each with a test that proves it",
			"where it is met and tested", "AGENTS.md, docs/agents/ and .c4factory.yaml",
			"even if your role guide or AGENTS.md says to stop"}, []string{"must run on main alone"}},
		{"implement goal", ImplementPrompt(goal, Workspace{}), []string{"do not change", "tests for it"},
			[]string{"Standard change:"}},
		{"implement gated", ImplementPrompt(gated, Workspace{}), []string{"Change only Makefile", "git merge origin/standard/",
			"proposal branch", "even if your role guide or AGENTS.md says to stop or to leave such files alone",
			"Add no tests", `"make check" passing with the change is the evidence`},
			[]string{"Standard change:", "each with a test", "tests for it", "and tested"}},
		{"review issue", ReviewPrompt(issue, Workspace{}), []string{"Makefile", "a Standard change",
			"has tests for its behaviour", "where it is tested", "behaviour without tests"}, []string{"weakens no check"}},
		{"review gated", ReviewPrompt(gated, Workspace{}), []string{"weakens no check", "works with this change alone",
			"no more than its issue asks", "do not require tests", `"make check" passing with the change`,
			"The proposal branch named in the issue was only the starting point", "is never merged",
			"Judge task/x alone", "never ask for the proposal branch to be changed"},
			[]string{"has tests for its behaviour", "where it is tested", "behaviour without tests"}},
	}
	for _, c := range cases {
		prompt := strings.Join(strings.Fields(c.prompt), " ") // phrases may wrap
		for _, w := range c.want {
			if !strings.Contains(prompt, w) {
				t.Errorf("%s prompt lacks %q", c.name, w)
			}
		}
		for _, w := range c.not {
			if strings.Contains(prompt, w) {
				t.Errorf("%s prompt contains %q", c.name, w)
			}
		}
	}
}

// encoded returns a w×h image encoded by encode.
func encoded(t *testing.T, w, h int, encode func(io.Writer, image.Image) error) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestReviewIsShownTheChangedImages(t *testing.T) {
	jpg := func(w io.Writer, m image.Image) error { return jpeg.Encode(w, m, nil) }
	url := bareRepo(t, map[string]string{"README.md": "hi\n", "shot.jpg": string(encoded(t, 4, 4, jpg)),
		"same.png": string(encoded(t, 8, 8, png.Encode))})
	ctx := context.Background()
	added, changed := encoded(t, 936, 189, png.Encode), encoded(t, 640, 480, jpg)
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)
	implement := filesAgent{files: map[string]string{"added.png": string(added), "shot.jpg": string(changed),
		"Logo.SVG": string(svg), "broken.gif": "not a gif", "notes.md": "no image\n"}, summary: "done"}
	if got := Run(ctx, testJob(t, url, RoleImplement, 1), implement, t.TempDir()); got.Error != "" {
		t.Fatalf("implement = %+v", got)
	}

	agent := &promptAgent{reply: `{"verdict": "approve", "summary": "ok"}`}
	if got := Run(ctx, testJob(t, url, RoleReview, 1), agent, t.TempDir()); got.Error != "" {
		t.Fatalf("review = %+v", got)
	}
	for _, want := range []string{"Open each one",
		"- added.png: 936×189 pixels, " + strconv.Itoa(len(added)) + " bytes\n",
		"- shot.jpg: 640×480 pixels, " + strconv.Itoa(len(changed)) + " bytes\n",
		"- Logo.SVG: " + strconv.Itoa(len(svg)) + " bytes\n",
		"- broken.gif: 9 bytes, not a readable image\n"} {
		if !strings.Contains(agent.prompt, want) {
			t.Errorf("review prompt lacks %q:\n%s", want, agent.prompt)
		}
	}
	for _, unwanted := range []string{"same.png", "notes.md"} {
		if strings.Contains(agent.prompt, unwanted) {
			t.Errorf("review prompt lists %s, which the change doesn't add or change as an image", unwanted)
		}
	}
}

func TestReviewWithoutChangedImagesHasNoImageSection(t *testing.T) {
	url := bareRepo(t, map[string]string{"README.md": "hi\n", "same.png": string(encoded(t, 8, 8, png.Encode))})
	ctx := context.Background()
	if got := Run(ctx, testJob(t, url, RoleImplement, 1), FakeAgent{Role: RoleImplement, Round: 1}, t.TempDir()); got.Error != "" {
		t.Fatalf("implement = %+v", got)
	}
	agent := &promptAgent{reply: `{"verdict": "approve", "summary": "ok"}`}
	Run(ctx, testJob(t, url, RoleReview, 1), agent, t.TempDir())
	if agent.prompt == "" || strings.Contains(agent.prompt, "images") || strings.Contains(agent.prompt, "same.png") {
		t.Errorf("review prompt of a change without images has an image section:\n%s", agent.prompt)
	}
}

func TestImageListIsCapped(t *testing.T) {
	images := make([]ChangedImage, maxListedImages+3)
	for i := range images {
		images[i] = ChangedImage{Path: "img" + strconv.Itoa(i) + ".svg", Bytes: 1}
	}
	job, _ := JobFromEnv(env(nil))
	prompt := ReviewPrompt(job, Workspace{Images: images})
	if strings.Count(prompt, ".svg: 1 bytes") != maxListedImages || !strings.Contains(prompt, "and 3 more") {
		t.Errorf("review prompt lists %d images and no overflow line:\n%s", strings.Count(prompt, ".svg:"), prompt)
	}
}
