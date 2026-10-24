// Package gitlab is the Deployer of a DeployTarget's gitlab member: each
// release ships as a GitLab release of one project, its files in the
// project's generic package registry and linked from the release, which is
// made last since GitLab has no drafts (docs/plans/gitlab-deploy.md).
package gitlab

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/go-logr/logr"

	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/oci"
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

// packageName is the generic package app's files go to: the Container's
// name. Its version is the release's tag (model.GitLabPackageVersion), so
// two Containers of one name shipping one version to one project keep
// their files apart (#1873).
func packageName(app string) string {
	_, container, ok := strings.Cut(app, "/")
	if !ok {
		return app
	}
	return container
}

// Ship takes the next write of shipping s as a GitLab release: with a source
// folder set, the release's source goes out first, as one commit on the
// default branch and its source tag (docs/plans/gitlab-deploy.md, Decision
// 4), and the release is of that commit; then the first of the release's
// files the project's generic package lacks, or holds at another size, is
// uploaded; with them all there the image is copied to the project's
// container registry, when the project takes it (takesImage, Decision 5),
// and then the release is created, linking the files and naming the image;
// nothing once the release is out. A release of the tag that the factory
// didn't make is left alone and fails with reason TagTaken.
func (d *Deployer) Ship(ctx context.Context, t ports.Target, s ports.Shipment) error {
	tag := model.ReleaseTag(t.Settings[model.GitLabSettingTagPrefix], s.Version)
	names := make([]string, len(s.Files))
	for i, f := range s.Files {
		names[i] = f.Name
	}
	// The rule runs offline first, as if the project took the image: a
	// shipment with nothing to publish fails before GitLab is asked.
	shipment := model.ReleaseShipmentFacts{Type: s.Type, Deployment: s.Deployment, Framework: s.Framework,
		ScaffoldedFrom: s.ScaffoldedFrom, Files: names, Image: s.Image != "", TakesImage: true,
		Source: t.Settings[model.GitHubSettingSourcePath] != "" && s.Source != nil}
	plan, problem := model.ReleaseShipment(shipment)
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
	release, err := c.Release(ctx, tag)
	if err != nil {
		return portError(err)
	}
	commit := s.Commit
	if plan.Source && release == nil {
		// The source goes out first, and the release is of its commit; a
		// release that is out keeps the source it went out with.
		done, err := d.shipSource(ctx, c, t, s, tag)
		if err != nil || done == nil {
			return portError(err)
		}
		commit = done.Commit
	}
	pkg, version := packageName(s.App), model.GitLabPackageVersion(tag)
	facts, held, err := shipFacts(ctx, c, release, pkg, version, commit, tag)
	if err != nil {
		return portError(err)
	}
	ship := model.GitLabShip{Tag: tag, Name: model.ReleaseName(s.DisplayName, s.Version),
		Body: model.ReleaseBody(s.Notes, marker), Marker: marker, Commit: commit, Files: files}
	step := model.NextGitLabStep(ship, facts)
	if step.Action == model.PublishCreateRelease && plan.Image && s.CopyImage != nil {
		// The files are there: the image goes next, when the project takes
		// it, and the release, last, names it (docs/plans/gitlab-deploy.md,
		// Decisions 3 and 5). The project is asked here, once per release.
		info, err := c.Project(ctx)
		if err != nil {
			return portError(err)
		}
		takes, err := takesImage(ctx, c, info, s.Image)
		if err != nil {
			return portError(err)
		}
		shipment.TakesImage = takes
		if _, problem := model.ReleaseShipment(shipment); problem != "" {
			return &ports.Error{Kind: ports.ErrArtifactMissing, Cause: errors.New(problem)}
		}
		if takes {
			image, copied, err := d.shipImage(ctx, c, info, t, s)
			if err != nil || copied {
				return err
			}
			step.Body = model.ReleaseBody(model.GitHubImageNotes(s.Notes, image), marker)
		}
	}
	return portError(d.write(ctx, c, t, s, step, files, held))
}

