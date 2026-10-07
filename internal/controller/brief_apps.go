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
	"net/url"
	"strings"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
)

// The Brief's apps (docs/plans/done/brief-releases.md, R8): a line per Container the
// Brief made, copied from its Crew, so the person sees each app's latest
// release and where it went without reading the Crews.

const (
	maxBriefApps = 100

	releasesOn  = "On"
	releasesOff = "Off"

	// MessageReleasesOff tells the person how to turn an app's releases on.
	MessageReleasesOff = "Releases are off: ask for them in a prompt, or name this app in spec.releases."
)

// listApps returns the Brief's app lines: one per Container it made, in the
// order it made them, then one per spec.releases entry naming none of them.
func (r *BriefReconciler) listApps(ctx context.Context, brief *c4v1alpha1.Brief) ([]c4v1alpha1.BriefApp, error) {
	var apps []c4v1alpha1.BriefApp
	made := map[string]bool{}
	for _, o := range brief.Status.Created {
		if o.Kind != model.KindContainer || made[o.Name] {
			continue
		}
		made[o.Name] = true
		app, err := r.app(ctx, brief, o.Name)
		if err != nil {
			return nil, err
		}
		apps = append(apps, app)
	}
	for _, entry := range brief.Spec.Releases {
		if made[entry.Container] {
			continue
		}
		apps = append(apps, c4v1alpha1.BriefApp{Container: entry.Container, Releases: releasesOff,
			Message: fmt.Sprintf("The Brief hasn't made a Container named %s: its releases start once a round makes it.",
				entry.Container)})
	}
	if len(apps) > maxBriefApps {
		apps = apps[:maxBriefApps]
	}
	return apps, nil
}

// app is the line of a Container the Brief made, from its Crew, named after
// it (ensureCrew), and its repository.
func (r *BriefReconciler) app(ctx context.Context, brief *c4v1alpha1.Brief, container string) (
	c4v1alpha1.BriefApp, error) {
	app := c4v1alpha1.BriefApp{Container: container, Releases: releasesOff}
	var crew c4v1alpha1.Crew
	err := r.Get(ctx, client.ObjectKey{Namespace: brief.Namespace, Name: container}, &crew)
	if apierrors.IsNotFound(err) {
		app.Message = "The app has no Crew yet, so nothing releases it."
		return app, nil
	}
	if err != nil {
		return app, err
	}
	app.Crew = crew.Name
	if crew.Spec.TagReleases {
		app.Releases = releasesOn
	}
	if crew.Spec.PublishReleases != nil {
		app.PublishTo = crew.Spec.PublishReleases.GitHub.Repository
	}
	if release := crew.Status.LastRelease; release != nil {
		app.LastRelease = &c4v1alpha1.BriefAppRelease{Tag: release.Tag, Time: release.Time.DeepCopy(),
			Run: release.Run}
		if release.Publish != nil {
			app.LastRelease.PublishState = release.Publish.State
			app.LastRelease.PublishedURL = release.Publish.URL
		}
		var c c4v1alpha1.Container
		if err := r.Get(ctx, client.ObjectKey{Namespace: brief.Namespace, Name: container}, &c); err == nil {
			app.LastRelease.URL = releasePageURL(c.Status.RepositoryURL, release.Tag)
		} else if !apierrors.IsNotFound(err) {
			return app, err
		}
	}
	app.Message = appMessage(&app, crew.Labels[c4v1alpha1.LabelBrief] == brief.Name)
	return app, nil
}

// releasePageURL is the page of tag's release in the repository at repoURL,
// as Gitea serves it; empty without a repository.
func releasePageURL(repoURL, tag string) string {
	if repoURL == "" {
		return ""
	}
	return strings.TrimSuffix(repoURL, "/") + "/releases/tag/" + url.PathEscape(tag)
}

// appMessage says in one sentence how an app's releases go.
func appMessage(app *c4v1alpha1.BriefApp, briefsCrew bool) string {
	if app.Releases == releasesOff {
		if !briefsCrew {
			return fmt.Sprintf("Releases are off, and they are for the owner of Crew %s to turn on: the Brief didn't make it.",
				app.Crew)
		}
		return MessageReleasesOff
	}
	release := app.LastRelease
	if release == nil {
		return "Releases are on: the next one follows once the issues of its milestone are merged."
	}
	switch {
	case release.Run == "Failed":
		return fmt.Sprintf("Release %s was tagged, but its CI run failed.", release.Tag)
	case release.PublishState == "Failed":
		return fmt.Sprintf("Release %s could not be published to %s; the Crew says why and tries again.",
			release.Tag, app.PublishTo)
	case release.PublishState == "Published":
		return fmt.Sprintf("Released %s and published it to %s.", release.Tag, app.PublishTo)
	case release.PublishState != "":
		return fmt.Sprintf("Released %s; publishing it to %s.", release.Tag, app.PublishTo)
	default:
		return fmt.Sprintf("Released %s.", release.Tag)
	}
}

// crewReleaseChanges passes a Crew's creation, deletion, spec changes and
// changes to its latest release: all the Brief's app lines read from it.
func crewReleaseChanges() builder.WatchesOption {
	return builder.WithPredicates(predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldCrew, ok1 := e.ObjectOld.(*c4v1alpha1.Crew)
			newCrew, ok2 := e.ObjectNew.(*c4v1alpha1.Crew)
			return !ok1 || !ok2 || oldCrew.Generation != newCrew.Generation ||
				!equality.Semantic.DeepEqual(oldCrew.Status.LastRelease, newCrew.Status.LastRelease)
		},
	})
}
