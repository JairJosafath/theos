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
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// ConditionPublished says how publishing a Crew's last release went, and why it
// failed.
const ConditionPublished = "Published"

// Reasons and Events of publishing besides the model's.
const (
	reasonPublishing          = "Publishing"
	reasonReleasePublished    = "ReleasePublished"
	reasonPublishFailed       = "ReleasePublishFailed"
	reasonReleaseNotPublished = "ReleaseNotPublished"
)

// States of status.lastRelease.publish.
const (
	publishPending    = "Pending"
	publishPublishing = "Publishing"
	publishPublished  = "Published"
	publishFailed     = "Failed"
)

// publishTokenKey is the key of the token in the Secret publishReleases names.
const publishTokenKey = "token"

// publishTimeout bounds one publishing pass: an upload streams a whole file.
const publishTimeout = 30 * time.Minute

// ReleaseTargets builds the clients of the repositories a Crew publishes its
// releases to.
type ReleaseTargets interface {
	GitHubReleases(cfg gitprovider.GitHubConfig) (gitprovider.ReleaseTarget, error)
}

// publishRelease takes the next steps of publishing the Crew's last release
// to its GitHub repository: the reads the decision needs, then one write, or
// the report of why it waits, failed or is done. A failure is a Warning Event
// once per release and reason, and is tried again on the next poll. The
// release is named after the Container, its display name or else its name.
func (r *CrewReconciler) publishRelease(ctx context.Context, crew *c4v1alpha1.Crew, container *c4v1alpha1.Container,
	status *c4v1alpha1.CrewStatus, provider gitprovider.Provider, repo gitprovider.Repository) {
	spec := crew.Spec.PublishReleases
	release := status.LastRelease
	if spec == nil || release == nil {
		apimeta.RemoveStatusCondition(&status.Conditions, ConditionPublished)
		return
	}
	log := logf.FromContext(ctx).WithValues("tag", release.Tag, "repository", spec.GitHub.Repository)
	facts := publishFacts(crew, container, release)
	if facts.Published {
		return
	}
	token, err := r.publishToken(ctx, crew.Namespace, facts.Secret)
	if err != nil {
		// Not NotFound: a fact, so the Crew says why publishing stopped.
		facts.SecretError = err.Error()
	}
	facts.SecretFound = token != ""

	pubCtx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	var target gitprovider.ReleaseTarget
	var gitea *gitprovider.Release
	for {
		step := model.NextPublishStep(facts)
		var err error
		switch step.Action {
		case model.PublishNone:
			return
		case model.PublishWait:
			r.setPublish(crew, status, publishPending, "", step.Reason, step.Message)
			return
		case model.PublishFail:
			r.publishFailed(ctx, crew, status, step)
			return
		case model.PublishDone:
			r.published(ctx, crew, status, step)
			return

		case model.PublishReadGitea:
			gitea, err = provider.GetRelease(pubCtx, repo, release.Tag)
			if err != nil {
				log.Error(err, "Failed to read the Gitea release")
				return
			}
			facts.Gitea = sourceRelease(gitea)
			continue
		case model.PublishReadIssues:
			if facts.Issues, err = releaseIssues(pubCtx, provider, repo, release.Tag); err != nil {
				log.Error(err, "Failed to read the release's issues")
				return
			}
			continue
		case model.PublishReadGitHub:
			if target == nil {
				target, err = r.releaseTargets().GitHubReleases(gitprovider.GitHubConfig{
					Repository: spec.GitHub.Repository, Token: token})
				if err != nil {
					log.Error(err, "Failed to build the GitHub client")
					return
				}
			}
			facts.GitHub, err = targetReleases(pubCtx, target, facts)
			if err != nil {
				facts.Failure = &model.PublishFailure{Reason: gitprovider.ReleaseReason(err), Detail: err.Error()}
			}
			continue

		case model.PublishWriteNotes:
			if err = provider.SetReleaseBody(pubCtx, repo, gitea.ID, step.Body); err != nil {
				log.Error(err, "Failed to write the release notes")
				return
			}
			log.Info("Wrote release notes")
		case model.PublishCreateDraft:
			var draft gitprovider.TargetRelease
			if draft, err = target.CreateDraft(pubCtx, step.Tag, step.Name, step.Body); err == nil {
				log.Info("Created release draft", "draft", draft.ID)
			}
		case model.PublishUploadAsset:
			var giteaErr error
			if giteaErr, err = uploadAsset(pubCtx, provider, repo, target, gitea, step); giteaErr != nil {
				// Gitea's side: not a GitHub failure, tried again on the next poll.
				log.Error(giteaErr, "Failed to download a release file from Gitea", "file", step.Asset.Name)
				return
			}
			if err == nil {
				log.Info("Uploaded release file", "file", step.Asset.Name, "size", step.Asset.Size)
			}
		case model.PublishDraft:
			if _, err = target.PublishRelease(pubCtx, step.Release, step.Latest); err == nil {
				log.Info("Published release draft", "draft", step.Release, "latest", step.Latest)
			}
		}
		if err != nil {
			// A failed write goes back in as a fact and comes out as its reason.
			facts.Failure = &model.PublishFailure{Reason: gitprovider.ReleaseReason(err), Detail: err.Error()}
			continue
		}
		// One write per poll: the next one starts over from what is there.
		r.setPublish(crew, status, publishPublishing, "", reasonPublishing,
			fmt.Sprintf("Publishing %s to %s", model.GitHubTag(spec.TagPrefix, release.Tag), spec.GitHub.Repository))
		return
	}
}

