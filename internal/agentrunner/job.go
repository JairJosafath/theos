// Package agentrunner is the logic of theos-agent, the program that runs inside an
// agent's sandbox. It reads one job from its environment, clones the Container's
// repository (or, planning, the Brief's) with a token scoped to that
// repository, runs the agent, and returns one Result. It has no Kubernetes credentials: the operator reads the Result from
// the sandbox pod's termination message.
//
// It must not import Kubernetes, controller-runtime, the api package or the Git
// host adapter; see docs/architecture.md.
package agentrunner

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/JairJosafath/theos/internal/model"
)

// Roles a sandbox can play in a Task round. Verify builds, starts and checks a
// change the reviewer approved, and pushes nothing. Plan is a Plan Task's
// round: it plans a Brief in the Brief's repository. Triage is a Triage
// Task's run: it reads the issue and the repository, and pushes nothing.
// Score is a Score Task's run: it judges a finished change against a
// Scorer's criteria, and pushes nothing.
const (
	RoleImplement = "implement"
	RoleReview    = "review"
	RoleVerify    = "verify"
	RolePlan      = "plan"
	RoleTriage    = "triage"
	RoleScore     = model.RoleScorer
)

// triageBranch is the local branch a triage run reads the base on; it is
// never pushed.
const triageBranch = "triage"

// scoreBranch is the local branch a judge run reads the base on; it is never
// pushed.
const scoreBranch = "score"

// Environment variables the operator sets on the sandbox.
const (
	EnvRole         = "THEOS_ROLE"
	EnvRepoURL      = "THEOS_REPO_URL"
	EnvBranch       = "THEOS_BRANCH"
	EnvBase         = "THEOS_BASE"
	EnvGoal         = "THEOS_GOAL"
	EnvFeedback     = "THEOS_FEEDBACK"
	EnvProject      = "THEOS_PROJECT"
	EnvContainer    = "THEOS_CONTAINER"
	EnvTask         = "THEOS_TASK"
	EnvRound        = "THEOS_ROUND"
	EnvMaxRounds    = "THEOS_MAX_ROUNDS"
	EnvGitUsername  = "THEOS_GIT_USERNAME"
	EnvGitToken     = "THEOS_GIT_TOKEN" //nolint:gosec // an environment variable's name
	EnvCheckCommand = "THEOS_CHECK_COMMAND"
	EnvContext      = "THEOS_CONTEXT"
	// EnvProtectedPaths lists the paths the agent must not change, the
	// platform's and the Standard's, comma-separated.
	EnvProtectedPaths = "THEOS_PROTECTED_PATHS"
	// EnvAllowedPaths lists the protected paths an approved Standard change lets
	// this Task change, comma-separated.
	EnvAllowedPaths = "THEOS_ALLOWED_PATHS"
	// EnvResumed is "true" when the Task resumes after its Standard change merged.
	EnvResumed = "THEOS_RESUMED"
	// EnvIssue is the number of the issue the goal comes from, if any.
	EnvIssue = "THEOS_ISSUE"
	// EnvSpecPath is the spec file of a Spec Task's issue, docs/specs/<issue>.md:
	// its implement run writes the spec there and changes nothing else, and
	// its review run reviews that spec.
	EnvSpecPath = "THEOS_SPEC_PATH"
	// EnvPullRequest is the number of the Task's open pull request, when an
	// implement run's branch already has one.
	EnvPullRequest = "THEOS_PULL_REQUEST"
	// EnvHarness and EnvModel say which agent program runs, with which model.
	EnvHarness = "THEOS_HARNESS"
	EnvModel   = "THEOS_MODEL"
	// EnvRelated lists the repositories of related Containers, as
	// name=url pairs, comma-separated. They are cloned read-only.
	EnvRelated = "THEOS_RELATED"
	// EnvBrief is the Brief a plan run plans.
	EnvBrief = "THEOS_BRIEF"
	// EnvGoRegistry is the owner's Go package registry, where GoProxy sends
	// the factory's module paths.
	EnvGoRegistry = "THEOS_GO_REGISTRY"
	// EnvNpmScope is the scope the owner's npm packages are named under, and
	// EnvNpmRegistry the registry that serves it; both or neither.
	EnvNpmScope    = "THEOS_NPM_SCOPE"
	EnvNpmRegistry = "THEOS_NPM_REGISTRY"
	// EnvNpmToken installs from a registry on another host than the Git
	// host; the Git host's own registry takes the agent's Git token.
	EnvNpmToken = "THEOS_NPM_TOKEN" //nolint:gosec // an environment variable's name
	// EnvPyPIIndex is the simple index the owner's Python packages are
	// installed from, and EnvPyPIToken installs from one on another host
	// than the Git host.
	EnvPyPIIndex = "THEOS_PYPI_INDEX"
	EnvPyPIToken = "THEOS_PYPI_TOKEN" //nolint:gosec // an environment variable's name
	// EnvSessionTokenLimit is the Agent's sessionTokenLimit: the size at which
	// an agent session hands off to a fresh one.
	EnvSessionTokenLimit = "THEOS_AGENT_SESSION_TOKEN_LIMIT" //nolint:gosec // an environment variable's name
	// EnvCheckpointDir is where the sandbox mounts its checkpoint volume:
	// theos-agent saves an interrupted run there and goes on from it in the
	// sandbox's next pod (checkpoint.go). Unset: no checkpoints.
	EnvCheckpointDir = "THEOS_CHECKPOINT_DIR"
	// EnvCriteria is a judge run's criteria, a JSON array of strings: what
	// the Scorer asks, kept apart from the evidence in the goal, which the
	// judge reads as data.
	EnvCriteria = "THEOS_CRITERIA"
	// EnvEnvironments are the test environments the run leased, a JSON
	// array of {"name", "description"}: the prompt lists them, and
	// theos-env run reaches each at THEOS_ENV_<NAME>_URL
	// (docs/plans/test-environments.md, T4 and T13).
	EnvEnvironments = "THEOS_ENVIRONMENTS"
)

