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

package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
	"github.com/JairJosafath/theos/internal/ports/fake"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// hostOn is the operator's Task host of owner on g: the operator's account,
// and the Container's agent bot's for the pull request.
func hostOn(g *fake.Git, owner string) *taskHost {
	operator := g.For(ports.Config{URL: "http://host.test", Owner: owner,
		Credentials: ports.Credentials{Username: "operator", Token: "operator-token"}})
	agent := g.For(ports.Config{URL: "http://host.test", Owner: owner,
		Credentials: ports.Credentials{Username: owner + "-agent", Token: "agent-token"}})
	return &taskHost{TaskHost: operator, agent: func(context.Context) (ports.GitHost, ports.Repository, error) {
		return agent, agent.Repository("orders-api"), nil
	}}
}

// #336: the operator's Task host keeps the host's contracts (GitHost, Issues
// and CI) on the fake host, its pull requests opened as the agent bot.
func TestTaskHostKeepsTheHostContracts336(t *testing.T) {
	g, owners := fake.New(), 0
	owner := func() *taskHost {
		owners++
		return hostOn(g, fmt.Sprintf("contract-%d", owners))
	}
	statuses := map[string]map[string]string{}
	contract.GitHost(t, contract.GitHostEnv{
		New:   func(*testing.T) ports.GitHost { return owner() },
		Login: "operator",
		SetStatus: func(_ *testing.T, repo ports.Repository, commit, context, state string) {
			key := repo.Owner + "/" + repo.Name + "@" + commit
			if statuses[key] == nil {
				statuses[key] = map[string]string{}
			}
			statuses[key][context] = state
			g.SetCheckContexts(repo.Owner, repo.Name, commit, worstState(statuses[key]), statuses[key])
		},
		Release: func(_ *testing.T, repo ports.Repository, tag, commit, body, asset, content string) {
			g.SetTag(repo.Owner, repo.Name, tag, commit)
			g.SetRelease(repo.Owner, repo.Name, tag, body, fake.ReleaseFile{Name: asset, Data: []byte(content)})
		},
	})
	contract.Issues(t, contract.IssuesEnv{
		New: func(*testing.T) (ports.GitHost, ports.Issues) {
			h := owner()
			return h, h
		},
		Assignee: "operator",
	})
	contract.CI(t, contract.CIEnv{
		New: func(*testing.T) (ports.GitHost, ports.CI) {
			h := owner()
			return h, h
		},
		NoLog: func(_ *testing.T, _ ports.GitHost, repo ports.Repository) string {
			checkURL := repo.WebURL + "/actions/runs/1/jobs/1"
			g.SetNoCheckLog(checkURL)
			return checkURL
		},
		Queue: func(_ *testing.T, _ ports.GitHost, repo ports.Repository, label string) {
			g.SetWaitingJobs(repo.Owner, ports.WaitingJob{ID: 1, RunID: 1, Name: "check", Labels: []string{label},
				URL: repo.WebURL + "/actions/runs/1/jobs/1"})
		},
	})
}

// worstState is the state a host combines contexts into: the worst of them.
func worstState(contexts map[string]string) string {
	for _, state := range []string{ports.CheckError, ports.CheckFailure, ports.CheckPending} {
		for _, s := range contexts {
			if s == state {
				return state
			}
		}
	}
	return ports.CheckSuccess
}

// #336: the round's pull request is opened as the Container's agent bot, as
// apply's openPR opened it, so the operator's own account can review it;
// the host's other calls are the operator's account's.
func TestTaskHostOpensThePullRequestAsTheAgent336(t *testing.T) {
	ctx := context.Background()
	g := fake.New()
	h := hostOn(g, "apps")
	repo, err := h.EnsureRepository(ctx, "orders-api", "the Task host's test")
	if err != nil {
		t.Fatal(err)
	}
	author := ports.Signature{Name: "theos", Email: "theos@example.com", When: time.Now()}
	base, err := h.PushInitialCommit(ctx, repo, scaffold.Tree{"README.md": {Data: []byte("hi\n")}}, "Scaffold",
		author)
	if err != nil {
		t.Fatal(err)
	}
	branch := model.TaskBranch("orders-api-3")
	if _, err := h.PushBranch(ctx, repo, base, branch, scaffold.Tree{"orders.go": {Data: []byte("// orders\n")}},
		nil, "Add orders", author); err != nil {
		t.Fatal(err)
	}
	pr, err := h.OpenPullRequest(ctx, repo, branch, ports.DefaultBranch, "Add orders", "Closes #3.")
	if err != nil {
		t.Fatal(err)
	}
	if got := g.PullAuthor("apps", "orders-api", pr.Number); got != "apps-agent" {
		t.Errorf("pull request author = %q, want the agent bot", got)
	}
	if err := h.ReviewPullRequest(ctx, repo, pr.Number, pr.HeadSHA, ports.ReviewApprove, "Looks right."); err != nil {
		t.Errorf("the operator's review of the agent's pull request = %v, want it taken", err)
	}
	if again, err := h.OpenPullRequest(ctx, repo, branch, ports.DefaultBranch, "Add orders", ""); err != nil ||
		again.Number != pr.Number {
		t.Errorf("opened again = %+v, %v; want the open one", again, err)
	}
}

