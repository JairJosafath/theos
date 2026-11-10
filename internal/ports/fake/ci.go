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

// The fake host's CI: the runs of a tag or commit, their jobs, the runners
// online, re-runs and the log every job serves.

func (g *Git) TagRuns(_ context.Context, repo ports.Repository, tag, commit string) (
	[]ports.WorkflowRun, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return slices.Clone(g.s.releaseState().runs[repo.Owner+"/"+repo.Name+"@"+tag+"@"+commit]), nil
}

func (g *Git) CommitRuns(_ context.Context, repo ports.Repository, commit string) (
	[]ports.WorkflowRun, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return slices.Clone(g.s.releaseState().commitRuns[repo.Owner+"/"+repo.Name+"@"+commit]), nil
}

// RunnersOnline doesn't know until a test sets it.
func (g *Git) RunnersOnline(context.Context) (int, bool) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	if n := g.s.releaseState().runners; n != nil {
		return *n, true
	}
	return 0, false
}

// RunJobs returns the jobs a test set for run id, none until it does.
func (g *Git) RunJobs(_ context.Context, repo ports.Repository, id int64) (
	[]ports.WorkflowJob, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return slices.Clone(g.s.releaseState().runJobs[fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, id)]), nil
}

// RunnerOnline says a runner a test set online is, and any other isn't.
func (g *Git) RunnerOnline(_ context.Context, id int64) (online, known bool) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.releaseState().onlineRunners[id], true
}

// SetCommitRuns sets the CI runs at commit.
func (g *Git) SetCommitRuns(owner, name, commit string, runs ...ports.WorkflowRun) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.releaseState().commitRuns[owner+"/"+name+"@"+commit] = runs
}

// RerunFailedJobs queues run id again, as Gitea's new attempt of it does.
func (g *Git) RerunFailedJobs(_ context.Context, repo ports.Repository, id int64) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.releaseState()
	key := fmt.Sprintf("%s/%s#%d", repo.Owner, repo.Name, id)
	s.reruns[key]++
	if s.refused[key] {
		return fmt.Errorf("re-run of run %d refused", id)
	}
	for _, byRef := range []map[string][]ports.WorkflowRun{s.runs, s.commitRuns} {
		for key, runs := range byRef {
			if !strings.HasPrefix(key, repo.Owner+"/"+repo.Name+"@") {
				continue
			}
			for i := range runs {
				if runs[i].ID == id {
					runs[i].Status, runs[i].Conclusion = "queued", ""
					return nil
				}
			}
		}
	}
	return fmt.Errorf("run %d of %s/%s not found", id, repo.Owner, repo.Name)
}

// RefuseRerun makes the host refuse, or no longer refuse, to re-run run id of
// owner/name.
func (g *Git) RefuseRerun(owner, name string, id int64, refuse bool) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.releaseState().refused[fmt.Sprintf("%s/%s#%d", owner, name, id)] = refuse
}

// Reruns is how many times a re-run of run id of owner/name was asked for.
func (g *Git) Reruns(owner, name string, id int64) int {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.releaseState().reruns[fmt.Sprintf("%s/%s#%d", owner, name, id)]
}

// SetRuns sets the workflow runs a push of tag at commit started, newest first.
func (g *Git) SetRuns(owner, name, tag, commit string, runs ...ports.WorkflowRun) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.releaseState().runs[owner+"/"+name+"@"+tag+"@"+commit] = runs
}

// SetRunnersOnline makes the host say n runners are online.
func (g *Git) SetRunnersOnline(n int) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.releaseState().runners = &n
}

// ForgetRunnersOnline makes the host no longer say how many runners are
// online, as before SetRunnersOnline.
func (g *Git) ForgetRunnersOnline() {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.releaseState().runners = nil
}

// SetRunJobs sets the jobs of run id of owner/name.
func (g *Git) SetRunJobs(owner, name string, id int64, jobs ...ports.WorkflowJob) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.releaseState().runJobs[fmt.Sprintf("%s/%s#%d", owner, name, id)] = jobs
}

// MarkRunnersOnline makes RunnerOnline say the runners ids are online.
func (g *Git) MarkRunnersOnline(ids ...int64) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	for _, id := range ids {
		g.s.releaseState().onlineRunners[id] = true
	}
}

// MarkRunnersOffline makes RunnerOnline say the runners ids are offline.
func (g *Git) MarkRunnersOffline(ids ...int64) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	for _, id := range ids {
		delete(g.s.releaseState().onlineRunners, id)
	}
}

// SetCheckLog sets the log the fake host serves for every CI job.
func (g *Git) SetCheckLog(log string) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.agentState().log = log
}

// SetNoCheckLog makes the CI job checkURL links to one that started and sent
// no log, as one whose runner went away before its first line: CheckLog of it
// is ErrNoCILog.
func (g *Git) SetNoCheckLog(checkURL string) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.agentState().noLogs[checkURL] = true
}

func (g *Git) CheckLog(_ context.Context, _ ports.Repository, checkURL string) (string, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	if g.s.agentState().noLogs[checkURL] {
		return "", fmt.Errorf("job of %s: %w", checkURL, ports.ErrNoCILog)
	}
	return g.s.agentState().log, nil
}