// TestEnvironment is a test environment a run leased, as the prompt names it.
type TestEnvironment struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// TestRuns means the environment runs test packages on devices, with
	// `theos-env test`, rather than commands (T12).
	TestRuns bool `json:"testRuns,omitempty"`
}

// CheckpointDir is where the operator mounts a sandbox's checkpoint volume.
const CheckpointDir = "/checkpoint"

// Harnesses: the agent programs theos-agent can run.
const (
	HarnessClaudeCode = "claude-code"
	HarnessStandIn    = "stand-in"
)

// Job is one sandbox run: implement a goal on a branch, review or verify that branch, or
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
	// SpecPath is a Spec Task's spec file: the implement run writes the spec
	// there and nothing else, and the review run reviews it. Empty for any
	// other Task.
	SpecPath string
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
	// CheckpointDir is the sandbox's checkpoint volume; empty: none.
	CheckpointDir string
	// PodInfoDir is where the pod sees its own annotations, which may ask the
	// run to pause (pause.go); empty: the run never pauses.
	PodInfoDir string
	// Criteria are a judge run's criteria; empty for any other role.
	Criteria []string
	// Environments are the test environments the run leased, by name.
	Environments []TestEnvironment
}

// parseRelated reads the related repositories, name=url pairs,
// comma-separated; nil when there are none.
func parseRelated(list string) (map[string]string, error) {
	var out map[string]string
	for pair := range strings.SplitSeq(list, ",") {
		if pair = strings.TrimSpace(pair); pair == "" {
			continue
		}
		name, repoURL, ok := strings.Cut(pair, "=")
		if !ok || name == "" || repoURL == "" {
			return nil, fmt.Errorf("%s: %q is not name=url", EnvRelated, pair)
		}
		if out == nil {
			out = map[string]string{}
		}
		out[name] = repoURL
	}
	return out, nil
}

// parseCriteria reads a judge run's criteria, a JSON array of strings; none
// when unset.
func parseCriteria(criteria string) ([]string, error) {
	if criteria == "" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(criteria), &out); err != nil {
		return nil, fmt.Errorf("%s is not a JSON array of strings: %w", EnvCriteria, err)
	}
	return out, nil
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
		SpecPath:    getenv(EnvSpecPath),

		CheckpointDir: getenv(EnvCheckpointDir),
		PodInfoDir:    PodInfoDir,
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
	related, err := parseRelated(getenv(EnvRelated))
	if err != nil {
		return Job{}, err
	}
	job.Related = related
	criteria, err := parseCriteria(getenv(EnvCriteria))
	if err != nil {
		return Job{}, err
	}
	job.Criteria = criteria
	if envs := getenv(EnvEnvironments); envs != "" {
		if err := json.Unmarshal([]byte(envs), &job.Environments); err != nil {
			return Job{}, fmt.Errorf("%s is not a JSON array of test environments: %w", EnvEnvironments, err)
		}
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
	if job.Role == RoleTriage {
		// A triage reads the base, and pushes no branch.
		job.Branch = triageBranch
	}
	if job.Role == RoleScore {
		// A judge reads the base, and pushes no branch.
		job.Branch = scoreBranch
		if len(job.Criteria) == 0 || len(job.Criteria) > model.MaxScoreCriteria {
			return fmt.Errorf("%s: a judge run needs 1 to %d criteria", EnvCriteria, model.MaxScoreCriteria)
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
	if !slices.Contains([]string{RoleImplement, RoleReview, RoleVerify, RolePlan, RoleTriage, RoleScore}, job.Role) {
		return fmt.Errorf("%s=%q: want %s, %s, %s, %s, %s or %s", EnvRole, job.Role, RoleImplement, RoleReview,
			RoleVerify, RolePlan, RoleTriage, RoleScore)
	}
	if job.Branch == job.Base {
		return errors.New("the work branch must not be the base branch")
	}
	if job.SpecPath != "" && (job.Issue == 0 || job.SpecPath != model.SpecPath(job.Issue)) {
		// A Spec Task writes its own issue's spec, there and nowhere else.
		return fmt.Errorf("%s=%q is not the spec of %s=%d", EnvSpecPath, job.SpecPath, EnvIssue, job.Issue)
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