// takesImage says whether the project takes image, s's image by digest:
// its container registry is on, its registry prefix names a path for the
// images, and the token may read and write it (docs/plans/gitlab-deploy.md,
// Decision 5): the copy's first call is a pull of the source tag, which
// GitLab grants with read_registry, and write_registry grants the push
// alone (#1956). An image the project doesn't take is logged, with why, by
// the Ship that makes the release, which ships without it.
func takesImage(ctx context.Context, c gitprovider.GitLabProject, info gitprovider.GitLabProjectInfo,
	image string) (bool, error) {
	if image == "" {
		return false, nil
	}
	var why string
	if _, _, err := imageHome(info); !info.RegistryEnabled {
		why = "the project's container registry is off"
	} else if err != nil {
		why = fmt.Sprintf("the project names no registry path for its images, only %q", info.RegistryPrefix)
	} else {
		scopes, err := c.TokenScopes(ctx)
		if err != nil {
			return false, err
		}
		var missing []string
		for _, scope := range []string{"read_registry", "write_registry"} {
			if !slices.Contains(scopes, scope) {
				missing = append(missing, scope)
			}
		}
		if len(missing) == 0 {
			return true, nil
		}
		why = fmt.Sprintf("the token lacks the %s scope; it has %s", strings.Join(missing, " and "),
			strings.Join(scopes, ", "))
	}
	logr.FromContextOrDiscard(ctx).Info("No image copied", "project", info.Path, "image", image, "why", why)
	return false, nil
}

// imageHome splits the project's container_registry_image_prefix into the
// registry's host and the path its images go under. A prefix without a path
// is one GitLab shouldn't give; it is the project's mistake, not a retry.
func imageHome(info gitprovider.GitLabProjectInfo) (host, home string, err error) {
	host, home, ok := strings.Cut(info.RegistryPrefix, "/")
	if !ok || host == "" || home == "" {
		return "", "", &gitprovider.ReleaseError{Reason: gitprovider.ReasonGitLabRejected,
			Message: fmt.Sprintf("gitlab: project %s names no registry path for its images, only %q", info.Path,
				info.RegistryPrefix)}
	}
	return host, home, nil
}

