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
	"io"
	"slices"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/ports"
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

// publishTimeout bounds one publishing pass: an upload streams a whole file.
const publishTimeout = 30 * time.Minute

// ReleaseTargets builds the clients of the repositories a Crew publishes its
// releases to.
type ReleaseTargets interface {
	GitHubReleases(cfg gitprovider.GitHubConfig) (gitprovider.ReleaseTarget, error)
	// GitHubToken is the token a Secret's data gives: its token, or its
	// GitHub App's installation token.
	GitHubToken(ctx context.Context, apiURL, repository string, secret map[string][]byte) (string, error)
}

// publishRelease takes the next steps of publishing the Crew's last release
// to its GitHub repository: the reads the decision needs, then one write, or
// the report of why it waits, failed or is done. A failure is a Warning Event
// once per release and reason, and is tried again on the next poll. The
// release is named after the Container, its display name or else its name.
func (r *CrewReconciler) publishRelease(ctx context.Context, crew *c4v1beta1.Crew, container *c4v1beta1.Container,
	status *c4v1beta1.CrewStatus, provider gitprovider.Provider, repo gitprovider.Repository,
	tasks []c4v1beta1.Task) {
	spec := crew.Spec.PublishReleases
	release := status.LastRelease
	if d := targetDiffers(crew); d != nil && release != nil &&
		(release.Publish == nil || release.Publish.State != publishPublished) {
		// Held, even at no target: the release waits, as a failed publish
		// does, and goes out at the next poll after the Crew and its Brief
		// agree.
		r.setPublish(crew, status, publishPending, "", c4v1beta1.ReasonTargetDiffersFromBrief,
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
	token, err := r.publishToken(ctx, crew.Namespace, facts.Secret, spec.GitHub.Repository)
	facts.SecretFound = token != ""
	_, refused := errors.AsType[*gitprovider.ReleaseError](err)
	switch {
	case refused:
		// GitHub refused to mint the GitHub App's token: the credential is
		// there, and refused.
		facts.SecretFound, facts.Failure = true, publishFailure(err)
	case err != nil:
		// Not NotFound: a fact, so the Crew says why publishing stopped.
		facts.SecretError = err.Error()
	}
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
			fmt.Sprintf("Publishing %s to %s", model.ReleaseTag(spec.TagPrefix, release.Tag), spec.GitHub.Repository))
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
	crew *c4v1beta1.Crew
	// container is the Container whose release goes out.
	container *c4v1beta1.Container
	log       logr.Logger
	provider  gitprovider.Provider
	repo      gitprovider.Repository
	spec      *c4v1beta1.CrewPublishReleases
	token     string
	tag       string
	facts     *model.PublishFacts
	// merged are the issues of the Container's Merged Tasks.
	merged []int
	target gitprovider.ReleaseTarget
	gitea  *gitprovider.Release
	// source is the release's source, once this pass put its tag out.
	source *c4v1beta1.CrewReleaseSource
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
		if p.gitea != nil {
			// The image goes to a registry, not to the GitHub release.
			p.gitea.Assets = slices.DeleteFunc(p.gitea.Assets, func(a gitprovider.ReleaseAsset) bool {
				return model.IsImageArchive(a.Name)
			})
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
		var known []string
		if known, err = knownCredentials(ctx, p.r.Client, p.r.APIReader, p.container, p.token); err != nil {
			p.log.Error(err, "Failed to read the credentials the source is checked against")
			return false
		}
		if p.facts.SourceFiles, err = readSource(ctx, p.r.Client, p.container, p.provider, p.repo, p.tag,
			known); err != nil {
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
		switch action {
		case model.PublishReadRepository:
			var repo gitprovider.TargetRepository
			if repo, err = p.target.Repository(ctx); err == nil {
				p.facts.Remote = &model.PublishTarget{Repository: repo.FullName, ID: repo.ID}
				p.private = new(repo.Private)
			}
		case model.PublishReadGitHub:
			p.facts.GitHub, err = targetReleases(ctx, p.target, *p.facts)
		default:
			var gh gitprovider.SourceFolder
			if gh, err = gitprovider.ReadSourceFolder(ctx, p.target, p.facts.Source.Path,
				model.SourceTag(*p.facts.Source, p.tag)); err == nil {
				p.facts.GitHubSource = gh.Facts
			}
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
		// The source commit is the github member's Deployer's, as the
		// release's writes are: it takes the same step from the same reads.
		var ok bool
		if ok, err = p.ship(ctx, step); !ok {
			return false, nil
		}
	case model.PublishSourceTag:
		var ok bool
		if ok, err = p.ship(ctx, step); !ok {
			return false, nil
		}
		if errors.Is(err, gitprovider.ErrRefExists) {
			// Made since it was read: the next poll reads whose it is.
			return true, nil
		}
		if err == nil {
			p.source = p.r.sourcePublished(p.log, p.crew, *p.facts, step, p.private)
		}
	case model.PublishRetargetDraft, model.PublishCreateDraft, model.PublishUploadAsset, model.PublishDraft:
		// The draft, upload and publish sequence is the github member's
		// Deployer's: it takes the same step from the same reads.
		var ok bool
		if ok, err = p.ship(ctx, step); !ok {
			return false, nil
		}
	}
	return true, err
}

// publishFacts are what the Crew's spec, its last release and the repository it
// published to say of publishing that release. A Crew a Brief made has its
// target set by the Brief's spec.releases.
func publishFacts(crew *c4v1beta1.Crew, container *c4v1beta1.Container, release *c4v1beta1.CrewRelease,
	status *c4v1beta1.CrewStatus) model.PublishFacts {
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
		Repository: spec.GitHub.Repository, Brief: crew.Labels[c4v1beta1.LabelBrief], Recorded: recorded,
		Accept:    crew.Annotations[c4v1beta1.AnnotationAcceptRepository],
		Published: release.Publish != nil && release.Publish.State == publishPublished, Runs: release.Run,
		Secret: spec.GitHub.TokenSecretRef.Name, Source: publishSource(crew)}
}

// recordTarget records in status the repository the Crew published to, after a
// pass that read facts and ended in last, with a Normal Event when a person's
// c4.factory.io/accept-repository took another.
func (r *CrewReconciler) recordTarget(ctx context.Context, crew *c4v1beta1.Crew, status *c4v1beta1.CrewStatus,
	facts model.PublishFacts, last model.PublishStep) {
	target, accepted := model.NextPublishTarget(facts, last)
	prev := status.PublishTarget
	status.PublishTarget = nil
	if target != nil {
		status.PublishTarget = &c4v1beta1.CrewPublishTarget{Repository: target.Repository, ID: target.ID}
	}
	if !accepted || prev != nil && prev.ID == target.ID {
		return
	}
	logf.FromContext(ctx).Info("Accepted the publish repository", "repository", target.Repository, "id", target.ID)
	r.event(crew, corev1.EventTypeNormal, reasonRepositoryAccepted, "PublishRelease", fmt.Sprintf(
		"Releases go to %s, GitHub id %d, as %s accepts", target.Repository, target.ID,
		c4v1beta1.AnnotationAcceptRepository))
}

// dropAccepted removes c4.factory.io/accept-repository once status records the
// id it accepts: recorded first, then removed, so a failed status write loses
// nothing.
func (r *CrewReconciler) dropAccepted(ctx context.Context, crew *c4v1beta1.Crew, status *c4v1beta1.CrewStatus) {
	value, ok := crew.Annotations[c4v1beta1.AnnotationAcceptRepository]
	if !ok || status.PublishTarget == nil || value != strconv.FormatInt(status.PublishTarget.ID, 10) {
		return
	}
	orig := crew.DeepCopy()
	delete(crew.Annotations, c4v1beta1.AnnotationAcceptRepository)
	if err := r.Patch(ctx, crew, client.MergeFrom(orig)); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to remove the accepted repository's annotation")
		return
	}
	logf.FromContext(ctx).Info("Removed the accepted repository's annotation", "id", value)
}

// publishToken reads the token from the Secret named name, live, so a Secret
// created after a failure is used on the next poll: its token, or its GitHub
// App's installation token for repository alone; "" when there is neither. A
// mint GitHub refuses is a *gitprovider.ReleaseError.
func (r *CrewReconciler) publishToken(ctx context.Context, ns, name, repository string) (string, error) {
	var secret corev1.Secret
	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &secret)
	if apierrors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return r.releaseTargets().GitHubToken(ctx, "", repository, secret.Data)
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
func mergedIssues(tasks []c4v1beta1.Task) []int {
	var out []int
	for _, t := range tasks {
		if t.Spec.Issue != 0 && t.Status.Phase == c4v1beta1.TaskMerged {
			out = append(out, int(t.Spec.Issue))
		}
	}
	return out
}

// targetReleases reads the GitHub release the operator made for the tag, or
// one of that tag someone else made, with its files.
func targetReleases(ctx context.Context, target gitprovider.ReleaseTarget,
	facts model.PublishFacts) (*model.TargetReleases, error) {
	tag := model.ReleaseTag(facts.TagPrefix, facts.Tag)
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

// sourceName is the name of the Gitea file GitHub stored as name.
func sourceName(gitea *model.SourceRelease, name string) string {
	if gitea == nil {
		return name
	}
	return model.ShippedFileName(gitea.Assets, name)
}

// ship takes the next write of shipping the release to GitHub through the
// github member's Deployer, streaming its files from Gitea; false ends the
// pass on a failure the operator's or Gitea's side, tried again on the next
// poll. A failed GitHub write is returned, for the rule to read as a fact.
func (p *publishPass) ship(ctx context.Context, step model.PublishStep) (bool, error) {
	deployer, err := p.r.Deployers.For(model.DeployMemberGitHub)
	if err != nil {
		p.log.Error(err, "Failed to find the GitHub deployer")
		return false, nil
	}
	settings := map[string]string{model.GitHubSettingRepository: p.spec.GitHub.Repository,
		model.GitHubSettingTagPrefix: p.facts.TagPrefix}
	if !p.facts.Latest {
		settings[model.GitHubSettingLatest] = "false"
	}
	if src := p.facts.Source; src != nil {
		settings[model.GitHubSettingSourcePath] = src.Path
		if src.TagPrefix != nil {
			settings[model.GitHubSettingSourceTagPrefix] = *src.TagPrefix
		}
	}
	target := ports.Target{Name: p.crew.Namespace + "/" + p.crew.Name, Member: model.DeployMemberGitHub,
		Settings: settings, Secret: map[string][]byte{model.GitHubSecretToken: []byte(p.token)}}
	shipment := ports.Shipment{App: p.crew.Namespace + "/" + p.facts.Container, DisplayName: p.facts.App,
		Version: p.tag, Commit: model.ShipCommit(*p.facts), Notes: p.gitea.Body}
	shipment.Files = releaseFiles(p.provider, p.repo, p.gitea.Assets)
	if p.facts.SourceFiles != nil {
		shipment.Source = p.facts.SourceFiles
		shipment.ReadSource = releaseSource(p.provider, p.repo, p.tag)
	}
	err = deployer.Ship(logr.NewContext(ctx, p.log), target, shipment)
	if ge, ok := errors.AsType[giteaError](err); ok {
		// Gitea's side: not a GitHub failure.
		switch step.Action {
		case model.PublishSourceCommit, model.PublishSourceTag:
			p.log.Error(ge.error, "Failed to read the release's source from Gitea")
		default:
			p.log.Error(ge.error, "Failed to download a release file from Gitea", "file", step.Asset.Name)
		}
		return false, nil
	}
	if _, ok := errors.AsType[*ports.Error](err); !ok && err != nil && isSourceStep(step) {
		// The source Gitea streamed isn't the one the Crew read (#1598): Gitea's
		// side too; the next poll reads it again.
		p.log.Error(err, "Failed to read the release's source from Gitea")
		return false, nil
	}
	return true, err
}

func isSourceStep(step model.PublishStep) bool {
	return step.Action == model.PublishSourceCommit || step.Action == model.PublishSourceTag
}

// releaseSource streams the files of the Git host's tag for the Deployer,
// each with its path and size; a failure of the Git host's own, not of each,
// is a giteaError.
func releaseSource(provider gitprovider.Provider, repo gitprovider.Repository,
	tag string) func(context.Context, func(string, int64, io.Reader) error) error {
	return func(ctx context.Context, each func(path string, size int64, content io.Reader) error) error {
		var eachErr error
		err := provider.ReadSource(ctx, repo, tag, func(f gitprovider.SourceFile, content io.Reader) error {
			eachErr = each(f.Path, f.Size, content)
			return eachErr
		})
		if err != nil && eachErr == nil {
			return giteaError{err}
		}
		return err
	}
}

// releaseFiles are the files of a Git host's release, each streamed from it
// when the Deployer sends it; a failure to give one is a giteaError.
func releaseFiles(provider gitprovider.Provider, repo gitprovider.Repository,
	assets []gitprovider.ReleaseAsset) []ports.File {
	files := make([]ports.File, 0, len(assets))
	for _, a := range assets {
		files = append(files, ports.File{Name: a.Name, Size: a.Size,
			Open: func(ctx context.Context) (io.ReadCloser, int64, error) {
				body, size, err := provider.OpenReleaseAsset(ctx, repo, a)
				if err != nil {
					return nil, 0, giteaError{err}
				}
				return body, size, nil
			}})
	}
	return files
}

// giteaError is a release file Gitea failed to give.
type giteaError struct{ error }

func (e giteaError) Unwrap() error { return e.error }

// eventReleaseTargetDiffers is the Warning Event of a Crew held because its
// release target differs from its Brief's.
const eventReleaseTargetDiffers = "ReleaseTargetDiffers"

// releaseTarget sets the ReleaseTarget condition of a Crew whose Brief sets
// where it publishes (AnnotationBriefPublishReleases), with a Warning Event
// once per target the Crew is held at; a Crew without the annotation has no
// condition.
func (r *CrewReconciler) releaseTarget(ctx context.Context, crew *c4v1beta1.Crew, status *c4v1beta1.CrewStatus) {
	if _, ok := crew.Annotations[c4v1beta1.AnnotationBriefPublishReleases]; !ok {
		apimeta.RemoveStatusCondition(&status.Conditions, c4v1beta1.ConditionReleaseTarget)
		r.targetSaid.Delete(crew.UID)
		return
	}
	d := targetDiffers(crew)
	if d == nil {
		setCondition(&status.Conditions, crew.Generation, c4v1beta1.ConditionReleaseTarget, metav1.ConditionTrue,
			c4v1beta1.ReasonReleaseTargetSetByBrief, model.ReleaseTargetSetByBrief(crew.Labels[c4v1beta1.LabelBrief]))
		r.targetSaid.Delete(crew.UID)
		return
	}
	message := model.ReleaseTargetDiffersMessage(*d)
	// The Reconciler remembers the target it warned about, as the last
	// poll's status write can fail after its Event; after a restart the
	// condition says whether it warned.
	said := publishJSON(crew.Spec.PublishReleases)
	last, remembered := r.targetSaid.Load(crew.UID)
	cond := apimeta.FindStatusCondition(status.Conditions, c4v1beta1.ConditionReleaseTarget)
	warned := cond != nil && cond.Status == metav1.ConditionFalse && cond.Message == message
	if remembered && last != said || !remembered && !warned {
		logf.FromContext(ctx).Info("Release target differs from the Brief's", "brief", d.Brief,
			"briefRepository", d.BriefRepo, "crewRepository", d.CrewRepo)
		r.event(crew, corev1.EventTypeWarning, eventReleaseTargetDiffers, "PublishRelease", message)
	}
	r.targetSaid.Store(crew.UID, said)
	setCondition(&status.Conditions, crew.Generation, c4v1beta1.ConditionReleaseTarget, metav1.ConditionFalse,
		c4v1beta1.ReasonReleaseTargetDiffers, message)
}

// setPublish records how publishing the last release went, in its status and
// the Published condition.
func (r *CrewReconciler) setPublish(crew *c4v1beta1.Crew, status *c4v1beta1.CrewStatus, state, url, reason,
	message string) {
	var source *c4v1beta1.CrewReleaseSource
	if prev := status.LastRelease.Publish; prev != nil {
		// The source stays recorded while the rest of the release goes out.
		source = prev.Source
	}
	status.LastRelease.Publish = &c4v1beta1.CrewReleasePublish{State: state, URL: url, Message: message,
		Source: source}
	value := metav1.ConditionFalse
	if state == publishPublished {
		value = metav1.ConditionTrue
	}
	setCondition(&status.Conditions, crew.Generation, ConditionPublished, value, reason, message)
}

// publishFailed reports a failure, with a Warning Event once per release and
// reason.
func (r *CrewReconciler) publishFailed(ctx context.Context, crew *c4v1beta1.Crew, status *c4v1beta1.CrewStatus,
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
func (r *CrewReconciler) published(ctx context.Context, crew *c4v1beta1.Crew, status *c4v1beta1.CrewStatus,
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
		status.LastRelease.Publish.Source = &c4v1beta1.CrewReleaseSource{Commit: s.Commit, Tag: s.Tag, URL: s.URL,
			Private: private}
	}
}

// sourcePublished says the release's source is out, with a Normal Event, and a
// Warning Event when go install can't fetch it; private is what this pass
// read of the repository.
func (r *CrewReconciler) sourcePublished(log logr.Logger, crew *c4v1beta1.Crew, facts model.PublishFacts,
	step model.PublishStep, private *bool) *c4v1beta1.CrewReleaseSource {
	url := model.SourceURL("", facts.Repository, facts.Source.Path, step.Tag)
	allowed := 0
	if facts.SourceFiles != nil {
		allowed = facts.SourceFiles.Allowed
	}
	log.Info("Published source", "tag", step.Tag, "commit", step.Commit, "url", url, "allowedSecretLines", allowed)
	message := fmt.Sprintf("The source of %s is published at %s", facts.Tag, url)
	if allowed > 0 {
		message += fmt.Sprintf("; %d lines marked %s were not checked for secrets", allowed, model.AllowSecretMark)
	}
	r.event(crew, corev1.EventTypeNormal, reasonSourcePublished, "PublishRelease", message)
	if step.Warning != "" {
		log.Info("Published source go install can't fetch from GitHub", "tag", step.Tag, "problem", step.Warning)
		r.event(crew, corev1.EventTypeWarning, reasonSourceNotGoInstallable, "PublishRelease",
			fmt.Sprintf("%s; the source of %s is published, but go install can't fetch it from GitHub",
				step.Warning, facts.Tag))
	}
	return &c4v1beta1.CrewReleaseSource{Commit: step.Commit, Tag: step.Tag, URL: url, Private: private}
}

// notePublishSkipped warns, when a Crew that publishes tags a release before
// the last one was published, that the last one is left to a person.
func (r *CrewReconciler) notePublishSkipped(ctx context.Context, crew *c4v1beta1.Crew, prev *c4v1beta1.CrewRelease) {
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
