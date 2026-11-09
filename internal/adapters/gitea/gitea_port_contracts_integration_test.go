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

package gitea

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/adapters/git"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// The Issues, CI, AgentAccess and Registry contract suites on the seeded
// Gitea, each subtest in an organization of its own, like
// TestGitHostContractIntegration.

// contractEnv is the seeded Gitea's address and the operator's credential.
type contractEnv struct {
	base  string
	creds ports.Credentials
}

// newContractEnv reads the seeded Gitea from the environment, skipping t when
// there is none.
func newContractEnv(t *testing.T) contractEnv {
	t.Helper()
	base := os.Getenv("THEOS_GITEA_URL")
	if base == "" {
		t.Skip("THEOS_GITEA_URL not set")
	}
	return contractEnv{base: base, creds: ports.Credentials{Username: os.Getenv("THEOS_GITEA_USERNAME"),
		Token: os.Getenv("THEOS_GITEA_TOKEN")}}
}

// provider is the provider of a fresh organization, which goes with its
// repositories when t ends.
func (e contractEnv) provider(t *testing.T) ports.Provider {
	t.Helper()
	owner := fmt.Sprintf("contract-%d", time.Now().UnixNano())
	createOrg(t, e.base, owner, e.creds.Token)
	// Cleanups run last first: the repositories go before their organization.
	t.Cleanup(func() {
		var repos []struct {
			Name string `json:"name"`
		}
		giteaJSON(t, e.base, e.creds.Token, http.MethodGet, "/api/v1/orgs/"+owner+"/repos?limit=50", nil, &repos)
		for _, r := range repos {
			deleteRepo(t, e.base, owner, r.Name, e.creds.Token)
		}
	})
	p, err := (Client{}).Provider(ports.Config{URL: e.base, Owner: owner, Credentials: e.creds})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestIssuesContractIntegration(t *testing.T) {
	e := newContractEnv(t)
	contract.Issues(t, contract.IssuesEnv{
		New: func(t *testing.T) (ports.GitHost, ports.Issues) {
			p := e.provider(t)
			return p, p
		},
		Assignee: e.creds.Username,
	})
}

// contractRunnerLabel is what the workflow NoLog pushes runs on, and the
// runner it registers takes jobs for.
const contractRunnerLabel = "theos-contract"

func TestCIContractIntegration(t *testing.T) {
	e := newContractEnv(t)
	contract.CI(t, contract.CIEnv{
		New: func(t *testing.T) (ports.GitHost, ports.CI) {
			p := e.provider(t)
			return p, p
		},
		// A runner registered over the runner protocol takes the job of a
		// workflow pushed for it and sends nothing, as one that went away.
		NoLog: func(t *testing.T, host ports.GitHost, repo ports.Repository) string {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			owner := host.(ports.OwnerRunners)
			token, err := owner.RunnerRegistrationToken(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var reg struct {
				Runner struct {
					ID    int64  `json:"id,string"`
					UUID  string `json:"uuid"`
					Token string `json:"token"`
				} `json:"runner"`
			}
			runnerCall(ctx, t, e.base, "Register", nil, map[string]any{"name": repo.Owner + "-runner",
				"token": token, "version": "v0.2.13", "labels": []string{contractRunnerLabel}, "ephemeral": true}, &reg)
			t.Cleanup(func() { _ = owner.DeleteRunner(context.Background(), reg.Runner.ID) })
			as := map[string]string{"x-runner-uuid": reg.Runner.UUID, "x-runner-token": reg.Runner.Token}
			runnerCall(ctx, t, e.base, "Declare", as,
				map[string]any{"version": "v0.2.13", "labels": []string{contractRunnerLabel}}, nil)

			head, err := host.BranchCommit(ctx, repo, ports.DefaultBranch)
			if err != nil {
				t.Fatal(err)
			}
			workflow := "on: [push]\njobs:\n  check:\n    runs-on: " + contractRunnerLabel +
				"\n    steps:\n      - run: echo check\n"
			if _, err := host.PushDefaultBranch(ctx, repo, head, scaffold.Tree{
				".gitea/workflows/check.yaml": {Data: []byte(workflow)}}, nil, "Add a workflow", testAuthor); err != nil {
				t.Fatal(err)
			}
			// Gitea schedules the run after the push returns.
			for {
				var task struct {
					Task *struct {
						ID string `json:"id"`
					} `json:"task"`
				}
				runnerCall(ctx, t, e.base, "FetchTask", as, map[string]any{"tasksVersion": "0"}, &task)
				if task.Task != nil {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("the runner never got the job")
				case <-time.After(500 * time.Millisecond):
				}
			}
			var jobs struct {
				Jobs []struct {
					HTMLURL string `json:"html_url"`
				} `json:"jobs"`
			}
			giteaJSON(t, e.base, e.creds.Token, http.MethodGet,
				fmt.Sprintf("/api/v1/repos/%s/%s/actions/jobs", repo.Owner, repo.Name), nil, &jobs)
			if len(jobs.Jobs) != 1 {
				t.Fatalf("the workflow has %d jobs, want 1", len(jobs.Jobs))
			}
			return jobs.Jobs[0].HTMLURL
		},
		// A workflow pushed for a label no runner takes queues its job,
		// once Gitea schedules the run after the push.
		Queue: func(t *testing.T, host ports.GitHost, repo ports.Repository, label string) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			head, err := host.BranchCommit(ctx, repo, ports.DefaultBranch)
			if err != nil {
				t.Fatal(err)
			}
			workflow := "on: [push]\njobs:\n  check:\n    runs-on: " + label + "\n    steps:\n      - run: echo check\n"
			if _, err := host.PushDefaultBranch(ctx, repo, head, scaffold.Tree{
				".gitea/workflows/wait.yaml": {Data: []byte(workflow)}}, nil, "Add a workflow", testAuthor); err != nil {
				t.Fatal(err)
			}
		},
	})
}

func TestAgentAccessContractIntegration(t *testing.T) {
	e := newContractEnv(t)
	contract.AgentAccess(t, contract.AgentAccessEnv{
		New: func(t *testing.T) (ports.GitHost, ports.AgentAccess) {
			p := e.provider(t)
			return p, p
		},
		// The sandbox pushes with git over HTTPS, signed in as the bot.
		Push: func(t *testing.T, as ports.Credentials, repo ports.Repository, base, branch string,
			files scaffold.Tree) error {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			author := ports.Signature{Name: as.Username, Email: as.Username + "@example.com", When: time.Now()}
			var err error
			if branch == ports.DefaultBranch {
				_, err = git.PushDefaultBranch(ctx, repo.CloneURL, &as, base, files, nil, "Push as the agent", author)
			} else {
				_, err = git.PushBranch(ctx, repo.CloneURL, &as, base, branch, files, nil, "Push as the agent", author)
			}
			return err
		},
	})
}

func TestRegistryContractIntegration(t *testing.T) {
	e := newContractEnv(t)
	contract.Registry(t, contract.RegistryEnv{
		New: func(t *testing.T) (ports.GitHost, ports.Registry) {
			p := e.provider(t)
			return p, p
		},
		// Gitea reads the pushed go.mod; the module goes before its
		// organization.
		GoMod: func(t *testing.T, repo ports.Repository, path string) {
			t.Cleanup(func() {
				req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/packages/%s/go/%s/v0.1.0",
					e.base, repo.Owner, url.PathEscape(path)), nil)
				req.Header.Set("Authorization", "token "+e.creds.Token)
				if resp, err := http.DefaultClient.Do(req); err == nil {
					_ = resp.Body.Close()
				}
			})
		},
	})
}