// publishFacts are what the Crew's spec and its last release say of publishing
// that release.
func publishFacts(crew *c4v1alpha1.Crew, container *c4v1alpha1.Container,
	release *c4v1alpha1.CrewRelease) model.PublishFacts {
	spec := crew.Spec.PublishReleases
	app := container.Spec.DisplayName
	if app == "" {
		app = container.Name
	}
	return model.PublishFacts{Configured: true, Namespace: crew.Namespace, Container: crew.Spec.ContainerRef.Name,
		Tag: release.Tag, TagPrefix: spec.TagPrefix, App: app, Latest: spec.Latest == nil || *spec.Latest,
		Repository: spec.GitHub.Repository,
		Published:  release.Publish != nil && release.Publish.State == publishPublished, Runs: release.Run,
		Secret: spec.GitHub.TokenSecretRef.Name}
}

// publishToken reads the token from the Secret named name, live, so a Secret
// created after a failure is used on the next poll; "" when there is none.
func (r *CrewReconciler) publishToken(ctx context.Context, ns, name string) (string, error) {
	var secret corev1.Secret
	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &secret)
	if apierrors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(secret.Data[publishTokenKey])), nil
}

// sourceRelease is the Gitea release as the rule reads it.
func sourceRelease(rel *gitprovider.Release) *model.SourceRelease {
	if rel == nil {
		return &model.SourceRelease{Missing: true}
	}
	out := &model.SourceRelease{Body: rel.Body}
	for _, a := range rel.Assets {
		out.Assets = append(out.Assets, model.ReleaseAsset{ID: a.ID, Name: a.Name, Size: a.Size})
	}
	return out
}

// releaseIssues reads the closed issues of the release's milestone, which the
// Crew closed when it tagged the release.
func releaseIssues(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository,
	tag string) ([]model.ReleaseIssue, error) {
	id, err := provider.EnsureMilestone(ctx, repo, tag, "")
	if err != nil {
		return nil, err
	}
	issues, err := provider.MilestoneIssues(ctx, repo, id)
	if err != nil {
		return nil, err
	}
	out := []model.ReleaseIssue{}
	for _, i := range issues {
		if i.State == "closed" {
			out = append(out, model.ReleaseIssue{Number: i.Number, Title: i.Title})
		}
	}
	return out, nil
}

// targetReleases reads the GitHub release the operator made for the tag, or
// one of that tag someone else made, with its files.
func targetReleases(ctx context.Context, target gitprovider.ReleaseTarget,
	facts model.PublishFacts) (*model.TargetReleases, error) {
	tag := model.GitHubTag(facts.TagPrefix, facts.Tag)
	found, err := target.FindRelease(ctx, tag, model.PublishMarker(facts.Namespace, facts.Container, tag))
	if err != nil || found == nil {
		return &model.TargetReleases{}, err
	}
	rel := model.TargetRelease{ID: found.ID, TagName: found.TagName, Draft: found.Draft, Body: found.Body,
		URL: found.URL}
	if found.Draft {
		assets, err := target.ListAssets(ctx, found.ID)
		if err != nil {
			return nil, err
		}
		for _, a := range assets {
			size := a.Size
			if !a.Uploaded {
				// An upload GitHub never finished is sent again, whatever its size.
				size = -1
			}
			rel.Assets = append(rel.Assets, model.ReleaseAsset{ID: a.ID, Name: sourceName(facts.Gitea, a.Name),
				Size: size})
		}
	}
	return &model.TargetReleases{Releases: []model.TargetRelease{rel}}, nil
}

// sourceName is the name of the Gitea file GitHub stored as name: GitHub turns
// a space, and other characters it doesn't keep in a file name, into a dot.
func sourceName(gitea *model.SourceRelease, name string) string {
	if gitea != nil {
		for _, a := range gitea.Assets {
			if githubAssetName(a.Name) == name {
				return a.Name
			}
		}
	}
	return name
}

