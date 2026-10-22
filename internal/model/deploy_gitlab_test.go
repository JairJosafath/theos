package model

import (
	"reflect"
	"testing"
)

// TestGitLabHostsOf covers docs/plans/gitlab-deploy.md's Decision 1: unset is
// gitlab.com, a self-hosted instance's API is under its url, a relative URL
// root included.
func TestGitLabHostsOf(t *testing.T) {
	tests := []struct {
		name, url string
		want      GitLabHosts
	}{
		{name: "unset is gitlab.com", url: "",
			want: GitLabHosts{API: "https://gitlab.com/api/v4", Web: "https://gitlab.com"}},
		{name: "a self-hosted instance", url: "https://git.example.com:8443/",
			want: GitLabHosts{API: "https://git.example.com:8443/api/v4", Web: "https://git.example.com:8443"}},
		{name: "an instance under a relative URL root", url: "https://example.com/gitlab",
			want: GitLabHosts{API: "https://example.com/gitlab/api/v4", Web: "https://example.com/gitlab"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GitLabHostsOf(tt.url); got != tt.want {
				t.Errorf("GitLabHostsOf(%q) = %+v, want %+v", tt.url, got, tt.want)
			}
		})
	}
}

// TestNextGitLabStep covers docs/plans/gitlab-deploy.md's Decision 3 (#1803):
// the files go to the package registry first, the release is made last in
// one call, and a release or tag the factory didn't make is TagTaken.
func TestNextGitLabStep(t *testing.T) {
	const app, tag, commit = "ns/orders-cli", "orders-cli-v1.0.0", "abc123"
	marker := ShipMarker(app, tag)
	bin := ReleaseAsset{ID: 7, Name: "app_linux_amd64.tar.gz", Size: 100}
	sums := ReleaseAsset{ID: 8, Name: "checksums.txt", Size: 10}
	ship := GitLabShip{Tag: tag, Name: ReleaseName("orders-cli", "v1.0.0"), Body: ReleaseBody("notes", marker),
		Marker: marker, Commit: commit, Files: []ReleaseAsset{bin, sums}}
	noSource := ship
	noSource.Commit = ""
	held := func(a ReleaseAsset, size int64) ReleaseAsset {
		return ReleaseAsset{ID: a.ID + 100, Name: a.Name, Size: size}
	}
	url := "https://gitlab.com/acme/orders/-/releases/" + tag
	tests := []struct {
		name string
		r    GitLabShip
		have GitLabFacts
		want PublishStep
	}{
		{name: "a fresh project uploads the first file", r: ship,
			want: PublishStep{Action: PublishUploadAsset, Asset: bin}},
		{name: "the next file the package lacks is uploaded", r: ship,
			have: GitLabFacts{Files: []ReleaseAsset{held(bin, 100)}},
			want: PublishStep{Action: PublishUploadAsset, Asset: sums}},
		{name: "a file at another size is replaced", r: ship,
			have: GitLabFacts{Files: []ReleaseAsset{held(bin, 99)}},
			want: PublishStep{Action: PublishUploadAsset, Asset: bin, Replace: &ReleaseAsset{ID: 107,
				Name: bin.Name, Size: 99}}},
		{name: "all files there creates the release on the source's commit", r: ship,
			have: GitLabFacts{TagCommit: commit, Files: []ReleaseAsset{held(sums, 10), held(bin, 100)}},
			want: PublishStep{Action: PublishCreateRelease, Tag: tag, Name: ship.Name, Body: ship.Body,
				Target: commit}},
		{name: "with no source the release goes on the default branch", r: noSource,
			have: GitLabFacts{Files: []ReleaseAsset{held(bin, 100), held(sums, 10)}},
			want: PublishStep{Action: PublishCreateRelease, Tag: tag, Name: ship.Name, Body: ship.Body}},
		{name: "with no source an existing tag is used as it is", r: noSource,
			have: GitLabFacts{TagCommit: "def456", Files: []ReleaseAsset{held(bin, 100), held(sums, 10)}},
			want: PublishStep{Action: PublishCreateRelease, Tag: tag, Name: ship.Name, Body: ship.Body}},
		{name: "our release is out", r: ship,
			have: GitLabFacts{Release: &TargetRelease{TagName: tag, Target: commit, Body: ship.Body, URL: url}},
			want: PublishStep{Action: PublishDone, URL: url}},
		{name: "a release the factory didn't make is TagTaken", r: ship,
			have: GitLabFacts{Release: &TargetRelease{TagName: tag, Body: "a person's release", URL: url}},
			want: PublishStep{Action: PublishFail, Reason: PublishTagTaken,
				Message: "gitlab: a release " + tag + " the factory didn't make is there; it is left alone"}},
		{name: "a tag on another commit is TagTaken", r: ship,
			have: GitLabFacts{TagCommit: "def456"},
			want: PublishStep{Action: PublishFail, Reason: PublishTagTaken,
				Message: "gitlab: tag " + tag + " names def456, not the release's source abc123; it is left alone"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NextGitLabStep(tt.r, tt.have); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NextGitLabStep() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestGitLabDeployState covers docs/plans/gitlab-deploy.md's Decision 3: the
// wanted version's marked release is Deployed, a ship of it under way is
// Deploying, else the app's newest marked release is what is out.
func TestGitLabDeployState(t *testing.T) {
	const app, prefix = "ns/orders-cli", "cli/"
	release := func(tag, body string) TargetRelease {
		return TargetRelease{TagName: tag, Body: body, URL: "https://gitlab.com/acme/orders/-/releases/" + tag}
	}
	ours := func(v string) TargetRelease {
		return release(prefix+v, "notes\n\n"+ShipMarker(app, prefix+v)+"\n")
	}
	v100, v110 := ours("v1.0.0"), ours("v1.1.0")
	theirs := release("cli/v1.3.0", ShipMarker("ns/other", "cli/v1.3.0"))
	foreign := release("cli/v1.4.0", "a release a person made")
	deployed := func(v string) DeployRun {
		return DeployRun{Version: v, Phase: DeployPhaseDeployed, Health: DeployHealthHealthy,
			URL: "https://gitlab.com/acme/orders/-/releases/cli/" + v}
	}
	tests := []struct {
		name string
		f    GitLabDeployFacts
		want DeployRun
	}{
		{name: "no release is the zero state", f: GitLabDeployFacts{Version: "v1.0.0"}},
		{name: "the wanted version's release is Deployed",
			f:    GitLabDeployFacts{Version: "v1.0.0", Releases: []TargetRelease{v110, v100}},
			want: deployed("v1.0.0")},
		{name: "a ship of the wanted version under way is Deploying",
			f: GitLabDeployFacts{Version: "v1.2.0", Shipping: true, Releases: []TargetRelease{v110, v100}},
			want: DeployRun{Version: "v1.2.0", Phase: DeployPhaseDeploying,
				Health: DeployHealthProgressing}},
		{name: "with the wanted version not begun the newest marked release is out",
			f:    GitLabDeployFacts{Version: "v1.2.0", Releases: []TargetRelease{foreign, theirs, v110, v100}},
			want: deployed("v1.1.0")},
		{name: "no version wanted reports the newest marked release",
			f:    GitLabDeployFacts{Shipping: true, Releases: []TargetRelease{foreign, v110, v100}},
			want: deployed("v1.1.0")},
		{name: "another app's and a person's releases are not ours",
			f: GitLabDeployFacts{Version: "v1.3.0", Releases: []TargetRelease{foreign, theirs}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.f.App, tt.f.TagPrefix = app, prefix
			if got := GitLabDeployState(tt.f); got != tt.want {
				t.Errorf("GitLabDeployState() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
