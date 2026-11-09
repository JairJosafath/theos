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

package fake_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
	"github.com/JairJosafath/theos/internal/ports/fake"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// newOwner returns a view of g for a fresh owner.
func newOwner(g *fake.Git, owners *int) *fake.Git {
	*owners++
	return g.For(ports.Config{URL: "http://host.test", Owner: fmt.Sprintf("contract-%d", *owners),
		Credentials: ports.Credentials{Username: "operator", Token: "operator-token"}})
}

func TestIssuesContract(t *testing.T) {
	g, owners := fake.New(), 0
	contract.Issues(t, contract.IssuesEnv{
		New: func(*testing.T) (ports.GitHost, ports.Issues) {
			h := newOwner(g, &owners)
			return h, h
		},
		Assignee: "operator",
	})
}

func TestCIContract(t *testing.T) {
	g, owners := fake.New(), 0
	contract.CI(t, contract.CIEnv{
		New: func(*testing.T) (ports.GitHost, ports.CI) {
			h := newOwner(g, &owners)
			return h, h
		},
		NoLog: func(_ *testing.T, _ ports.GitHost, repo ports.Repository) string {
			checkURL := repo.WebURL + "/actions/runs/1/jobs/1"
			g.SetNoCheckLog(checkURL)
			return checkURL
		},
		Queue: func(_ *testing.T, _ ports.GitHost, repo ports.Repository, label string) {
			g.SetWaitingJobs(repo.Owner, ports.WaitingJob{ID: 1, RunID: 1, Name: "check", Labels: []string{label},
				URL: repo.WebURL + "/actions/runs/1/jobs/1"})
		},
	})
}

func TestAgentAccessContract(t *testing.T) {
	g, owners := fake.New(), 0
	contract.AgentAccess(t, contract.AgentAccessEnv{
		New: func(*testing.T) (ports.GitHost, ports.AgentAccess) {
			h := newOwner(g, &owners)
			return h, h
		},
		Push: func(_ *testing.T, as ports.Credentials, repo ports.Repository, base, branch string,
			files scaffold.Tree) error {
			_, err := g.PushAs(context.Background(), as, repo, base, branch, files)
			return err
		},
	})
}

func TestRegistryContract(t *testing.T) {
	g, owners := fake.New(), 0
	contract.Registry(t, contract.RegistryEnv{
		New: func(*testing.T) (ports.GitHost, ports.Registry) {
			h := newOwner(g, &owners)
			return h, h
		},
		// The fake keeps a go.mod apart from the files pushed: the
		// controllers' tests set it with SetGoMod.
		GoMod: func(_ *testing.T, repo ports.Repository, path string) {
			g.SetGoMod(repo.Owner, repo.Name, path)
		},
	})
}
