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
	"slices"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// How a round takes requests (docs/plans/done/brief-requests.md, decisions 7 to
// 9): a new round takes every request let through, the planner answers each
// in plan/requests.yaml, and applying the round records what each became,
// which the Brief then says on the issue, once.

// takeRequests marks the requests a new round takes, those let through that
// no round took, as the round's, and returns their names for
// status.roundRequests.
func takeRequests(status *c4v1beta1.BriefStatus) []string {
	var names []string
	for i := range status.Requests {
		req := &status.Requests[i]
		if req.Stage != model.RequestAccepted || req.Round != 0 {
			continue
		}
		req.Round, req.Stage = status.Round, model.RequestPlanning
		names = append(names, requestName(*req))
	}
	return names
}

// releaseRequests hands back the requests of a round that ended without
// applying, failed or asking for input: they wait, let through, for the next
// round, which a new prompt or request starts, as the round's prompts do.
func releaseRequests(status *c4v1beta1.BriefStatus) {
	for i := range status.Requests {
		req := &status.Requests[i]
		if req.Round == status.Round && req.Stage == model.RequestPlanning {
			req.Round, req.Stage = 0, model.RequestAccepted
		}
	}
}

// heldRequests are the requests handed back by the last round, when it is
// over and didn't apply: they wait for something new to start a round. A
// request an applied round took and that came back, reopened, is new.
func heldRequests(status *c4v1beta1.BriefStatus) []string {
	n := len(status.History)
	if !roundOver(status) || status.History[n-1].Outcome == model.ReasonApplied {
		return nil
	}
	return status.RoundRequests
}

// roundRequests are the requests the running round took, and those of them
// still open on their tracker, which its plan must answer.
func roundRequests(status *c4v1beta1.BriefStatus) (took, open []string) {
	for _, req := range status.Requests {
		name := requestName(req)
		if req.Round == status.Round && slices.Contains(status.RoundRequests, name) {
			open = append(open, name)
		}
	}
	return status.RoundRequests, open
}

// answerRequests records what the round made of each request it took: the
// issues a taken one became and their release, or why it was skipped. The
// Brief says so on GitHub once the round is applied (answerPlanned).
func (r *BriefReconciler) answerRequests(ctx context.Context, run *roundApply, plan model.Plan) error {
	status := &run.brief.Status
	filed := map[int]int{}
	for _, issue := range plan.Issues {
		if f := run.filed(issue.N); f != nil {
			filed[issue.N] = int(f.Number)
		}
	}
	releases := map[string]string{}
	for _, answer := range plan.Requests {
		i := slices.IndexFunc(status.Requests, func(s c4v1beta1.BriefRequest) bool {
			return s.Round == status.Round && requestName(s) == answer.Request
		})
		if i < 0 {
			continue // closed on its tracker during the round
		}
		req := &status.Requests[i]
		if answer.Answer == model.AnswerSkip {
			req.Stage, req.Why = model.RequestSkipped, model.ClipSentences(oneLine(answer.Why), model.MaxAnswerWhy)
			continue
		}
		req.Stage, req.Issues = model.RequestPlanned, model.RequestIssues(answer, plan.Issues, filed)
		for _, name := range req.Issues {
			container, _, _ := strings.Cut(name, "#")
			if !run.released[container] {
				continue
			}
			release, ok := releases[container]
			if !ok {
				var err error
				if release, err = r.roundRelease(ctx, run.brief, container); err != nil {
					return err
				}
				releases[container] = release
			}
			if release != "" {
				req.Release = release
				break
			}
		}
		logf.FromContext(ctx).Info("A round answered a request", "request", answer.Request, "answer", answer.Answer,
			"issues", len(req.Issues), "release", req.Release)
	}
	return nil
}

// roundRelease is the title of the round's release milestone in the
// Container's repository, "" when the round made none there.
func (r *BriefReconciler) roundRelease(ctx context.Context, brief *c4v1beta1.Brief, container string) (
	string, error) {
	var c c4v1beta1.Container
	if err := r.Get(ctx, client.ObjectKey{Namespace: brief.Namespace, Name: container}, &c); err != nil {
		return "", err
	}
	provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, c.Namespace, c.Spec.GitProviderRef.Name)
	if err != nil {
		return "", err
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	milestones, err := provider.ListAllMilestones(gitCtx, provider.Repository(c.Name))
	if err != nil {
		return "", err
	}
	for _, m := range milestones {
		if model.HasReleaseMilestoneMarker(m.Description, brief.Name, int(brief.Status.Round)) {
			return m.Title, nil
		}
	}
	return "", nil
}

