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

package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"strconv"
	"strings"
	"testing"

	githubdeploy "github.com/JairJosafath/theos/internal/adapters/github"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
)

// TestGitHubDeployerContract runs the Deployer's contract on the github
// member's adapter, against the fake GitHub the publish tests use: once with
// the release alone, once with its source committed to a folder first, each
// release then of its source tag's commit (#1598), and once with a GitHub
// App's Secret, whose installation token is minted once per target and kept
// (#1601).
func TestGitHubDeployerContract(t *testing.T) {
	for _, tt := range []struct {
		name     string
		settings map[string]string
		app      bool
	}{
		{name: "release", settings: map[string]string{}},
		{name: "release and source", settings: map[string]string{model.GitHubSettingSourcePath: "cli"}},
		{name: "release with a GitHub App", settings: map[string]string{}, app: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gh := newFakeGitHub()
			t.Cleanup(gh.server.Close)
			appKey, appPEM := newGitHubAppKey()
			_, otherPEM := newGitHubAppKey()
			repos := 0
			var installations []string
			target := func(token string) ports.Target {
				repos++
				repository := fmt.Sprintf("contract/repo-%d", repos)
				// The fake's repository holds a first commit, which a source
				// commit builds on.
				gh.addRepository(repository, "t0ken")
				settings := maps.Clone(tt.settings)
				settings[model.GitHubSettingRepository] = repository
				secret := map[string][]byte{"token": []byte(token)}
				if tt.app {
					installation := strconv.Itoa(1000 + repos)
					gh.addApp("42", installation, repository, &appKey.PublicKey)
					key := appPEM
					if token == "t0ken" {
						installations = append(installations, installation)
					} else {
						key = otherPEM
					}
					secret = map[string][]byte{"appId": []byte("42"), "installationId": []byte(installation),
						"privateKey": key}
				}
				return ports.Target{Name: "contract/" + repository, Member: model.DeployMemberGitHub,
					Settings: settings, Secret: secret}
			}
			t.Cleanup(func() {
				for _, installation := range installations {
					if n := gh.appMints(installation); n > 1 {
						t.Errorf("installation %s minted %d tokens, want at most 1, kept for every call", installation, n)
					}
				}
			})
			contract.Deployer(t, contract.DeployerEnv{
				Deployer: githubdeploy.New(gh),
				Target:   func(*testing.T) ports.Target { return target("t0ken") },
				Refused:  func(*testing.T) ports.Target { return target("wrong") },
				Deploys: func(t *testing.T, tg ports.Target, app string) int {
					repository := tg.Settings[model.GitHubSettingRepository]
					n := 0
					for _, r := range gh.releases(repository) {
						if !strings.Contains(r.Body, model.ShipMarkerPrefix(app)) {
							continue
						}
						n++
						if path := tg.Settings[model.GitHubSettingSourcePath]; path != "" {
							tag := model.SourceTag(model.PublishSource{Path: path}, r.TagName)
							if commit := gh.ref(repository, "tags/"+tag); commit == "" || r.Target != commit {
								t.Errorf("release %s is of %q, want its source tag %s's commit %q",
									r.TagName, r.Target, tag, commit)
							}
						}
					}
					return n
				},
			})
		})
	}
}

