/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	githubdeploy "github.com/JairJosafath/c4factory/internal/adapters/github"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/ports"
	"github.com/JairJosafath/c4factory/internal/ports/contract"
)

// TestGitHubDeployerContract runs the Deployer's contract on the github
// member's adapter, against the fake GitHub the publish tests use.
func TestGitHubDeployerContract(t *testing.T) {
	gh := newFakeGitHub()
	t.Cleanup(gh.server.Close)
	repos := 0
	target := func(token string) ports.Target {
		repos++
		repository := fmt.Sprintf("contract/repo-%d", repos)
		gh.addRepository(repository, "t0ken")
		return ports.Target{Name: "contract/" + repository, Member: model.DeployMemberGitHub,
			Settings: map[string]string{model.GitHubSettingRepository: repository},
			Secret:   map[string][]byte{"token": []byte(token)}}
	}
	contract.Deployer(t, contract.DeployerEnv{
		Deployer: githubdeploy.New(gh),
		Target:   func(*testing.T) ports.Target { return target("t0ken") },
		Refused:  func(*testing.T) ports.Target { return target("wrong") },
		Deploys: func(_ *testing.T, tt ports.Target, app string) int {
			n := 0
			for _, r := range gh.releases(tt.Settings[model.GitHubSettingRepository]) {
				if strings.Contains(r.Body, model.ShipMarkerPrefix(app)) {
					n++
				}
			}
			return n
		},
	})
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
	st, err := d.State(ctx, target("acme/orders-cli"), s.App)
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
	st, err = d.State(ctx, target("acme/orders-cli"), s.App)
	if err != nil || st.Version != "v1.0.0" || st.Phase != ports.PhaseDeployed || st.Health != ports.HealthHealthy ||
		st.URL == "" {
		t.Errorf("State() = %+v, %v; want v1.0.0 Deployed and Healthy at its page", st, err)
	}

	// A release of the tag the factory didn't make is left alone.
	other := s
	other.App = "ns/other"
	if err := d.Ship(ctx, target("acme/orders-cli"), other); gitprovider.ReleaseReason(err) != gitprovider.ReasonTagTaken {
		t.Errorf("Ship() over another app's tag = %v, want TagTaken", err)
	}
}
