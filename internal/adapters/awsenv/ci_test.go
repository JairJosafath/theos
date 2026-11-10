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

package awsenv

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

const runnerURL = "https://downloads.example.com/ci/v3.5.0/runner-linux-${arch}"

// jobRunner is a CI runner registered for one job, as a CI hands it over.
func jobRunner() ports.JobRunner {
	return ports.JobRunner{ID: 42, Name: "gpu-ci-0123456789", Labels: []string{"env-gpu"},
		Files: map[string][]byte{".runner": []byte(`{"id":42,"token":"runner-own-token"}` + "\n"),
			"config.yaml": []byte("runner:\n  capacity: 1\n")},
		Args: []string{"daemon", "--config", model.RemoteRunnerDir + "/config.yaml"}}
}

// ciRequest is a CI lease's request of env, held by runner 42.
func ciRequest(env ports.Environment) ports.LeaseRequest {
	return ports.LeaseRequest{ID: model.CILeaseID(env.Name, env.UID, "git", 7, 0), Env: env,
		Holder: model.CIHolder("git", 42), Token: rand.Text(), Deadline: 45 * time.Minute}
}

// A machine that runs a CI job writes the runner's files for root alone,
// serves theos-env until its metadata service is off, starts the runner
// only then, and shuts down once it exits; it downloads both checked
// against SHA256SUMS (step 8).
func TestRunnerUserData(t *testing.T) {
	token, runner := rand.Text(), jobRunner()
	script, err := RunnerUserData(toolsURL, runnerURL, token, runner, 44*time.Minute+30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"#!/bin/sh\n", "set -eu\n", "shutdown -h +45 ",
		"Linux) os=linux ;;\n",
		"fetch 'https://github.com/JairJosafath/theos/releases/download/v0.31.0/theos-env-'\"$os\"'-'\"$arch\"'' " +
			"/usr/local/bin/theos-env\n",
		"fetch 'https://downloads.example.com/ci/v3.5.0/runner-linux-'\"$arch\"'' /usr/local/bin/theos-ci-runner\n",
		`"${1%/*}/SHA256SUMS"`, "sha256sum -c -", "umask 077\n",
		"printf '%s' '" + base64.StdEncoding.EncodeToString(runner.Files[".runner"]) + "' | base64 -d >" +
			model.RemoteRunnerDir + "/.runner\n",
		"printf '%s' '" + base64.StdEncoding.EncodeToString(runner.Files["config.yaml"]) + "' | base64 -d >" +
			model.RemoteRunnerDir + "/config.yaml\n",
		"THEOS_ENV_TOKEN=" + token + " /usr/local/bin/theos-env serve --listen :443 --work /work --tls &\n",
		"http://169.254.169.254/latest/api/token",
		"cd " + model.RemoteRunnerDir + "\n/usr/local/bin/theos-ci-runner daemon --config " + model.RemoteRunnerDir +
			"/config.yaml || true\nshutdown -h now\n",
		"systemd-run --unit=theos-ci-job /usr/local/sbin/theos-ci-job\n",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("user data lacks %q:\n%s", want, script)
		}
	}
	metadata, serve := strings.Index(script, "169.254.169.254"), strings.Index(script, "theos-env serve")
	kill, start := strings.Index(script, `kill "$serve"`), strings.Index(script, "/usr/local/bin/theos-ci-runner daemon")
	if serve < 0 || metadata < serve || kill < metadata || start < kill {
		t.Errorf("the runner starts at %d, before theos-env (%d) answered and the metadata service (%d) stopped, "+
			"or theos-env (%d) still serves", start, serve, metadata, kill)
	}
	if strings.Contains(script, "runner-own-token") {
		t.Error("the runner's files are written as they are, not as base64 the shell reads as one word")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh to read the script with")
	}
	if out, err := exec.Command(sh, "-n", "-c", script).CombinedOutput(); err != nil { //nolint:gosec // the test's own script
		t.Errorf("sh -n: %v: %s", err, out)
	}

	bad := func(mutate func(*ports.JobRunner)) ports.JobRunner {
		r := jobRunner()
		r.Files = map[string][]byte{".runner": r.Files[".runner"]}
		mutate(&r)
		return r
	}
	for _, tc := range []struct {
		name                 string
		tools, runnerAt, tok string
		runner               ports.JobRunner
	}{
		{"a runner over plain http", toolsURL, "http://example.com/ci-runner-${arch}", token, jobRunner()},
		{"a runner address with a quote", toolsURL, "https://example.com/it's/ci-runner", token, jobRunner()},
		{"no runner address", toolsURL, "", token, jobRunner()},
		{"a token the shell reads", toolsURL, runnerURL, "$(reboot)", jobRunner()},
		{"a file outside the runner's folder", toolsURL, runnerURL, token,
			bad(func(r *ports.JobRunner) { r.Files["../etc/passwd"] = []byte("x") })},
		{"a file named ..", toolsURL, runnerURL, token, bad(func(r *ports.JobRunner) { r.Files[".."] = []byte("x") })},
		{"an argument the shell reads", toolsURL, runnerURL, token,
			bad(func(r *ports.JobRunner) { r.Args = []string{"daemon;", "reboot"} })},
		{"no arguments", toolsURL, runnerURL, token, bad(func(r *ports.JobRunner) { r.Args = nil })},
		{"user data over EC2's limit", toolsURL, runnerURL, token,
			bad(func(r *ports.JobRunner) { r.Files["big"] = make([]byte, maxUserData) })},
	} {
		if _, err := RunnerUserData(tc.tools, tc.runnerAt, tc.tok, tc.runner, time.Hour); err == nil {
			t.Errorf("%s: RunnerUserData succeeded", tc.name)
		}
	}
}

