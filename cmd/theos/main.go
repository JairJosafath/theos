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

// Command theos is theos with no cluster (docs/plans/no-cluster.md): it
// writes a project repository's model files, plans them from an idea,
// scaffolds each Container's repository as the operator does, protects its
// main and says what holds and what a cluster would add. It wires the host adapters onto
// internal/nocluster, as cmd/main.go does for the operator, and links no
// Kubernetes client.
package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/JairJosafath/theos/internal/adapters/git"
	// The one Git host adapter beside GitHub's; the operator's, as in cmd/main.go.
	hostadapter "github.com/JairJosafath/theos/internal/adapters/gitea"
	"github.com/JairJosafath/theos/internal/adapters/github"
	"github.com/JairJosafath/theos/internal/buildinfo"
	"github.com/JairJosafath/theos/internal/modelfacts"
	"github.com/JairJosafath/theos/internal/nocluster"
	"github.com/JairJosafath/theos/internal/ports"
)

// theosURL is the theos repository, whose chart's catalog init writes and
// whose templates the entries name.
const theosURL = "https://github.com/JairJosafath/theos"

const usage = `theos works a project with no cluster.

Usage:
  theos init [-C dir] -project NAME [-remote URL] [-provider P] [-ref TAG]
  theos scaffold [-C dir]
  theos protect [-C dir]
  theos status [-C dir]
  theos agents [-C dir] [-harness claude-code|codex]
  theos plan [-C dir] [-new]
  theos mcp [-C dir]
  theos next [-C dir]
  theos claim|heartbeat|release [-C dir] -task TASK -session NAME
  theos done [-C dir] -task TASK -session NAME [-commit SHA | -verdict approve|changes] -summary TEXT
  theos merge [-C dir] -task TASK
  theos update [-C dir] [-container NAME [-from REF]]
  theos export [-C dir] > project.yaml
  theos import [-C dir] [-f FILE] [-force]

theos agents writes the lead's files into the project repository from
model/: for Claude Code, CLAUDE.md, AGENTS.md, a subagent in
.claude/agents/ for each Agent a Crew names, and .mcp.json; for Codex,
AGENTS.md, .codex/agents/ and .codex/config.toml. theos status names one
that changed since. theos mcp serves the lead runtime's tools
over stdio, as .mcp.json starts it: theos_next, theos_claim,
theos_heartbeat, theos_release, theos_done and theos_status. next, claim,
heartbeat, release and done are the same on the command line. There is no
merge tool: a person merges, on the host's pages or with theos merge, which
runs only at a terminal and merges only a pull request a session other than
its author's approved on its head, with green checks and no protected path.

theos plan turns an idea into the model and filed issues, with the Brief's
plan format and checks and no Brief. With no plan/model.yaml it starts a
round: it writes the story of model/model.yaml there. A session playing the
designer writes the round in plan/ (choices.md, issues/<nn>-<container>.md,
summary.md), and theos plan again checks it: a refused plan prints each
reason and writes nothing; an accepted one is merged into model/model.yaml,
keeping what a plan can't write, and its issues are filed blockers first into
each Container's repository that has its first commit. plan/filed.yaml
records them, so a second run files none twice. plan/releases.yaml and
plan/parallel.yaml are printed, and nothing acts on them. theos plan -new
starts the next round once every issue of this one is filed.

theos update brings each Container's Standard files to its Standard's ref
in model/setup.yaml, as the operator's update does, and opens the pull
request a person merges on the host's pages: theos merge merges Tasks only.
The ref the files are at is read from main's history (the scaffold's
Scaffolded-From trailer, then each update's Standard-Update); -from names it
for one Container whose history doesn't. theos export prints the model files
as one stream for kubectl apply, with adopt: true on every Container whose
repository has commits and every Crew holding each change for a person.
theos import reads what kubectl get -o yaml prints of a project's namespace
(-f, else standard input) and writes model/model.yaml and model/setup.yaml;
-force replaces the ones there.

The credential is THEOS_GIT_TOKEN, else what git credential fill gives for
its host; theos never stores or prints it. It is sent only to the host it
is for: THEOS_GIT_HOST, which THEOS_GIT_TOKEN needs beside it; without the
token, git credential fill is asked for the host of the project
repository's origin remote. It goes over https unless THEOS_GIT_HOST
(http://host) or the origin remote says http, or the host is the machine
itself. A GitProvider on another host or scheme, or whose url holds a user,
is refused before any request; a redirect to another host is not followed;
templates elsewhere are fetched without the credential, and init's fetch of
the catalog never carries it.

Install a release with:
  go install github.com/JairJosafath/theos/cmd/theos@<tag>
`

