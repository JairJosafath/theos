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
	"slices"
	"strings"
	"testing"
	"time"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/fake"
)

// A slow call to the Git host must not leave a merged issue open: each call gets
// its own time, so the comment and the close still happen after a follow-up that
// the host filed but answered too late for (vscode-extension #19).
func TestCloseIssueAfterALateAnswer(t *testing.T) {
	git := fake.New()
	provider := git
	repo := ports.Repository{Owner: "tools", Name: "vscode-extension"}
	n := git.AddIssue(repo.Owner, repo.Name, "Check the README's claims", "", []string{model.LabelInProgress})
	git.AnswerLate()

	r := &TaskReconciler{GitTimeout: 50 * time.Millisecond}
	task := &c4v1beta1.Task{ObjectMeta: objectMeta("tools", "vscode-extension-19"),
		Spec: c4v1beta1.TaskSpec{Issue: int32(n)}}
	status := &c4v1beta1.TaskStatus{Round: 3, PullRequestURL: "http://gitea/tools/vscode-extension/pulls/35"}
	reviewer := &c4v1beta1.Agent{ObjectMeta: objectMeta("tools", "claude-reviewer")}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	r.closeIssue(ctx, provider, repo, task, status, reviewer, agentName(reviewer),
		[]model.FollowUp{{Title: "Warn when the server is too old", Body: "Compare serverInfo.version."}}, nil)

	got := git.Issue(repo.Owner, repo.Name, n)
	if got.State != "closed" {
		t.Errorf("issue #%d is %s, want closed", n, got.State)
	}
	if len(got.Comments) != 1 || !strings.HasPrefix(got.Comments[0], "Merged in ") {
		t.Errorf("comments %q, want the merge reported", got.Comments)
	}
	if slices.Contains(got.Labels, model.LabelInProgress) {
		t.Errorf("labels %v, want %s removed", got.Labels, model.LabelInProgress)
	}
}

// A rerun of the merge step files the open criteria once: by status.byHand, or,
// when the status lost it, by the marker in the issue's body (#474).
func TestCloseIssueFilesByHandOnce(t *testing.T) {
	git := fake.New()
	provider := git
	repo := ports.Repository{Owner: "tools", Name: "cel-lens"}
	n := git.AddIssue(repo.Owner, repo.Name, "README shows the hover", "", []string{model.LabelInProgress})
	r := &TaskReconciler{GitTimeout: time.Second}
	task := &c4v1beta1.Task{ObjectMeta: objectMeta("tools", "cel-lens-1"), Spec: c4v1beta1.TaskSpec{Issue: int32(n)}}
	reviewer := &c4v1beta1.Agent{ObjectMeta: objectMeta("tools", "reviewer")}
	open := []model.OpenCriterion{{Criterion: "The README has a screenshot of the hover", Why: "No desktop editor."}}
	byHand := func() []ports.Issue {
		issues, err := provider.ListIssues(context.Background(), repo, model.LabelByHand)
		if err != nil {
			t.Fatal(err)
		}
		return issues
	}

	status := &c4v1beta1.TaskStatus{Round: 1, IssueTitle: "README shows the hover",
		PullRequestURL: "http://gitea/tools/cel-lens/pulls/2"}
	r.closeIssue(context.Background(), provider, repo, task, status, reviewer, agentName(reviewer), nil, open)
	filed := byHand()
	if len(filed) != 1 || status.ByHand != filed[0].URL {
		t.Fatalf("by-hand issues %v, status.byHand %q; want one, recorded", filed, status.ByHand)
	}

	for _, rerun := range []struct {
		name   string
		byHand string
	}{{"with status.byHand", status.ByHand}, {"with status.byHand lost", ""}} {
		again := &c4v1beta1.TaskStatus{Round: 1, IssueTitle: status.IssueTitle, PullRequestURL: status.PullRequestURL,
			ByHand: rerun.byHand}
		r.closeIssue(context.Background(), provider, repo, task, again, reviewer, agentName(reviewer), nil, open)
		if got := byHand(); len(got) != 1 {
			t.Errorf("%s: %d by-hand issues, want 1", rerun.name, len(got))
		}
		if again.ByHand != filed[0].URL {
			t.Errorf("%s: status.byHand %q, want %q", rerun.name, again.ByHand, filed[0].URL)
		}
		comments := git.Issue(repo.Owner, repo.Name, n).Comments
		if last := comments[len(comments)-1]; !strings.Contains(last, filed[0].URL) {
			t.Errorf("%s: closing comment %q doesn't link the by-hand issue", rerun.name, last)
		}
	}
}
