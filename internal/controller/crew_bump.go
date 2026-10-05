/*
Copyright 2026.

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
	"time"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// reasonBumpFiled is the Event of a filed bump issue.
const reasonBumpFiled = "BumpFiled"

// fileBumps files a bump issue for the Crew's last release in each Container
// that bundles its Container and has none yet. A failure is logged and tried
// again on the next poll.
func (r *CrewReconciler) fileBumps(ctx context.Context, crew *c4v1alpha1.Crew, status *c4v1alpha1.CrewStatus,
	repo gitprovider.Repository) {
	release := status.LastRelease
	if release == nil {
		return
	}
	log := logf.FromContext(ctx)
	bundlers, err := r.bundlers(ctx, crew.Namespace, crew.Spec.ContainerRef.Name)
	if err != nil {
		log.Error(err, "Failed to read the Containers that bundle this one")
		return
	}
	filed := make([]string, 0, len(release.Bumps))
	for _, b := range release.Bumps {
		filed = append(filed, b.Container)
	}
	for _, b := range model.BundlersToBump(release.Time.Time, bundlers, filed) {
		bump := model.Bump{Bundled: crew.Spec.ContainerRef.Name, Tag: release.Tag, Commit: release.Commit,
			TagURL: repo.WebURL + "/src/tag/" + release.Tag, Bundler: b}
		issue, isNew, err := r.fileBump(ctx, crew.Namespace, bump)
		if err != nil {
			log.Error(err, "Failed to file a bump issue", "bundler", b.Container, "tag", release.Tag)
			continue
		}
		release.Bumps = append(release.Bumps, c4v1alpha1.CrewReleaseBump{Container: b.Container, IssueURL: issue.URL})
		if isNew {
			log.Info("Filed a bump issue", "bundler", b.Container, "tag", release.Tag, "issue", issue.URL)
			if r.Recorder != nil {
				r.Recorder.Eventf(crew, nil, corev1.EventTypeNormal, reasonBumpFiled, "FileBump",
					"Filed %s to bump %s to %s", issue.URL, b.Container, release.Tag)
			}
		}
	}
}

// bundlers are the Containers that bundle the named one, through a Relationship
// that says bundles.
func (r *CrewReconciler) bundlers(ctx context.Context, ns, name string) ([]model.Bundler, error) {
	var rels c4v1alpha1.RelationshipList
	if err := r.List(ctx, &rels, client.InNamespace(ns),
		client.MatchingFields{indexElement: elementKey(c4v1alpha1.ElementContainer, name)}); err != nil {
		return nil, err
	}
	var out []model.Bundler
	for _, rel := range rels.Items {
		if !rel.Spec.Bundles || rel.Spec.Source.Kind != c4v1alpha1.ElementContainer ||
			rel.Spec.Target.Kind != c4v1alpha1.ElementContainer || rel.Spec.Target.Name != name {
			continue
		}
		b := model.Bundler{Container: rel.Spec.Source.Name, Relationship: rel.Name,
			Description: rel.Spec.Description, Created: rel.CreationTimestamp.Time}
		var source c4v1alpha1.Container
		err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: b.Container}, &source)
		if client.IgnoreNotFound(err) != nil {
			return nil, err
		}
		b.Ready = err == nil && source.Status.Phase == c4v1alpha1.ContainerReady
		out = append(out, b)
	}
	return out, nil
}

// fileBump returns the open bump issue for the release in the bundler's
// repository, or files it there: labelled kind/bump and status/ready, in the
// bundler's first open release. isNew says it was filed now.
func (r *CrewReconciler) fileBump(ctx context.Context, ns string, bump model.Bump) (gitprovider.Issue, bool, error) {
	var bundler c4v1alpha1.Container
	if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: bump.Bundler.Container}, &bundler); err != nil {
		return gitprovider.Issue{}, false, err
	}
	provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, ns, bundler.Spec.GitProviderRef.Name)
	if err != nil {
		return gitprovider.Issue{}, false, err
	}
	repo := provider.Repository(bundler.Name)
	gitCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	l := model.BumpLabel()
	labels := append(issueLabels(),
		gitprovider.Label{Name: l.Name, Color: l.Color, Description: l.Description, Exclusive: l.Exclusive})
	if err := provider.EnsureLabels(gitCtx, repo, labels); err != nil {
		return gitprovider.Issue{}, false, err
	}
	open, err := provider.ListIssues(gitCtx, repo, model.LabelBump)
	if err != nil {
		return gitprovider.Issue{}, false, err
	}
	for _, issue := range open {
		if model.HasBumpMarker(issue.Body, bump.Bundled, bump.Tag) {
			return issue, false, nil
		}
	}
	milestones, err := provider.ListMilestones(gitCtx, repo)
	if err != nil {
		return gitprovider.Issue{}, false, fmt.Errorf("milestones: %w", err)
	}
	releases := make([]model.ReleaseMilestone, 0, len(milestones))
	for _, m := range milestones {
		releases = append(releases, model.ReleaseMilestone{Release: model.Release{Title: m.Title, Due: m.Due}, ID: m.ID})
	}
	title, body := model.BumpIssue(bump)
	issue, err := provider.CreateIssue(gitCtx, repo, title, body, []string{model.LabelBump, model.LabelReady},
		model.FirstOpenRelease(releases))
	return issue, err == nil, err
}
