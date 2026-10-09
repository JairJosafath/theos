/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// When a request ships (docs/plans/brief-requests.md, decision 9's released
// stage): once every issue a planned request became is closed and a
// published release holds each, or it was done in a Container that
// publishes nowhere, the Brief says where on the issue, once, and closes it
// as completed.

// shipCheck is when the Brief last looked whether a planned request
// shipped, and the releases of its Containers' Crews it saw then. It lives in
// memory: a restart looks again.
type shipCheck struct {
	at       time.Time
	releases string
}

// shipContainer is what the Brief knows of a Container a request's issues are
// in: its repository and its Crew's releases.
type shipContainer struct {
	provider  gitprovider.Provider
	repo      gitprovider.Repository
	publishes bool
	last      *c4v1alpha1.CrewRelease
}

// shipRequests marks each planned request of a repository the Brief reads
// as Released once its issues shipped, with where. It looks at a request's
// issues at most every suggestions recheck, and at once when a release of one
// of their Containers changes. A request whose planned comment isn't said yet
// waits for it, so the two come in order. A Git host that fails is logged and
// looked at again later; nothing here fails the reconcile.
func (r *BriefReconciler) shipRequests(ctx context.Context, brief *c4v1alpha1.Brief) {
	containers := map[string]*shipContainer{}
	now := r.now()
	for i := range brief.Status.Requests {
		s := &brief.Status.Requests[i]
		req := model.Request{Repository: s.Repository, Number: int(s.Number), Stage: s.Stage, Round: int(s.Round)}
		if s.Stage != model.RequestPlanned || len(s.Issues) == 0 || !readsRequests(brief, s.Repository) ||
			r.owesPlanned(brief, req) {
			continue
		}
		shipped, err := r.requestShipped(ctx, brief, s, containers, now)
		if err != nil {
			logf.FromContext(ctx).Info("Could not tell whether a request shipped", "request",
				model.RequestName(s.Repository, int(s.Number)), "error", err.Error())
			continue
		}
		if shipped == nil {
			continue
		}
		s.Stage, s.Shipped = model.RequestReleased, nil
		for _, sh := range shipped {
			s.Shipped = append(s.Shipped, c4v1alpha1.BriefRequestShipped{Tag: sh.Tag, URL: model.Clip(sh.URL, 300)})
		}
		logf.FromContext(ctx).Info("A request shipped", "request", model.RequestName(s.Repository, int(s.Number)),
			"releases", len(shipped))
	}
}

// readsRequests reports whether the Brief reads repository's issues: an
// entry of spec.releases names it with a requests mode other than Off.
func readsRequests(brief *c4v1alpha1.Brief, repository string) bool {
	return slices.ContainsFunc(brief.Spec.Releases, func(e c4v1alpha1.BriefRelease) bool {
		return e.GitHub.Repository == repository && requestsMode(&e) != model.RequestsOff
	})
}

