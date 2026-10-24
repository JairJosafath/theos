// Package github is the Deployer of a DeployTarget's github member: each
// release ships as a GitHub release of one repository, its tag, notes and
// files, by the draft, upload and publish sequence the Crew's release copy
// (Crew.spec.publishReleases.github) uses too.
package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/go-logr/logr"

	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/oci"
	"github.com/JairJosafath/c4factory/internal/ports"
)

// Releases builds the release client of a GitHub repository, and the token
// a target's Secret gives: gitprovider.Client on github.com, a fake GitHub in
// tests.
type Releases interface {
	GitHubReleases(cfg gitprovider.GitHubConfig) (gitprovider.ReleaseTarget, error)
	GitHubToken(ctx context.Context, apiURL, repository string, secret map[string][]byte) (string, error)
}

// Deployer ships releases to GitHub. It is the tool itself, so Ship takes one
// write per call.
type Deployer struct {
	releases Releases
	// registry is where images go, host[:port], a fake one in tests; "" is
	// the target's own, GHCR or its GitHub Enterprise Server's.
	registry string
}

var _ ports.Deployer = (*Deployer)(nil)

// Option configures a Deployer.
type Option func(*Deployer)

// WithImageRegistry sends images to host instead of the target's registry,
// for tests.
func WithImageRegistry(host string) Option {
	return func(d *Deployer) { d.registry = host }
}

// New returns the github member's Deployer, on github.com or the target's
// GitHub Enterprise Server when releases is nil.
func New(releases Releases, opts ...Option) *Deployer {
	if releases == nil {
		releases = gitprovider.Client{}
	}
	d := &Deployer{releases: releases}
	for _, o := range opts {
		o(d)
	}
	return d
}

// hosts are the places t talks to: github.com's, or its url's.
func hosts(t ports.Target) model.GitHubHosts {
	return model.GitHubHostsOf(t.Settings[model.GitHubSettingURL])
}

// client is the target's repository, with the Secret's token or its GitHub
// App's installation token, minted at the target's API for that repository
// alone. A mint GitHub refuses is CredentialRefused.
func (d *Deployer) client(ctx context.Context, t ports.Target) (gitprovider.ReleaseTarget, error) {
	h := hosts(t)
	repository := t.Settings[model.GitHubSettingRepository]
	token, err := d.releases.GitHubToken(ctx, h.API, repository, t.Secret)
	if err != nil {
		return nil, portError(err)
	}
	return d.releases.GitHubReleases(gitprovider.GitHubConfig{Repository: repository,
		Token: token, APIURL: h.API, UploadURL: h.Upload})
}

// registry is where t's images go: the Deployer's when set, else t's own.
func (d *Deployer) registryOf(t ports.Target) string {
	if d.registry != "" {
		return d.registry
	}
	return hosts(t).Registry
}

// Check reads the repository with the target's token.
func (d *Deployer) Check(ctx context.Context, t ports.Target) error {
	c, err := d.client(ctx, t)
	if err != nil {
		return err
	}
	_, err = c.Repository(ctx)
	return portError(err)
}

