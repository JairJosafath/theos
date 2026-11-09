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
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/JairJosafath/theos/internal/model"
)

// testRunPoll is how often `theos-env test` looks for the operator's answer
// on the lease's branch.
var testRunPoll = 10 * time.Second

// testMain runs a test package on the devices of the environment the run
// leased as name (docs/plans/done/test-environments.md, T12, step 10). The run
// can't reach the operator, which holds the provider's credential, so it
// asks over the Git host it pushes to: it pushes a request commit, the app,
// the test package and the request signed with the lease's token, to the
// lease's branch, its parent the commit they were built from; waits for
// the operator's answer on the same branch; writes the report and the
// artifacts into --out; prints the summary; and exits 0 when every test
// passed, 1 when one didn't, 125 when nothing ran.
func testMain(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		_, _ = fmt.Fprint(stderr, usage)
		return exitUsage
	}
	name := args[0]
	flags := flag.NewFlagSet("theos-env test", flag.ContinueOnError)
	flags.SetOutput(stderr)
	t := tester{name: name, stdout: stdout, stderr: stderr, poll: testRunPoll}
	flags.StringVar(&t.typ, "type", "", "the test package's type: "+strings.Join(model.TestRunTypeNames(), ", "))
	flags.StringVar(&t.app, "app", "", "the app: an .apk, or an .ipa")
	flags.StringVar(&t.tests, "tests", "", "the test package")
	flags.StringVar(&t.out, "out", filepath.Join("test-runs", name), "where the report and the artifacts go")
	flags.DurationVar(&t.timeout, "timeout", 3*time.Hour, "how long to wait for the result")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() > 0 || t.typ == "" || t.app == "" || t.tests == "" {
		_, _ = fmt.Fprint(stderr, usage)
		return exitUsage
	}
	t.url, t.token = getenv(model.TestEnvVar(name, model.TestEnvURL)), getenv(model.TestEnvVar(name, model.TestEnvToken))
	report, err := t.run()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "theos-env: %v\n", err)
		return exitFailed
	}
	if report.Result == model.TestRunRefused {
		_, _ = fmt.Fprintf(stderr, "theos-env: the operator ran nothing: %s\n", report.Message)
		return exitFailed
	}
	_, _ = fmt.Fprint(stdout, model.TestRunSummary(report))
	_, _ = fmt.Fprintf(stdout, "The report and the artifacts are in %s.\n", t.out)
	return model.TestRunExitCode(report)
}

// tester is one test run's request.
type tester struct {
	name, url, token     string
	typ, app, tests, out string
	timeout, poll        time.Duration
	stdout, stderr       io.Writer
}

func (t *tester) run() (model.TestRunReport, error) {
	if t.url == "" || t.token == "" {
		return model.TestRunReport{}, fmt.Errorf("this run leased no environment %s: %s and %s are unset", t.name,
			model.TestEnvVar(t.name, model.TestEnvURL), model.TestEnvVar(t.name, model.TestEnvToken))
	}
	branch, ok := model.TestRunBranchOf(t.url)
	if !ok {
		return model.TestRunReport{}, fmt.Errorf("environment %s runs commands, not test packages: use "+
			"theos-env run %s -- <command>", t.name, t.name)
	}
	lease := strings.TrimPrefix(branch, model.TestRunBranchPrefix)
	app, tests, err := t.files()
	if err != nil {
		return model.TestRunReport{}, err
	}
	head, err := gitOut("rev-parse", "HEAD")
	if err != nil {
		return model.TestRunReport{}, fmt.Errorf("test asks for a run of the git commit it is called on: %w", err)
	}
	if dirty, _ := gitOut("status", "--porcelain", "--untracked-files=no"); dirty != "" {
		_, _ = fmt.Fprintf(t.stderr, "theos-env: the working tree has changes not committed; the run is of %s\n", head)
	}
	request := model.NewTestRunRequest(t.token, lease, head, t.typ, filepath.Base(t.app), app,
		filepath.Base(t.tests), tests)
	commit, err := requestCommit(head, t.name, request, app, tests)
	if err != nil {
		return model.TestRunReport{}, err
	}
	if _, err := gitOut("push", "--force", "--quiet", "origin", commit+":refs/heads/"+branch); err != nil {
		return model.TestRunReport{}, fmt.Errorf("asking for the run on branch %s: %w", branch, err)
	}
	_, _ = fmt.Fprintf(t.stderr, "theos-env: asked %s for a %s run of %s; waiting for the operator's answer on "+
		"branch %s\n", t.name, t.typ, head[:min(len(head), 12)], branch)
	answer, err := t.wait(branch, commit)
	if err != nil {
		return model.TestRunReport{}, err
	}
	return t.write(answer)
}

// files reads the app and the test package, refusing ones the request
// can't take.
func (t *tester) files() ([]byte, []byte, error) {
	var sizes [2]int
	for i, f := range []string{t.app, t.tests} {
		info, err := os.Stat(f)
		if err != nil {
			return nil, nil, err
		}
		sizes[i] = int(min(info.Size(), int64(model.TestRunMaxUpload)+1))
	}
	if err := model.CheckTestRunFiles(t.typ, filepath.Base(t.app), sizes[0], filepath.Base(t.tests),
		sizes[1]); err != nil {
		return nil, nil, err
	}
	app, err := os.ReadFile(t.app)
	if err != nil {
		return nil, nil, err
	}
	tests, err := os.ReadFile(t.tests)
	return app, tests, err
}