// deps are what run reaches the world through; main gives the real ones.
type deps struct {
	hosts     nocluster.Hosts
	templates modelfacts.TemplateFetcher
	runner    nocluster.Runner
	getenv    func(string) string
	stdin     io.Reader
	stdout    io.Writer
	stderr    io.Writer
	now       func() time.Time
	// terminal reports whether a person's terminal runs theos: theos merge
	// runs nowhere else.
	terminal func() bool
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], deps{hosts: hosts{}, templates: git.Client{}, runner: execRunner{},
		getenv: os.Getenv, stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr, now: time.Now,
		terminal: stdinTerminal})
	stop()
	os.Exit(code)
}

// run runs one command and returns the exit code.
func run(ctx context.Context, args []string, d deps) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(d.stderr, usage)
		return 2
	}
	cmd := args[0]
	flags := flag.NewFlagSet("theos "+cmd, flag.ContinueOnError)
	flags.SetOutput(d.stderr)
	dir := flags.String("C", ".", "the project repository's root")
	project := flags.String("project", "", "init: the project's name")
	remote := flags.String("remote", "", "init: the project repository's remote; origin's unless set")
	provider := flags.String("provider", "", "init: the Git host's provider; asked unless the remote is on github.com")
	ref := flags.String("ref", releaseTag(), "init: the theos release whose catalog the model pins")
	harnessName := flags.String("harness", "", "agents: the tool that plays the lead, claude-code (the default) or codex")
	newRound := flags.Bool("new", false, "plan: start the next round once every issue of the one in plan/ is filed")
	containerName := flags.String("container", "", "update: the one Container to update")
	from := flags.String("from", "", "update: the ref the Container's Standard files are at, if its history doesn't say")
	importFile := flags.String("f", "-", "import: the file kubectl get -o yaml wrote; - for standard input")
	force := flags.Bool("force", false, "import: replace model/model.yaml and model/setup.yaml")
	var lead doneIn
	flags.StringVar(&lead.Task, "task", "", "claim, heartbeat, release, done, merge: the Task, as theos next names it")
	flags.StringVar(&lead.Session, "session", "", "claim, heartbeat, release, done: your session's name")
	flags.StringVar(&lead.Commit, "commit", "", "done: an implementer's pushed commit")
	flags.StringVar(&lead.Verdict, "verdict", "", "done: a reviewer's verdict, approve or changes")
	flags.StringVar(&lead.Summary, "summary", "", "done: what you did, or why your verdict")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	env := nocluster.Env{Hosts: d.hosts, Templates: d.templates, Out: d.stdout, Now: d.now,
		CredentialHost: func(ctx context.Context) (nocluster.Bound, error) {
			return nocluster.CredentialHost(ctx, d.getenv, d.runner, *dir)
		},
		Credential: func(ctx context.Context, b nocluster.Bound, hostURL string) (ports.Credentials, error) {
			return nocluster.Credential(ctx, d.getenv, d.runner, b, hostURL)
		}}
	var err error
	switch cmd {
	case "init":
		err = initModel(ctx, d, *dir, nocluster.InitOptions{Project: *project, Remote: *remote, Provider: *provider,
			TheosURL: theosURL, Ref: *ref})
	case "mcp":
		err = serveMCP(ctx, leadEnv(env, d, *dir), d, *dir)
	case "merge":
		err = mergeCommand(ctx, leadEnv(env, d, *dir), d, *dir, lead.Task)
	case "plan":
		err = nocluster.Plan(ctx, env, projectFiles(*dir), nocluster.PlanOptions{New: *newRound})
	case "export":
		// The stream is the output; what export says goes to standard error.
		env.Out = d.stderr
		err = nocluster.Export(ctx, env, os.DirFS(*dir), d.stdout)
	case "import":
		err = importModel(d, *dir, *importFile, *force)
	case "next", "claim", "heartbeat", "release", "done":
		err = leadCommand(ctx, cmd, leadEnv(env, d, *dir), d.stdout, *dir, lead)
	case "scaffold", "protect", "status", "agents", "update":
		var m *nocluster.Model
		if m, err = nocluster.Read(os.DirFS(*dir)); err != nil {
			break
		}
		switch cmd {
		case "agents":
			err = nocluster.Agents(d.stdout, m, *harnessName, os.DirFS(*dir), func(path string, data []byte) error {
				return writeProjectFile(*dir, path, data)
			})
		case "scaffold":
			err = nocluster.Scaffold(ctx, env, m)
		case "protect":
			err = nocluster.Protect(ctx, env, m)
		case "update":
			err = nocluster.Update(ctx, env, m, nocluster.UpdateOptions{Container: *containerName, From: *from})
		default:
			env.Project = os.DirFS(*dir)
			err = nocluster.Status(ctx, env, m)
		}
	default:
		_, _ = fmt.Fprintf(d.stderr, "theos: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	if err != nil {
		_, _ = fmt.Fprintf(d.stderr, "theos %s: %v\n", cmd, err)
		return 1
	}
	return 0
}

