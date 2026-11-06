package fake

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/JairJosafath/theos/internal/ports"
)

// The fake host's jobs that wait for a runner, as a test sets them, and the
// runners registered for one job, which a remote test environment's machine
// runs (docs/plans/test-environments.md, step 8).

var _ ports.JobRunners = (*Git)(nil)

// SetWaitingJobs sets owner's jobs that wait for a runner; none ends the
// wait, as a runner taking them or a person cancelling them does.
func (g *Git) SetWaitingJobs(owner string, jobs ...ports.WaitingJob) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.runnerState()
	if s.waiting == nil {
		s.waiting = map[string][]ports.WaitingJob{}
	}
	s.waiting[owner] = slices.Clone(jobs)
}

// WaitingJobs returns the owner's waiting jobs a runner with labels could
// take, oldest first.
func (g *Git) WaitingJobs(_ context.Context, labels []string) ([]ports.WaitingJob, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	var out []ports.WaitingJob
	for _, j := range g.s.runnerState().waiting[g.cfg.Owner] {
		if len(j.Labels) > 0 && !slices.ContainsFunc(j.Labels, func(l string) bool { return !slices.Contains(labels, l) }) {
			out = append(out, j)
		}
	}
	slices.SortFunc(out, func(a, b ports.WaitingJob) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

// JobRunnerFile is the fake's registration file of a runner for one job:
// what its machine reads.
type JobRunnerFile struct {
	ID      int64    `json:"id"`
	Name    string   `json:"name"`
	Address string   `json:"address"`
	Labels  []string `json:"labels"`
	Token   string   `json:"token"`
}

// RegisterJobRunner registers an ephemeral runner to the owner, offline
// until SetRunnerOnline, listed with its labels' names, and returns its file
// (runner.json, a JobRunnerFile) with a token of its own.
func (g *Git) RegisterJobRunner(_ context.Context, reg ports.RunnerRegistration) (ports.JobRunner, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.runnerState()
	if err := s.tokenErr[g.cfg.Owner]; err != nil {
		return ports.JobRunner{}, err
	}
	names := make([]string, len(reg.Labels))
	for i, l := range reg.Labels {
		names[i], _, _ = strings.Cut(l, ":")
	}
	s.lastID++
	s.runners[g.cfg.Owner] = append(s.runners[g.cfg.Owner],
		ports.Runner{ID: s.lastID, Name: reg.Name, Labels: names, Ephemeral: true})
	file, err := json.Marshal(JobRunnerFile{ID: s.lastID, Name: reg.Name, Address: reg.Address, Labels: reg.Labels,
		Token: fmt.Sprintf("fake-runner-%d", s.lastID)})
	if err != nil {
		return ports.JobRunner{}, err
	}
	return ports.JobRunner{ID: s.lastID, Name: reg.Name, Labels: names,
		Files: map[string][]byte{"runner.json": file}, Args: []string{"daemon"}}, nil
}
