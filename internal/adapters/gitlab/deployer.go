// Package gitlab is the Deployer of a DeployTarget's gitlab member: each
// release ships as a GitLab release of one project, its files in the
// project's generic package registry and linked from the release, which is
// made last since GitLab has no drafts (docs/plans/gitlab-deploy.md).
package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/go-logr/logr"

	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/ports"
)

// Projects builds the client of a GitLab project: gitprovider.Client on the
// target's instance, a fake GitLab in tests.
type Projects interface {
	GitLab(cfg gitprovider.GitLabConfig) (gitprovider.GitLabProject, error)
}

// Deployer ships releases to GitLab. It is the tool itself, so Ship takes one
// write per call.
type Deployer struct {
	projects Projects
}

var _ ports.Deployer = (*Deployer)(nil)

// New returns the gitlab member's Deployer, on gitlab.com or the target's own
// instance when projects is nil.
func New(projects Projects) *Deployer {
	if projects == nil {
		projects = gitprovider.Client{}
	}
	return &Deployer{projects: projects}
}

// hosts are the places t talks to: gitlab.com's, or its url's.
func hosts(t ports.Target) model.GitLabHosts {
	return model.GitLabHostsOf(t.Settings[model.GitLabSettingURL])
}

// client is the target's project, with the Secret's token. A Secret without
// the token refuses the credential; a setting the client can't take (the
// project path, the url) is the target's own mistake, as on GitHub.
func (d *Deployer) client(t ports.Target) (gitprovider.GitLabProject, error) {
	token := strings.TrimSpace(string(t.Secret[model.GitLabSecretToken]))
	if token == "" {
		return nil, &ports.Error{Kind: ports.ErrCredentialRefused,
			Cause: fmt.Errorf("gitlab: the Secret has no %s", model.GitLabSecretToken)}
	}
	return d.projects.GitLab(gitprovider.GitLabConfig{URL: t.Settings[model.GitLabSettingURL],
		Project: t.Settings[model.GitLabSettingProject], Token: token})
}

// Check reads the project with the target's token and the token's scopes: a
// token GitLab refuses, one that can't see the project, or one without the
// api scope refuses the credential (docs/plans/gitlab-deploy.md, Decision 2).
func (d *Deployer) Check(ctx context.Context, t ports.Target) error {
	c, err := d.client(t)
	if err != nil {
		return err
	}
	if _, err := c.Project(ctx); err != nil {
		return portError(err)
	}
	scopes, err := c.TokenScopes(ctx)
	if err != nil {
		return portError(err)
	}
	if !slices.Contains(scopes, "api") {
		return &ports.Error{Kind: ports.ErrCredentialRefused,
			Cause: fmt.Errorf("gitlab: the token lacks the api scope; it has %s", strings.Join(scopes, ", "))}
	}
	return nil
}

// packageName is the generic package app's files go to: the Container's name.
func packageName(app string) string {
	_, container, ok := strings.Cut(app, "/")
	if !ok {
		return app
	}
	return container
}

