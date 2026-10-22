package model

import (
	"reflect"
	"testing"
)

// TestGitHubDeployState covers docs/plans/github-deploy-state.md's Decision 2
// (#1445, #1485): the wanted version's marked release decides, and Latest
// moves back on a rollback only when another of the app's releases holds it.
func TestGitHubDeployState(t *testing.T) {
	const app = "ns/orders-cli"
	release := func(id int64, tag string, draft bool, body string) TargetRelease {
		return TargetRelease{ID: id, TagName: tag, Draft: draft, Body: body,
			URL: "https://github.com/acme/orders/releases/tag/" + tag}
	}
	ours := func(id int64, tag string, draft bool) TargetRelease {
		return release(id, tag, draft, "notes\n\n"+ShipMarker(app, tag)+"\n")
	}
	v100, v110 := ours(1, "cli/v1.0.0", false), ours(2, "cli/v1.1.0", false)
	v120draft := ours(3, "cli/v1.2.0", true)
	theirs := release(4, "cli/v1.3.0", false, ShipMarker("ns/other", "cli/v1.3.0"))
	foreign := release(5, "v9.0.0", false, "a release a person made")
	deploying := func(v string) DeployRun {
		return DeployRun{Version: v, Phase: DeployPhaseDeploying, Health: DeployHealthProgressing}
	}
	deployed := func(v string) DeployRun {
		return DeployRun{Version: v, Phase: DeployPhaseDeployed, Health: DeployHealthHealthy,
			URL: "https://github.com/acme/orders/releases/tag/cli/" + v}
	}
	tests := []struct {
		name string
		f    GitHubDeployFacts
		// noLatest is a target that doesn't take Latest; bare one without a
		// tag prefix.
		noLatest, bare bool
		want           GitHubDeployReport
	}{
		{name: "the wanted version's draft is Deploying",
			f:    GitHubDeployFacts{Version: "v1.2.0", Releases: []TargetRelease{v100, v110, v120draft}},
			want: GitHubDeployReport{DeployRun: deploying("v1.2.0")}},
		{name: "the wanted version published and Latest is Deployed",
			f:    GitHubDeployFacts{Version: "v1.1.0", Releases: []TargetRelease{v100, v110}, Latest: &v110},
			want: GitHubDeployReport{DeployRun: deployed("v1.1.0")}},
		{name: "a rollback by pin is Deployed on a target that doesn't take Latest (#1445)", noLatest: true,
			f:    GitHubDeployFacts{Version: "v1.0.0", Releases: []TargetRelease{v100, v110}, Latest: &v110},
			want: GitHubDeployReport{DeployRun: deployed("v1.0.0")}},
		{name: "a rollback by pin is Deploying while another of the app's releases is Latest, and Ship makes it Latest (#1445)",
			f:    GitHubDeployFacts{Version: "v1.0.0", Releases: []TargetRelease{v100, v110}, Latest: &v110},
			want: GitHubDeployReport{DeployRun: deploying("v1.0.0"), MakeLatest: 1}},
		{name: "a rollback by pin is Deployed once Latest moved back",
			f:    GitHubDeployFacts{Version: "v1.0.0", Releases: []TargetRelease{v100, v110}, Latest: &v100},
			want: GitHubDeployReport{DeployRun: deployed("v1.0.0")}},
		{name: "Latest held by another app's release is left alone",
			f:    GitHubDeployFacts{Version: "v1.0.0", Releases: []TargetRelease{v100, v110, theirs}, Latest: &theirs},
			want: GitHubDeployReport{DeployRun: deployed("v1.0.0")}},
		{name: "Latest held by a release the factory didn't make is left alone",
			f:    GitHubDeployFacts{Version: "v1.0.0", Releases: []TargetRelease{v100, foreign}, Latest: &foreign},
			want: GitHubDeployReport{DeployRun: deployed("v1.0.0")}},
		{name: "no Latest at all is Deployed",
			f:    GitHubDeployFacts{Version: "v1.0.0", Releases: []TargetRelease{v100}},
			want: GitHubDeployReport{DeployRun: deployed("v1.0.0")}},
		{name: "no marked release of the wanted version: the newest draft is Deploying",
			f:    GitHubDeployFacts{Version: "v2.0.0", Releases: []TargetRelease{v100, v110, v120draft}},
			want: GitHubDeployReport{DeployRun: deploying("v1.2.0")}},
		{name: "no marked release of the wanted version: the newest published one is Deployed",
			f:    GitHubDeployFacts{Version: "v2.0.0", Releases: []TargetRelease{v110, v100, theirs}},
			want: GitHubDeployReport{DeployRun: deployed("v1.1.0")}},
		{name: "a draft older than the newest published release doesn't hold the state",
			f: GitHubDeployFacts{Version: "v2.0.0", Releases: []TargetRelease{ours(1, "cli/v1.0.0", true),
				ours(2, "cli/v1.1.0", false)}},
			want: GitHubDeployReport{DeployRun: deployed("v1.1.0")}},
		{name: "no release of the app is the zero state",
			f: GitHubDeployFacts{Version: "v1.0.0", Releases: []TargetRelease{theirs, foreign}}},
		{name: "a published release of the tag without the marker isn't the app's", bare: true,
			f: GitHubDeployFacts{Version: "v9.0.0", Releases: []TargetRelease{foreign}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.f.App, tt.f.TakesLatest = app, !tt.noLatest
			if !tt.bare {
				tt.f.TagPrefix = "cli/"
			}
			if got := GitHubDeployState(tt.f); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GitHubDeployState() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestGitHubReadsLatest(t *testing.T) {
	const app = "ns/orders-cli"
	published := TargetRelease{ID: 1, TagName: "v1.0.0", Body: ShipMarker(app, "v1.0.0")}
	draft := TargetRelease{ID: 2, TagName: "v1.1.0", Draft: true, Body: ShipMarker(app, "v1.1.0")}
	tests := []struct {
		name string
		f    GitHubDeployFacts
		want bool
	}{
		{name: "a published wanted release on a target that takes Latest",
			f: GitHubDeployFacts{App: app, Version: "v1.0.0", TakesLatest: true, Releases: []TargetRelease{published}}, want: true},
		{name: "not on a target that doesn't take Latest",
			f: GitHubDeployFacts{App: app, Version: "v1.0.0", Releases: []TargetRelease{published}}},
		{name: "not for a draft",
			f: GitHubDeployFacts{App: app, Version: "v1.1.0", TakesLatest: true, Releases: []TargetRelease{published, draft}}},
		{name: "not with no marked release of the wanted version",
			f: GitHubDeployFacts{App: app, Version: "v2.0.0", TakesLatest: true, Releases: []TargetRelease{published}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GitHubReadsLatest(tt.f); got != tt.want {
				t.Errorf("GitHubReadsLatest() = %v, want %v", got, tt.want)
			}
		})
	}
}
