package model

import (
	"fmt"
	"slices"
	"strings"
)

// GitLabSecretToken is the key of a gitlab target's Secret holding its
// credential: any GitLab access token, sent as PRIVATE-TOKEN
// (docs/plans/gitlab-deploy.md, Decision 2).
const GitLabSecretToken = "token" //nolint:gosec // a key name, not a credential

// GitLabWeb is gitlab.com, where a target without url publishes.
const GitLabWeb = "https://gitlab.com"

// GitLabHosts are the places a gitlab target talks to: gitlab.com's, or a
// self-hosted GitLab's when the target sets url.
type GitLabHosts struct {
	// API is the REST API's base, <Web>/api/v4.
	API string
	// Web is where people browse the project.
	Web string
}

// GitLabHostsOf are the hosts of a gitlab target whose url is url: gitlab.com
// when "", else the instance at url, a trailing slash dropped, its API under
// /api/v4 (docs/plans/gitlab-deploy.md, Decision 1). A url with a path is an
// instance under a relative URL root, and its API sits under that path.
func GitLabHostsOf(url string) GitLabHosts {
	url = strings.TrimRight(strings.TrimSpace(url), "/")
	if url == "" {
		url = GitLabWeb
	}
	return GitLabHosts{API: url + "/api/v4", Web: url}
}

// GitLabShip is a release shipped as a GitLab release
// (docs/plans/gitlab-deploy.md, Decision 3).
type GitLabShip struct {
	// Tag, Name and Body are the release's tag, name and description, the
	// description ending in Marker (ReleaseBody).
	Tag, Name, Body string
	// Marker is the ShipMarker in Body that makes a release of Tag the
	// factory's own.
	Marker string
	// Commit is the commit the tag goes on, the source's once step 3 ships
	// one; "" means no source: the release's ref is the default branch and
	// a tag already there is used as it is.
	Commit string
	// Files are the release's files, which go to the project's generic
	// package registry before the release links them.
	Files []ReleaseAsset
}

// GitLabFacts are what a GitLab project holds of a release being shipped.
type GitLabFacts struct {
	// Release is the release of the tag, nil when there is none: Body its
	// description, URL its web page, Target its commit.
	Release *TargetRelease
	// TagCommit is the commit the tag names, "" when there is no tag.
	TagCommit string
	// Files are the generic package's files for the release's name and
	// version: ID the file's id, Name and Size.
	Files []ReleaseAsset
}

// NextGitLabStep is the next write of shipping r to a GitLab project that
// holds have. GitLab has no drafts, so the release is made last, in one
// call, and a release of the tag means it is out (docs/plans/gitlab-deploy.md,
// Decision 3): ours, marked, is PublishDone at its URL; one without the
// marker, or the tag at another commit than r's, is TagTaken and left alone.
// Otherwise the first of r's files the package lacks, or holds at another
// size, is uploaded (the other deleted first), and with them all there the
// release is created on r's commit.
func NextGitLabStep(r GitLabShip, have GitLabFacts) PublishStep {
	switch {
	case have.Release != nil && strings.Contains(have.Release.Body, r.Marker):
		return PublishStep{Action: PublishDone, URL: have.Release.URL}
	case have.Release != nil:
		return gitLabTagTaken("gitlab: a release %s the factory didn't make is there; it is left alone", r.Tag)
	case have.TagCommit != "" && r.Commit != "" && have.TagCommit != r.Commit:
		return gitLabTagTaken("gitlab: tag %s names %s, not the release's source %s; it is left alone",
			r.Tag, have.TagCommit, r.Commit)
	}
	for _, f := range r.Files {
		var held *ReleaseAsset
		for i := range have.Files {
			if have.Files[i].Name == f.Name {
				held = &have.Files[i]
				break
			}
		}
		switch {
		case held == nil:
			return PublishStep{Action: PublishUploadAsset, Asset: f}
		case held.Size != f.Size:
			return PublishStep{Action: PublishUploadAsset, Asset: f, Replace: held}
		}
	}
	return PublishStep{Action: PublishCreateRelease, Tag: r.Tag, Name: r.Name, Body: r.Body, Target: r.Commit}
}

// gitLabTagTaken fails a release whose tag the factory doesn't hold.
func gitLabTagTaken(format string, args ...any) PublishStep {
	return PublishStep{Action: PublishFail, Reason: PublishTagTaken, Message: fmt.Sprintf(format, args...)}
}