// Ship takes the next write of shipping s as a GitHub release: with
// source.path set, commit the release's source to the folder and tag it
// first; then create the draft, retarget it, upload a file or publish it;
// nothing once the release is out. A release of the tag that the factory didn't make is left alone and
// fails with reason TagTaken. With a packagesToken and an image, the image is
// copied to the target's registry before the draft is made, and the release's notes name it.
func (d *Deployer) Ship(ctx context.Context, t ports.Target, s ports.Shipment) error {
	tag := model.ReleaseTag(t.Settings[model.GitHubSettingTagPrefix], s.Version)
	names := make([]string, len(s.Files))
	for i, f := range s.Files {
		names[i] = f.Name
	}
	plan, problem := model.ReleaseShipment(model.ReleaseShipmentFacts{Type: s.Type, Deployment: s.Deployment,
		Framework: s.Framework, ScaffoldedFrom: s.ScaffoldedFrom,
		Files: names, Source: t.Settings[model.GitHubSettingSourcePath] != "" && s.Source != nil,
		Image: s.Image != "", TakesImage: strings.TrimSpace(string(t.Secret[model.GitHubSecretPackagesToken])) != ""})
	if problem != "" {
		return &ports.Error{Kind: ports.ErrArtifactMissing, Cause: errors.New(problem)}
	}
	packages, _ := d.packages(t)
	shipsImage := plan.Image && s.CopyImage != nil
	c, err := d.client(ctx, t)
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
		if model.GitHubLatestHeld(s.App, t.Settings[model.GitHubSettingTagPrefix], ours, release(latest)) {
			if _, err := c.PublishRelease(ctx, ours.ID, true); err != nil {
				return portError(err)
			}
			logr.FromContextOrDiscard(ctx).Info("Made release Latest", "release", ours.ID, "tag", tag)
			return nil
		}
	}
	commit := s.Commit
	if plan.Source && (ours == nil || ours.Draft) {
		// The source goes out first, and the release is of its commit; a
		// release that is out keeps the source it went out with.
		done, err := d.shipSource(ctx, c, t, s, tag)
		if err != nil || done == nil {
			return portError(err)
		}
		commit = done.Commit
	}
	notes := s.Notes
	if shipsImage && (ours == nil || ours.Draft) {
		image, copied, err := d.shipImage(ctx, t, packages, s)
		if err != nil || copied {
			return err
		}
		notes = model.GitHubImageNotes(notes, image)
	}
	step := model.NextReleaseStep(model.ReleaseShip{Tag: tag, Name: model.ReleaseName(s.DisplayName, s.Version),
		Body: model.ReleaseBody(notes, marker), Commit: commit, Files: files, Latest: takesLatest}, ours)
	return portError(d.write(ctx, c, s, step))
}

// packages is the login of t's packagesToken at t's registry; ok
// is false when t's Secret has none. GHCR takes any username with a classic
// token; the repository's owner is the one it names.
func (d *Deployer) packages(t ports.Target) (r oci.Registry, ok bool) {
	token := strings.TrimSpace(string(t.Secret[model.GitHubSecretPackagesToken]))
	owner, _, _ := strings.Cut(t.Settings[model.GitHubSettingRepository], "/")
	return oci.Registry{Host: d.registryOf(t), Username: owner, Password: token}, token != ""
}

// imageRepository is where t keeps app's image at its registry.
func imageRepository(t ports.Target, app string) string {
	_, container, _ := strings.Cut(app, "/")
	return model.ImageRepository(t.Settings[model.GitHubSettingRepository], container,
		t.Settings[model.GitHubSettingTagPrefix])
}

// shipImage copies s's image to the registry, tagged its version, unless the
// tag already names its digest; image is the copy, host/repository@digest,
// and copied says this call wrote it, so Ship takes no other write. Another
// digest under the tag fails with reason TagTaken naming the image. The
// registry refusing the token refuses the credential, any other failure to
// write the copy is unreachable, and a failure to read the image keeps the
// caller's error.
func (d *Deployer) shipImage(ctx context.Context, t ports.Target, packages oci.Registry,
	s ports.Shipment) (image string, copied bool, err error) {
	_, digest, _ := strings.Cut(s.Image, "@")
	repository := imageRepository(t, s.App)
	ref := packages.Host + "/" + repository
	have, err := oci.Resolve(ctx, packages, repository, s.Version)
	switch {
	case err != nil:
		return "", false, registryError(ref, err)
	case have == digest:
		return ref + "@" + digest, false, nil
	case have != "":
		return "", false, &gitprovider.ReleaseError{Reason: gitprovider.ReasonTagTaken,
			Message: fmt.Sprintf("github: image %s:%s names %s, not the release's %s; it is left alone", ref,
				s.Version, have, digest)}
	}
	err = s.CopyImage(ctx, ports.Registry{Host: packages.Host, Username: packages.Username,
		Password: packages.Password}, repository, s.Version)
	if errors.Is(err, oci.ErrSource) {
		return "", false, err
	}
	if err != nil {
		return "", false, registryError(ref, err)
	}
	logr.FromContextOrDiscard(ctx).Info("Copied the image", "image", s.Image, "copy", ref+"@"+digest)
	return ref + "@" + digest, true, nil
}