// The GitHub adapter's own cases: a repository the token can't see refuses
// the credential, and a release half shipped is Deploying.
func TestGitHubDeployer(t *testing.T) {
	gh := newFakeGitHub()
	t.Cleanup(gh.server.Close)
	gh.addRepository("acme/orders-cli", "t0ken")
	d := githubdeploy.New(gh)
	ctx := context.Background()
	target := func(repository string) ports.Target {
		return ports.Target{Name: "ns/prod", Member: model.DeployMemberGitHub,
			Settings: map[string]string{model.GitHubSettingRepository: repository,
				model.GitHubSettingTagPrefix: "cli/"},
			Secret: map[string][]byte{"token": []byte("t0ken\n")}}
	}
	if err := d.Check(ctx, target("acme/missing")); !errors.Is(err, ports.ErrCredentialRefused) {
		t.Errorf("Check() of a repository GitHub doesn't show = %v, want ErrCredentialRefused", err)
	}
	file := ports.File{Name: "orders cli.tar.gz", Size: 3, Open: func(context.Context) (io.ReadCloser, int64, error) {
		return io.NopCloser(strings.NewReader("bin")), 3, nil
	}}
	s := ports.Shipment{App: "ns/orders-cli", DisplayName: "Orders CLI", Version: "v1.0.0", Files: []ports.File{file}}
	if err := d.Ship(ctx, target("acme/orders-cli"), s); err != nil {
		t.Fatalf("Ship() = %v", err)
	}
	st, err := d.State(ctx, target("acme/orders-cli"), s.App, "v1.0.0")
	if err != nil || st.Version != "v1.0.0" || st.Phase != ports.PhaseDeploying {
		t.Errorf("State() of a draft = %+v, %v; want v1.0.0 Deploying", st, err)
	}
	// The upload, then the publish; the third ship finds it out.
	for range 3 {
		if err := d.Ship(ctx, target("acme/orders-cli"), s); err != nil {
			t.Fatalf("Ship() = %v", err)
		}
	}
	rels := gh.releases("acme/orders-cli")
	if len(rels) != 1 || rels[0].Draft || rels[0].TagName != "cli/v1.0.0" || rels[0].Name != "Orders CLI v1.0.0" ||
		len(rels[0].assets) != 1 {
		t.Fatalf("releases = %+v, want cli/v1.0.0 published with its one file", rels)
	}
	st, err = d.State(ctx, target("acme/orders-cli"), s.App, "v1.0.0")
	if err != nil || st.Version != "v1.0.0" || st.Phase != ports.PhaseDeployed || st.Health != ports.HealthHealthy ||
		st.URL == "" {
		t.Errorf("State() = %+v, %v; want v1.0.0 Deployed and Healthy at its page", st, err)
	}

	// A release of the tag the factory didn't make is left alone.
	other := s
	other.App = "ns/other"
	if err := d.Ship(ctx, target("acme/orders-cli"), other); ports.ReleaseReason(err) != ports.ReasonTagTaken {
		t.Errorf("Ship() over another app's tag = %v, want TagTaken", err)
	}
}

// shipUntilDeployed ships s to target until State, asked for s's version,
// says Deployed.
func shipUntilDeployed(t *testing.T, d ports.Deployer, target ports.Target, s ports.Shipment) {
	t.Helper()
	shipUntilDeployedIn(context.Background(), t, d, target, s)
}

// shipUntilDeployedIn is shipUntilDeployed with the context the Ships and
// States run under, for a test that reads what they log.
func shipUntilDeployedIn(ctx context.Context, t *testing.T, d ports.Deployer, target ports.Target,
	s ports.Shipment) {
	t.Helper()
	for range 10 {
		if err := d.Ship(ctx, target, s); err != nil {
			t.Fatalf("Ship(%s) = %v", s.Version, err)
		}
		st, err := d.State(ctx, target, s.App, s.Version)
		if err != nil {
			t.Fatalf("State() = %v", err)
		}
		if st.Version == s.Version && st.Phase == ports.PhaseDeployed {
			return
		}
	}
	t.Fatalf("%s isn't Deployed after 10 Ship calls", s.Version)
}

// publishedReleaseID is the id of repository's published release of tag.
func publishedReleaseID(t *testing.T, gh *fakeGitHub, repository, tag string) int64 {
	t.Helper()
	for _, r := range gh.releases(repository) {
		if r.TagName == tag && !r.Draft {
			return r.ID
		}
	}
	t.Fatalf("no published release %s in %s", tag, repository)
	return 0
}

