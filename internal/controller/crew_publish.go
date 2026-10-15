/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
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
	reasonRepositoryAccepted  = "PublishRepositoryAccepted"
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
	status *c4v1alpha1.CrewStatus, provider gitprovider.Provider, repo gitprovider.Repository,
	tasks []c4v1alpha1.Task) {
	spec := crew.Spec.PublishReleases
	release := status.LastRelease
	if d := targetDiffers(crew); d != nil && release != nil &&
		(release.Publish == nil || release.Publish.State != publishPublished) {
		// Held, even at no target: the release waits, as a failed publish
		// does, and goes out at the next poll after the Crew and its Brief
		// agree.
		r.setPublish(crew, status, publishPending, "", c4v1alpha1.ReasonTargetDiffersFromBrief,
			model.ReleaseTargetPublishMessage(*d))
		return
	}
	if spec == nil || release == nil {
		if release != nil && release.Publish != nil && release.Publish.State != publishPublished {
			// Settled to publish nowhere: a release that waited, or failed,
			// no longer goes out (#1021). One that went out stays recorded.
			release.Publish = nil
		}
		apimeta.RemoveStatusCondition(&status.Conditions, ConditionPublished)
		return
	}
	log := logf.FromContext(ctx).WithValues("tag", release.Tag, "repository", spec.GitHub.Repository)
	r.dropAccepted(ctx, crew, status)
	facts := publishFacts(crew, container, release, status)
	// The repository published to is recorded however the pass ends.
	var last model.PublishStep
	defer func() { r.recordTarget(ctx, crew, status, facts, last) }()
	if facts.Published {
		return
	}
	token, err := r.publishToken(ctx, crew.Namespace, facts.Secret)
	if err != nil {
		// Not NotFound: a fact, so the Crew says why publishing stopped.
		facts.SecretError = err.Error()
	}
	facts.SecretFound = token != ""
	if facts.Source != nil {
		if facts.Overlaps, err = r.sourceOverlaps(ctx, crew); err != nil {
			log.Error(err, "Failed to list the Crews publishing source")
			return
		}
	}

	pubCtx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	pass := &publishPass{r: r, crew: crew, container: container, log: log, provider: provider, repo: repo, spec: spec, token: token,
		tag: release.Tag, facts: &facts, merged: mergedIssues(tasks)}
	for {
		step := model.NextPublishStep(facts)
		last = step
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
			r.published(ctx, crew, status, step, pass.private)
			return
		case model.PublishReadGitea, model.PublishReadIssues, model.PublishReadRepository, model.PublishReadGitHub,
			model.PublishReadSource, model.PublishReadGitHubSource:
			if !pass.read(pubCtx, step.Action) {
				return
			}
			continue
		default:
			var ok bool
			if ok, err = pass.write(pubCtx, step); !ok {
				return
			}
		}
		if err != nil {
			// A failed write goes back in as a fact and comes out as its reason.
			facts.Failure = publishFailure(err)
			continue
		}
		// One write per poll: the next one starts over from what is there.
		r.setPublish(crew, status, publishPublishing, "", reasonPublishing,
			fmt.Sprintf("Publishing %s to %s", model.GitHubTag(spec.TagPrefix, release.Tag), spec.GitHub.Repository))
		if pass.source != nil {
			status.LastRelease.Publish.Source = pass.source
		}
		return
	}
}

// publishPass is what one publishing pass read: the facts, and the Gitea
// release, the GitHub client and what it read of the source folder, which the
// writes use.
type publishPass struct {
	r    *CrewReconciler
	crew *c4v1alpha1.Crew
	// container is the Container whose release goes out.
	container *c4v1alpha1.Container
	log       logr.Logger
	provider  gitprovider.Provider
	repo      gitprovider.Repository
	spec      *c4v1alpha1.CrewPublishReleases
	token     string
	tag       string
	facts     *model.PublishFacts
	// merged are the issues of the Container's Merged Tasks.
	merged []int
	target gitprovider.ReleaseTarget
	gitea  *gitprovider.Release
	gh     *githubSource
	// source is the release's source, once this pass put its tag out.
	source *c4v1alpha1.CrewReleaseSource
	// private is whether GitHub reports the repository private, once read.
	private *bool
}