// requestShipped is where a planned request shipped, nil while one of its
// issues is still to go or when it isn't time to look again.
func (r *BriefReconciler) requestShipped(ctx context.Context, brief *c4v1alpha1.Brief, s *c4v1alpha1.BriefRequest,
	containers map[string]*shipContainer, now time.Time) ([]model.Shipped, error) {
	var releases []string
	for _, name := range s.Issues {
		container, _, _ := strings.Cut(name, "#")
		c, err := r.shipContainer(ctx, brief, container, containers)
		if err != nil {
			return nil, err
		}
		if c.last != nil {
			state := ""
			if c.last.Publish != nil {
				state = c.last.Publish.State
			}
			releases = append(releases, container+" "+c.last.Tag+" "+state)
		}
	}
	key := requestsKey(brief, s.Repository) + fmt.Sprintf("#%d %d", s.Number, s.Round)
	seen := strings.Join(releases, ",")
	r.requestsMu.Lock()
	last, checked := r.requestsShip[key]
	r.requestsMu.Unlock()
	if checked && last.releases == seen && now.Sub(last.at) < r.suggestionsRecheck() {
		return nil, nil
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	facts := make([]model.ShipIssue, 0, len(s.Issues))
	for _, name := range s.Issues {
		container, number, _ := strings.Cut(name, "#")
		n, err := strconv.Atoi(number)
		if err != nil {
			return nil, fmt.Errorf("issue %s: %w", name, err)
		}
		c := containers[container]
		issue, err := c.provider.GetIssue(gitCtx, c.repo, n)
		if err != nil {
			return nil, fmt.Errorf("issue %s: %w", name, err)
		}
		fact := model.ShipIssue{URL: issue.URL, Closed: issue.State == stateClosed, Milestone: issue.MilestoneTitle,
			Publishes: c.publishes}
		if c.last != nil && c.last.Publish != nil {
			fact.LastTag = c.last.Tag
			fact.LastPublished = c.last.Publish.State == model.PublishPublished
			fact.LastURL = c.last.Publish.URL
		}
		facts = append(facts, fact)
	}
	r.requestsMu.Lock()
	if r.requestsShip == nil {
		r.requestsShip = map[string]shipCheck{}
	}
	r.requestsShip[key] = shipCheck{at: now, releases: seen}
	r.requestsMu.Unlock()
	shipped, _ := model.RequestShipped(facts)
	return shipped, nil
}

// shipContainer reads a Container's repository and Crew once per look.
func (r *BriefReconciler) shipContainer(ctx context.Context, brief *c4v1alpha1.Brief, name string,
	containers map[string]*shipContainer) (*shipContainer, error) {
	if c, ok := containers[name]; ok {
		return c, nil
	}
	var container c4v1alpha1.Container
	if err := r.Get(ctx, client.ObjectKey{Namespace: brief.Namespace, Name: name}, &container); err != nil {
		return nil, err
	}
	provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, container.Namespace,
		container.Spec.GitProviderRef.Name)
	if err != nil {
		return nil, err
	}
	c := &shipContainer{provider: provider, repo: provider.Repository(container.Name)}
	var crew c4v1alpha1.Crew
	err = r.Get(ctx, client.ObjectKey{Namespace: brief.Namespace, Name: name}, &crew)
	switch {
	case apierrors.IsNotFound(err):
	case err != nil:
		return nil, err
	default:
		c.publishes, c.last = crew.Spec.PublishReleases != nil, crew.Status.LastRelease
	}
	containers[name] = c
	return c, nil
}

// answerReleased says on each released request where it shipped, once, and
// closes it as completed; it then leaves the list. It goes on past a request
// it fails, and returns the first failure.
func (r *BriefReconciler) answerReleased(ctx context.Context, brief *c4v1alpha1.Brief,
	tracker gitprovider.IssueTracker, requests []model.Request) ([]model.Request, error) {
	var first error
	out := requests[:0:0]
	for _, req := range requests {
		if req.Stage != model.RequestReleased {
			out = append(out, req)
			continue
		}
		err := sayReleased(ctx, tracker, brief.Name, req)
		switch {
		case gitprovider.IssuesReason(err) == gitprovider.ReasonIssueNotFound:
			// Deleted or moved on GitHub: nothing to say.
		case err != nil:
			if first == nil {
				first = err
			}
			out = append(out, req)
			continue
		default:
			logf.FromContext(ctx).Info("Said on a request where it shipped", "request",
				model.RequestName(req.Repository, req.Number))
		}
		r.forgetIssue(brief, req.Repository, req.Number)
	}
	return out, first
}

// sayReleased comments where the request shipped unless the Brief did for
// the round that planned it, then closes it as completed.
func sayReleased(ctx context.Context, tracker gitprovider.IssueTracker, brief string, req model.Request) error {
	marker := model.RequestReleasedMarker(brief, req.Round)
	said, err := tracker.FindComment(ctx, req.Number, marker)
	if err != nil {
		return err
	}
	if said == nil {
		text := model.ReleasedComment(brief, req.Shipped) + marker + "\n"
		if _, err := tracker.Comment(ctx, req.Number, text); err != nil {
			return err
		}
	}
	return tracker.CloseIssue(ctx, req.Number, gitprovider.CloseCompleted)
}