// #336: the reconciler leaves the engine a pull request opened, checks run
// again and a failure, where the engine acts on the run the reconciler
// would (endsAsTheEngine), noting whether the Task had a pull request; a
// round no agent judged drops an earlier verdict first, as apply does.
func TestTaskTurnLeavesTheEngineTheHostSteps336(t *testing.T) {
	for action, engines := range map[model.TaskAction]bool{
		model.ActionOpenPR: true, model.ActionRerunChecks: true, model.ActionFail: true,
		model.ActionMerge: false, model.ActionUpdateBranch: false, model.ActionWait: false,
		model.ActionPropose: false, model.ActionAwaitApproval: false, model.ActionSpecDeclined: false,
		model.ActionStartImplement: false, model.ActionRetry: false,
	} {
		if got := hostStep(action); got != engines {
			t.Errorf("hostStep(%s) = %v, want %v", action, got, engines)
		}
	}
	ctx := context.Background()
	task := workersTask("apps")
	status := &c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskChecking, Round: 1, PullRequest: 7,
		Review: &c4v1beta1.TaskReview{Verdict: model.VerdictApprove}}
	turn := &taskTurn{r: &TaskReconciler{}, task: task, status: status}
	carried, err := turn.carry(ctx, model.TaskFacts{}, model.TaskStep{Action: model.ActionFail, Unreviewed: true})
	if err != nil || carried {
		t.Fatalf("carry() = %v, %v; want the failure left to the engine", carried, err)
	}
	if !turn.byHost || !turn.reopened || status.Review != nil {
		t.Errorf("byHost %v, reopened %v, review %+v; want the host's step, the pull request noted, the "+
			"verdict dropped", turn.byHost, turn.reopened, status.Review)
	}
}