// shipImage copies s's image to the project's container registry, tagged its
// version, logged in as the token's user with the token, unless the tag
// already names its digest; image is the copy, host/repository@digest, and
// copied says this call wrote it, so Ship takes no other write. Another
// digest under the tag fails with reason TagTaken naming the image. The
// registry refusing the login refuses the credential, any other failure to
// write the copy is unreachable, and a failure to read the image keeps the
// caller's error.
func (d *Deployer) shipImage(ctx context.Context, c gitprovider.GitLabProject, info gitprovider.GitLabProjectInfo,
	t ports.Target, s ports.Shipment) (image string, copied bool, err error) {
	host, home, err := imageHome(info)
	if err != nil {
		return "", false, err
	}
	user, err := c.User(ctx)
	if err != nil {
		return "", false, portError(err)
	}
	registry := oci.Registry{Host: host, Username: user,
		Password: strings.TrimSpace(string(t.Secret[model.GitLabSecretToken]))}
	_, digest, _ := strings.Cut(s.Image, "@")
	repository := model.ImageRepository(home, packageName(s.App), t.Settings[model.GitLabSettingTagPrefix])
	ref := host + "/" + repository
	have, err := oci.Resolve(ctx, registry, repository, s.Version)
	switch {
	case err != nil:
		return "", false, registryError(ref, err)
	case have == digest:
		return ref + "@" + digest, false, nil
	case have != "":
		return "", false, &gitprovider.ReleaseError{Reason: gitprovider.ReasonTagTaken,
			Message: fmt.Sprintf("gitlab: image %s:%s names %s, not the release's %s; it is left alone", ref,
				s.Version, have, digest)}
	}
	err = s.CopyImage(ctx, ports.Registry{Host: host, Username: user, Password: registry.Password},
		repository, s.Version)
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
// refused login refuses the credential, anything else is unreachable.
func registryError(ref string, err error) error {
	err = fmt.Errorf("image registry %s: %w", ref, err)
	if oci.Refused(err) {
		return &ports.Error{Kind: ports.ErrCredentialRefused, Cause: err}
	}
	return &ports.Error{Kind: ports.ErrUnreachable, Cause: err}
}

// shipFacts reads what NextGitLabStep decides on, given release, the
// release of tag as Ship read it: the package's files, and the tag's commit
// when the release has a source commit to check it against. held maps each
// package file's id to its package, for the delete.
func shipFacts(ctx context.Context, c gitprovider.GitLabProject, release *model.TargetRelease, pkg, version,
	commit, tag string) (facts model.GitLabFacts, held map[int64]int64, err error) {
	facts.Release = release
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
	tag := model.ReleaseTag(t.Settings[model.GitLabSettingTagPrefix], s.Version)
	pkg, version := packageName(s.App), model.GitLabPackageVersion(tag)
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

// sourceTarget is where the target puts a release's source, and the source
// tag of version; ok is false when it publishes none. The settings are the
// github member's, as the controller sets them for both.
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

// shipSource reads the project's head and source tag and takes the next
// write of publishing s's source, as model.NextGitLabSourceStep says. done is
// the source commit and tag once both are there, and nil when it wrote.
func (d *Deployer) shipSource(ctx context.Context, c gitprovider.GitLabProject, t ports.Target, s ports.Shipment,
	tag string) (done *model.PublishedSource, err error) {
	src, sourceTag, _ := sourceTarget(t, s.Version)
	ship := model.SourceShip{Source: src, Files: s.Source, Web: hosts(t).Web,
		Repository: t.Settings[model.GitLabSettingProject], App: s.DisplayName, Owner: s.App, Tag: s.Version,
		ReleaseTag: tag}
	// What fails before any read: a symlink, a file too large.
	if step, _ := model.NextGitLabSourceStep(ship, nil); step.Action == model.PublishFail {
		return nil, &gitprovider.ReleaseError{Reason: step.Reason, Message: step.Message}
	}
	facts, branch, head, err := readSource(ctx, c, src.Path, sourceTag)
	if err != nil {
		return nil, err
	}
	step, done := model.NextGitLabSourceStep(ship, facts)
	if done != nil {
		return done, nil
	}
	log := logr.FromContextOrDiscard(ctx)
	switch step.Action {
	case model.PublishFail:
		return nil, &gitprovider.ReleaseError{Reason: step.Reason, Message: step.Message}
	case model.PublishSourceCommit:
		actions := model.GitLabSourceActions(step.SourceCommit.Files, head)
		commit, err := commitSource(ctx, c, s, branch, facts.Head, step.SourceCommit, actions)
		if err != nil {
			return nil, err
		}
		log.Info("Committed release source", "commit", commit, "path", step.SourceCommit.Path,
			"actions", len(actions))
	case model.PublishSourceTag:
		if err := c.CreateTag(ctx, step.Tag, step.Commit); err != nil {
			return nil, err
		}
		log.Info("Tagged release source", "sourceTag", step.Tag, "commit", step.Commit)
	}
	return nil, nil
}

// readSource reads what NextGitLabSourceStep decides on: the default
// branch's head, the folder path's tree at it and at sourceTag. branch is the
// default branch, head the folder's files at its head, paths from the
// folder's root.
func readSource(ctx context.Context, c gitprovider.GitLabProject, path, sourceTag string) (facts *model.GitHubSource,
	branch string, head []model.SourceFile, err error) {
	info, err := c.Project(ctx)
	if err != nil {
		return nil, "", nil, err
	}
	facts = &model.GitHubSource{}
	if branch = info.DefaultBranch; branch == "" {
		// An empty project: no head to put the source on.
		return facts, "", nil, nil
	}
	if facts.Head, err = c.Branch(ctx, branch); err != nil {
		return nil, "", nil, err
	}
	if facts.Head != "" {
		if head, facts.HeadTree, err = folderFiles(ctx, c, facts.Head, path); err != nil {
			return nil, "", nil, err
		}
		for _, f := range head {
			facts.HeadBlobs = append(facts.HeadBlobs, f.Blob)
		}
	}
	if facts.TagCommit, err = c.Tag(ctx, sourceTag); err != nil || facts.TagCommit == "" {
		return facts, branch, head, err
	}
	if _, facts.TagTree, err = folderFiles(ctx, c, facts.TagCommit, path); err != nil {
		return nil, "", nil, err
	}
	return facts, branch, head, nil
}

// folderFiles are the files under path at ref, paths from the folder's root,
// and their git tree, "" when there is no folder there.
func folderFiles(ctx context.Context, c gitprovider.GitLabProject, ref, path string) ([]model.SourceFile, string, error) {
	query := path
	if path == "." {
		query = ""
	}
	entries, err := c.Tree(ctx, ref, query)
	if err != nil {
		return nil, "", err
	}
	var files []model.SourceFile
	for _, e := range entries {
		if e.Type != "blob" {
			continue
		}
		rel := e.Path
		if path != "." {
			rel = strings.TrimPrefix(e.Path, path+"/")
		}
		files = append(files, model.SourceFile{Path: rel, Mode: model.FileMode(e.Mode), Blob: e.ID})
	}
	if len(files) == 0 {
		return nil, "", nil
	}
	tree, err := model.TreeSHA(files)
	if err != nil {
		return nil, "", &gitprovider.ReleaseError{Reason: gitprovider.ReasonGitLabError,
			Message: fmt.Sprintf("gitlab: the tree of %s at %s: %v", path, ref, err)}
	}
	return files, tree, nil
}

// rooted is the folder path's file at rel, from the repository's root.
func rooted(path, rel string) string {
	if path == "." {
		return rel
	}
	return path + "/" + rel
}

// commitSource makes the one commit of sc's source on branch, whose head
// the actions were decided on: actions on the folder, each create's and
// update's content streamed from s.ReadSource and sent base64, the mode set
// by chmod. Each update, delete and chmod names its file's last commit as of
// head as its last_commit_id, so GitLab refuses it when another commit
// touched that file since: that fails BranchMoved and the next poll decides
// again. GitLab compares last_commit_id with the file's own last commit, not
// with the head (#1950), and the commit names no start_sha: that starts a new
// branch, refused on one that exists (#1979). A failure of ReadSource itself
// is a SourceError, the Git host's side.
func commitSource(ctx context.Context, c gitprovider.GitLabProject, s ports.Shipment, branch, head string,
	sc *model.SourceCommit, actions []model.GitLabSourceAction) (string, error) {
	if len(actions) == 0 {
		return "", fmt.Errorf("the folder %s differs from the source of %s, yet no file does", sc.Path, s.Version)
	}
	want := map[string]int{}
	for i, a := range actions {
		if a.Action == model.GitLabActionCreate || a.Action == model.GitLabActionUpdate {
			want[a.File.Path] = i
		}
	}
	content := make([]string, len(actions))
	if len(want) > 0 {
		if s.ReadSource == nil {
			return "", &SourceError{Err: errors.New("the shipment can't stream its source")}
		}
		readErr := s.ReadSource(ctx, func(path string, _ int64, r io.Reader) error {
			i, ok := want[path]
			if !ok {
				return nil
			}
			delete(want, path)
			b, err := io.ReadAll(r)
			if err != nil {
				return err
			}
			if sha := model.BlobSHA(b); sha != actions[i].File.Blob {
				return fmt.Errorf("%s read back as blob %s, not %s", path, sha, actions[i].File.Blob)
			}
			content[i] = base64.StdEncoding.EncodeToString(b)
			return nil
		})
		switch {
		case readErr != nil:
			return "", &SourceError{Err: readErr}
		case len(want) > 0:
			return "", &SourceError{Err: fmt.Errorf("the source of %s changed while it was published: %d files are gone",
				s.Version, len(want))}
		}
	}
	commit := gitprovider.GitLabCommit{Branch: branch, Message: sc.Message}
	for i, a := range actions {
		action := gitprovider.GitLabCommitAction{Action: a.Action, FilePath: rooted(sc.Path, a.File.Path)}
		if a.Action != model.GitLabActionCreate {
			last, err := c.FileLastCommit(ctx, head, action.FilePath)
			if err != nil {
				return "", err
			}
			action.LastCommitID = last
		}
		switch a.Action {
		case model.GitLabActionCreate, model.GitLabActionUpdate:
			action.Content, action.Encoding = content[i], "base64"
		case model.GitLabActionChmod:
			executable := a.File.Mode == model.ModeExecutable
			action.ExecuteFilemode = &executable
		}
		commit.Actions = append(commit.Actions, action)
	}
	sha, err := c.CreateCommit(ctx, commit)
	if re, ok := errors.AsType[*gitprovider.ReleaseError](err); ok && re.Reason == gitprovider.ReasonGitLabRejected &&
		(strings.Contains(re.Message, "Could not update") || strings.Contains(re.Message, "has changed since")) {
		// As GitHub's path: the next poll reads the branch again and decides.
		return "", &gitprovider.ReleaseError{Reason: model.PublishBranchMoved, Status: re.Status, Message: fmt.Sprintf(
			"gitlab: another commit landed on %s first; the next poll puts the source of %s on it", branch, s.Version)}
	}
	return sha, err
}

// SourceError is a release's source that couldn't be read again: the Git
// host's side, not GitLab's.
type SourceError struct {
	Err error
}

func (e *SourceError) Error() string { return fmt.Sprintf("reading release source: %v", e.Err) }

func (e *SourceError) Unwrap() error { return e.Err }

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
	tag := model.ReleaseTag(facts.TagPrefix, version)
	if version != "" && model.GitHubMarkedRelease(app, tag, releases) == nil {
		files, err := c.PackageFiles(ctx, packageName(app), model.GitLabPackageVersion(tag))
		if err != nil {
			return ports.DeployState{}, portError(err)
		}
		facts.Shipping = len(files) > 0
	}
	run := model.GitLabDeployState(facts)
	state := ports.DeployState{Version: run.Version, Phase: run.Phase, Health: run.Health, URL: run.URL,
		Reason: run.Reason, Message: run.Message}
	if run.Phase == model.DeployPhaseDeployed {
		// The image is reported where the project's registry holds the
		// version's tag; a registry that is off has none, and one that
		// can't say leaves it out, as the release still runs.
		if state.Image, err = d.image(ctx, c, t, app, run.Version); err != nil {
			return ports.DeployState{}, portError(err)
		}
	}
	if src, sourceTag, ok := sourceTarget(t, run.Version); ok && run.Version != "" {
		commit, err := c.Tag(ctx, sourceTag)
		if err != nil {
			return ports.DeployState{}, portError(err)
		}
		if commit != "" {
			state.Source = &model.PublishedSource{Commit: commit, Tag: sourceTag,
				URL: model.GitLabSourceURL(hosts(t).Web, t.Settings[model.GitLabSettingProject], src.Path, sourceTag)}
		}
	}
	return state, nil
}

// image is app's image at version as the project's container registry holds
// it, host/repository@digest, or "" when the registry is off, holds no such
// tag or can't say. An error is GitLab's, reading the project or the user.
func (d *Deployer) image(ctx context.Context, c gitprovider.GitLabProject, t ports.Target, app,
	version string) (string, error) {
	info, err := c.Project(ctx)
	if err != nil {
		return "", err
	}
	host, home, err := imageHome(info)
	if !info.RegistryEnabled || err != nil {
		return "", nil
	}
	user, err := c.User(ctx)
	if err != nil {
		return "", err
	}
	repository := model.ImageRepository(home, packageName(app), t.Settings[model.GitLabSettingTagPrefix])
	registry := oci.Registry{Host: host, Username: user,
		Password: strings.TrimSpace(string(t.Secret[model.GitLabSecretToken]))}
	digest, err := oci.Resolve(ctx, registry, repository, version)
	if err != nil || digest == "" {
		return "", nil
	}
	return host + "/" + repository + "@" + digest, nil
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
	if _, ok := errors.AsType[*SourceError](err); ok {
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
