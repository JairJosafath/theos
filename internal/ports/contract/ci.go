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

package contract

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
)

// CIEnv builds the CIs under test and does on their host what a runner does
// there.
type CIEnv struct {
	// New returns the GitHost of a fresh owner that holds no repository,
	// where the suite makes the repositories, and the owner's CI.
	New func(t *testing.T) (ports.GitHost, ports.CI)
	// NoLog returns the check URL of a CI job of repo, whose main holds a
	// commit, that started and sent no log line, as one whose runner went
	// away before its first does. It may push to repo through host.
	NoLog func(t *testing.T, host ports.GitHost, repo ports.Repository) string
	// Queue starts a job of repo, whose main holds a commit, that runs on
	// label alone, which no runner takes. It may push to repo through host.
	// Only a CI with JobRunners needs it.
	Queue func(t *testing.T, host ports.GitHost, repo ports.Repository, label string)
	// Cannot names the subtests the host can't run, with why; they are
	// skipped.
	Cannot map[string]string
}

// CI states what the controllers rely on of every CI that needs no runner to
// see: a repository with no workflow has no runs of a commit or a tag;
// RunnersOnline answers, or says it doesn't know; and the log of a job that
// sent none is ErrNoCILog. A CI with JobRunners lists the jobs that wait for
// a label's runner and registers a runner for one job whose files hold no
// registration token (docs/plans/done/test-environments.md, step 8).
func CI(t *testing.T, env CIEnv) {
	t.Helper()
	ctx, cancel := suiteContext()
	defer cancel()
	runSubtests(t, ciSuite{ctx: ctx, env: env}, env.Cannot, []subtest[ciSuite]{
		{"has no runs of a repository with no workflow", ciSuite.noRuns},
		{"says how many runners are online, or that it doesn't know", ciSuite.runnersOnline},
		{"has no log of a job that sent none", ciSuite.noLog},
		{"lists the jobs that wait for a runner of their labels", ciSuite.waitingJobs},
		{"registers a runner for one job, whose files hold its own credential alone", ciSuite.jobRunner},
	})
}

// ciSuite holds what every subtest of CI shares.
type ciSuite struct {
	ctx context.Context
	env CIEnv
}

// seeded is a fresh owner's GitHost and CI, with the contract repository
// holding the initial commit, and that commit.
func (s ciSuite) seeded(t *testing.T) (ports.GitHost, ports.CI, ports.Repository, string) {
	t.Helper()
	h, ci := s.env.New(t)
	repo := must(h.EnsureRepository(s.ctx, contractRepo, "the CI contract suite"))(t)
	head := must(h.PushInitialCommit(s.ctx, repo, seedFiles, "Scaffold\n", contractAuthor))(t)
	return h, ci, repo, head
}

func (s ciSuite) noRuns(t *testing.T) {
	h, ci, repo, head := s.seeded(t)
	if err := h.CreateTag(s.ctx, repo, firstTag, head, "Release v0.1.0"); err != nil {
		t.Fatal(err)
	}
	if runs := must(ci.CommitRuns(s.ctx, repo, head))(t); len(runs) != 0 {
		t.Errorf("CommitRuns() of a repository with no workflow = %+v, want none", runs)
	}
	if runs := must(ci.TagRuns(s.ctx, repo, firstTag, head))(t); len(runs) != 0 {
		t.Errorf("TagRuns() of a repository with no workflow = %+v, want none", runs)
	}
}

func (s ciSuite) runnersOnline(t *testing.T) {
	_, ci := s.env.New(t)
	online, known := ci.RunnersOnline(s.ctx)
	if online < 0 || !known && online != 0 {
		t.Errorf("RunnersOnline() = %d, %t; want a count, or 0 when it doesn't know", online, known)
	}
}