// writeProjectFile writes data at path under dir, a path theos agents or
// theos plan renders, making its folder.
func writeProjectFile(dir, path string, data []byte) error {
	full := filepath.Join(dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil { //nolint:gosec // the project's own folder
		return err
	}
	return os.WriteFile(full, data, 0o644) //nolint:gosec // files the project repository commits
}

// projectFiles is the project repository at dir, as theos plan reads and
// writes it.
func projectFiles(dir string) nocluster.ProjectFiles {
	return nocluster.ProjectFiles{FS: os.DirFS(dir),
		Write: func(path string, data []byte) error { return writeProjectFile(dir, path, data) },
		Remove: func(path string) error {
			return os.Remove(filepath.Join(dir, filepath.FromSlash(path))) //nolint:gosec // a round's own file
		}}
}

// initModel writes the model files under dir, refusing to replace any.
func initModel(ctx context.Context, d deps, dir string, opts nocluster.InitOptions) error {
	if opts.Ref == "" {
		return errors.New("this build names no release; pass -ref with the theos release's tag")
	}
	for _, name := range []string{nocluster.ModelFile, nocluster.SetupFile, nocluster.SettingsFile} {
		//nolint:gosec // dir is the person's own -C
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%s exists; init writes a new model only", name)
		}
	}
	if err := nocluster.CheckProject(opts.Project); err != nil {
		return err
	}
	if opts.Remote == "" {
		out, err := d.runner.Run(ctx, "", "git", "-C", dir, "remote", "get-url", "origin")
		if err != nil {
			return fmt.Errorf("reading origin's address (or pass -remote): %w", err)
		}
		opts.Remote = strings.TrimSpace(string(out))
	}
	answers := bufio.NewReader(d.stdin)
	opts.Ask = func(question string) (string, error) {
		_, _ = fmt.Fprint(d.stderr, question)
		answer, err := answers.ReadString('\n')
		if strings.TrimSpace(answer) == "" && err != nil {
			return "", fmt.Errorf("no answer (pass -provider): %w", err)
		}
		if strings.TrimSpace(answer) == "" {
			return "", errors.New("no answer (pass -provider)")
		}
		return answer, nil
	}
	files, err := nocluster.Init(ctx, d.templates, opts)
	if err != nil {
		return err
	}
	// The model is committed to the project repository, as any of its files.
	if err := os.MkdirAll(filepath.Join(dir, "model"), 0o755); err != nil { //nolint:gosec // a repository's folder
		return err
	}
	for _, name := range []string{nocluster.ModelFile, nocluster.SetupFile, nocluster.SettingsFile} {
		//nolint:gosec // a repository's file, holding no credential
		if err := os.WriteFile(filepath.Join(dir, name), files[name], 0o644); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(d.stdout, "wrote %s\n", name)
	}
	return nil
}

