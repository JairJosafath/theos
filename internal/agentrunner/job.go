// Package agentrunner is the logic of c4agent, the program that runs inside an
// agent's sandbox. It reads one job from its environment, clones the Container's
// repository (or, planning, the Brief's) with a token scoped to that
// repository, runs the agent, and returns one Result. It has no Kubernetes credentials: the operator reads the Result from
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

	"github.com/JairJosafath/c4factory/internal/model"
)

// Roles a sandbox can play in a Task round. Plan is a Plan Task's round: it
// plans a Brief in the Brief's repository.
const (
	RoleImplement = "implement"
	RoleReview    = "review"
	RolePlan      = "plan"
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
	EnvGitToken     = "C4_GIT_TOKEN" //nolint:gosec // an environment variable's name
	EnvCheckCommand = "C4_CHECK_COMMAND"
	EnvContext      = "C4_CONTEXT"
	// EnvProtectedPaths lists the paths the agent must not change, the
	// platform's and the Standard's, comma-separated.
	EnvProtectedPaths = "C4_PROTECTED_PATHS"
	// EnvAllowedPaths lists the protected paths an approved Standard change lets
	// this Task change, comma-separated.
	EnvAllowedPaths = "C4_ALLOWED_PATHS"
	// EnvResumed is "true" when the Task resumes after its Standard change merged.
	EnvResumed = "C4_RESUMED"
	// EnvIssue is the number of the issue the goal comes from, if any.
	EnvIssue = "C4_ISSUE"
	// EnvPullRequest is the number of the Task's open pull request, when an
	// implement run's branch already has one.
	EnvPullRequest = "C4_PULL_REQUEST"
	// EnvHarness and EnvModel say which agent program runs, with which model.
	EnvHarness = "C4_HARNESS"
	EnvModel   = "C4_MODEL"
	// EnvRelated lists the repositories of related Containers, as
	// name=url pairs, comma-separated. They are cloned read-only.
	EnvRelated = "C4_RELATED"
	// EnvBrief is the Brief a plan run plans.
	EnvBrief = "C4_BRIEF"
	// EnvGoRegistry is the owner's Go package registry, where GoProxy sends
	// the factory's module paths.
	EnvGoRegistry = "C4_GO_REGISTRY"
	// EnvNpmScope is the scope the owner's npm packages are named under, and
	// EnvNpmRegistry the registry that serves it; both or neither.
	EnvNpmScope    = "C4_NPM_SCOPE"
	EnvNpmRegistry = "C4_NPM_REGISTRY"
	// EnvNpmToken installs from a registry on another host than the Git
	// host; the Git host's own registry takes the agent's Git token.
	EnvNpmToken = "C4_NPM_TOKEN" //nolint:gosec // an environment variable's name
	// EnvPyPIIndex is the simple index the owner's Python packages are
	// installed from, and EnvPyPIToken installs from one on another host
	// than the Git host.
	EnvPyPIIndex = "C4_PYPI_INDEX"
	EnvPyPIToken = "C4_PYPI_TOKEN" //nolint:gosec // an environment variable's name
	// EnvSessionTokenLimit is the Agent's sessionTokenLimit: the size at which
	// an agent session hands off to a fresh one.
	EnvSessionTokenLimit = "C4AGENT_SESSION_TOKEN_LIMIT"
)

// Harnesses: the agent programs c4agent can run.
const (
	HarnessClaudeCode = "claude-code"
	HarnessStandIn    = "stand-in"
)

// Job is one sandbox run: implement a goal on a branch, review that branch, or
// plan a round of a Brief on plan/<round>.
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
	// ProtectedPaths are the paths the agent must not change, as the operator
	// computed them for the Container's CI system: the platform's and the
	// Standard's.
	ProtectedPaths []string
	// AllowedPaths gate the Task: when set, it may change only these paths.
	AllowedPaths []string
	// Resumed is true when the Task resumes after its Standard change merged.
	Resumed bool
	// Issue is the issue the goal comes from; 0 for a written goal.
	Issue int
	// PullRequest is the open pull request of the branch an implement run
	// works on; 0 when it has none yet. With one, a run that commits nothing
	// is a round for the reviewer: it may have changed only the pull request.
	PullRequest int
	// Harness is the agent program, and Model the model it uses (empty: its default).
	Harness string
	Model   string
	// Related are the repositories of related Containers, by Container name.
	Related map[string]string
	// Brief is the Brief a plan run plans; its repository is RepoURL.
	Brief string
	// GoRegistry is the owner's Go package registry; empty: no GoProxy.
	GoRegistry string
	// NpmScope and NpmRegistry are the owner's npm scope and the registry
	// that serves it; empty: no .npmrc. NpmToken installs from a registry
	// on another host than the Git host.
	NpmScope, NpmRegistry, NpmToken string
	// PyPIIndex is the simple index the owner's Python packages are
	// installed from, and PyPIToken installs from one on another host than
	// the Git host.
	PyPIIndex, PyPIToken string
	// SessionTokenLimit is the size at which an agent session hands off to a
	// fresh one; 0: sessions run to their end.
	SessionTokenLimit int64
}