// read takes a read step, adding what it read to the facts; false ends the
// pass, on a failure Gitea's side or of building the GitHub client, both tried
// again on the next poll. A failed GitHub read is a fact.
func (p *publishPass) read(ctx context.Context, action model.PublishAction) bool {
	var err error
	switch action {
	case model.PublishReadGitea:
		if p.gitea, err = p.provider.GetRelease(ctx, p.repo, p.tag); err != nil {
			p.log.Error(err, "Failed to read the Gitea release")
			return false
		}
		p.facts.Gitea = sourceRelease(p.gitea)
	case model.PublishReadIssues:
		if p.facts.Issues, err = releaseIssues(ctx, p.provider, p.repo, p.tag, p.merged); err != nil {
			p.log.Error(err, "Failed to read the release's issues")
			return false
		}
	case model.PublishReadSource:
		// A tag's source doesn't change: it is read once per release.
		if last, ok := p.r.publishSources.Load(p.crew.UID); ok && last.(sourceRead).tag == p.tag {
			p.facts.SourceFiles = last.(sourceRead).source
			break
		}
		if p.facts.SourceFiles, err = readSource(ctx, p.r.Client, p.container, p.provider, p.repo, p.tag); err != nil {
			p.log.Error(err, "Failed to read the release's source from Gitea")
			return false
		}
		p.r.publishSources.Store(p.crew.UID, sourceRead{tag: p.tag, source: p.facts.SourceFiles})
	case model.PublishReadRepository, model.PublishReadGitHub, model.PublishReadGitHubSource:
		if p.target == nil {
			p.target, err = p.r.releaseTargets().GitHubReleases(gitprovider.GitHubConfig{
				Repository: p.spec.GitHub.Repository, Token: p.token})
			if err != nil {
				p.log.Error(err, "Failed to build the GitHub client")
				return false
			}
		}
		if action == model.PublishReadRepository {
			var repo gitprovider.TargetRepository
			if repo, err = p.target.Repository(ctx); err == nil {
				p.facts.Remote = &model.PublishTarget{Repository: repo.FullName, ID: repo.ID}
				p.private = new(repo.Private)
			}
		} else if action == model.PublishReadGitHub {
			p.facts.GitHub, err = targetReleases(ctx, p.target, *p.facts)
		} else if p.gh, err = readGitHubSource(ctx, p.target, p.facts.Source.Path,
			model.SourceTag(*p.facts.Source, p.tag)); err == nil {
			p.facts.GitHubSource = p.gh.facts
		}
		if err != nil {
			p.facts.Failure = publishFailure(err)
		}
	}
	return true
}