// Ship takes the next write of shipping s as a GitLab release: the first of
// the release's files the project's generic package lacks, or holds at
// another size, is uploaded; with them all there the release is created,
// linking them; nothing once the release is out. A release of the tag that
// the factory didn't make is left alone and fails with reason TagTaken.
// The source and the image are steps 3 and 4 of docs/plans/gitlab-deploy.md.
func (d *Deployer) Ship(ctx context.Context, t ports.Target, s ports.Shipment) error {
	tag := model.GitHubTag(t.Settings[model.GitLabSettingTagPrefix], s.Version)
	names := make([]string, len(s.Files))
	for i, f := range s.Files {
		names[i] = f.Name
	}
	plan, problem := model.ReleaseShipment(model.ReleaseShipmentFacts{Type: s.Type, Deployment: s.Deployment,
		Framework: s.Framework, Files: names})
	if problem != "" {
		return &ports.Error{Kind: ports.ErrArtifactMissing, Cause: errors.New(problem)}
	}
	c, err := d.client(t)
	if err != nil {
		return err
	}
	marker := model.ShipMarker(s.App, tag)
	// A file's ID is its index in s.Files, which write streams it from.
	var files []model.ReleaseAsset
	for i, f := range s.Files {
		if slices.Contains(plan.Files, f.Name) {
			files = append(files, model.ReleaseAsset{ID: int64(i), Name: f.Name, Size: f.Size})
		}
	}
	pkg, version := packageName(s.App), s.Version
	facts, held, err := shipFacts(ctx, c, tag, pkg, version, s.Commit)
	if err != nil {
		return portError(err)
	}
	ship := model.GitLabShip{Tag: tag, Name: model.ReleaseName(s.DisplayName, s.Version),
		Body: model.ReleaseBody(s.Notes, marker), Marker: marker, Commit: s.Commit, Files: files}
	step := model.NextGitLabStep(ship, facts)
	return portError(d.write(ctx, c, t, s, step, files, held))
}

// shipFacts reads what NextGitLabStep decides on: the release of tag, the
// package's files, and the tag's commit when the release has a source
// commit to check it against. held maps each package file's id to its
// package, for the delete.
func shipFacts(ctx context.Context, c gitprovider.GitLabProject, tag, pkg, version,
	commit string) (facts model.GitLabFacts, held map[int64]int64, err error) {
	if facts.Release, err = c.Release(ctx, tag); err != nil {
		return facts, nil, err
	}
	if facts.Release != nil {
		// The release is out: its files aren't read, nothing is written.
		return facts, nil, nil
	}
	pkgFiles, err := c.PackageFiles(ctx, pkg, version)
	if err != nil {
		return facts, nil, err
	}
	held = make(map[int64]int64, len(pkgFiles))
	for _, f := range pkgFiles {
		facts.Files = append(facts.Files, model.ReleaseAsset{ID: f.ID, Name: f.Name, Size: f.Size})
		held[f.ID] = f.PackageID
	}
	if commit != "" {
		if facts.TagCommit, err = c.Tag(ctx, tag); err != nil {
			return facts, nil, err
		}
	}
	return facts, held, nil
}

// write takes step, logging what it wrote: files are the release's, as
// Ship planned them, linked when the release is created; held maps a
// package file's id to its package, for the delete a replace takes first.
func (d *Deployer) write(ctx context.Context, c gitprovider.GitLabProject, t ports.Target, s ports.Shipment,
	step model.PublishStep, files []model.ReleaseAsset, held map[int64]int64) error {
	log := logr.FromContextOrDiscard(ctx)
	pkg, version := packageName(s.App), s.Version
	switch step.Action {
	case model.PublishFail:
		return &gitprovider.ReleaseError{Reason: step.Reason, Message: step.Message}
	case model.PublishUploadAsset:
		file := s.Files[step.Asset.ID]
		if step.Replace != nil {
			if err := c.DeletePackageFile(ctx, held[step.Replace.ID], step.Replace.ID); err != nil {
				return err
			}
		}
		body, size, err := file.Open(ctx)
		if err != nil {
			return &FileError{Name: file.Name, Err: err}
		}
		defer func() { _ = body.Close() }()
		if err := c.UploadPackageFile(ctx, pkg, version, file.Name, size, body); err != nil {
			return err
		}
		log.Info("Uploaded release file", "package", pkg, "version", version, "file", file.Name, "size", size)
	case model.PublishCreateRelease:
		links := make([]gitprovider.GitLabReleaseLink, 0, len(files))
		for _, f := range files {
			links = append(links, gitprovider.GitLabReleaseLink{Name: f.Name,
				URL: packageFileURL(t, pkg, version, f.Name), DirectAssetPath: "/" + f.Name, LinkType: "package"})
		}
		ref := step.Target
		if ref == "" {
			// No source: the tag goes on the default branch's head, unless
			// it is already there, when GitLab keeps it and ignores ref.
			info, err := c.Project(ctx)
			if err != nil {
				return err
			}
			if ref = info.DefaultBranch; ref == "" {
				return &gitprovider.ReleaseError{Reason: gitprovider.ReasonGitLabRejected,
					Message: fmt.Sprintf("gitlab: project %s is empty, so there is no commit to tag %s at; "+
						"push a first commit to it", info.Path, step.Tag)}
			}
		}
		rel, err := c.CreateRelease(ctx, gitprovider.GitLabRelease{Tag: step.Tag, Name: step.Name,
			Description: step.Body, Ref: ref, Links: links})
		if re, ok := errors.AsType[*gitprovider.ReleaseError](err); ok && re.Status == http.StatusConflict {
			// Made since Ship read the tag, by another reconcile or by
			// someone else: the next Ship reads it and says whose it is.
			if got, rerr := c.Release(ctx, step.Tag); rerr == nil && got != nil {
				log.Info("Release already there", "tag", step.Tag, "url", got.URL)
				return nil
			}
		}
		if err != nil {
			return err
		}
		log.Info("Created release", "tag", step.Tag, "ref", ref, "url", rel.URL, "files", len(links))
	}
	return nil
}