// registryError is the port's error for err from the registry at ref: a
// refused token refuses the credential, anything else is unreachable.
func registryError(ref string, err error) error {
	err = fmt.Errorf("image registry %s: %w", ref, err)
	if oci.Refused(err) {
		return &ports.Error{Kind: ports.ErrCredentialRefused, Cause: err}
	}
	return &ports.Error{Kind: ports.ErrUnreachable, Cause: err}
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

// sourceTarget is where the target puts a release's source, and the source
// tag of version; ok is false when it publishes none.
func sourceTarget(t ports.Target, version string) (src model.PublishSource, tag string, ok bool) {
	src.Path = t.Settings[model.GitHubSettingSourcePath]
	if src.Path == "" {
		return src, "", false
	}
	if prefix, set := t.Settings[model.GitHubSettingSourceTagPrefix]; set {
		src.TagPrefix = &prefix
	}
	return src, model.SourceTag(src, version), true
}

// shipSource reads the repository's head and source tag and takes the next
// write of publishing s's source, as model.NextSourceStep says. done is the
// source commit and tag once both are there, and nil when it wrote.
func (d *Deployer) shipSource(ctx context.Context, c gitprovider.ReleaseTarget, t ports.Target, s ports.Shipment,
	tag string) (done *model.PublishedSource, err error) {
	src, sourceTag, _ := sourceTarget(t, s.Version)
	gh, err := gitprovider.ReadSourceFolder(ctx, c, src.Path, sourceTag)
	if err != nil {
		return nil, err
	}
	step, done := model.NextSourceStep(model.SourceShip{Source: src, Files: s.Source, Web: hosts(t).Web,
		Repository: t.Settings[model.GitHubSettingRepository], App: s.DisplayName, Owner: s.App, Tag: s.Version,
		ReleaseTag: tag}, gh.Facts)
	if done != nil {
		return done, nil
	}
	log := logr.FromContextOrDiscard(ctx)
	switch step.Action {
	case model.PublishFail:
		return nil, &gitprovider.ReleaseError{Reason: step.Reason, Message: step.Message}
	case model.PublishSourceCommit:
		commit, err := commitSource(ctx, c, s, gh, step.SourceCommit)
		if err != nil {
			return nil, err
		}
		log.Info("Committed release source", "commit", commit, "path", step.SourceCommit.Path,
			"blobs", len(step.SourceCommit.Upload))
	case model.PublishSourceTag:
		if err := c.CreateRef(ctx, "tags/"+step.Tag, step.Commit); err != nil {
			return nil, err
		}
		log.Info("Tagged release source", "sourceTag", step.Tag, "commit", step.Commit)
		if step.Warning != "" {
			log.Info("Release source isn't go installable", "sourceTag", step.Tag, "problem", step.Warning)
		}
	}
	return nil, nil
}

// commitSource uploads the blobs GitHub lacks, streamed from s.ReadSource,
// builds the folder's tree and the root tree, the head's with only the folder
// replaced, commits it on the head and fast-forwards the default branch.
func commitSource(ctx context.Context, c gitprovider.SourceTarget, s ports.Shipment, gh gitprovider.SourceFolder,
	sc *model.SourceCommit) (string, error) {
	if err := uploadBlobs(ctx, c, s, sc.Upload); err != nil {
		return "", err
	}
	entries := make([]gitprovider.GitTreeEntry, 0, len(sc.Files))
	for _, f := range sc.Files {
		entries = append(entries, gitprovider.GitTreeEntry{Path: f.Path, Mode: string(f.Mode), Type: "blob", SHA: f.Blob})
	}
	folder, err := c.CreateTree(ctx, "", entries)
	if err != nil {
		return "", err
	}
	if folder != sc.Tree {
		return "", fmt.Errorf("github made the source's tree %s, not %s", folder, sc.Tree)
	}
	root := folder
	if sc.Path != "." {
		if root, err = c.CreateTree(ctx, gh.HeadRoot, []gitprovider.GitTreeEntry{
			{Path: sc.Path, Mode: "040000", Type: "tree", SHA: folder}}); err != nil {
			return "", err
		}
	}
	commit, err := c.CreateCommit(ctx, sc.Message, root, []string{sc.Parent})
	if err != nil {
		return "", err
	}
	return commit, c.UpdateRef(ctx, "heads/"+gh.Branch, commit)
}

// uploadBlobs streams each file of upload from s.ReadSource into a blob. A
// failure of ReadSource itself is a SourceError, the Git host's side.
func uploadBlobs(ctx context.Context, c gitprovider.SourceTarget, s ports.Shipment, upload []model.SourceFile) error {
	if len(upload) == 0 {
		return nil
	}
	if s.ReadSource == nil {
		return &SourceError{Err: errors.New("the shipment can't stream its source")}
	}
	want := map[string]model.SourceFile{}
	for _, f := range upload {
		want[f.Path] = f
	}
	var githubErr error
	readErr := s.ReadSource(ctx, func(path string, size int64, content io.Reader) error {
		file, ok := want[path]
		if !ok {
			return nil
		}
		delete(want, path)
		sha, err := c.CreateBlob(ctx, size, content)
		switch {
		case err != nil:
			githubErr = err
		case sha != file.Blob:
			githubErr = fmt.Errorf("github stored %s as blob %s, not %s", path, sha, file.Blob)
		}
		return githubErr
	})
	switch {
	case githubErr != nil:
		return githubErr
	case readErr != nil:
		return &SourceError{Err: readErr}
	case len(want) > 0:
		return &SourceError{Err: fmt.Errorf("the source of %s changed while it was published: %d files are gone",
			s.Version, len(want))}
	}
	return nil
}

// SourceError is a release's source that couldn't be read again: the Git
// host's side, not GitHub's.
type SourceError struct {
	Err error
}

func (e *SourceError) Error() string { return fmt.Sprintf("reading the release's source: %v", e.Err) }

func (e *SourceError) Unwrap() error { return e.Err }

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
	c, err := d.client(ctx, t)
	if err != nil {
		return ports.DeployState{}, err
	}
	facts, err := deployFacts(ctx, c, t, app, version)
	if err != nil {
		return ports.DeployState{}, portError(err)
	}
	run := model.GitHubDeployState(facts).DeployRun
	state := ports.DeployState{Version: run.Version, Phase: run.Phase, Health: run.Health, URL: run.URL,
		Reason: run.Reason, Message: run.Message}
	if packages, ok := d.packages(t); ok && run.Phase == model.DeployPhaseDeployed {
		// The image is reported where the registry holds the version's tag;
		// a registry that can't say leaves it out, as the release still runs.
		repository := imageRepository(t, app)
		digest, err := oci.Resolve(ctx, packages, repository, run.Version)
		if err == nil && digest != "" {
			state.Image = packages.Host + "/" + repository + "@" + digest
		}
	}
	if src, sourceTag, ok := sourceTarget(t, run.Version); ok && run.Version != "" {
		commit, err := c.Ref(ctx, "tags/"+sourceTag)
		if err != nil {
			return ports.DeployState{}, portError(err)
		}
		if commit != "" {
			state.Source = &model.PublishedSource{Commit: commit, Tag: sourceTag,
				URL: model.SourceURL(hosts(t).Web, t.Settings[model.GitHubSettingRepository], src.Path, sourceTag)}
		}
	}
	return state, nil
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
	if _, ok := errors.AsType[*SourceError](err); ok {
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
