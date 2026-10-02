// Package agentrunner is the logic of c4agent, the program that runs inside an
// agent's sandbox. It reads one job from its environment, clones the Container's
// repository with a token scoped to that repository, runs the agent, and returns
// one Result. It has no Kubernetes credentials: the operator reads the Result from
// the sandbox pod's termination message.
//
// It must not import Kubernetes, controller-runtime, the api package or the Git
// host adapter; see docs/architecture.md.
package agentrunner

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Roles a sandbox can play in a Task round.
const (
	RoleImplement = "implement"
	RoleReview    = "review"
)

// Environment variables the operator sets on the sandbox.
const (
	EnvRole         = "C4_ROLE"
	EnvRepoURL      = "C4_REPO_URL"
	EnvBranch       = "C4_BRANCH"
	EnvBase         = "C4_BASE"
	EnvGoal         = "C4_GOAL"
	EnvFeedback     = "C4_FEEDBACK"
	EnvProject      = "C4_PROJECT"
	EnvContainer    = "C4_CONTAINER"
	EnvTask         = "C4_TASK"
	EnvRound        = "C4_ROUND"
	EnvMaxRounds    = "C4_MAX_ROUNDS"
	EnvGitUsername  = "C4_GIT_USERNAME"
	EnvGitToken     = "C4_GIT_TOKEN"
	EnvCheckCommand = "C4_CHECK_COMMAND"
	EnvContext      = "C4_CONTEXT"
	// EnvProtectedPaths lists the Standard's protected paths, comma-separated.
	EnvProtectedPaths = "C4_PROTECTED_PATHS"
	// EnvAllowedPaths lists the protected paths an approved Standard change lets
	// this Task change, comma-separated.
	EnvAllowedPaths = "C4_ALLOWED_PATHS"
	// EnvResumed is "true" when the Task resumes after its Standard change merged.
	EnvResumed = "C4_RESUMED"
	// EnvIssue is the number of the issue the goal comes from, if any.
	EnvIssue = "C4_ISSUE"
	// EnvHarness and EnvModel say which agent program runs, with which model.
	EnvHarness = "C4_HARNESS"
	EnvModel   = "C4_MODEL"
	// EnvRelated lists the repositories of related Containers, as
	// name=url pairs, comma-separated. They are cloned read-only.
	EnvRelated = "C4_RELATED"
)

// Harnesses: the agent programs c4agent can run.
const (
	HarnessClaudeCode = "claude-code"
	HarnessStandIn    = "stand-in"
)

// Job is one sandbox run: implement a goal on a branch, or review that branch.
type Job struct {
	Role     string
	RepoURL  string
	Branch   string
	Base     string
	Goal     string
	Feedback string
	// Context says what the application is for: its system, description and needs.
	Context   string
	Project   string
	Container string
	Task      string
	Round     int
	// MaxRounds is the last round the Task gets; 0 when unknown.
	MaxRounds int
	Username  string
	Token     string
	// CheckCommand is the quality contract, run in the repository after the agent.
	CheckCommand []string
	// ProtectedPaths are the Standard's paths the agent must not change, next to
	// the platform's.
	ProtectedPaths []string
	// AllowedPaths gate the Task: when set, it may change only these paths.
	AllowedPaths []string
	// Resumed is true when the Task resumes after its Standard change merged.
	Resumed bool
	// Issue is the issue the goal comes from; 0 for a written goal.
	Issue int
	// Harness is the agent program, and Model the model it uses (empty: its default).
	Harness string
	Model   string
	// Related are the repositories of related Containers, by Container name.
	Related map[string]string
}

// JobFromEnv reads a Job from environment variables.
func JobFromEnv(getenv func(string) string) (Job, error) {
	job := Job{
		Role:      getenv(EnvRole),
		RepoURL:   getenv(EnvRepoURL),
		Branch:    getenv(EnvBranch),
		Base:      getenv(EnvBase),
		Goal:      getenv(EnvGoal),
		Feedback:  getenv(EnvFeedback),
		Context:   getenv(EnvContext),
		Project:   getenv(EnvProject),
		Container: getenv(EnvContainer),
		Task:      getenv(EnvTask),
		Username:  getenv(EnvGitUsername),
		Token:     getenv(EnvGitToken),
		Harness:   getenv(EnvHarness),
		Model:     getenv(EnvModel),
	}
	if job.Base == "" {
		job.Base = "main"
	}
	job.ProtectedPaths = splitList(getenv(EnvProtectedPaths))
	job.AllowedPaths = splitList(getenv(EnvAllowedPaths))
	if resumed := getenv(EnvResumed); resumed != "" {
		var err error
		if job.Resumed, err = strconv.ParseBool(resumed); err != nil {
			return Job{}, fmt.Errorf("%s=%q is not true or false", EnvResumed, resumed)
		}
	}
	if job.Harness == "" {
		job.Harness = HarnessClaudeCode
	}
	if job.Harness != HarnessClaudeCode && job.Harness != HarnessStandIn {
		return Job{}, fmt.Errorf("%s=%q: want %s or %s", EnvHarness, job.Harness, HarnessClaudeCode, HarnessStandIn)
	}
	if issue := getenv(EnvIssue); issue != "" {
		n, err := strconv.Atoi(issue)
		if err != nil || n < 1 {
			return Job{}, fmt.Errorf("%s=%q is not an issue number", EnvIssue, issue)
		}
		job.Issue = n
	}
	for pair := range strings.SplitSeq(getenv(EnvRelated), ",") {
		if pair = strings.TrimSpace(pair); pair == "" {
			continue
		}
		name, repoURL, ok := strings.Cut(pair, "=")
		if !ok || name == "" || repoURL == "" {
			return Job{}, fmt.Errorf("%s: %q is not name=url", EnvRelated, pair)
		}
		if job.Related == nil {
			job.Related = map[string]string{}
		}
		job.Related[name] = repoURL
	}
	job.CheckCommand = strings.Fields(getenv(EnvCheckCommand))
	if len(job.CheckCommand) == 0 {
		job.CheckCommand = []string{"make", "check"}
	}
	round := getenv(EnvRound)
	if round == "" {
		round = "1"
	}
	n, err := strconv.Atoi(round)
	if err != nil || n < 1 {
		return Job{}, fmt.Errorf("%s=%q is not a round number", EnvRound, round)
	}
	job.Round = n
	if last := getenv(EnvMaxRounds); last != "" {
		if job.MaxRounds, err = strconv.Atoi(last); err != nil || job.MaxRounds < 0 {
			return Job{}, fmt.Errorf("%s=%q is not a round number", EnvMaxRounds, last)
		}
	}

	var missing []string
	for name, value := range map[string]string{
		EnvRole: job.Role, EnvRepoURL: job.RepoURL, EnvBranch: job.Branch, EnvGoal: job.Goal,
		EnvContainer: job.Container, EnvGitUsername: job.Username, EnvGitToken: job.Token,
	} {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return Job{}, fmt.Errorf("missing %s", strings.Join(sortedCopy(missing), ", "))
	}
	if job.Role != RoleImplement && job.Role != RoleReview {
		return Job{}, fmt.Errorf("%s=%q: want %s or %s", EnvRole, job.Role, RoleImplement, RoleReview)
	}
	if job.Branch == job.Base {
		return Job{}, errors.New("the work branch must not be the base branch")
	}
	return job, nil
}

// splitList splits a comma-separated list, trimming blanks and dropping empties.
func splitList(s string) []string {
	var out []string
	for p := range strings.SplitSeq(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
