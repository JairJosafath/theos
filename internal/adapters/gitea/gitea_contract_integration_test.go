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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
)

// TestGitHostContractIntegration runs the GitHost contract suite on the seeded
// Gitea, each subtest in an organization of its own, like TestGiteaIntegration.
func TestGitHostContractIntegration(t *testing.T) {
	base := os.Getenv("THEOS_GITEA_URL")
	if base == "" {
		t.Skip("THEOS_GITEA_URL not set")
	}
	creds := ports.Credentials{Username: os.Getenv("THEOS_GITEA_USERNAME"), Token: os.Getenv("THEOS_GITEA_TOKEN")}
	contract.GitHost(t, contract.GitHostEnv{
		New: func(t *testing.T) ports.GitHost {
			owner := fmt.Sprintf("contract-%d", time.Now().UnixNano())
			createOrg(t, base, owner, creds.Token)
			// Cleanups run last first: the repository goes before its organization.
			t.Cleanup(func() { deleteRepo(t, base, owner, "contract", creds.Token) })
			p, err := (Client{}).Provider(ports.Config{URL: base, Owner: owner, Credentials: creds})
			if err != nil {
				t.Fatal(err)
			}
			return p
		},
		SetStatus: func(t *testing.T, repo ports.Repository, commit, context, state string) {
			giteaJSON(t, base, creds.Token, http.MethodPost,
				fmt.Sprintf("/api/v1/repos/%s/%s/statuses/%s", repo.Owner, repo.Name, commit),
				map[string]string{"state": state, "context": context}, nil)
		},
		Release: func(t *testing.T, repo ports.Repository, tag, commit, body, asset, content string) {
			id := createRelease(t, base, creds.Token, repo, tag, commit)
			giteaJSON(t, base, creds.Token, http.MethodPatch,
				fmt.Sprintf("/api/v1/repos/%s/%s/releases/%d", repo.Owner, repo.Name, id),
				map[string]string{"body": body}, nil)
			uploadAsset(t, base, creds.Token, repo, id, asset, content)
		},
	})
}

// TestEventsContractIntegration runs the Events contract suite on the seeded
// Gitea, each subtest in an organization of its own, whose hook delivers to a
// listener of the subtest's own; the listener's second address is localhost
// for 127.0.0.1, which the local Gitea's webhook settings let it reach.
func TestEventsContractIntegration(t *testing.T) {
	base := os.Getenv("THEOS_GITEA_URL")
	if base == "" {
		t.Skip("THEOS_GITEA_URL not set")
	}
	creds := ports.Credentials{Username: os.Getenv("THEOS_GITEA_USERNAME"), Token: os.Getenv("THEOS_GITEA_TOKEN")}
	contract.Events(t, contract.EventsEnv{
		New: func(t *testing.T) ports.GitHost {
			owner := fmt.Sprintf("contract-events-%d", time.Now().UnixNano())
			createOrg(t, base, owner, creds.Token)
			// Cleanups run last first: the repository goes before its organization.
			t.Cleanup(func() { deleteRepo(t, base, owner, "contract", creds.Token) })
			p, err := (Client{}).Provider(ports.Config{URL: base, Owner: owner, Credentials: creds})
			if err != nil {
				t.Fatal(err)
			}
			return p
		},
		Receiver: func(t *testing.T, _ ports.GitHost) contract.Receiver {
			var mu sync.Mutex
			var got []contract.Delivery
			listener := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				header := r.Header.Clone()
				mu.Lock()
				got = append(got, contract.Delivery{URL: "http://" + r.Host + r.URL.RequestURI(),
					Header: header.Get, Body: body})
				mu.Unlock()
			}))
			t.Cleanup(listener.Close)
			return contract.Receiver{
				Bases: [2]string{listener.URL, strings.Replace(listener.URL, "127.0.0.1", "localhost", 1)},
				Received: func() []contract.Delivery {
					mu.Lock()
					defer mu.Unlock()
					return append([]contract.Delivery(nil), got...)
				},
			}
		},
		Hooks: func(t *testing.T, h ports.GitHost) []string {
			var hooks []giteaHook
			giteaJSON(t, base, creds.Token, http.MethodGet, "/api/v1/orgs/"+h.Repository("contract").Owner+"/hooks",
				nil, &hooks)
			out := make([]string, 0, len(hooks))
			for _, hook := range hooks {
				out = append(out, hook.Config.URL)
			}
			return out
		},
	})
}