func (s ciSuite) noLog(t *testing.T) {
	h, ci, repo, _ := s.seeded(t)
	checkURL := s.env.NoLog(t, h, repo)
	if log, err := ci.CheckLog(s.ctx, repo, checkURL); !errors.Is(err, ports.ErrNoCILog) {
		t.Errorf("CheckLog() of a job that sent no log = %q, %v; want ErrNoCILog", log, err)
	}
}

// jobRunners is ci's JobRunners, skipping t when it has none.
func jobRunners(t *testing.T, ci ports.CI) ports.JobRunners {
	t.Helper()
	runners, ok := ci.(ports.JobRunners)
	if !ok {
		t.Skip("the CI has no JobRunners")
	}
	return runners
}

func (s ciSuite) waitingJobs(t *testing.T) {
	h, ci, repo, _ := s.seeded(t)
	runners := jobRunners(t, ci)
	const label, other = "env-contract", "env-other"
	s.env.Queue(t, h, repo, label)
	// The host may schedule the job after Queue returns.
	deadline := time.Now().Add(time.Minute)
	var jobs []ports.WaitingJob
	for {
		jobs = must(runners.WaitingJobs(s.ctx, []string{label, "base"}))(t)
		if len(jobs) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if len(jobs) != 1 || jobs[0].ID == 0 || !slices.Equal(jobs[0].Labels, []string{label}) {
		t.Fatalf("WaitingJobs(%s) = %+v, want the one job queued on it", label, jobs)
	}
	if jobs := must(runners.WaitingJobs(s.ctx, []string{other}))(t); len(jobs) != 0 {
		t.Errorf("WaitingJobs(%s) = %+v, want none: the job runs on %s", other, jobs, label)
	}
}

func (s ciSuite) jobRunner(t *testing.T) {
	_, ci := s.env.New(t)
	runners := jobRunners(t, ci)
	owner, ok := ci.(ports.OwnerRunners)
	if !ok {
		t.Skip("the CI has no OwnerRunners")
	}
	const address = "https://ci.example.com"
	runner := must(runners.RegisterJobRunner(s.ctx, ports.RunnerRegistration{Name: "contract-job-runner",
		Labels: []string{"env-contract:host"}, Address: address, Timeout: time.Hour}))(t)
	t.Cleanup(func() { _ = owner.DeleteRunner(context.Background(), runner.ID) })
	if runner.ID == 0 || runner.Name != "contract-job-runner" || len(runner.Files) == 0 || len(runner.Args) == 0 {
		t.Fatalf("RegisterJobRunner() = %+v, want the runner, its files and its arguments", runner)
	}
	registration := must(owner.RunnerRegistrationToken(s.ctx))(t)
	var withAddress bool
	for name, data := range runner.Files {
		if strings.Contains(string(data), registration) {
			t.Errorf("file %s holds the registration token, which would register runners at will", name)
		}
		withAddress = withAddress || strings.Contains(string(data), address)
	}
	if !withAddress {
		t.Errorf("no file names the address the runner reaches the host at, %s", address)
	}
	listed := must(owner.Runners(s.ctx))(t)
	i := slices.IndexFunc(listed, func(r ports.Runner) bool { return r.ID == runner.ID })
	if i < 0 {
		t.Fatalf("Runners() = %+v, want runner %d", listed, runner.ID)
	}
	if got := listed[i]; got.Name != runner.Name || !got.Ephemeral || got.Online || got.Busy ||
		!slices.Equal(got.Labels, []string{"env-contract"}) {
		t.Errorf("Runners() lists %+v, want it ephemeral, offline until its machine calls, on env-contract", got)
	}
	if err := owner.DeleteRunner(s.ctx, runner.ID); err != nil {
		t.Fatal(err)
	}
	if listed := must(owner.Runners(s.ctx))(t); slices.ContainsFunc(listed, func(r ports.Runner) bool {
		return r.ID == runner.ID
	}) {
		t.Errorf("runner %d is still listed after DeleteRunner", runner.ID)
	}
}