// write takes a write step; false ends the pass on a failure Gitea's side,
// tried again on the next poll. A failed GitHub write is returned, for the
// rule to read as a fact.
func (p *publishPass) write(ctx context.Context, step model.PublishStep) (bool, error) {
	var err error
	switch step.Action {
	case model.PublishWriteNotes:
		if err = p.provider.SetReleaseBody(ctx, p.repo, p.gitea.ID, step.Body); err != nil {
			p.log.Error(err, "Failed to write the release notes")
			return false, nil
		}
		p.log.Info("Wrote release notes")
	case model.PublishSourceCommit:
		var commit string
		var giteaErr error
		commit, giteaErr, err = commitSource(ctx, p.provider, p.repo, p.tag, p.target, p.gh, step.SourceCommit)
		if giteaErr != nil {
			p.log.Error(giteaErr, "Failed to read the release's source from Gitea")
			return false, nil
		}
		if err == nil {
			p.log.Info("Committed release source", "commit", commit, "path", step.SourceCommit.Path,
				"blobs", len(step.SourceCommit.Upload))
		}
	case model.PublishSourceTag:
		err = p.target.CreateRef(ctx, "tags/"+step.Tag, step.Commit)
		if errors.Is(err, gitprovider.ErrRefExists) {
			// Made since it was read: the next poll reads whose it is.
			return true, nil
		}
		if err == nil {
			p.source = p.r.sourcePublished(p.log, p.crew, *p.facts, step, p.private)
		}
	case model.PublishRetargetDraft:
		if _, err = p.target.RetargetDraft(ctx, step.Release, step.Target); err == nil {
			p.log.Info("Retargeted release draft", "draft", step.Release, "commit", step.Target)
		}
	case model.PublishCreateDraft:
		var draft gitprovider.TargetRelease
		if draft, err = p.target.CreateDraft(ctx, step.Tag, step.Name, step.Body, step.Target); err == nil {
			p.log.Info("Created release draft", "draft", draft.ID)
		}
	case model.PublishUploadAsset:
		var giteaErr error
		if giteaErr, err = uploadAsset(ctx, p.provider, p.repo, p.target, p.gitea, step); giteaErr != nil {
			// Gitea's side: not a GitHub failure, tried again on the next poll.
			p.log.Error(giteaErr, "Failed to download a release file from Gitea", "file", step.Asset.Name)
			return false, nil
		}
		if err == nil {
			p.log.Info("Uploaded release file", "file", step.Asset.Name, "size", step.Asset.Size)
		}
	case model.PublishDraft:
		if _, err = p.target.PublishRelease(ctx, step.Release, step.Latest); err == nil {
			p.log.Info("Published release draft", "draft", step.Release, "latest", step.Latest)
		}
	}
	return true, err
}

// publishFacts are what the Crew's spec, its last release and the repository it
// published to say of publishing that release. A Crew a Brief made has its
// target set by the Brief's spec.releases.
func publishFacts(crew *c4v1alpha1.Crew, container *c4v1alpha1.Container, release *c4v1alpha1.CrewRelease,
	status *c4v1alpha1.CrewStatus) model.PublishFacts {
	spec := crew.Spec.PublishReleases
	app := container.Spec.DisplayName
	if app == "" {
		app = container.Name
	}
	var recorded *model.PublishTarget
	if t := status.PublishTarget; t != nil {
		recorded = &model.PublishTarget{Repository: t.Repository, ID: t.ID}
	}
	return model.PublishFacts{Configured: true, Namespace: crew.Namespace, Container: crew.Spec.ContainerRef.Name,
		Tag: release.Tag, TagPrefix: spec.TagPrefix, App: app, Latest: spec.Latest == nil || *spec.Latest,
		Repository: spec.GitHub.Repository, Brief: crew.Labels[c4v1alpha1.LabelBrief], Recorded: recorded,
		Accept:    crew.Annotations[c4v1alpha1.AnnotationAcceptRepository],
		Published: release.Publish != nil && release.Publish.State == publishPublished, Runs: release.Run,
		Secret: spec.GitHub.TokenSecretRef.Name, Source: publishSource(crew)}
}

// recordTarget records in status the repository the Crew published to, after a
// pass that read facts and ended in last, with a Normal Event when a person's
// c4.factory.io/accept-repository took another.
func (r *CrewReconciler) recordTarget(ctx context.Context, crew *c4v1alpha1.Crew, status *c4v1alpha1.CrewStatus,
	facts model.PublishFacts, last model.PublishStep) {
	target, accepted := model.NextPublishTarget(facts, last)
	prev := status.PublishTarget
	status.PublishTarget = nil
	if target != nil {
		status.PublishTarget = &c4v1alpha1.CrewPublishTarget{Repository: target.Repository, ID: target.ID}
	}
	if !accepted || prev != nil && prev.ID == target.ID {
		return
	}
	logf.FromContext(ctx).Info("Accepted the publish repository", "repository", target.Repository, "id", target.ID)
	r.event(crew, corev1.EventTypeNormal, reasonRepositoryAccepted, "PublishRelease", fmt.Sprintf(
		"Releases go to %s, GitHub id %d, as %s accepts", target.Repository, target.ID,
		c4v1alpha1.AnnotationAcceptRepository))
}