// GitLabDeployFacts are what the gitlab member read of a target's project for
// one entry of a Container's spec.deploy (docs/plans/gitlab-deploy.md,
// Decision 3).
type GitLabDeployFacts struct {
	// App is the Container's namespace/name, whose markers name it; Version
	// is the version the entry wants, "" for the newest release.
	App, Version string
	// TagPrefix is the target's, before every tag of the project.
	TagPrefix string
	// Releases are the project's releases, newest first, as GitLab lists them.
	Releases []TargetRelease
	// Shipping says a file of the wanted version is already in the package
	// registry: a ship of it began.
	Shipping bool
}

// GitLabDeployState is what the gitlab member reports for f. A GitLab
// release is made last, so the wanted version's marked release is Deployed
// and Healthy at its page; without it, a ship of that version under way
// (Shipping) is Deploying; else the newest of the app's marked releases is
// Deployed, so the status shows what is out while the new one ships. None at
// all is the zero state. GitLab has no Latest to hold, so a rollback is
// Deployed on the old release as soon as it is there.
func GitLabDeployState(f GitLabDeployFacts) DeployRun {
	deployed := func(r *TargetRelease) DeployRun {
		return DeployRun{Version: strings.TrimPrefix(r.TagName, f.TagPrefix), Phase: DeployPhaseDeployed,
			Health: DeployHealthHealthy, URL: r.URL}
	}
	if f.Version != "" {
		if wanted := GitHubMarkedRelease(f.App, ReleaseTag(f.TagPrefix, f.Version), f.Releases); wanted != nil {
			return deployed(wanted)
		}
		if f.Shipping {
			return DeployRun{Version: f.Version, Phase: DeployPhaseDeploying, Health: DeployHealthProgressing}
		}
	}
	prefix := ShipMarkerPrefix(f.App)
	for i := range f.Releases {
		if strings.Contains(f.Releases[i].Body, prefix) {
			return deployed(&f.Releases[i])
		}
	}
	return DeployRun{}
}

// GitLabSourceURL is the source folder at its tag on GitLab: web the
// instance (gitlab.com when ""), project its path.
func GitLabSourceURL(web, project, path, tag string) string {
	if web == "" {
		web = GitLabWeb
	}
	url := strings.TrimRight(web, "/") + "/" + project + "/-/tree/" + tag
	if path != "." {
		url += "/" + path
	}
	return url
}

// The actions of a GitLab source commit, as the Commits API names them.
const (
	GitLabActionCreate = "create"
	GitLabActionUpdate = "update"
	GitLabActionDelete = "delete"
)

// GitLabSourceAction is one change of a source commit on GitLab: Action on
// File's path, File the file wanted there (its path alone for a delete).
type GitLabSourceAction struct {
	Action string
	File   SourceFile
}

// GitLabSourceActions are the actions of the one commit that turns the
// folder holding have into want, both with paths from the folder's root,
// sorted by path: create for a file not there, update for one whose blob or
// mode differs, delete for one want lacks. Nil when nothing differs.
func GitLabSourceActions(want, have []SourceFile) []GitLabSourceAction {
	held := make(map[string]SourceFile, len(have))
	for _, f := range have {
		held[f.Path] = f
	}
	var out []GitLabSourceAction
	for _, f := range want {
		h, ok := held[f.Path]
		delete(held, f.Path)
		switch {
		case !ok:
			out = append(out, GitLabSourceAction{Action: GitLabActionCreate, File: f})
		case h.Blob != f.Blob || h.Mode != f.Mode:
			out = append(out, GitLabSourceAction{Action: GitLabActionUpdate, File: f})
		}
	}
	for path := range held {
		out = append(out, GitLabSourceAction{Action: GitLabActionDelete, File: SourceFile{Path: path}})
	}
	slices.SortFunc(out, func(a, b GitLabSourceAction) int { return strings.Compare(a.File.Path, b.File.Path) })
	return out
}

// NextGitLabSourceStep is NextSourceStep for a GitLab project, have what it
// holds at the default branch's head and the source tag (nil before it is
// read). A source with a symlink fails SourceUnsupported before any read,
// since the Commits API can't carry one; done's URL is the folder on
// GitLab, and no step warns about go install, which GoInstallProblem judges
// for github.com.
func NextGitLabSourceStep(s SourceShip, have *GitHubSource) (step PublishStep, done *PublishedSource) {
	for _, f := range s.Files.Files {
		if f.Mode == ModeSymlink {
			return fail(PublishSourceUnsupported, "%s is a symlink, which GitLab's Commits API can't carry; "+
				"the source of %s isn't published", f.Path, s.Tag), nil
		}
	}
	step, done = NextSourceStep(s, have)
	step.Warning = ""
	if done != nil {
		done.URL = GitLabSourceURL(s.Web, s.Repository, s.Source.Path, done.Tag)
	}
	return step, done
}
