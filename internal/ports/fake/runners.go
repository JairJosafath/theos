/*
Copyright 2026 The theos Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package fake

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/JairJosafath/theos/internal/ports"
)

// The fake host's CI runners: a registration token per owner organization,
// and the runners registered with it, as a runner pod's registration makes
// them.

type runnerStore struct {
	// runners is each owner's runners, oldest first, and lastID the newest
	// runner's ID, unique across owners as the host's are.
	runners map[string][]ports.Runner
	lastID  int64
	// tokenErr is what each owner's registration token call fails with.
	tokenErr map[string]error
	// waiting is each owner's jobs that wait for a runner (ci_jobs.go).
	waiting map[string][]ports.WaitingJob
}

func (s *state) runnerState() *runnerStore {
	if s.runners == nil {
		s.runners = &runnerStore{runners: map[string][]ports.Runner{}}
	}
	return s.runners
}

// RunnerTokenPrefix starts every owner's registration token, which is the
// prefix and the owner's name.
const RunnerTokenPrefix = "fake-runner-registration-" //nolint:gosec // a fake token's prefix, no credential

// FailRunnerToken makes owner's registration token call fail with err; nil
// lets it answer again.
func (g *Git) FailRunnerToken(owner string, err error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.runnerState()
	if s.tokenErr == nil {
		s.tokenErr = map[string]error{}
	}
	s.tokenErr[owner] = err
}

// RunnerRegistrationToken is the same for an owner every time, unless
// FailRunnerToken made it fail.
func (g *Git) RunnerRegistrationToken(context.Context) (string, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	if err := g.s.runnerState().tokenErr[g.cfg.Owner]; err != nil {
		return "", err
	}
	return RunnerTokenPrefix + g.cfg.Owner, nil
}

func (g *Git) Runners(context.Context) ([]ports.Runner, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return slices.Clone(g.s.runnerState().runners[g.cfg.Owner]), nil
}

// DeleteRunner removes the owner's runner id, and leaves another owner's.
func (g *Git) DeleteRunner(_ context.Context, id int64) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.runnerState()
	s.runners[g.cfg.Owner] = slices.DeleteFunc(s.runners[g.cfg.Owner], func(r ports.Runner) bool { return r.ID == id })
	return nil
}

// RegisterRunner registers an ephemeral runner name with labels to the owner
// whose registration token it holds, as `gitea-runner register --ephemeral`
// does, and returns it, offline until SetRunnerOnline. A token that is no
// owner's is refused.
func (g *Git) RegisterRunner(token, name string, labels ...string) (ports.Runner, error) {
	owner, ok := strings.CutPrefix(token, RunnerTokenPrefix)
	if !ok || owner == "" {
		return ports.Runner{}, fmt.Errorf("runner registration token %q is no owner's", token)
	}
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.runnerState()
	s.lastID++
	r := ports.Runner{ID: s.lastID, Name: name, Labels: labels, Ephemeral: true}
	s.runners[owner] = append(s.runners[owner], r)
	return r, nil
}

// SetRunnerOnline makes owner's runner name online or offline, and busy or
// idle, as its daemon calling the host, taking a job or stopping does.
func (g *Git) SetRunnerOnline(owner, name string, online, busy bool) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	for i, r := range g.s.runnerState().runners[owner] {
		if r.Name == name {
			g.s.runners.runners[owner][i].Online, g.s.runners.runners[owner][i].Busy = online, busy
		}
	}
}
