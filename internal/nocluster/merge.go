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

package nocluster

import (
	"context"
	"errors"
	"fmt"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/engine"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/modelfacts"
	"github.com/JairJosafath/theos/internal/ports"
)

// MergeResult is what theos merge did.
type MergeResult struct {
	Task        string `json:"task"`
	PullRequest string `json:"pullRequest"`
	Merged      bool   `json:"merged"`
	Reason      string `json:"reason,omitempty"`
	Says        string `json:"says"`
}

// ErrMergeRefused is Merge's error for a merge model.DecidePersonMerge
// refuses; the result says why.
var ErrMergeRefused = errors.New("the merge is refused")

// Merge merges task's pull request for a person (docs/plans/lead-runtime.md,
// decision 8; no-cluster decision 10): only once a counted approving
// verdict on its head is another session's than its author's, the checks
// main requires passed on it and it changes no protected path, which theos
// holds itself on every host. The host's merge holds what it holds besides.
// The next tick sees the merge and ends the Task.
func Merge(ctx context.Context, env Env, m *Model, task string) (MergeResult, error) {
	ls, err := leads(ctx, env, m)
	if err != nil {
		return MergeResult{}, err
	}
	l, issue, err := leadOf(ls, task)
	if err != nil {
		return MergeResult{}, err
	}
	res := MergeResult{Task: task}
	rec, err := l.rt.Store().Progress(ctx, task)
	if err != nil {
		return res, err
	}
	if rec.PullRequest == 0 {
		return res, fmt.Errorf("%w: %s has no pull request yet", ErrMergeRefused, task)
	}
	res.PullRequest = rec.PullRequestURL
	pr, err := l.host.GetPullRequest(ctx, l.repo, rec.PullRequest)
	if err != nil {
		return res, err
	}
	files, err := l.host.PullRequestFiles(ctx, l.repo, rec.PullRequest)
	if err != nil {
		return res, err
	}
	comments, err := l.comments(ctx, issue)
	if err != nil {
		return res, err
	}
	var standard *c4v1beta1.Standard
	if s := l.container.view.Choices().Standard; s != "" {
		standard = modelfacts.Find(m.Objects.Standards, s)
	}
	rule, _, err := modelfacts.Protection(l.container.view, l.container.res.Provider, standard)
	if err != nil {
		return res, err
	}
	checks, err := l.host.CombinedStatus(ctx, l.repo, pr.HeadSHA)
	if err != nil {
		return res, err
	}
	d := model.DecidePersonMerge(model.PersonMergeFacts{Setting: m.Settings.Merge,
		Open: pr.State == "open" && !pr.Merged, Head: pr.HeadSHA, Comments: comments, Rules: l.rules,
		Checks: model.BaseState(rule.StatusContexts, checks.State, checks.Contexts), Files: files,
		Protected: rule.ProtectedPaths})
	res.Reason, res.Says = d.Reason, d.Says
	if !d.Merge {
		return res, fmt.Errorf("%w: %s: %s", ErrMergeRefused, d.Reason, d.Says)
	}
	title := engine.PullTitle(issueTitle(ctx, l.host, l.repo, issue), task)
	if err := l.host.MergePullRequest(ctx, l.repo, rec.PullRequest, pr.HeadSHA, title,
		fmt.Sprintf("Task %s, round %d, merged by a person with theos merge.", task, rec.Progress.Round)); err != nil {
		return res, err
	}
	res.Merged, res.Says = true, "merged; the next theos next ends the Task and closes its issue"
	return res, nil
}

// issueTitle is the issue's title, "" when it can't be read.
func issueTitle(ctx context.Context, host ports.Issues, repo ports.Repository, number int) string {
	i, err := host.GetIssue(ctx, repo, number)
	if err != nil {
		return ""
	}
	return i.Title
}
