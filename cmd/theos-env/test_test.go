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

package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/model"
)

// gitAt runs git in dir with stdin and env, and returns its output; the
// test fails on an error.
func gitAt(t *testing.T, dir string, stdin []byte, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir, cmd.Stdin = dir, bytes.NewReader(stdin)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.Output()
	if err != nil {
		t.Errorf("git %v in %s: %v", args, dir, err)
	}
	return strings.TrimSpace(string(out))
}

// hostedRepo is gitRepo with its commit pushed to a bare repository, its
// origin, standing in for the Git host; it returns both folders.
func hostedRepo(t *testing.T) (work, host string) {
	t.Helper()
	work, host = gitRepo(t), t.TempDir()
	gitAt(t, host, nil, nil, "init", "-q", "--bare")
	gitAt(t, work, nil, nil, "remote", "add", "origin", host)
	gitAt(t, work, nil, nil, "push", "-q", "origin", "HEAD:refs/heads/main")
	return work, host
}

// operator answers the first request on branch of host as the operator
// does: it reads the request and its files, checks them against the
// lease's token, and pushes the answer, report and artifacts on top of
// main to the branch. Its channel gives an error it met, or nil.
func operator(t *testing.T, host, branch, token string, report model.TestRunReport,
	artifacts map[string]string) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(20 * time.Second)
		var request string
		for request == "" && time.Now().Before(deadline) {
			out, _ := exec.Command("git", "--git-dir", host, "rev-parse", "--verify", "-q", "refs/heads/"+branch).Output()
			request = strings.TrimSpace(string(out))
			time.Sleep(10 * time.Millisecond)
		}
		show := func(path string) []byte {
			out, _ := exec.Command("git", "--git-dir", host, "cat-file", "blob", request+":"+path).Output()
			return out
		}
		var r model.TestRunRequest
		if err := json.Unmarshal(show(model.TestRunRequestFile), &r); err != nil {
			done <- err
			return
		}
		lease := strings.TrimPrefix(branch, model.TestRunBranchPrefix)
		if err := model.CheckTestRun(token, lease, r, show(model.TestRunAppDir+r.App),
			show(model.TestRunTestsDir+r.Tests)); err != nil {
			report = model.TestRunReport{Result: model.TestRunRefused, Message: err.Error()}
		}
		parent, _ := exec.Command("git", "--git-dir", host, "rev-parse", request+"^").Output()
		if strings.TrimSpace(string(parent)) != r.Commit {
			report = model.TestRunReport{Result: model.TestRunRefused, Message: "the request's parent isn't its commit"}
		}
		report.Environment, report.Commit, report.Type = "devices", r.Commit, r.Type
		data, _ := json.Marshal(report)
		files := map[string]string{model.TestRunAnswerFile: request + "\n", model.TestRunReportFile: string(data)}
		for p, c := range artifacts {
			files[model.TestRunArtifactsDir+p] = c
		}
		index := filepath.Join(t.TempDir(), "index")
		env := []string{"GIT_DIR=" + host, "GIT_INDEX_FILE=" + index}
		for p, c := range files {
			blob := gitAt(t, host, []byte(c), env, "hash-object", "-w", "--stdin")
			gitAt(t, host, nil, env, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+p)
		}
		tree := gitAt(t, host, nil, env, "write-tree")
		main := gitAt(t, host, nil, env, "rev-parse", "refs/heads/main")
		answer := gitAt(t, host, nil, env, "-c", "user.name=theos", "-c", "user.email=theos@example.com",
			"commit-tree", tree, "-p", main, "-m", "the result")
		gitAt(t, host, nil, env, "update-ref", "refs/heads/"+branch, answer)
		done <- nil
	}()
	return done
}

// testIn runs theos-env test devices with args in folder, the lease's
// variables set, and returns its exit code and output.
func testIn(t *testing.T, folder, url, token string, args ...string) (int, string, string) {
	t.Helper()
	t.Chdir(folder)
	vars := map[string]string{"THEOS_ENV_DEVICES_URL": url, "THEOS_ENV_DEVICES_TOKEN": token}
	var stdout, stderr bytes.Buffer
	code := mainWith(append([]string{"test", "devices"}, args...), &stdout, &stderr,
		func(k string) string { return vars[k] })
	return code, stdout.String(), stderr.String()
}