// dropAccepted removes c4.factory.io/accept-repository once status records the
// id it accepts: recorded first, then removed, so a failed status write loses
// nothing.
func (r *CrewReconciler) dropAccepted(ctx context.Context, crew *c4v1alpha1.Crew, status *c4v1alpha1.CrewStatus) {
	value, ok := crew.Annotations[c4v1alpha1.AnnotationAcceptRepository]
	if !ok || status.PublishTarget == nil || value != strconv.FormatInt(status.PublishTarget.ID, 10) {
		return
	}
	orig := crew.DeepCopy()
	delete(crew.Annotations, c4v1alpha1.AnnotationAcceptRepository)
	if err := r.Patch(ctx, crew, client.MergeFrom(orig)); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to remove the accepted repository's annotation")
		return
	}
	logf.FromContext(ctx).Info("Removed the accepted repository's annotation", "id", value)
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
// Crew closed when it tagged the release, and keeps those a Task merged.
func releaseIssues(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository,
	tag string, merged []int) ([]model.ReleaseIssue, error) {
	id, err := provider.EnsureMilestone(ctx, repo, tag, "")
	if err != nil {
		return nil, err
	}
	issues, err := provider.MilestoneIssues(ctx, repo, id)
	if err != nil {
		return nil, err
	}
	closed := []model.ReleaseIssue{}
	for _, i := range issues {
		if i.State == stateClosed {
			closed = append(closed, model.ReleaseIssue{Number: i.Number, Title: i.Title})
		}
	}
	return model.MergedReleaseIssues(closed, merged), nil
}