// packageFileURL is where t's project serves the generic package file name of
// pkg at version: the API's download URL.
func packageFileURL(t ports.Target, pkg, version, name string) string {
	return hosts(t).API + "/projects/" + url.PathEscape(t.Settings[model.GitLabSettingProject]) +
		"/packages/generic/" + url.PathEscape(pkg) + "/" + url.PathEscape(version) + "/" + url.PathEscape(name)
}

// FileError is a release file that couldn't be opened: the Git host's side,
// not GitLab's.
type FileError struct {
	Name string
	Err  error
}

func (e *FileError) Error() string { return fmt.Sprintf("opening release file %s: %v", e.Name, e.Err) }

func (e *FileError) Unwrap() error { return e.Err }

// State reports version's release of app, as model.GitLabDeployState says:
// the wanted version's marked release is Deployed and healthy at its page;
// a version whose files are in the package registry but whose release isn't
// made yet is Deploying; else the app's newest marked release.
func (d *Deployer) State(ctx context.Context, t ports.Target, app, version string) (ports.DeployState, error) {
	c, err := d.client(t)
	if err != nil {
		return ports.DeployState{}, err
	}
	releases, err := c.ListReleases(ctx)
	if err != nil {
		return ports.DeployState{}, portError(err)
	}
	facts := model.GitLabDeployFacts{App: app, Version: version, TagPrefix: t.Settings[model.GitLabSettingTagPrefix],
		Releases: releases}
	if version != "" && model.GitHubMarkedRelease(app, model.GitHubTag(facts.TagPrefix, version), releases) == nil {
		files, err := c.PackageFiles(ctx, packageName(app), version)
		if err != nil {
			return ports.DeployState{}, portError(err)
		}
		facts.Shipping = len(files) > 0
	}
	run := model.GitLabDeployState(facts)
	return ports.DeployState{Version: run.Version, Phase: run.Phase, Health: run.Health, URL: run.URL,
		Reason: run.Reason, Message: run.Message}, nil
}

// portError makes a GitLab failure one of the port's errors where it is one,
// keeping GitLab's: a refused token or a project it can't see refuses the
// credential, a 5xx, a rate limit or the network is unreachable; anything
// else (a rejected write, TagTaken) stays as it is and fails the entry.
func portError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*FileError](err); ok {
		return err
	}
	re, ok := errors.AsType[*gitprovider.ReleaseError](err)
	switch {
	case !ok, re.Reason == gitprovider.ReasonGitLabError:
		return &ports.Error{Kind: ports.ErrUnreachable, Cause: err}
	case re.Reason == gitprovider.ReasonUnauthorized, re.Reason == gitprovider.ReasonRepositoryNotFound:
		return &ports.Error{Kind: ports.ErrCredentialRefused, Cause: err}
	}
	return err
}
