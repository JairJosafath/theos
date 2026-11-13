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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// jobRunners is a Gitea at handler, as the webshop organization's.
func jobRunners(t *testing.T, handler http.HandlerFunc) ports.JobRunners {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p, err := Client{}.Provider(ports.Config{URL: srv.URL, Owner: "webshop", Credentials: ports.Credentials{Token: "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	runners, ok := p.(ports.JobRunners)
	if !ok {
		t.Fatal("the Gitea provider has no JobRunners")
	}
	return runners
}

// WaitingJobs lists the organization's queued jobs a runner of the labels
// could take, every page of them, oldest first (step 8).
func TestWaitingJobs(t *testing.T) {
	job := func(id int64, status string, labels ...string) string {
		l, _ := json.Marshal(labels)
		return fmt.Sprintf(`{"id":%d,"run_id":%d,"name":"check","status":%q,"labels":%s,"created_at":"2026-11-06T10:00:00Z",`+
			`"html_url":"http://gitea/webshop/app/actions/runs/%d/jobs/%d"}`, id, id/10, status, l, id/10, id)
	}
	full := make([]string, 0, issuePageSize)
	full = append(full, job(30, "queued", "env-gpu"))
	for i := range issuePageSize - 1 {
		full = append(full, job(int64(1000+i), "queued", "go"))
	}
	pages := map[string]string{
		"1": `{"jobs":[` + strings.Join(full, ",") + `]}`,
		"2": `{"jobs":[` + strings.Join([]string{job(20, "queued", "env-gpu"), job(21, "queued", "env-gpu", "other"),
			job(22, "waiting", "env-gpu"), job(30, "queued", "env-gpu")}, ",") + `]}`,
	}
	var calls []string
	p := jobRunners(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path+" status="+r.URL.Query().Get("status")+" page="+r.URL.Query().Get("page"))
		_, _ = w.Write([]byte(pages[r.URL.Query().Get("page")]))
	})
	jobs, err := p.WaitingJobs(context.Background(), []string{"env-gpu"})
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, len(jobs))
	for _, j := range jobs {
		ids = append(ids, j.ID)
	}
	if want := []int64{20, 30}; !reflect.DeepEqual(ids, want) {
		t.Errorf("WaitingJobs(env-gpu) = %v, want %v: queued, on env-gpu alone, each once, oldest first", ids, want)
	}
	if len(jobs) > 0 {
		want := ports.WaitingJob{ID: 20, RunID: 2, Name: "check", Labels: []string{"env-gpu"},
			Created: time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC), URL: "http://gitea/webshop/app/actions/runs/2/jobs/20"}
		if !reflect.DeepEqual(jobs[0], want) {
			t.Errorf("WaitingJobs()[0] = %+v, want %+v", jobs[0], want)
		}
	}
	want := []string{"GET /api/v1/orgs/webshop/actions/jobs status=queued page=1",
		"GET /api/v1/orgs/webshop/actions/jobs status=queued page=2"}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %q, want %q", calls, want)
	}

	failing := jobRunners(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"org not found"}`))
	})
	if _, err := failing.WaitingJobs(context.Background(), []string{"env-gpu"}); err == nil ||
		!strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("WaitingJobs() of no organization = %v, want its HTTP 404", err)
	}
}

// RegisterJobRunner registers an ephemeral runner with the organization's
// registration token, sends the operator's credential nowhere but the
// token's call, and hands the machine the runner's own token alone (step
// 8).
func TestRegisterJobRunner(t *testing.T) {
	var calls []string
	var registered map[string]any
	p := jobRunners(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path+" auth="+r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/api/v1/orgs/webshop/actions/runners/registration-token":
			_, _ = w.Write([]byte(`{"token":"org-registration-token"}`))
		case "/api/actions/runner.v1.RunnerService/Register":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &registered)
			if r.Header.Get("Connect-Protocol-Version") != "1" || r.Header.Get("Content-Type") != "application/json" {
				w.WriteHeader(http.StatusUnsupportedMediaType)
				return
			}
			_, _ = w.Write([]byte(`{"runner":{"id":"42","uuid":"uuid-42","token":"runner-own-token","name":"gpu-ci-1"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	runner, err := p.RegisterJobRunner(context.Background(), ports.RunnerRegistration{Name: "gpu-ci-1",
		Labels: []string{"env-gpu:host"}, Address: "https://git.example.com", Timeout: 90 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := []string{"POST /api/v1/orgs/webshop/actions/runners/registration-token auth=token secret",
		"POST /api/actions/runner.v1.RunnerService/Register auth="}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Errorf("calls = %q, want %q", calls, wantCalls)
	}
	wantRegistered := map[string]any{"name": "gpu-ci-1", "token": "org-registration-token", "version": registeredVersion,
		"labels": []any{"env-gpu"}, "ephemeral": true}
	if !reflect.DeepEqual(registered, wantRegistered) {
		t.Errorf("registered %v, want %v", registered, wantRegistered)
	}
	if runner.ID != 42 || runner.Name != "gpu-ci-1" || !reflect.DeepEqual(runner.Labels, []string{"env-gpu"}) {
		t.Errorf("RegisterJobRunner() = %d %q %q", runner.ID, runner.Name, runner.Labels)
	}
	var file map[string]any
	if err := json.Unmarshal(runner.Files[".runner"], &file); err != nil {
		t.Fatalf(".runner is no JSON: %v", err)
	}
	wantFile := map[string]any{"WARNING": runnerFileWarning, "id": float64(42), "uuid": "uuid-42", "name": "gpu-ci-1",
		"token": "runner-own-token", "address": "https://git.example.com", "labels": []any{"env-gpu:host"},
		"ephemeral": true}
	if !reflect.DeepEqual(file, wantFile) {
		t.Errorf(".runner = %v, want %v", file, wantFile)
	}
	config := string(runner.Files["config.yaml"])
	for _, want := range []string{"file: " + model.RemoteRunnerDir + "/.runner", "capacity: 1", "timeout: 90m",
		"workdir_parent: " + model.RemoteRunnerDir + "/work"} {
		if !strings.Contains(config, want) {
			t.Errorf("config.yaml lacks %q:\n%s", want, config)
		}
	}
	if want := []string{"daemon", "--config", model.RemoteRunnerDir + "/config.yaml"}; !reflect.DeepEqual(runner.Args, want) {
		t.Errorf("Args = %q, want %q", runner.Args, want)
	}
	for name, data := range runner.Files {
		if strings.Contains(string(data), "org-registration-token") || strings.Contains(string(data), "secret") {
			t.Errorf("%s holds the registration token or the operator's credential", name)
		}
	}

	refusing := jobRunners(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/registration-token") {
			_, _ = w.Write([]byte(`{"token":"org-registration-token"}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"unauthenticated","message":"runner registration token not found"}`))
	})
	if _, err := refusing.RegisterJobRunner(context.Background(), ports.RunnerRegistration{Name: "gpu-ci-2",
		Labels: []string{"env-gpu:host"}, Address: "https://git.example.com"}); err == nil ||
		!strings.Contains(err.Error(), "HTTP 401") {
		t.Errorf("RegisterJobRunner() refused = %v, want its HTTP 401", err)
	}
}
