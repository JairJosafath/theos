// Package github is the Deployer of a DeployTarget's github member: each
// release ships as a GitHub release of one repository, its tag, notes and
// files, by the draft, upload and publish sequence the Crew's release copy
// (Crew.spec.publishReleases.github) uses too.
package github

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-logr/logr"

	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/ports"
)

// tokenKey is the key of the token in the target's Secret.
const tokenKey = "token"

// Releases builds the release client of a GitHub repository:
// gitprovider.Client on github.com, a fake GitHub in tests.
type Releases interface {
	GitHubReleases(cfg gitprovider.GitHubConfig) (gitprovider.ReleaseTarget, error)
}

// Deployer ships releases to GitHub. It is the tool itself, so Ship takes one
// write per call.
type Deployer struct {
	releases Releases
}

var _ ports.Deployer = (*Deployer)(nil)

// New returns the github member's Deployer, on github.com when releases is nil.
func New(releases Releases) *Deployer {
	if releases == nil {
		releases = gitprovider.Client{}
	}
	return &Deployer{releases: releases}
}

func (d *Deployer) client(t ports.Target) (gitprovider.ReleaseTarget, error) {
	return d.releases.GitHubReleases(gitprovider.GitHubConfig{Repository: t.Settings[model.GitHubSettingRepository],
		Token: strings.TrimSpace(string(t.Secret[tokenKey]))})
}

// Check reads the repository with the target's token.
func (d *Deployer) Check(ctx context.Context, t ports.Target) error {
	c, err := d.client(t)
	if err != nil {
		return err
	}
	_, err = c.Repository(ctx)
	return portError(err)
}

// Ship takes the next write of shipping s as a GitHub release: create the
// draft, retarget it, upload a file or publish it; nothing once the release
// is out. A release of the tag that the factory didn't make is left alone and
// fails with reason TagTaken.
func (d *Deployer) Ship(ctx context.Context, t ports.Target, s ports.Shipment) error {
	tag := model.GitHubTag(t.Settings[model.GitHubSettingTagPrefix], s.Version)
	if len(s.Files) == 0 {
		return &ports.Error{Kind: ports.ErrArtifactMissing,
			Cause: fmt.Errorf("release %s of %s has no files to ship to GitHub", s.Version, s.App)}
	}
	c, err := d.client(t)
	if err != nil {
		return err
	}
	marker := model.ShipMarker(s.App, tag)
	files := make([]model.ReleaseAsset, len(s.Files))
	for i, f := range s.Files {
		files[i] = model.ReleaseAsset{ID: int64(i), Name: f.Name, Size: f.Size}
	}
	ours, err := marked(ctx, c, tag, marker, files)
	if err != nil {
		return portError(err)
	}
	takesLatest := t.Settings[model.GitHubSettingLatest] != "false"
	if ours != nil && !ours.Draft && takesLatest {
		// The release is out; what is left is Latest, when another of the
		// app's releases took it (a rollback, docs/plans/github-deploy-state.md).
		latest, err := c.LatestRelease(ctx)
		if err != nil {
			return portError(err)
		}
		if model.GitHubLatestHeld(s.App, ours, release(latest)) {
			if _, err := c.PublishRelease(ctx, ours.ID, true); err != nil {
				return portError(err)
			}
			logr.FromContextOrDiscard(ctx).Info("Made release Latest", "release", ours.ID, "tag", tag)
			return nil
		}
	}
	step := model.NextReleaseStep(model.ReleaseShip{Tag: tag, Name: model.ReleaseName(s.DisplayName, s.Version),
		Body: model.ReleaseBody(s.Notes, marker), Commit: s.Commit, Files: files, Latest: takesLatest}, ours)
	return portError(d.write(ctx, c, s, step))
}

// release is r as the model sees it, nil for nil.
func release(r *gitprovider.TargetRelease) *model.TargetRelease {
	if r == nil {
		return nil
	}
	return &model.TargetRelease{ID: r.ID, TagName: r.TagName, Target: r.Commit, Draft: r.Draft, Body: r.Body,
		URL: r.URL}
}

// marked returns the release of tag whose body holds marker, with its files
// when it is a draft; nil when there is none. A published release of tag
// without it is TagTaken.
func marked(ctx context.Context, c gitprovider.ReleaseTarget, tag, marker string,
	files []model.ReleaseAsset) (*model.TargetRelease, error) {
	found, err := c.FindRelease(ctx, tag, marker)
	if err != nil || found == nil {
		return nil, err
	}
	if !strings.Contains(found.Body, marker) {
		return nil, &gitprovider.ReleaseError{Reason: gitprovider.ReasonTagTaken,
			Message: fmt.Sprintf("github: a release %s the factory didn't make is there; it is left alone", tag)}
	}
	rel := release(found)
	if !found.Draft {
		return rel, nil
	}
	assets, err := c.ListAssets(ctx, found.ID)
	if err != nil {
		return nil, err
	}
	for _, a := range assets {
		size := a.Size
		if !a.Uploaded {
			// An upload GitHub never finished is sent again, whatever its size.
			size = -1
		}
		rel.Assets = append(rel.Assets, model.ReleaseAsset{ID: a.ID, Name: model.ShippedFileName(files, a.Name),
			Size: size})
	}
	return rel, nil
}