// #336: what a host step the engine carried out says is the reconciler's, as
// apply's openPR, rerunChecks and fail say it: the Progressing reason after
// the tick, when to look again, the opened pull request's notes after the
// status write, and a failure's words before the run ends, its issue handed
// back once the Task failed.
func TestTaskTurnSaysWhatAHostStepSays336(t *testing.T) {
	ctx := context.Background()
	task := workersTask("apps")
	now := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	r := &TaskReconciler{Now: func() time.Time { return now }}
	reason := func(status *c4v1beta1.TaskStatus) (string, string) {
		cond := apimeta.FindStatusCondition(status.Conditions, ConditionProgressing)
		if cond == nil {
			return "", ""
		}
		return cond.Reason, cond.Message
	}
	opened := model.TaskStep{Action: model.ActionOpenPR, Message: "The agent pushed abc1234"}
	rerun := model.TaskStep{Action: model.ActionRerunChecks, Message: "CI run 7 was killed; running it again"}
	for _, tc := range []struct {
		name          string
		phase         string
		decided, done model.TaskStep
		reason, says  string
		requeue       bool
	}{
		{"a pull request opened", model.TaskImplementing, opened, opened, "Checking",
			opened.Message + "; waiting for checks", true},
		{"a commit pushed again, its pull request opened", model.TaskImplementing,
			model.TaskStep{Action: model.ActionRerunChecks, Message: "pushed again"},
			model.TaskStep{Action: model.ActionRerunChecks, Message: "pushed again"}, "Checking",
			"pushed again; waiting for checks", true},
		{"checks run again", model.TaskChecking, rerun, rerun, "RerunningChecks", rerun.Message, false},
		{"a wait for the runs to end", model.TaskChecking, rerun, model.TaskStep{Action: model.ActionWait,
			Recheck: true, Message: "wait for the runs"}, "Waiting", "wait for the runs", true},
		{"a failure read as it stands", model.TaskChecking, rerun, model.TaskStep{Action: model.ActionWait,
			Message: "reading its checks"}, "Checking", "reading its checks", false},
	} {
		status := &c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskPhase(tc.phase), Round: 1}
		turn := &taskTurn{r: r, task: task, status: status, step: tc.decided, byHost: true,
			facts: model.TaskFacts{Phase: tc.phase}}
		turn.carried(ctx, tc.done)
		if got, says := reason(status); got != tc.reason || says != tc.says {
			t.Errorf("%s: Progressing %s, %q; want %s, %q", tc.name, got, says, tc.reason, tc.says)
		}
		if requeued := turn.result.RequeueAfter > 0; requeued != tc.requeue {
			t.Errorf("%s: requeue after %v, want one %v", tc.name, turn.result.RequeueAfter, tc.requeue)
		}
		if (turn.notes != nil) != (tc.phase == model.TaskImplementing) {
			t.Errorf("%s: notes after the status write %v, want them for a pull request opened alone", tc.name,
				turn.notes != nil)
		}
	}

	// A step the reconciler carried out itself says nothing more here.
	status := &c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskChecking, Round: 1}
	turn := &taskTurn{r: r, task: task, status: status, step: rerun}
	turn.carried(ctx, rerun)
	if len(status.Conditions) != 0 {
		t.Errorf("conditions = %+v, want none for a step apply carried out", status.Conditions)
	}

	// A failure decided keeps the review's verdict and why in the status, as
	// apply's; one the engine turned a rerun into leaves the feedback. (A
	// Task with no issue has none to hand back.)
	task.Spec.Issue = 0
	for _, decided := range []model.TaskAction{model.ActionFail, model.ActionRerunChecks} {
		status := &c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskChecking, Round: 1, Feedback: "earlier"}
		turn := &taskTurn{r: r, task: task, status: status, step: model.TaskStep{Action: decided}, byHost: true}
		failure := model.TaskStep{Action: model.ActionFail, Reason: model.ReasonNoCIResult, Feedback: "why",
			Message: "CI never reported"}
		if err := turn.ending(ctx, model.TaskFacts{}, failure); err != nil {
			t.Fatal(err)
		}
		if want := map[model.TaskAction]string{model.ActionFail: "why",
			model.ActionRerunChecks: "earlier"}[decided]; status.Feedback != want {
			t.Errorf("decided %s: feedback %q, want %q", decided, status.Feedback, want)
		}
		if turn.failed == nil || turn.failed.Message != failure.Message {
			t.Fatalf("decided %s: failed = %+v, want the failure kept for after the tick", decided, turn.failed)
		}
		r.end(task, status, model.TaskEnd{Phase: model.TaskFailed, Reason: model.ReasonNoCIResult,
			Message: failure.Message})
		turn.carried(ctx, failure)
		if got, says := reason(status); got != model.ReasonNoCIResult || says != failure.Message {
			t.Errorf("decided %s: Progressing %s, %q; want the failure's", decided, got, says)
		}
	}
}

// #336: an agent's outage the engine carried out says, after the tick, that
// the Task waits for the agent until the record's agentBackAt, and looks
// again then, as apply's does.
func TestTaskTurnSaysWhenTheAgentIsBack336(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	back := now.Add(30 * time.Minute)
	r := &TaskReconciler{Now: func() time.Time { return now }}
	status := &c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskImplementing, Round: 1}
	setRecord(status, ports.TaskRecord{Progress: progressOf(status), AgentBack: back})
	if status.AgentBackAt == nil || !status.AgentBackAt.Time.Equal(back) || !recordOf(status).AgentBack.Equal(back) {
		t.Fatalf("agentBackAt = %v, want the record's %v, read back", status.AgentBackAt, back)
	}
	step := model.TaskStep{Action: model.ActionAgentUnavailable, Message: "The agent is unavailable"}
	turn := &taskTurn{r: r, task: workersTask("apps"), status: status, step: step, endsTurn: true}
	turn.ended(ctx, step)
	cond := apimeta.FindStatusCondition(status.Conditions, ConditionProgressing)
	if cond == nil || cond.Reason != reasonAgentUnavailable || cond.Status != metav1.ConditionTrue ||
		cond.Message != "The agent is unavailable; trying again at "+back.Format(time.RFC3339) {
		t.Errorf("Progressing = %+v, want AgentUnavailable until %v", cond, back)
	}
	if turn.result.RequeueAfter != 30*time.Minute {
		t.Errorf("requeue after %v, want when the agent is back", turn.result.RequeueAfter)
	}
	setRecord(status, ports.TaskRecord{Progress: progressOf(status)})
	if status.AgentBackAt != nil {
		t.Errorf("agentBackAt = %v, want it forgotten with the record's", status.AgentBackAt)
	}
}
