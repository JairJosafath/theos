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
	otherTarget := ours(6, "lib/v1.0.0", false)
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
		{name: "Latest held by the app's release under another target's prefix is left alone (#1445)",
			f:    GitHubDeployFacts{Version: "v1.0.0", Releases: []TargetRelease{v100, v110, otherTarget}, Latest: &otherTarget},
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

func TestGitHubImage(t *testing.T) {
	tests := []struct {
		name, repository, container, tagPrefix, want string
	}{
		{"a prefix gives each Container its own image", "acme/shop", "orders-api", "orders-api-",
			"acme/shop/orders-api"},
		{"an empty prefix gives the repository to one Container", "acme/shop", "orders-api", "", "acme/shop"},
		{"lower-cased, as GHCR wants it", "Acme/Shop", "orders-api", "v", "acme/shop/orders-api"},
		{"lower-cased without a prefix too", "Acme/Shop", "orders-api", "", "acme/shop"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GitHubImage(tt.repository, tt.container, tt.tagPrefix); got != tt.want {
				t.Errorf("GitHubImage(%q, %q, %q) = %q, want %q", tt.repository, tt.container, tt.tagPrefix, got,
					tt.want)
			}
		})
	}
}

func TestGitHubImageNotes(t *testing.T) {
	const image = "ghcr.io/acme/shop/orders-api@sha256:abc"
	tests := []struct {
		name, notes, image, want string
	}{
		{"no image leaves the notes", "Fixes the cart.\n", "", "Fixes the cart.\n"},
		{"the image follows the notes", "Fixes the cart.\n\n", image,
			"Fixes the cart.\n\nImage: `" + image + "`\n"},
		{"no notes is the image alone", " ", image, "Image: `" + image + "`\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GitHubImageNotes(tt.notes, tt.image); got != tt.want {
				t.Errorf("GitHubImageNotes(%q, %q) = %q, want %q", tt.notes, tt.image, got, tt.want)
			}
		})
	}
}

// TestGitHubShipment covers docs/plans/done/github-deploy.md's Decisions 1 and 2
// (#1599): every type ships to a github target, its source when the target
// publishes it, its files without the image archive, its image only where
// its Deployment builds one, and a release with none of the three is
// ArtifactMissing.
func TestGitHubShipment(t *testing.T) {
	types := []string{TypeStaticSite, TypeService, TypeJob, TypeWorker, TypeMobileApp, TypeCLI, TypeExtension,
		TypeOperator, TypeFunction}
	deployments := []string{"", DeploymentK8sManifests, DeploymentGiteaRelease}
	// files are what each type's release carries: cli, extension and
	// operator releases have files, an operator's its image archive too.
	files := func(typ string) []string {
		switch typ {
		case TypeCLI, TypeExtension:
			return []string{"app_linux_amd64.tar.gz", "checksums.txt"}
		case TypeOperator:
			return []string{"install.yaml", "operator" + ImageArchiveSuffix}
		}
		return nil
	}
	for _, typ := range types {
		for _, deployment := range deployments {
			for _, source := range []bool{false, true} {
				for _, token := range []bool{false, true} {
					f := GitHubShipmentFacts{Type: typ, Deployment: deployment, Files: files(typ), Source: source,
						Image: BuildsImage(deployment, "", typ), PackagesToken: token}
					plan, problem := GitHubShipment(f)
					name := typ + "/" + deployment
					if plan.Source != source {
						t.Errorf("%s source=%v: plan.Source = %v", name, source, plan.Source)
					}
					for _, file := range plan.Files {
						if IsImageArchive(file) {
							t.Errorf("%s: ships the image archive %s as a release file", name, file)
						}
					}
					if want := token && BuildsImage(deployment, "", typ); plan.Image != want {
						t.Errorf("%s token=%v: plan.Image = %v, want %v", name, token, plan.Image, want)
					}
					wantProblem := !source && len(plan.Files) == 0 && !plan.Image
					if (problem != "") != wantProblem {
						t.Errorf("%s source=%v token=%v: problem = %q, want one: %v", name, source, token, problem,
							wantProblem)
					}
				}
			}
		}
	}
	tests := []struct {
		name        string
		f           GitHubShipmentFacts
		want        GitHubShipmentPlan
		wantProblem string
	}{
		{name: "a service with no files and no source has nothing to publish",
			f:           GitHubShipmentFacts{Type: TypeService},
			wantProblem: GitHubNothingToPublish},
		{name: "a service with no files ships its source",
			f:    GitHubShipmentFacts{Type: TypeService, Source: true},
			want: GitHubShipmentPlan{Source: true}},
		{name: "a k8s-manifests service ships its published image with a packages token",
			f: GitHubShipmentFacts{Type: TypeService, Deployment: DeploymentK8sManifests, Image: true,
				PackagesToken: true},
			want: GitHubShipmentPlan{Image: true}},
		{name: "no packages token ships no image",
			f:           GitHubShipmentFacts{Type: TypeService, Deployment: DeploymentK8sManifests, Image: true},
			wantProblem: GitHubNothingToPublish},
		{name: "an image not yet published ships none",
			f: GitHubShipmentFacts{Type: TypeService, Deployment: DeploymentK8sManifests,
				PackagesToken: true},
			wantProblem: GitHubNothingToPublish},
		{name: "a gitea-release service builds no image",
			f: GitHubShipmentFacts{Type: TypeService, Deployment: DeploymentGiteaRelease, Image: true,
				PackagesToken: true},
			wantProblem: GitHubNothingToPublish},
		{name: "an operator's image archive is left out of its files",
			f: GitHubShipmentFacts{Type: TypeOperator, Deployment: DeploymentGiteaRelease,
				Files: []string{"install.yaml", "operator" + ImageArchiveSuffix}},
			want: GitHubShipmentPlan{Files: []string{"install.yaml"}}},
		{name: "an image archive alone is nothing to publish",
			f: GitHubShipmentFacts{Type: TypeOperator, Deployment: DeploymentGiteaRelease,
				Files: []string{"operator" + ImageArchiveSuffix}},
			wantProblem: GitHubNothingToPublish},
		{name: "a cli ships its files and source",
			f:    GitHubShipmentFacts{Type: TypeCLI, Files: []string{"a.tar.gz"}, Source: true},
			want: GitHubShipmentPlan{Source: true, Files: []string{"a.tar.gz"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, problem := GitHubShipment(tt.f)
			if problem != tt.wantProblem {
				t.Errorf("problem = %q, want %q", problem, tt.wantProblem)
			}
			if tt.wantProblem == "" && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GitHubShipment() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