// requestCommit is a commit holding the request, the app and the test
// package alone, its parent head: built in an index of its own, so the
// working tree and its index stay as they are.
func requestCommit(head, name string, request model.TestRunRequest, app, tests []byte) (string, error) {
	data, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		return "", err
	}
	index := filepath.Join(os.TempDir(), "theos-env-index-"+rand.Text())
	defer func() { _ = os.Remove(index) }()
	env := []string{"GIT_INDEX_FILE=" + index}
	for path, content := range map[string][]byte{model.TestRunRequestFile: append(data, '\n'),
		model.TestRunAppDir + request.App: app, model.TestRunTestsDir + request.Tests: tests} {
		blob, err := gitIn(content, nil, "hash-object", "-w", "--stdin")
		if err != nil {
			return "", err
		}
		if _, err := gitIn(nil, env, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+path); err != nil {
			return "", err
		}
	}
	tree, err := gitIn(nil, env, "write-tree")
	if err != nil {
		return "", err
	}
	return gitIn(nil, nil, "-c", "user.name=theos-env", "-c", "user.email=theos-env@theos.invalid", "commit-tree",
		tree, "-p", head, "-m", fmt.Sprintf("theos-env test %s: %s of %s", name, request.Type, head))
}

// wait polls branch until the operator answers request on it, and returns
// the answer's commit, fetched.
func (t *tester) wait(branch, request string) (string, error) {
	deadline := time.Now().Add(t.timeout)
	local := "refs/theos-env/" + t.name
	for {
		out, err := gitOut("ls-remote", "origin", "refs/heads/"+branch)
		if err != nil {
			return "", err
		}
		sha, _, _ := strings.Cut(out, "\t")
		switch {
		case sha == "":
			return "", fmt.Errorf("branch %s is gone: the lease ended before the run was answered", branch)
		case sha != request:
			if _, err := gitOut("fetch", "--quiet", "origin", "+refs/heads/"+branch+":"+local); err != nil {
				return "", err
			}
			answers, err := gitOut("show", local+":"+model.TestRunAnswerFile)
			if err != nil || strings.TrimSpace(answers) != request {
				return "", fmt.Errorf("branch %s has another request now: another theos-env test replaced this one",
					branch)
			}
			return gitOut("rev-parse", local)
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("no answer on branch %s after %s", branch, t.timeout)
		}
		time.Sleep(t.poll)
	}
}

// write writes answer's report, summary and artifacts into the out folder,
// replacing the last run's, and returns the report.
func (t *tester) write(answer string) (model.TestRunReport, error) {
	var report model.TestRunReport
	data, err := gitIn(nil, nil, "cat-file", "blob", answer+":"+model.TestRunReportFile)
	if err != nil {
		return report, err
	}
	if err := json.Unmarshal([]byte(data), &report); err != nil {
		return report, fmt.Errorf("reading the operator's report: %w", err)
	}
	if err := os.RemoveAll(filepath.Join(t.out, "artifacts")); err != nil {
		return report, err
	}
	if err := os.MkdirAll(t.out, 0o755); err != nil { //nolint:gosec // the run's own folder
		return report, err
	}
	listed, err := gitOut("ls-tree", "-r", "--name-only", answer, "--", model.TestRunArtifactsDir)
	if err != nil {
		return report, err
	}
	for path := range strings.Lines(listed) {
		path = strings.TrimSpace(path)
		rel := strings.TrimPrefix(path, model.TestRunArtifactsDir)
		if path == "" || !fs.ValidPath(rel) {
			continue
		}
		content, err := gitBytes("cat-file", "blob", answer+":"+path)
		if err != nil {
			return report, err
		}
		to := filepath.Join(t.out, "artifacts", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil { //nolint:gosec // the run's own folder
			return report, err
		}
		if err := os.WriteFile(to, content, 0o644); err != nil { //nolint:gosec // evidence to commit
			return report, err
		}
	}
	for file, content := range map[string]string{"report.json": data, "summary.txt": model.TestRunSummary(report)} {
		if err := os.WriteFile(filepath.Join(t.out, file), []byte(content), 0o644); err != nil { //nolint:gosec // evidence
			return report, err
		}
	}
	return report, nil
}

// gitIn runs git with stdin and env added, and returns its output, trimmed.
func gitIn(stdin []byte, env []string, args ...string) (string, error) {
	out, err := gitRun(stdin, env, args...)
	return strings.TrimSpace(string(out)), err
}

// gitBytes runs git and returns its output as it is.
func gitBytes(args ...string) ([]byte, error) { return gitRun(nil, nil, args...) }

func gitRun(stdin []byte, env []string, args ...string) ([]byte, error) {
	var stderr bytes.Buffer
	cmd := exec.Command("git", args...) //nolint:gosec // this file's own arguments
	cmd.Stderr = &stderr
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