// importModel writes the model files theos import makes of file, - for
// standard input, under dir: refusing to replace one unless force, and
// keeping model/settings.yaml.
func importModel(d deps, dir, file string, force bool) error {
	if !force {
		for _, name := range []string{nocluster.ModelFile, nocluster.SetupFile} {
			//nolint:gosec // dir is the person's own -C
			if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("%s exists; pass -force to replace it with the import", name)
			}
		}
	}
	var data []byte
	var err error
	if file == "-" {
		data, err = io.ReadAll(d.stdin)
	} else {
		data, err = os.ReadFile(file) //nolint:gosec // the person's own -f
	}
	if err != nil {
		return err
	}
	settings, err := os.ReadFile(filepath.Join(dir, nocluster.SettingsFile)) //nolint:gosec // dir is the person's own -C
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	imported, err := nocluster.Import(data, settings)
	if err != nil {
		return err
	}
	for _, f := range []struct {
		name string
		data []byte
	}{{nocluster.ModelFile, imported.Story}, {nocluster.SetupFile, imported.Setup}} {
		if err := writeProjectFile(dir, f.name, f.data); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(d.stdout, "wrote %s\n", f.name)
	}
	for _, n := range imported.Notes {
		_, _ = fmt.Fprintln(d.stdout, n)
	}
	return nil
}

// releaseTag is the theos release this binary is: the version a release
// stamps, else the module's when go install fetched a tag; "" for neither.
func releaseTag() string {
	if buildinfo.Version != buildinfo.DevVersion {
		return "v" + strings.TrimPrefix(buildinfo.Version, "v")
	}
	if info, ok := debug.ReadBuildInfo(); ok && strings.HasPrefix(info.Main.Version, "v") &&
		!strings.Contains(info.Main.Version, "-") {
		return info.Main.Version
	}
	return ""
}

// hosts builds a GitProvider's GitHost from the host adapters, on HTTP
// clients that follow a redirect only where the request began (sameOrigin).
type hosts struct{}

// hostTimeout is hostadapter's own default timeout, kept; GitHub's GitHost
// has none, its ctx bounds each call.
const hostTimeout = 30 * time.Second

func (hosts) GitHost(cfg ports.Config) (ports.GitHost, error) {
	if cfg.Kind == "github" {
		return github.Client{HTTP: &http.Client{CheckRedirect: sameOrigin}}.GitHost(cfg)
	}
	return hostadapter.Client{HTTP: &http.Client{Timeout: hostTimeout, CheckRedirect: sameOrigin}}.Provider(cfg)
}

// maxRedirects is net/http's own limit.
const maxRedirects = 10

// sameOrigin follows a redirect only on the scheme, host and port the
// request began on, so a redirect to another host is not followed with the
// credential (#268). Its refusal names the two hosts only.
func sameOrigin(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	first := via[0].URL
	from, errFrom := nocluster.Host(first.Scheme + "://" + first.Host)
	to, errTo := nocluster.Host(req.URL.Scheme + "://" + req.URL.Host)
	if errFrom != nil || errTo != nil || from != to || !strings.EqualFold(first.Scheme, req.URL.Scheme) {
		return fmt.Errorf("a redirect from %s to %s is not followed: theos sends the credential only to the host "+
			"it is for", first.Host, req.URL.Host)
	}
	return nil
}

// execRunner runs a real command. Its error says how the command ended and
// carries none of its output.
type execRunner struct{}

func (execRunner) Run(ctx context.Context, stdin, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // git, with theos's own arguments
	cmd.Stdin = strings.NewReader(stdin)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out.Bytes(), nil
}
