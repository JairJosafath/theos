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
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// A round's issue with uses: builds on the released Go module of each app it
// names (docs/plans/done/app-modules.md, decision 7): it is readied once each has
// published one, with a line pinning that version.

// usedModules is what an issue's uses: come to in one reconcile: the module of
// each app it uses once all are published, else what it waits for.
type usedModules struct {
	modules []model.UsedModule
	waits   []string
}

// usesLookup is what one reconcile read for the round's uses: issues.
type usesLookup struct {
	// crews are the namespace's Crews by Container.
	crews map[string]*c4v1alpha1.Crew
	// tags are, by Container, the title of the round's release milestone in
	// its repository, which its release's tag takes; "" while it has none.
	tags map[string]string
}

// usedModules resolves an issue's uses:. An app the round releases, with
// issues of the round, must have made the round's release; any other gives
// its newest. Either way the release must have published its Go module.
func (r *BriefReconciler) usedModules(ctx context.Context, run *roundApply, issues []model.PlanIssue,
	issue model.PlanIssue) (usedModules, error) {
	var out usedModules
	if len(issue.Uses) == 0 {
		return out, nil
	}
	if run.uses == nil {
		var crews c4v1alpha1.CrewList
		if err := r.List(ctx, &crews, client.InNamespace(run.brief.Namespace)); err != nil {
			return out, err
		}
		run.uses = &usesLookup{crews: map[string]*c4v1alpha1.Crew{}, tags: map[string]string{}}
		for i := range crews.Items {
			run.uses.crews[crews.Items[i].Spec.ContainerRef.Name] = &crews.Items[i]
		}
	}
	for _, name := range issue.Uses {
		want := ""
		if run.released[name] && len(model.IssuesFor(issues, name)) > 0 {
			tag, err := r.roundReleaseTag(ctx, run, name)
			if err != nil {
				return out, fmt.Errorf("reading the release milestone of %s: %w", name, err)
			}
			if tag == "" {
				out.waits = append(out.waits, fmt.Sprintf("%s's release of round %s", name, run.round))
				continue
			}
			want = tag
		}
		var release *c4v1alpha1.CrewRelease
		if crew := run.uses.crews[name]; crew != nil {
			release = crew.Status.LastRelease
		}
		switch {
		case release == nil && want == "":
			out.waits = append(out.waits, fmt.Sprintf("%s's first release", name))
		case release == nil || (want != "" && release.Tag != want):
			out.waits = append(out.waits, fmt.Sprintf("%s's release %s", name, want))
		case release.GoModule == nil || release.GoModule.State == moduleStateFailed:
			out.waits = append(out.waits, fmt.Sprintf("%s's release %s to publish its Go module", name, release.Tag))
		case release.GoModule.State != moduleStatePublished:
			out.waits = append(out.waits, fmt.Sprintf("%s, whose release %s published no Go module: %s", name,
				release.Tag, release.GoModule.Message))
		default:
			out.modules = append(out.modules, model.UsedModule{Path: release.GoModule.Path,
				Version: release.GoModule.Version})
		}
	}
	return out, nil
}

// roundReleaseTag returns the title of the round's release milestone in the
// repository of Container name, found by its marker, or "" when it has none.
func (r *BriefReconciler) roundReleaseTag(ctx context.Context, run *roundApply, name string) (string, error) {
	if tag, ok := run.uses.tags[name]; ok {
		return tag, nil
	}
	var c c4v1alpha1.Container
	if err := r.Get(ctx, client.ObjectKey{Namespace: run.brief.Namespace, Name: name}, &c); err != nil {
		return "", err
	}
	provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, c.Namespace, c.Spec.GitProviderRef.Name)
	if err != nil {
		return "", err
	}
	gitCtx, cancel := context.WithTimeout(ctx, r.gitTimeout())
	defer cancel()
	milestones, err := provider.ListAllMilestones(gitCtx, provider.Repository(name))
	if err != nil {
		return "", err
	}
	tag := ""
	for _, m := range milestones {
		if model.HasReleaseMilestoneMarker(m.Description, run.brief.Name, int(run.brief.Status.Round)) {
			tag = m.Title
			break
		}
	}
	run.uses.tags[name] = tag
	return tag, nil
}

// pinUses gives a filed issue the line of each module it uses, writing body
// when a line is missing, so a restart writes it once.
func (r *BriefReconciler) pinUses(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository,
	filed gitprovider.Issue, body string, modules []model.UsedModule) error {
	for _, m := range modules {
		if !strings.Contains(filed.Body, model.UsesLine(m)) {
			if err := provider.EditIssueBody(ctx, repo, filed.Number, body); err != nil {
				return err
			}
			logf.FromContext(ctx).Info("Pinned the modules a Brief issue uses", "repository", repo.Owner+"/"+repo.Name,
				"number", filed.Number, "modules", len(modules))
			return nil
		}
	}
	return nil
}

// roundWaitMessage is the Brief's Applying message while its round waits for
// Containers to be Ready, or issues for the modules they use.
func roundWaitMessage(round int, containers, uses []string) string {
	var parts []string
	if len(containers) > 0 {
		parts = append(parts, fmt.Sprintf("Round %d waits for %s to be Ready", round, strings.Join(containers, ", ")))
	}
	if len(uses) > 0 {
		lead := fmt.Sprintf("Round %d readies its issues that use another app's module once it is published: ", round)
		if len(parts) > 0 {
			lead = "it readies its issues that use another app's module once it is published: "
		}
		parts = append(parts, lead+strings.Join(uses, "; "))
	}
	return model.Clip(strings.Join(parts, "; "), 1000)
}
