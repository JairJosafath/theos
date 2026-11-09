/*
Copyright 2026 The theos Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package model

import "strings"

// GitHubDeployFacts are what the github member read of a target's repository
// for one entry of a Container's spec.deploy (docs/plans/github-deploy-state.md,
// Decisions 1 and 2).
type GitHubDeployFacts struct {
	// App is the Container's namespace/name, whose markers name it; Version
	// is the version the entry wants, its pin, else the newest release.
	App, Version string
	// TagPrefix is the target's, before every tag of the repository.
	TagPrefix string
	// TakesLatest says the target's releases become the repository's Latest.
	TakesLatest bool
	// Releases are every release of the repository, drafts included.
	Releases []TargetRelease
	// Latest is GitHub's Latest release, nil when there is none or the member
	// didn't read it: it reads it only when GitHubReadsLatest says.
	Latest *TargetRelease
}

// GitHubDeployReport is what the github member reports for an entry, and
// the release Ship makes Latest.
type GitHubDeployReport struct {
	DeployRun
	// MakeLatest is the id of the wanted release Ship makes the repository's
	// Latest, 0 when there is nothing to make Latest.
	MakeLatest int64
}

// GitHubMarkedRelease is the release of tag whose body holds app's marker
// for it, a published one before a draft; nil when there is none.
func GitHubMarkedRelease(app, tag string, releases []TargetRelease) *TargetRelease {
	marker := ShipMarker(app, tag)
	var draft *TargetRelease
	for i := range releases {
		r := &releases[i]
		if !strings.Contains(r.Body, marker) {
			continue
		}
		if !r.Draft {
			return r
		}
		if draft == nil {
			draft = r
		}
	}
	return draft
}

// GitHubLatestHeld says GitHub's Latest, latest, is another of app's marked
// releases than wanted under the target's tagPrefix, so a target that takes
// Latest moves it back to wanted. Latest held by a release of another app,
// by the app's release under another target's prefix, or by one the factory
// didn't make, is left alone.
func GitHubLatestHeld(app, tagPrefix string, wanted, latest *TargetRelease) bool {
	return wanted != nil && latest != nil && latest.ID != wanted.ID &&
		strings.Contains(latest.Body, ShipMarkerPrefix(app)) && strings.HasPrefix(latest.TagName, tagPrefix)
}

// GitHubReadsLatest says the member reads GitHub's Latest for f: the target
// takes Latest and the wanted version's marked release is published, so
// where Latest is decides between Deployed and Deploying.
func GitHubReadsLatest(f GitHubDeployFacts) bool {
	if !f.TakesLatest {
		return false
	}
	wanted := GitHubMarkedRelease(f.App, ReleaseTag(f.TagPrefix, f.Version), f.Releases)
	return wanted != nil && !wanted.Draft
}

// GitHubDeployState is what the github member reports for f. GitHub's
// releases stand side by side, so it reports the wanted version's marked
// release: a draft is Deploying; a published one Deployed and Healthy at its
// page, unless the target takes Latest and another of the app's marked
// releases holds it, which is Deploying with MakeLatest set, as Ship makes
// the wanted release Latest. With no marked release of the wanted version it
// reports the app's newest marked release by id, a draft newer than the
// newest published one Deploying, else the published one Deployed, so the
// status shows what is out while the new one ships; none at all is the zero
// state.
func GitHubDeployState(f GitHubDeployFacts) GitHubDeployReport {
	version := func(r *TargetRelease) string { return strings.TrimPrefix(r.TagName, f.TagPrefix) }
	deploying := func(r *TargetRelease) GitHubDeployReport {
		return GitHubDeployReport{DeployRun: DeployRun{Version: version(r), Phase: DeployPhaseDeploying,
			Health: DeployHealthProgressing}}
	}
	deployed := func(r *TargetRelease) GitHubDeployReport {
		return GitHubDeployReport{DeployRun: DeployRun{Version: version(r), Phase: DeployPhaseDeployed,
			Health: DeployHealthHealthy, URL: r.URL}}
	}
	if wanted := GitHubMarkedRelease(f.App, ReleaseTag(f.TagPrefix, f.Version), f.Releases); wanted != nil {
		switch {
		case wanted.Draft:
			return deploying(wanted)
		case f.TakesLatest && GitHubLatestHeld(f.App, f.TagPrefix, wanted, f.Latest):
			rep := deploying(wanted)
			rep.MakeLatest = wanted.ID
			return rep
		}
		return deployed(wanted)
	}
	prefix := ShipMarkerPrefix(f.App)
	var published, draft *TargetRelease
	for i := range f.Releases {
		r := &f.Releases[i]
		if !strings.Contains(r.Body, prefix) {
			continue
		}
		// GitHub's ids grow with time, so the highest is the newest.
		switch {
		case r.Draft && (draft == nil || r.ID > draft.ID):
			draft = r
		case !r.Draft && (published == nil || r.ID > published.ID):
			published = r
		}
	}
	switch {
	case draft != nil && (published == nil || draft.ID > published.ID):
		return deploying(draft)
	case published != nil:
		return deployed(published)
	}
	return GitHubDeployReport{}
}

// The keys of a github target's Secret, or of the Crew's
// publishReleases.github.tokenSecretRef, holding its credential: a token, or a
// GitHub App's id, the id of its installation on the repository and its
// private key in PEM (docs/plans/done/github-deploy.md, decision 6).
const (
	GitHubSecretToken          = "token"
	GitHubSecretAppID          = "appId"
	GitHubSecretInstallationID = "installationId"
	GitHubSecretPrivateKey     = "privateKey"
)

// GitHubAppKeys are a GitHub App's keys, in a message's order.
var GitHubAppKeys = []string{GitHubSecretAppID, GitHubSecretInstallationID, GitHubSecretPrivateKey}

// GitHubApp is a GitHub App's installation, as a Secret names it.
type GitHubApp struct {
	AppID, InstallationID string
	PrivateKey            []byte
}

// GitHubCredential reads the credential of a GitHub Secret's data: its
// token when set, which wins, else the App when its three keys are set; ok
// is false when it holds neither whole.
func GitHubCredential(data map[string][]byte) (token string, app *GitHubApp, ok bool) {
	if token = strings.TrimSpace(string(data[GitHubSecretToken])); token != "" {
		return token, nil, true
	}
	if len(missingKeys(GitHubAppKeys, data)) > 0 {
		return "", nil, false
	}
	return "", &GitHubApp{AppID: strings.TrimSpace(string(data[GitHubSecretAppID])),
		InstallationID: strings.TrimSpace(string(data[GitHubSecretInstallationID])),
		PrivateKey:     data[GitHubSecretPrivateKey]}, true
}

// GitHubHosts are the places a github target talks to: github.com's, or a
// GitHub Enterprise Server's when the target sets url.
type GitHubHosts struct {
	// API and Upload are the REST API's and the release uploads' bases;
	// "" is github.com's, which the client defaults to.
	API, Upload string
	// Web is where people browse the repository.
	Web string
	// Registry is the container registry, host[:port], images go to.
	Registry string
}

// GitHubWeb is github.com, where a target without url publishes.
const GitHubWeb = "https://github.com"

// GitHubHostsOf are the hosts of a github target whose url is url: github.com
// and GHCR when "", else the GitHub Enterprise Server at https://host, its
// API under /api/v3, its uploads under /api/uploads and its registry at
// containers.<host>, the one the server takes with subdomain isolation on
// (docs/plans/done/github-enterprise.md).
func GitHubHostsOf(url string) GitHubHosts {
	url = strings.TrimRight(url, "/")
	if url == "" {
		return GitHubHosts{Web: GitHubWeb, Registry: GHCRHost}
	}
	return GitHubHosts{API: url + "/api/v3", Upload: url + "/api/uploads", Web: url,
		Registry: "containers." + strings.TrimPrefix(url, "https://")}
}

// ImageRepository is the repository a Container's image goes to at a github
// or gitlab target's registry: home/<container>, lower-cased as registries
// want it, or home alone when the target's tagPrefix is "", which gives the
// repository to one Container. home is a github target's owner/name
// (docs/plans/done/github-deploy.md, Decision 5) or a gitlab project's
// container_registry_image_prefix without its host
// (docs/plans/gitlab-deploy.md, Decision 5).
func ImageRepository(home, container, tagPrefix string) string {
	if tagPrefix == "" {
		return strings.ToLower(home)
	}
	return strings.ToLower(home + "/" + container)
}

// GitHubImageNotes are a release's notes with a line naming its image,
// host/repository@digest, as the github target copied it; notes alone when
// image is "".
func GitHubImageNotes(notes, image string) string {
	if image == "" {
		return notes
	}
	line := "Image: `" + image + "`"
	if strings.TrimSpace(notes) == "" {
		return line + "\n"
	}
	return strings.TrimRight(notes, "\n") + "\n\n" + line + "\n"
}

// NothingToPublish is the ArtifactMissing message of a release a github or
// gitlab target has nothing of to ship.
const NothingToPublish = "nothing to publish: no release files, no source, no image"

// ReleaseShipmentFacts are what a github or gitlab target could ship of a
// release of a Container (docs/plans/done/github-deploy.md, Decisions 1 and 2;
// docs/plans/gitlab-deploy.md, Decision 3).
type ReleaseShipmentFacts struct {
	// Type, Deployment and Framework are the Container's type, Deployment
	// and Framework.
	Type, Deployment, Framework string
	// ScaffoldedFrom is the Container's status.scaffoldedFrom, name@ref per
	// template, which says whether its release builds an image (#1797).
	ScaffoldedFrom []string
	// Files are the names of the release's files on the Git host.
	Files []string
	// Source says the target publishes the source and the release has one.
	Source bool
	// Image says the release's image is published, by digest, for its version.
	Image bool
	// TakesImage says the target takes the image: a github target's Secret
	// holds GitHubSecretPackagesToken; a gitlab target's project registry is
	// on and its token may write to it (docs/plans/gitlab-deploy.md,
	// Decision 5).
	TakesImage bool
}

// ReleaseShipmentPlan is what a github or gitlab target ships of a release,
// besides the release itself, which always goes with the release's notes.
type ReleaseShipmentPlan struct {
	// Source says the release's source goes to the target's folder.
	Source bool
	// Files are the release files the release carries.
	Files []string
	// Image says the release's image goes to the target's container registry.
	Image bool
}

// ReleaseShipment decides what a github or gitlab target ships of a release:
// the source when the target publishes it; the release's files, its image
// archive left out, as the image goes to a registry; and the image when the
// Container's Deployment builds one, it is published and the target takes
// it. With none of the three, problem is NothingToPublish. Both members share
// the rule, so a Container ships the same things to either
// (docs/plans/gitlab-deploy.md, Decision 3).
func ReleaseShipment(f ReleaseShipmentFacts) (plan ReleaseShipmentPlan, problem string) {
	plan.Source = f.Source
	for _, name := range f.Files {
		if !IsImageArchive(name) {
			plan.Files = append(plan.Files, name)
		}
	}
	plan.Image = f.Image && f.TakesImage && BuildsImage(f.Deployment, f.Framework, f.Type, f.ScaffoldedFrom)
	if !plan.Source && len(plan.Files) == 0 && !plan.Image {
		return plan, NothingToPublish
	}
	return plan, ""
}
