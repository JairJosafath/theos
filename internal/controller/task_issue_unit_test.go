package controller

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// A slow call to the Git host must not leave a merged issue open: each call gets
// its own time, so the comment and the close still happen after a follow-up that
// the host filed but answered too late for (vscode-extension #19).
func TestCloseIssueAfterALateAnswer(t *testing.T) {
	git := &fakeGit{repos: map[string]*fakeRepo{}, hangs: map[string]chan struct{}{}}
	provider := &fakeProvider{git: git}
	repo := gitprovider.Repository{Owner: "tools", Name: "vscode-extension"}
	n := git.addIssue(repo.Owner, repo.Name, "Check the README's claims", "", []string{model.LabelInProgress})
	git.issueState().late = true

	r := &TaskReconciler{GitTimeout: 50 * time.Millisecond}
	task := &c4v1alpha1.Task{ObjectMeta: objectMeta("tools", "vscode-extension-19"),
		Spec: c4v1alpha1.TaskSpec{Issue: int32(n)}}
	status := &c4v1alpha1.TaskStatus{Round: 3, PullRequestURL: "http://gitea/tools/vscode-extension/pulls/35"}
	reviewer := &c4v1alpha1.Agent{ObjectMeta: objectMeta("tools", "claude-reviewer")}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	r.closeIssue(ctx, provider, repo, task, status, reviewer,
		[]model.FollowUp{{Title: "Warn when the server is too old", Body: "Compare serverInfo.version."}})

	got := git.issue(repo.Owner, repo.Name, n)
	if got.State != "closed" {
		t.Errorf("issue #%d is %s, want closed", n, got.State)
	}
	if len(got.comments) != 1 || !strings.HasPrefix(got.comments[0], "Merged in ") {
		t.Errorf("comments %q, want the merge reported", got.comments)
	}
	if slices.Contains(got.Labels, model.LabelInProgress) {
		t.Errorf("labels %v, want %s removed", got.Labels, model.LabelInProgress)
	}
}