// write takes step, logging what it wrote.
func (d *Deployer) write(ctx context.Context, c gitprovider.ReleaseTarget, s ports.Shipment,
	step model.PublishStep) error {
	log := logr.FromContextOrDiscard(ctx)
	switch step.Action {
	case model.PublishRetargetDraft:
		if _, err := c.RetargetDraft(ctx, step.Release, step.Target); err != nil {
			return err
		}
		log.Info("Retargeted release draft", "draft", step.Release, "commit", step.Target)
	case model.PublishCreateDraft:
		draft, err := c.CreateDraft(ctx, step.Tag, step.Name, step.Body, step.Target)
		if err != nil {
			return err
		}
		log.Info("Created release draft", "draft", draft.ID)
	case model.PublishUploadAsset:
		file := s.Files[step.Asset.ID]
		if step.Replace != nil {
			if err := c.DeleteAsset(ctx, step.Replace.ID); err != nil {
				return err
			}
		}
		body, size, err := file.Open(ctx)
		if err != nil {
			return &FileError{Name: file.Name, Err: err}
		}
		defer func() { _ = body.Close() }()
		if _, err := c.UploadAsset(ctx, step.Release, file.Name, size, body); err != nil {
			return err
		}
		log.Info("Uploaded release file", "file", file.Name, "size", step.Asset.Size)
	case model.PublishDraft:
		if _, err := c.PublishRelease(ctx, step.Release, step.Latest); err != nil {
			return err
		}
		log.Info("Published release draft", "draft", step.Release, "latest", step.Latest)
	}
	return nil
}

// FileError is a release file that couldn't be opened: the Git host's side,
// not GitHub's.
type FileError struct {
	Name string
	Err  error
}

func (e *FileError) Error() string { return fmt.Sprintf("opening release file %s: %v", e.Name, e.Err) }

func (e *FileError) Unwrap() error { return e.Err }

// State reports version's release of app, as model.GitHubDeployState says:
// a draft is Deploying; a published one Deployed and healthy at its page, as
// a GitHub release is once it is there, unless the target takes Latest and
// another of the app's releases holds it, which Ship moves back; with no
// release of version, the app's newest.
func (d *Deployer) State(ctx context.Context, t ports.Target, app, version string) (ports.DeployState, error) {
	c, err := d.client(t)
	if err != nil {
		return ports.DeployState{}, err
	}
	facts, err := deployFacts(ctx, c, t, app, version)
	if err != nil {
		return ports.DeployState{}, portError(err)
	}
	run := model.GitHubDeployState(facts).DeployRun
	return ports.DeployState{Version: run.Version, Phase: run.Phase, Health: run.Health, URL: run.URL,
		Reason: run.Reason, Message: run.Message}, nil
}

// deployFacts reads what GitHubDeployState decides on: every release, and
// GitHub's Latest only when GitHubReadsLatest says it matters.
func deployFacts(ctx context.Context, c gitprovider.ReleaseTarget, t ports.Target, app,
	version string) (model.GitHubDeployFacts, error) {
	all, err := c.ListReleases(ctx)
	if err != nil {
		return model.GitHubDeployFacts{}, err
	}
	facts := model.GitHubDeployFacts{App: app, Version: version, TagPrefix: t.Settings[model.GitHubSettingTagPrefix],
		TakesLatest: t.Settings[model.GitHubSettingLatest] != "false", Releases: make([]model.TargetRelease, len(all))}
	for i := range all {
		facts.Releases[i] = *release(&all[i])
	}
	if model.GitHubReadsLatest(facts) {
		latest, err := c.LatestRelease(ctx)
		if err != nil {
			return model.GitHubDeployFacts{}, err
		}
		facts.Latest = release(latest)
	}
	return facts, nil
}

// portError makes a GitHub failure one of the port's errors where it is one,
// keeping GitHub's: a refused token or a repository it can't see refuses the
// credential, a 5xx, a rate limit or the network is unreachable.
func portError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*FileError](err); ok {
		return err
	}
	re, ok := errors.AsType[*gitprovider.ReleaseError](err)
	switch {
	case !ok, re.Reason == gitprovider.ReasonGitHubError:
		return &ports.Error{Kind: ports.ErrUnreachable, Cause: err}
	case re.Reason == gitprovider.ReasonUnauthorized, re.Reason == gitprovider.ReasonRepositoryNotFound:
		return &ports.Error{Kind: ports.ErrCredentialRefused, Cause: err}
	}
	return err
}