// oneLine is s with its line breaks and runs of spaces made single spaces.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// owesRequests reports whether a request of a repository or source the
// Brief reads waits for its planned comment.
func (r *BriefReconciler) owesRequests(brief *c4v1beta1.Brief) bool {
	return slices.ContainsFunc(brief.Status.Requests, func(s c4v1beta1.BriefRequest) bool {
		req := model.Request{Repository: s.Repository, Source: s.Source, Key: s.Key, Number: int(s.Number),
			Stage: s.Stage, Round: int(s.Round)}
		return readsOrigin(brief, req) && r.owesPlanned(brief, req)
	})
}

// owesPlanned reports whether a request waits for its planned comment: a
// skipped one until it is closed, a planned one until the Brief knows it
// commented.
func (r *BriefReconciler) owesPlanned(brief *c4v1beta1.Brief, req model.Request) bool {
	switch req.Stage {
	case model.RequestSkipped:
		return true
	case model.RequestPlanned:
		r.requestsMu.Lock()
		defer r.requestsMu.Unlock()
		return !r.requestsSaid[saidKey(brief, req)]
	}
	return false
}

func saidKey(brief *c4v1beta1.Brief, req model.Request) string {
	return requestsKey(brief, originID(req.Repository, req.Source)) +
		fmt.Sprintf("#%d %s %d", req.Number, model.CommentPlanned, req.Round)
}

// answerPlanned says on each request a round answered what it became, once:
// the issues and their release, or the skip's why, and closes a skipped one
// as not planned; a closed one leaves the list. It goes on past a request it
// fails, and returns the first failure.
func (r *BriefReconciler) answerPlanned(ctx context.Context, brief *c4v1beta1.Brief,
	tracker ports.IssueTracker, requests []model.Request) ([]model.Request, error) {
	var first error
	out := requests[:0:0]
	now := r.now()
	for _, req := range requests {
		if !r.owesPlanned(brief, req) {
			out = append(out, req)
			continue
		}
		text := model.SkippedComment(brief.Name, req.Why)
		if req.Stage == model.RequestPlanned {
			text = model.PlannedComment(brief.Name, plannedIssues(brief, req.Issues), req.Release)
		}
		text += model.RequestRoundMarker(brief.Name, req.Round) + "\n"
		err := r.heldClose(brief, req, now)
		if err == nil {
			err = sayPlanned(ctx, tracker, brief.Name, req, requestComment(req, text))
			r.holdClose(brief, req, err, now)
		}
		switch {
		case ports.IssuesReason(err) == ports.ReasonIssueNotFound:
			// Deleted or moved on the tracker: nothing to say.
			if req.Stage == model.RequestPlanned {
				r.markSaid(brief, req)
				out = append(out, req)
				continue
			}
		case err != nil:
			if first == nil {
				first = err
			}
			out = append(out, req)
			continue
		case req.Stage == model.RequestPlanned:
			logf.FromContext(ctx).Info("Said on a request what it became", "request", req.Name())
			r.markSaid(brief, req)
			out = append(out, req)
			continue
		default:
			logf.FromContext(ctx).Info("Skipped a request", "request", req.Name())
		}
		r.forgetIssue(brief, req)
	}
	return out, first
}

// sayPlanned comments text on the request unless the Brief did for the
// round that answered it, then closes a skipped one as not planned.
func sayPlanned(ctx context.Context, tracker ports.IssueTracker, brief string, req model.Request,
	text string) error {
	said, err := tracker.FindComment(ctx, req.Number, model.RequestRoundMarker(brief, req.Round))
	if err != nil {
		return err
	}
	if said == nil {
		if _, err := tracker.Comment(ctx, req.Number, text); err != nil {
			return err
		}
	}
	if req.Stage != model.RequestSkipped {
		return nil
	}
	return tracker.CloseIssue(ctx, req.Number, ports.CloseNotPlanned)
}

func (r *BriefReconciler) markSaid(brief *c4v1beta1.Brief, req model.Request) {
	r.requestsMu.Lock()
	defer r.requestsMu.Unlock()
	if r.requestsSaid == nil {
		r.requestsSaid = map[string]bool{}
	}
	r.requestsSaid[saidKey(brief, req)] = true
}

// plannedIssues are the issues a request became, by their titles and pages
// as the Brief recorded them: container#number when it has no title, and no
// page when it has none.
func plannedIssues(brief *c4v1beta1.Brief, issues []string) []model.PlannedIssue {
	out := make([]model.PlannedIssue, 0, len(issues))
	for _, name := range issues {
		container, number, _ := strings.Cut(name, "#")
		issue := model.PlannedIssue{Title: name}
		for _, i := range brief.Status.Issues {
			if strings.HasSuffix(i.Repository, "/"+container) && fmt.Sprint(i.Number) == number {
				if i.Title != "" {
					issue.Title = i.Title
				}
				issue.URL = i.URL
				break
			}
		}
		out = append(out, issue)
	}
	return out
}
