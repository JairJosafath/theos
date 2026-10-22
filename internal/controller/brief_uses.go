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

// A round's issue with uses: builds on what the release of each app it names
// published: its Go module (docs/plans/done/app-modules.md, decision 7), its
// npm and PyPI packages (docs/plans/done/app-packages.md, decision 7). It is
// readied once each release has settled both ways and published something,
// with a line pinning each thing at that version.

// usedPackages is what an issue's uses: come to in one reconcile: what each
// app it uses published once all have, else what it waits for.
type usedPackages struct {
	packages []model.UsedPackage
	waits    []string
}

// usesLookup is what one reconcile read for the round's uses: issues.
type usesLookup struct {
	// crews are the namespace's Crews by Container.
	crews map[string]*c4v1alpha1.Crew
	// tags are, by Container, the title of the round's release milestone in
	// its repository, which its release's tag takes; "" while it has none.
	tags map[string]string
	// indexes are, by Container, the PyPI index its packages are installed
	// from: its GitProvider's.
	indexes map[string]string
}

// usedPackages resolves an issue's uses:. An app the round releases, with
// issues of the round, must have made the round's release; any other gives
// its newest. Either way the release must have settled its Go module and its
// packages, and published at least one of them.
func (r *BriefReconciler) usedPackages(ctx context.Context, run *roundApply, issues []model.PlanIssue,
	issue model.PlanIssue) (usedPackages, error) {
	var out usedPackages
	if len(issue.Uses) == 0 {
		return out, nil
	}
	if run.uses == nil {
		var crews c4v1alpha1.CrewList
		if err := r.List(ctx, &crews, client.InNamespace(run.brief.Namespace)); err != nil {
			return out, err
		}
		run.uses = &usesLookup{crews: map[string]*c4v1alpha1.Crew{}, tags: map[string]string{},
			indexes: map[string]string{}}
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
		case release.GoModule == nil || release.GoModule.State == model.ModuleStateFailed ||
			release.GoModule.State == model.ModuleStateWaiting:
			out.waits = append(out.waits, fmt.Sprintf("%s's release %s to publish its Go module", name, release.Tag))
		case release.Packages == nil || release.Packages.State == model.PackagesStateFailed ||
			release.Packages.State == model.PackagesStateWaiting:
			out.waits = append(out.waits, fmt.Sprintf("%s's release %s to publish its packages", name, release.Tag))
		default:
			published, err := r.releasePublished(ctx, run, name, release)
			if err != nil {
				return out, err
			}
			if len(published) == 0 {
				out.waits = append(out.waits, model.UsesNothingPublished(name, release.Tag, release.GoModule.Message,
					release.Packages.Message))
				continue
			}
			out.packages = append(out.packages, published...)
		}
	}
	return out, nil
}

// releasePublished lists what a settled release published that another app
// installs: its Go module when it has one, then each package in the order
// the Crew listed them; a PyPI package carries the index it is installed
// from, read once per Container from its GitProvider.
func (r *BriefReconciler) releasePublished(ctx context.Context, run *roundApply, name string,
	release *c4v1alpha1.CrewRelease) ([]model.UsedPackage, error) {
	var out []model.UsedPackage
	if release.GoModule.State == model.ModuleStatePublished {
		out = append(out, model.UsedPackage{Ecosystem: model.EcosystemGo, Name: release.GoModule.Path,
			Version: release.GoModule.Version})
	}
	for _, p := range release.Packages.Published {
		pkg := model.UsedPackage{Ecosystem: p.Ecosystem, Name: p.Name, Version: p.Version}
		if p.Ecosystem == model.EcosystemPyPI {
			index, err := r.pypiIndex(ctx, run, name)
			if err != nil {
				return nil, fmt.Errorf("reading the PyPI index of %s: %w", name, err)
			}
			pkg.Index = index
		}
		out = append(out, pkg)
	}
	return out, nil
}

// pypiIndex returns the PyPI index the packages of Container name are
// installed from: its GitProvider's.
func (r *BriefReconciler) pypiIndex(ctx context.Context, run *roundApply, name string) (string, error) {
	if index, ok := run.uses.indexes[name]; ok {
		return index, nil
	}
	var c c4v1alpha1.Container
	if err := r.Get(ctx, client.ObjectKey{Namespace: run.brief.Namespace, Name: name}, &c); err != nil {
		return "", err
	}
	provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, c.Namespace, c.Spec.GitProviderRef.Name)
	if err != nil {
		return "", err
	}
	index := provider.Repository(name).PyPIIndex
	run.uses.indexes[name] = index
	return index, nil
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

// pinUses gives a filed issue the line of each thing it uses, writing body
// when a line is missing, so a restart writes it once.
func (r *BriefReconciler) pinUses(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository,
	filed gitprovider.Issue, body string, packages []model.UsedPackage) error {
	for _, p := range packages {
		if !strings.Contains(filed.Body, model.UsesLine(p)) {
			if err := provider.EditIssueBody(ctx, repo, filed.Number, body); err != nil {
				return err
			}
			logf.FromContext(ctx).Info("Pinned the packages a Brief issue uses", "repository", repo.Owner+"/"+repo.Name,
				"number", filed.Number, "packages", len(packages))
			return nil
		}
	}
	return nil
}

// roundWaitMessage is the Brief's Applying message while its round waits for
// Containers to be Ready, or issues for the releases they use.
func roundWaitMessage(round int, containers, uses []string) string {
	var parts []string
	if len(containers) > 0 {
		parts = append(parts, fmt.Sprintf("Round %d waits for %s to be Ready", round, strings.Join(containers, ", ")))
	}
	if len(uses) > 0 {
		lead := fmt.Sprintf("Round %d readies its issues that use another app's release once it is published: ", round)
		if len(parts) > 0 {
			lead = "it readies its issues that use another app's release once it is published: "
		}
		parts = append(parts, lead+strings.Join(uses, "; "))
	}
	return model.Clip(strings.Join(parts, "; "), 1000)
}