// JobFromEnv reads a Job from environment variables.
func JobFromEnv(getenv func(string) string) (Job, error) {
	job := Job{
		Role:        getenv(EnvRole),
		RepoURL:     getenv(EnvRepoURL),
		Branch:      getenv(EnvBranch),
		Base:        getenv(EnvBase),
		Goal:        getenv(EnvGoal),
		Feedback:    getenv(EnvFeedback),
		Context:     getenv(EnvContext),
		Project:     getenv(EnvProject),
		Container:   getenv(EnvContainer),
		Task:        getenv(EnvTask),
		Username:    getenv(EnvGitUsername),
		Token:       getenv(EnvGitToken),
		Harness:     getenv(EnvHarness),
		Model:       getenv(EnvModel),
		Brief:       getenv(EnvBrief),
		GoRegistry:  getenv(EnvGoRegistry),
		NpmScope:    getenv(EnvNpmScope),
		NpmRegistry: getenv(EnvNpmRegistry),
		NpmToken:    getenv(EnvNpmToken),
		PyPIIndex:   getenv(EnvPyPIIndex),
		PyPIToken:   getenv(EnvPyPIToken),
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
	if pr := getenv(EnvPullRequest); pr != "" {
		n, err := strconv.Atoi(pr)
		if err != nil || n < 1 {
			return Job{}, fmt.Errorf("%s=%q is not a pull request number", EnvPullRequest, pr)
		}
		job.PullRequest = n
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
		job.CheckCommand = strings.Fields(model.CheckCommand)
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
	if limit := getenv(EnvSessionTokenLimit); limit != "" {
		if job.SessionTokenLimit, err = strconv.ParseInt(limit, 10, 64); err != nil || job.SessionTokenLimit < 0 {
			return Job{}, fmt.Errorf("%s=%q is not a number of tokens", EnvSessionTokenLimit, limit)
		}
	}
	if last := getenv(EnvMaxRounds); last != "" {
		if job.MaxRounds, err = strconv.Atoi(last); err != nil || job.MaxRounds < 0 {
			return Job{}, fmt.Errorf("%s=%q is not a round number", EnvMaxRounds, last)
		}
	}

	if err := job.check(); err != nil {
		return Job{}, err
	}
	return job, nil
}

// check refuses a job that misses what its role needs, and sets a plan's branch.
func (job *Job) check() error {
	if job.Role == RolePlan {
		// A round's plan always goes to its own branch.
		if job.Branch == "" {
			job.Branch = model.PlanBranch(job.Round)
		}
		if job.Branch != model.PlanBranch(job.Round) {
			return fmt.Errorf("%s=%q: a plan of round %d goes to %s", EnvBranch, job.Branch, job.Round,
				model.PlanBranch(job.Round))
		}
	}

	required := map[string]string{
		EnvRole: job.Role, EnvRepoURL: job.RepoURL, EnvBranch: job.Branch, EnvGoal: job.Goal,
		EnvContainer: job.Container, EnvGitUsername: job.Username, EnvGitToken: job.Token,
	}
	if job.Role == RolePlan {
		// The brief repository holds the goal, and a Brief has no Container.
		delete(required, EnvGoal)
		delete(required, EnvContainer)
		required[EnvBrief] = job.Brief
	}
	var missing []string
	for name, value := range required {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing %s", strings.Join(sortedCopy(missing), ", "))
	}
	if job.Role != RoleImplement && job.Role != RoleReview && job.Role != RolePlan {
		return fmt.Errorf("%s=%q: want %s, %s or %s", EnvRole, job.Role, RoleImplement, RoleReview, RolePlan)
	}
	if job.Branch == job.Base {
		return errors.New("the work branch must not be the base branch")
	}
	return nil
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
