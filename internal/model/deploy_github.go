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
// releases than wanted, so a target that takes Latest moves it back to
// wanted. Latest held by a release of another app, or by one the factory
// didn't make, is left alone.
func GitHubLatestHeld(app string, wanted, latest *TargetRelease) bool {
	return wanted != nil && latest != nil && latest.ID != wanted.ID &&
		strings.Contains(latest.Body, ShipMarkerPrefix(app))
}

// GitHubReadsLatest says the member reads GitHub's Latest for f: the target
// takes Latest and the wanted version's marked release is published, so
// where Latest is decides between Deployed and Deploying.
func GitHubReadsLatest(f GitHubDeployFacts) bool {
	if !f.TakesLatest {
		return false
	}
	wanted := GitHubMarkedRelease(f.App, GitHubTag(f.TagPrefix, f.Version), f.Releases)
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
	if wanted := GitHubMarkedRelease(f.App, GitHubTag(f.TagPrefix, f.Version), f.Releases); wanted != nil {
		switch {
		case wanted.Draft:
			return deploying(wanted)
		case f.TakesLatest && GitHubLatestHeld(f.App, wanted, f.Latest):
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