// A machine that runs a CI job is fenced and tagged as any lease's: only
// its user data differs, and its holder is the runner (step 8).
func TestRunnerLaunchInputIsFenced(t *testing.T) {
	req := ciRequest(newWorld().gpu())
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	in, err := RunnerLaunchInput(req, jobRunner(), "sg-0123456789abcdef0", 1_006_000, toolsURL, runnerURL, now)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := LaunchInput(req, "sg-0123456789abcdef0", 1_006_000, toolsURL, now)
	if err != nil {
		t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString(aws.ToString(in.UserData))
	if err != nil || !strings.Contains(string(data), "/usr/local/bin/theos-ci-runner daemon") {
		t.Errorf("user data %q, want the script that runs the CI runner", data)
	}
	in.UserData, plain.UserData = nil, nil
	if !reflect.DeepEqual(in, plain) {
		t.Errorf("RunnerLaunchInput = %+v, want LaunchInput's %+v but for its user data", in, plain)
	}
	if got := tag(in.TagSpecifications[0].Tags, TagHolder); got != "ci/git/42" {
		t.Errorf("holder tag %q, want the runner's, ci/git/42", got)
	}
}

// LeaseRunner leases a machine as Lease does, on the fake EC2: one per ID,
// Ready once theos-env answered and the metadata service is off, listed
// with the runner as its holder, terminated on Return; without a runner
// address it starts nothing (step 8).
func TestLeaseRunner(t *testing.T) {
	w := newWorld()
	env := w.gpu()
	req := ciRequest(env)
	if why := w.p.RunnerMissing(); why == "" {
		t.Error("RunnerMissing() = \"\" with no runner address")
	}
	if _, err := w.p.LeaseRunner(context.Background(), req, jobRunner()); err == nil ||
		!strings.Contains(err.Error(), "--test-environment-runner-url") {
		t.Errorf("LeaseRunner() with no runner address = %v, want it to name the setting", err)
	}
	if w.aws.live(env.UID) != 0 {
		t.Fatal("a machine started with no runner to run")
	}

	w.p.WithRunnerURL(runnerURL)
	if why := w.p.RunnerMissing(); why != "" {
		t.Errorf("RunnerMissing() = %q with a runner address", why)
	}
	lease := func() ports.Lease {
		t.Helper()
		l, err := w.p.LeaseRunner(context.Background(), req, jobRunner())
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	if l := lease(); l.State != model.LeasePending || l.Holder != req.Holder {
		t.Errorf("LeaseRunner() = %+v, want a Pending lease held by the runner", l)
	}
	lease()
	if n := w.aws.live(env.UID); n != 1 {
		t.Fatalf("machines %d after two LeaseRunner calls, want 1", n)
	}
	if len(w.aws.launches) != 1 {
		t.Fatalf("launches %d, want 1", len(w.aws.launches))
	}
	data, _ := base64.StdEncoding.DecodeString(aws.ToString(w.aws.launches[0].UserData))
	if !strings.Contains(string(data), "/usr/local/bin/theos-ci-runner daemon") {
		t.Errorf("the machine's user data runs no CI runner:\n%s", data)
	}

	w.aws.ready(env.UID, req.ID)
	w.get(t, env, req.ID)
	w.aws.applyMetadata(env.UID, req.ID)
	if l := w.get(t, env, req.ID); l.State != model.LeaseReady {
		t.Errorf("Get() = %+v, want Ready once theos-env answered and the metadata service is off", l)
	}
	leases, err := w.p.Leases(context.Background(), env)
	if err != nil || len(leases) != 1 || leases[0].ID != req.ID || leases[0].Holder != "ci/git/42" {
		t.Errorf("Leases() = %+v, %v; want the CI lease, held by its runner", leases, err)
	}
	if err := w.p.Return(context.Background(), env, req.ID); err != nil {
		t.Fatal(err)
	}
	if n := w.aws.live(env.UID); n != 0 {
		t.Errorf("machines %d after Return, want 0", n)
	}
}