// apks writes an app and its test package into work.
func apks(t *testing.T, work string) []string {
	t.Helper()
	for name, content := range map[string]string{"app-debug.apk": "the app", "app-debug-androidTest.apk": "its tests"} {
		if err := os.WriteFile(filepath.Join(work, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return []string{"--type", "instrumentation", "--app", "app-debug.apk", "--tests", "app-debug-androidTest.apk"}
}

// A test run asks over the Git host: theos-env test pushes the request,
// signed with the lease's token, the app and the test package to the
// lease's branch, its parent the run's commit, and leaves the working tree
// and its index as they were; it writes the operator's answer, the report
// and the artifacts, into test-runs/<name>/, prints the summary and exits
// 1 when a test failed (T12, step 10).
func TestTestAsksOverTheGitHost(t *testing.T) {
	testRunPoll = 10 * time.Millisecond
	work, host := hostedRepo(t)
	lease, token := model.LeaseID("devices", "add-share-sheet-r1-implement"), rand.Text()
	branch := model.TestRunBranch(lease)
	status := gitAt(t, work, nil, nil, "status", "--porcelain", "--untracked-files=no")
	answered := operator(t, host, branch, token, model.TestRunReport{Result: model.TestRunFailed,
		Counters: model.TestRunCounters{Total: 2, Passed: 1, Failed: 1},
		Devices:  []model.TestRunDevice{{Name: "Google Pixel 8", OS: "14", Result: model.TestRunFailed}},
		Problems: []model.TestRunProblem{{Device: "Google Pixel 8", Suite: "ShareSheetTest", Test: "sharesTheCart",
			Result: model.TestRunFailed, Message: "expected: <3> but was: <2>"}}},
		map[string]string{"Google-Pixel-8/share-sheet.png": "a screenshot"})
	code, stdout, stderr := testIn(t, work, model.TestRunEndpoint(lease), token, apks(t, work)...)
	if err := <-answered; err != nil {
		t.Fatal(err)
	}
	if code != 1 {
		t.Errorf("exit %d, want 1: a test failed\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	for _, want := range []string{"on devices: failed: 1 passed, 1 failed of 2 tests on 1 device.",
		"ShareSheetTest.sharesTheCart on Google Pixel 8: failed", "test-runs/devices"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	shot, err := os.ReadFile(filepath.Join(work, "test-runs", "devices", "artifacts", "Google-Pixel-8", "share-sheet.png"))
	if err != nil || string(shot) != "a screenshot" {
		t.Errorf("the screenshot is %q, %v", shot, err)
	}
	var report model.TestRunReport
	data, _ := os.ReadFile(filepath.Join(work, "test-runs", "devices", "report.json"))
	if err := json.Unmarshal(data, &report); err != nil || report.Result != model.TestRunFailed ||
		report.Commit != gitAt(t, work, nil, nil, "rev-parse", "HEAD") {
		t.Errorf("report.json = %+v, %v; want the run's, of its commit", report, err)
	}
	summary, _ := os.ReadFile(filepath.Join(work, "test-runs", "devices", "summary.txt"))
	if !strings.Contains(string(summary), "1 failed of 2 tests") {
		t.Errorf("summary.txt = %q", summary)
	}
	// The request commit holds the request and the files alone, on the
	// run's commit, and the run's index is as it was.
	bare := []string{"GIT_DIR=" + host}
	request := gitAt(t, work, nil, bare, "cat-file", "blob", "refs/heads/"+branch+":"+model.TestRunAnswerFile)
	files := gitAt(t, work, nil, bare, "ls-tree", "-r", "--name-only", request)
	if files != ".theos-env/app/app-debug.apk\n.theos-env/request.json\n.theos-env/tests/app-debug-androidTest.apk" {
		t.Errorf("the request commit holds %q", files)
	}
	if parent := gitAt(t, work, nil, bare, "rev-parse", request+"^"); parent != report.Commit {
		t.Errorf("the request's parent is %s, want the run's commit %s", parent, report.Commit)
	}
	if got := gitAt(t, work, nil, nil, "status", "--porcelain", "--untracked-files=no"); got != status {
		t.Errorf("the tracked files' status was %q, is %q", status, got)
	}
	if staged := gitAt(t, work, nil, nil, "diff", "--cached", "--name-only"); staged != "" {
		t.Errorf("the request staged %q in the run's index", staged)
	}
}

// A request the lease's token didn't sign is answered with nothing run,
// and theos-env says why and exits 125.
func TestTestOfAnotherLeasesTokenRunsNothing(t *testing.T) {
	testRunPoll = 10 * time.Millisecond
	work, host := hostedRepo(t)
	lease := model.LeaseID("devices", "add-share-sheet-r1-implement")
	answered := operator(t, host, model.TestRunBranch(lease), rand.Text(),
		model.TestRunReport{Result: model.TestRunPassed}, nil)
	code, _, stderr := testIn(t, work, model.TestRunEndpoint(lease), rand.Text(), apks(t, work)...)
	if err := <-answered; err != nil {
		t.Fatal(err)
	}
	if code != exitFailed || !strings.Contains(stderr, "ran nothing") || !strings.Contains(stderr, "isn't signed") {
		t.Errorf("exit %d, stderr %q; want 125, saying the request isn't signed", code, stderr)
	}
}

// test refuses what it can't ask for before it pushes anything: an
// environment that runs commands, none leased, a type it doesn't know, a
// file of the wrong kind.
func TestTestRefusesWhatItCantAskFor(t *testing.T) {
	work, host := hostedRepo(t)
	lease := model.LeaseID("devices", "add-share-sheet-r1-implement")
	args := apks(t, work)
	for _, tt := range []struct {
		name, url, token string
		args             []string
		code             int
		says             string
	}{
		{"an environment that runs commands", "http://browser-0123456789.apps:7070", "t", args, exitFailed,
			"runs commands, not test packages: use theos-env run devices"},
		{"no lease", "", "", args, exitFailed, "leased no environment devices"},
		{"an unknown type", model.TestRunEndpoint(lease), "t",
			[]string{"--type", "espresso", "--app", "app-debug.apk", "--tests", "app-debug-androidTest.apk"}, exitFailed,
			"is none of"},
		{"an app of the wrong kind", model.TestRunEndpoint(lease), "t",
			[]string{"--type", "xctest", "--app", "app-debug.apk", "--tests", "app-debug-androidTest.apk"}, exitFailed,
			"is a .ipa file"},
		{"no app", model.TestRunEndpoint(lease), "t", []string{"--type", "instrumentation"}, exitUsage, "usage"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			code, _, stderr := testIn(t, work, tt.url, tt.token, tt.args...)
			if code != tt.code || !strings.Contains(stderr, tt.says) {
				t.Errorf("exit %d, stderr %q; want %d, saying %q", code, stderr, tt.code, tt.says)
			}
		})
	}
	if refs := gitAt(t, host, nil, []string{"GIT_DIR=" + host}, "for-each-ref", "refs/heads/theos-env/"); refs != "" {
		t.Errorf("a refused request pushed %s", refs)
	}
}
