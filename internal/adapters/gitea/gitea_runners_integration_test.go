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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
)

// TestGiteaRunnersIntegration registers ephemeral runners with two new
// organizations' registration tokens, the way the runner's `register
// --ephemeral` does, and finds each in its own organization's runners and not
// in the other's, online once it has called Gitea and offline until then.
// Like TestGiteaIntegration; the organizations and runners go when it ends.
func TestGiteaRunnersIntegration(t *testing.T) {
	base := os.Getenv("THEOS_GITEA_URL")
	if base == "" {
		t.Skip("THEOS_GITEA_URL not set")
	}
	creds := ports.Credentials{Username: os.Getenv("THEOS_GITEA_USERNAME"), Token: os.Getenv("THEOS_GITEA_TOKEN")}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	stamp := time.Now().UnixNano()
	owners := [2]ports.OwnerRunners{}
	for i := range owners {
		org := fmt.Sprintf("it-runners-%d-%d", stamp, i)
		createOrg(t, base, org, creds.Token)
		p, err := Client{}.Provider(ports.Config{URL: base, Owner: org, Credentials: creds})
		if err != nil {
			t.Fatal(err)
		}
		owners[i] = p.(ports.OwnerRunners)
	}
	a, b := owners[0], owners[1]

	tokenA, err := a.RunnerRegistrationToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := a.RunnerRegistrationToken(ctx); err != nil || again != tokenA {
		t.Errorf("asking again gave another token (%v): a runner registering with the first would be refused", err)
	}
	tokenB, err := b.RunnerRegistrationToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tokenB == tokenA {
		t.Fatal("two organizations got the same registration token")
	}

	runnerA := registerRunner(ctx, t, base, tokenA, fmt.Sprintf("it-a-%d", stamp))
	t.Cleanup(func() { _ = a.DeleteRunner(context.Background(), runnerA.id) })
	runnerB := registerRunner(ctx, t, base, tokenB, fmt.Sprintf("it-b-%d", stamp))
	t.Cleanup(func() { _ = b.DeleteRunner(context.Background(), runnerB.id) })
	runnerA.declare(ctx, t, base)

	find := func(p ports.OwnerRunners, id int64) *ports.Runner {
		t.Helper()
		runners, err := p.Runners(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if i := slices.IndexFunc(runners, func(r ports.Runner) bool { return r.ID == id }); i >= 0 {
			return &runners[i]
		}
		return nil
	}
	want := ports.Runner{ID: runnerA.id, Name: runnerA.name, Labels: []string{runnerLabel}, Ephemeral: true, Online: true}
	if got := find(a, runnerA.id); got == nil || !reflect.DeepEqual(*got, want) {
		t.Errorf("organization a's runners have %+v, want %+v", got, want)
	}
	want = ports.Runner{ID: runnerB.id, Name: runnerB.name, Labels: []string{runnerLabel}, Ephemeral: true}
	if got := find(b, runnerB.id); got == nil || !reflect.DeepEqual(*got, want) {
		t.Errorf("organization b's runners have %+v, want %+v: one that never called Gitea is offline", got, want)
	}
	if find(b, runnerA.id) != nil || find(a, runnerB.id) != nil {
		t.Error("an organization's runners list another's runner")
	}

	if err := a.DeleteRunner(ctx, runnerB.id); err != nil {
		t.Errorf("deleting another organization's runner: %v, want no error, as for one that is gone", err)
	}
	if find(b, runnerB.id) == nil {
		t.Error("an organization deleted another's runner")
	}
	if err := a.DeleteRunner(ctx, runnerA.id); err != nil {
		t.Fatal(err)
	}
	if find(a, runnerA.id) != nil {
		t.Error("a deleted runner is still listed")
	}
	if err := a.DeleteRunner(ctx, runnerA.id); err != nil {
		t.Errorf("deleting a runner twice: %v", err)
	}
}

// runnerLabel is the test runners' label, which no workflow runs on.
const runnerLabel = "c4-it-runner:host"

// testRunner is a runner registered through Gitea's runner protocol, with the
// credentials it calls Gitea with afterwards.
type testRunner struct {
	id                int64
	name, uuid, token string
}

// registerRunner registers an ephemeral runner with a registration token, as
// `gitea-runner register --ephemeral` does, over the runner protocol's JSON.
func registerRunner(ctx context.Context, t *testing.T, base, token, name string) testRunner {
	t.Helper()
	var out struct {
		Runner struct {
			ID    int64  `json:"id,string"`
			UUID  string `json:"uuid"`
			Token string `json:"token"`
		} `json:"runner"`
	}
	runnerCall(ctx, t, base, "Register", nil, map[string]any{
		"name": name, "token": token, "version": "v0.2.13", "labels": []string{runnerLabel}, "ephemeral": true,
	}, &out)
	return testRunner{id: out.Runner.ID, name: name, uuid: out.Runner.UUID, token: out.Runner.Token}
}

// declare calls Gitea as the runner does when its daemon starts, which makes
// it online.
func (r testRunner) declare(ctx context.Context, t *testing.T, base string) {
	t.Helper()
	runnerCall(ctx, t, base, "Declare", map[string]string{"x-runner-uuid": r.uuid, "x-runner-token": r.token},
		map[string]any{"version": "v0.2.13", "labels": []string{runnerLabel}}, nil)
}

// runnerCall calls method of Gitea's runner service in the Connect protocol's
// JSON form.
func runnerCall(ctx context.Context, t *testing.T, base, method string, headers map[string]string, in, out any) {
	t.Helper()
	body, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimSuffix(base, "/")+"/api/actions/runner.v1.RunnerService/"+method, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var data bytes.Buffer
	_, _ = data.ReadFrom(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("runner %s: HTTP %d: %s", method, resp.StatusCode, data.String())
	}
	if out != nil {
		if err := json.Unmarshal(data.Bytes(), out); err != nil {
			t.Fatalf("runner %s: %v", method, err)
		}
	}
}

// createOrg creates organization name, deleted when the test ends.
func createOrg(t *testing.T, base, name, token string) {
	t.Helper()
	api := strings.TrimSuffix(base, "/") + "/api/v1/orgs"
	call := func(method, url string, body []byte) int {
		req, _ := http.NewRequest(method, url, bytes.NewReader(body))
		req.Header.Set("Authorization", "token "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Logf("%s %s: %v", method, url, err)
			return 0
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	body, _ := json.Marshal(map[string]string{"username": name, "visibility": "public"})
	if status := call(http.MethodPost, api, body); status != http.StatusCreated {
		t.Fatalf("creating organization %s: HTTP %d", name, status)
	}
	t.Cleanup(func() {
		if status := call(http.MethodDelete, api+"/"+name, nil); status != http.StatusNoContent {
			t.Logf("cleanup of organization %s: HTTP %d", name, status)
		}
	})
}
