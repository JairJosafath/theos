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
	}
	if job.Base == "" {
		job.Base = "main"
	}
	for p := range strings.SplitSeq(getenv(EnvProtectedPaths), ",") {
		if p = strings.TrimSpace(p); p != "" {
			job.ProtectedPaths = append(job.ProtectedPaths, p)
		}
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