// TestGitHubDeployerRollback: an entry pinned to an older published release
// reaches Deployed, as State is told the version it wants (#1445). On a
// target that takes Latest, Ship moves Latest back to it first; on one that
// never does, nothing is written.
func TestGitHubDeployerRollback(t *testing.T) {
	gh := newFakeGitHub()
	t.Cleanup(gh.server.Close)
	d := githubdeploy.New(gh)
	ctx := context.Background()
	file := ports.File{Name: "orders-cli.tar.gz", Size: 3, Open: func(context.Context) (io.ReadCloser, int64, error) {
		return io.NopCloser(strings.NewReader("bin")), 3, nil
	}}
	shipment := func(version string) ports.Shipment {
		return ports.Shipment{App: "ns/orders-cli", DisplayName: "Orders CLI", Version: version,
			Files: []ports.File{file}}
	}
	shipped := func(t *testing.T, target ports.Target, version string) {
		t.Helper()
		shipUntilDeployed(t, d, target, shipment(version))
	}
	releaseID := func(t *testing.T, repository, tag string) int64 {
		t.Helper()
		return publishedReleaseID(t, gh, repository, tag)
	}

	t.Run("a pin to an older release on a target that takes Latest moves Latest back (#1445)", func(t *testing.T) {
		gh.addRepository("acme/orders-cli", "t0ken")
		target := ports.Target{Name: "ns/prod", Member: model.DeployMemberGitHub,
			Settings: map[string]string{model.GitHubSettingRepository: "acme/orders-cli",
				model.GitHubSettingTagPrefix: "cli/"},
			Secret: map[string][]byte{"token": []byte("t0ken")}}
		shipped(t, target, "v1.0.0")
		shipped(t, target, "v1.1.0")
		older, newer := releaseID(t, "acme/orders-cli", "cli/v1.0.0"), releaseID(t, "acme/orders-cli", "cli/v1.1.0")
		if got := gh.latestRelease("acme/orders-cli"); got != newer {
			t.Fatalf("Latest = %d after shipping v1.1.0, want %d", got, newer)
		}
		st, err := d.State(ctx, target, "ns/orders-cli", "v1.0.0")
		if err != nil || st.Version != "v1.0.0" || st.Phase != ports.PhaseDeploying {
			t.Errorf("State(v1.0.0) while v1.1.0 is Latest = %+v, %v; want v1.0.0 Deploying", st, err)
		}
		writes := gh.writes("acme/orders-cli")
		if err := d.Ship(ctx, target, shipment("v1.0.0")); err != nil {
			t.Fatalf("Ship(v1.0.0) = %v", err)
		}
		if got := gh.latestRelease("acme/orders-cli"); got != older {
			t.Errorf("Latest = %d after shipping v1.0.0 again, want %d: the pinned release", got, older)
		}
		if n := gh.writes("acme/orders-cli"); n != writes+1 {
			t.Errorf("writes = %d, want %d: the one that makes v1.0.0 Latest", n, writes+1)
		}
		st, err = d.State(ctx, target, "ns/orders-cli", "v1.0.0")
		if err != nil || st.Version != "v1.0.0" || st.Phase != ports.PhaseDeployed || st.Health != ports.HealthHealthy ||
			st.URL != "https://github.com/acme/orders-cli/releases/tag/cli/v1.0.0" {
			t.Errorf("State(v1.0.0) = %+v, %v; want v1.0.0 Deployed and Healthy at its page", st, err)
		}
		// Shipping it again writes nothing more.
		if err := d.Ship(ctx, target, shipment("v1.0.0")); err != nil {
			t.Fatalf("Ship(v1.0.0) again = %v", err)
		}
		if n := gh.writes("acme/orders-cli"); n != writes+1 {
			t.Errorf("writes = %d after shipping the Latest release again, want %d", n, writes+1)
		}
		// Unpinned, the entry wants the newest: v1.1.0 is out, so it is
		// Deployed there, and Ship moves Latest to it.
		st, err = d.State(ctx, target, "ns/orders-cli", "v1.1.0")
		if err != nil || st.Version != "v1.1.0" || st.Phase != ports.PhaseDeploying {
			t.Errorf("State(v1.1.0) while v1.0.0 is Latest = %+v, %v; want v1.1.0 Deploying", st, err)
		}
	})

	t.Run("a pin to an older release on a target that never takes Latest is Deployed at once (#1445)",
		func(t *testing.T) {
			gh.addRepository("acme/orders-lib", "t0ken")
			target := ports.Target{Name: "ns/lib", Member: model.DeployMemberGitHub,
				Settings: map[string]string{model.GitHubSettingRepository: "acme/orders-lib",
					model.GitHubSettingLatest: "false"},
				Secret: map[string][]byte{"token": []byte("t0ken")}}
			shipped(t, target, "v1.0.0")
			shipped(t, target, "v1.1.0")
			if got := gh.latestRelease("acme/orders-lib"); got != 0 {
				t.Fatalf("Latest = %d on a target that never takes it, want none", got)
			}
			st, err := d.State(ctx, target, "ns/orders-cli", "v1.0.0")
			if err != nil || st.Version != "v1.0.0" || st.Phase != ports.PhaseDeployed ||
				st.URL != "https://github.com/acme/orders-lib/releases/tag/v1.0.0" {
				t.Errorf("State(v1.0.0) = %+v, %v; want v1.0.0 Deployed at its page", st, err)
			}
			writes := gh.writes("acme/orders-lib")
			if err := d.Ship(ctx, target, shipment("v1.0.0")); err != nil {
				t.Fatalf("Ship(v1.0.0) = %v", err)
			}
			if n := gh.writes("acme/orders-lib"); n != writes {
				t.Errorf("writes = %d after shipping a release that is out, want %d: nothing to write", n, writes)
			}
			if got := gh.latestRelease("acme/orders-lib"); got != 0 {
				t.Errorf("Latest = %d after the Ship, want none still", got)
			}
		})
}
