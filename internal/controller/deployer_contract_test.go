/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"fmt"
	"strings"
	"testing"

	githubdeploy "github.com/JairJosafath/c4factory/internal/adapters/github"
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