// mergedIssues returns the issues of the Merged Tasks among tasks.
func mergedIssues(tasks []c4v1alpha1.Task) []int {
	var out []int
	for _, t := range tasks {
		if t.Spec.Issue != 0 && t.Status.Phase == c4v1alpha1.TaskMerged {
			out = append(out, int(t.Spec.Issue))
		}
	}
	return out
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
	rel := model.TargetRelease{ID: found.ID, TagName: found.TagName, Target: found.Commit, Draft: found.Draft,
		Body: found.Body, URL: found.URL}
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

// eventReleaseTargetDiffers is the Warning Event of a Crew held because its
// release target differs from its Brief's.
const eventReleaseTargetDiffers = "ReleaseTargetDiffers"

// releaseTarget sets the ReleaseTarget condition of a Crew whose Brief sets
// where it publishes (AnnotationBriefPublishReleases), with a Warning Event
// once per target the Crew is held at; a Crew without the annotation has no
// condition.
func (r *CrewReconciler) releaseTarget(ctx context.Context, crew *c4v1alpha1.Crew, status *c4v1alpha1.CrewStatus) {
	if _, ok := crew.Annotations[c4v1alpha1.AnnotationBriefPublishReleases]; !ok {
		apimeta.RemoveStatusCondition(&status.Conditions, c4v1alpha1.ConditionReleaseTarget)
		r.targetSaid.Delete(crew.UID)
		return
	}
	d := targetDiffers(crew)
	if d == nil {
		setCondition(&status.Conditions, crew.Generation, c4v1alpha1.ConditionReleaseTarget, metav1.ConditionTrue,
			c4v1alpha1.ReasonReleaseTargetSetByBrief, model.ReleaseTargetSetByBrief(crew.Labels[c4v1alpha1.LabelBrief]))
		r.targetSaid.Delete(crew.UID)
		return
	}
	message := model.ReleaseTargetDiffersMessage(*d)
	// The Reconciler remembers the target it warned about, as the last
	// poll's status write can fail after its Event; after a restart the
	// condition says whether it warned.
	said := publishJSON(crew.Spec.PublishReleases)
	last, remembered := r.targetSaid.Load(crew.UID)
	cond := apimeta.FindStatusCondition(status.Conditions, c4v1alpha1.ConditionReleaseTarget)
	warned := cond != nil && cond.Status == metav1.ConditionFalse && cond.Message == message
	if remembered && last != said || !remembered && !warned {
		logf.FromContext(ctx).Info("Release target differs from the Brief's", "brief", d.Brief,
			"briefRepository", d.BriefRepo, "crewRepository", d.CrewRepo)
		r.event(crew, corev1.EventTypeWarning, eventReleaseTargetDiffers, "PublishRelease", message)
	}
	r.targetSaid.Store(crew.UID, said)
	setCondition(&status.Conditions, crew.Generation, c4v1alpha1.ConditionReleaseTarget, metav1.ConditionFalse,
		c4v1alpha1.ReasonReleaseTargetDiffers, message)
}

// setPublish records how publishing the last release went, in its status and
// the Published condition.
func (r *CrewReconciler) setPublish(crew *c4v1alpha1.Crew, status *c4v1alpha1.CrewStatus, state, url, reason,
	message string) {
	var source *c4v1alpha1.CrewReleaseSource
	if prev := status.LastRelease.Publish; prev != nil {
		// The source stays recorded while the rest of the release goes out.
		source = prev.Source
	}
	status.LastRelease.Publish = &c4v1alpha1.CrewReleasePublish{State: state, URL: url, Message: message,
		Source: source}
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

// published records the release as published, with a Normal Event, and its
// source, private as this pass read it, or as recorded when it read nothing.
func (r *CrewReconciler) published(ctx context.Context, crew *c4v1alpha1.Crew, status *c4v1alpha1.CrewStatus,
	step model.PublishStep, private *bool) {
	tag := status.LastRelease.Tag
	said := tag + "\n" + model.PublishPublished
	if last, _ := r.publishSaid.Load(crew.UID); last != said {
		logf.FromContext(ctx).Info("Published release", "tag", tag, "url", step.URL)
		r.event(crew, corev1.EventTypeNormal, reasonReleasePublished, "PublishRelease", step.Message)
	}
	r.publishSaid.Store(crew.UID, said)
	if prev := status.LastRelease.Publish; private == nil && prev != nil && prev.Source != nil {
		private = prev.Source.Private
	}
	r.setPublish(crew, status, publishPublished, step.URL, model.PublishPublished, step.Message)
	if s := step.Source; s != nil {
		status.LastRelease.Publish.Source = &c4v1alpha1.CrewReleaseSource{Commit: s.Commit, Tag: s.Tag, URL: s.URL,
			Private: private}
	}
}

// sourcePublished says the release's source is out, with a Normal Event, and a
// Warning Event when go install can't fetch it; private is what this pass
// read of the repository.
func (r *CrewReconciler) sourcePublished(log logr.Logger, crew *c4v1alpha1.Crew, facts model.PublishFacts,
	step model.PublishStep, private *bool) *c4v1alpha1.CrewReleaseSource {
	url := model.SourceURL(facts.Repository, facts.Source.Path, step.Tag)
	log.Info("Published source", "tag", step.Tag, "commit", step.Commit, "url", url)
	r.event(crew, corev1.EventTypeNormal, reasonSourcePublished, "PublishRelease",
		fmt.Sprintf("The source of %s is published at %s", facts.Tag, url))
	if step.Warning != "" {
		log.Info("Published source go install can't fetch from GitHub", "tag", step.Tag, "problem", step.Warning)
		r.event(crew, corev1.EventTypeWarning, reasonSourceNotGoInstallable, "PublishRelease",
			fmt.Sprintf("%s; the source of %s is published, but go install can't fetch it from GitHub",
				step.Warning, facts.Tag))
	}
	return &c4v1alpha1.CrewReleaseSource{Commit: step.Commit, Tag: step.Tag, URL: url, Private: private}
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