func githubAssetName(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', strings.ContainsRune("._+-", r):
			return r
		}
		return '.'
	}, name)
}

// uploadAsset streams a Gitea release file to the draft, deleting the draft's
// file of that name first when it has one. It returns Gitea's failure apart
// from GitHub's.
func uploadAsset(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository,
	target gitprovider.ReleaseTarget, gitea *gitprovider.Release, step model.PublishStep) (giteaErr, err error) {
	var asset *gitprovider.ReleaseAsset
	for i, a := range gitea.Assets {
		if a.ID == step.Asset.ID {
			asset = &gitea.Assets[i]
		}
	}
	if asset == nil {
		return fmt.Errorf("gitea release %s has no file %s", gitea.Tag, step.Asset.Name), nil
	}
	if step.Replace != nil {
		if err := target.DeleteAsset(ctx, step.Replace.ID); err != nil {
			return nil, err
		}
	}
	body, size, err := provider.OpenReleaseAsset(ctx, repo, *asset)
	if err != nil {
		return err, nil
	}
	defer func() { _ = body.Close() }()
	_, err = target.UploadAsset(ctx, step.Release, asset.Name, size, body)
	return nil, err
}

// setPublish records how publishing the last release went, in its status and
// the Published condition.
func (r *CrewReconciler) setPublish(crew *c4v1alpha1.Crew, status *c4v1alpha1.CrewStatus, state, url, reason,
	message string) {
	status.LastRelease.Publish = &c4v1alpha1.CrewReleasePublish{State: state, URL: url, Message: message}
	value := metav1.ConditionFalse
	if state == publishPublished {
		value = metav1.ConditionTrue
	}
	setCondition(&status.Conditions, crew.Generation, ConditionPublished, value, reason, message)
}

// publishFailed reports a failure, with a Warning Event once per release and
// reason.
func (r *CrewReconciler) publishFailed(ctx context.Context, crew *c4v1alpha1.Crew, status *c4v1alpha1.CrewStatus,
	step model.PublishStep) {
	tag := status.LastRelease.Tag
	prev := status.LastRelease.Publish
	// The last poll's status write can fail after its Event, so the Reconciler
	// remembers what it said too.
	said := tag + "\n" + step.Reason
	last, _ := r.publishSaid.Load(crew.UID)
	cond := apimeta.FindStatusCondition(status.Conditions, ConditionPublished)
	if (prev == nil || prev.State != publishFailed || cond == nil || cond.Reason != step.Reason) && last != said {
		logf.FromContext(ctx).Info("Release publish failed", "tag", tag, "reason", step.Reason,
			"message", step.Message)
		r.event(crew, corev1.EventTypeWarning, reasonPublishFailed, "PublishRelease", step.Message)
	}
	r.publishSaid.Store(crew.UID, said)
	r.setPublish(crew, status, publishFailed, "", step.Reason, step.Message)
}

// published records the release as published, with a Normal Event.
func (r *CrewReconciler) published(ctx context.Context, crew *c4v1alpha1.Crew, status *c4v1alpha1.CrewStatus,
	step model.PublishStep) {
	tag := status.LastRelease.Tag
	said := tag + "\n" + model.PublishPublished
	if last, _ := r.publishSaid.Load(crew.UID); last != said {
		logf.FromContext(ctx).Info("Published release", "tag", tag, "url", step.URL)
		r.event(crew, corev1.EventTypeNormal, reasonReleasePublished, "PublishRelease", step.Message)
	}
	r.publishSaid.Store(crew.UID, said)
	r.setPublish(crew, status, publishPublished, step.URL, model.PublishPublished, step.Message)
}

// notePublishSkipped warns, when a Crew that publishes tags a release before
// the last one was published, that the last one is left to a person.
func (r *CrewReconciler) notePublishSkipped(ctx context.Context, crew *c4v1alpha1.Crew, prev *c4v1alpha1.CrewRelease) {
	if crew.Spec.PublishReleases == nil || prev == nil ||
		(prev.Publish != nil && prev.Publish.State == publishPublished) {
		return
	}
	message := fmt.Sprintf("%s was not published to %s before the next release was tagged; it is left to a person",
		prev.Tag, crew.Spec.PublishReleases.GitHub.Repository)
	logf.FromContext(ctx).Info("Left a release unpublished", "tag", prev.Tag)
	r.event(crew, corev1.EventTypeWarning, reasonReleaseNotPublished, "PublishRelease", message)
}

func (r *CrewReconciler) releaseTargets() ReleaseTargets {
	if r.Publish != nil {
		return r.Publish
	}
	return gitprovider.Client{}
}
