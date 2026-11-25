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
	"context"
	"crypto/rand"
	"errors"
	"go/build"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/nocluster"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/fake"
	"github.com/JairJosafath/theos/internal/scaffold"
)

type fakeHosts struct{ git *fake.Git }

func (h fakeHosts) GitHost(cfg ports.Config) (ports.GitHost, error) { return h.git.For(cfg), nil }

// checkout serves this checkout's templates at any pinned ref, and its
// chart as the theos repository's.
type checkout struct{ t *testing.T }

func (c checkout) FetchTemplate(_ context.Context, url, ref, dir string, _ *ports.Credentials) (scaffold.Tree, error) {
	if ref == "" {
		c.t.Fatalf("fetched %s with no ref", url)
	}
	if dir == "dist/chart" {
		data, err := os.ReadFile(filepath.Join("..", "..", "dist", "chart", "values.yaml"))
		return scaffold.Tree{"values.yaml": scaffold.File{Data: data}}, err
	}
	name := path.Base(url)
	if dir != "" {
		name = path.Base(dir)
	}
	return scaffold.ReadTree(os.DirFS(filepath.Join("..", "..", "templates", name)))
}

// noRunner fails the test when a command is run.
type noRunner struct{ t *testing.T }

func (r noRunner) Run(_ context.Context, _, name string, args ...string) ([]byte, error) {
	r.t.Errorf("ran %s %v", name, args)
	return nil, nil
}

// testDeps runs theos on a fake host with an environment holding vars only.
func testDeps(t *testing.T, vars map[string]string) (deps, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	return deps{hosts: fakeHosts{fake.New()}, templates: checkout{t}, runner: noRunner{t},
		getenv: func(key string) string { return vars[key] },
		stdin:  strings.NewReader(""), stdout: &stdout, stderr: &stderr,
		now: func() time.Time { return time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC) }}, &stdout, &stderr
}

// webshopDir is a project repository holding the webshop sample as model
// files.
func webshopDir(t *testing.T) string {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "model"), 0o755); err != nil {
		t.Fatal(err)
	}
	cat := func(out string, files ...string) {
		docs := make([]string, 0, len(files))
		for _, f := range files {
			data, err := os.ReadFile(filepath.Join("..", "..", "config", "samples", "webshop", f))
			if err != nil {
				t.Fatal(err)
			}
			docs = append(docs, string(data))
		}
		if err := os.WriteFile(filepath.Join(dir, out), []byte(strings.Join(docs, "\n---\n")), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cat(nocluster.ModelFile, "project.yaml", "story.yaml")
	cat(nocluster.SetupFile, "factory.yaml", "bundle.yaml")
	return dir
}

// providerHost is the host of the GitProvider of the model under dir.
func providerHost(t *testing.T, dir string) string {
	m, err := nocluster.Read(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	host, err := nocluster.Host(m.Objects.GitProviders[0].Spec.URL)
	if err != nil {
		t.Fatal(err)
	}
	return host
}

// #268: scaffold, protect and status read the model files under -C and run
// on the host the adapters give, with the token from THEOS_GIT_TOKEN for
// the host THEOS_GIT_HOST names, which no output carries; a second
// scaffold pushes nothing.
func TestCommands268(t *testing.T) {
	token := rand.Text()
	dir := webshopDir(t)
	d, stdout, stderr := testDeps(t, map[string]string{nocluster.TokenVar: token,
		nocluster.HostVar: "http://" + providerHost(t, dir)})
	for _, cmd := range []string{"scaffold", "scaffold", "protect", "status"} {
		if code := run(context.Background(), []string{cmd, "-C", dir}, d); code != 0 {
			t.Fatalf("theos %s exited %d:\n%s", cmd, code, stderr)
		}
	}
	out := stdout.String()
	for _, want := range []string{"orders-api: pushed the initial commit with ", "orders-api: scaffolded already",
		"main takes changes through pull requests", "orders-api: scaffolded at ", "AgentHoldsOwnerCredential: "} {
		if !strings.Contains(out, want) {
			t.Errorf("the output does not say %q:\n%s", want, out)
		}
	}
	if pushes := d.hosts.(fakeHosts).git.PushCount(); pushes != 4 {
		t.Errorf("%d pushes, want one per Container", pushes)
	}
	if strings.Contains(out+stderr.String(), token) {
		t.Error("theos printed the token")
	}
}

// #268: theos status before any scaffold, with no repository on the host
// yet, says in a plain line that each Container is not scaffolded.
func TestStatusOfMissingRepositories268(t *testing.T) {
	dir := webshopDir(t)
	d, stdout, stderr := testDeps(t, map[string]string{nocluster.TokenVar: rand.Text(),
		nocluster.HostVar: "http://" + providerHost(t, dir)})
	if code := run(context.Background(), []string{"status", "-C", dir}, d); code != 0 {
		t.Fatalf("theos status exited %d:\n%s", code, stderr)
	}
	m, err := nocluster.Read(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range m.Containers {
		if want := c.Name + ": not scaffolded; theos scaffold makes "; !strings.Contains(stdout.String(), want) {
			t.Errorf("status does not say %q:\n%s", want, stdout)
		}
	}
}

// originRunner answers git remote get-url origin with origin, failing as
// git does when it is "", and git credential fill with password, and
// records every command.
type originRunner struct {
	origin, password string
	calls            []string
}

func (r *originRunner) Run(_ context.Context, _, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, strings.Join(append([]string{name}, args...), " "))
	switch {
	case slices.Contains(args, "fill"):
		return []byte("password=" + r.password + "\n"), nil
	case r.origin == "" || !slices.Contains(args, "get-url"):
		return nil, errors.New("exit status 2")
	}
	return []byte(r.origin + "\n"), nil
}

// countingHosts counts the GitHosts it builds: none built, no request made.
type countingHosts struct {
	fakeHosts
	built *int
}

func (h countingHosts) GitHost(cfg ports.Config) (ports.GitHost, error) {
	*h.built++
	return h.fakeHosts.GitHost(cfg)
}

// #268: the credential's host is THEOS_GIT_HOST, which THEOS_GIT_TOKEN
// needs beside it; without the token, git credential fill is asked for the
// origin remote's host of the -C directory. With no host, with the token
// and no THEOS_GIT_HOST, and with a GitProvider on another host, scaffold,
// protect and status refuse before any request, naming THEOS_GIT_HOST and
// where the credential's host came from, and print no token.
func TestCredentialHost268(t *testing.T) {
	dir := webshopDir(t)
	host := providerHost(t, dir)
	for name, tc := range map[string]struct {
		vars   map[string]string
		origin string
		helper bool
		code   int
		says   []string
	}{
		"#268 no THEOS_GIT_HOST and no origin remote": {helper: true, code: 1, says: []string{nocluster.HostVar}},
		"#268 THEOS_GIT_HOST on another host": {vars: map[string]string{nocluster.HostVar: "git.example.org"},
			code: 1, says: []string{"git.example.org", host, nocluster.HostVar + "=http://" + host}},
		"#268 THEOS_GIT_HOST with a scheme of one slash": {vars: map[string]string{nocluster.HostVar: "http:/" + host},
			code: 1, says: []string{nocluster.HostVar + " names no host"}},
		"#268 THEOS_GIT_TOKEN with no THEOS_GIT_HOST": {origin: "http://" + host + "/acme/theos-shop.git", code: 1,
			says: []string{nocluster.TokenVar + " is sent only to " + nocluster.HostVar}},
		"#268 the origin remote on another host": {origin: "https://git.example.org/acme/theos-shop.git",
			helper: true, code: 1,
			says: []string{"git.example.org", host, nocluster.HostVar, "from the origin remote of " + dir}},
		"#268 the origin remote on the model's host": {origin: "http://" + host + "/acme/theos-shop.git",
			helper: true},
	} {
		t.Run(name, func(t *testing.T) {
			token := rand.Text()
			vars := map[string]string{nocluster.TokenVar: token}
			if tc.helper {
				vars = map[string]string{}
			}
			maps.Copy(vars, tc.vars)
			d, stdout, stderr := testDeps(t, vars)
			built := 0
			d.hosts = countingHosts{fakeHosts: d.hosts.(fakeHosts), built: &built}
			for _, cmd := range []string{"scaffold", "protect", "status"} {
				r := &originRunner{origin: tc.origin, password: token}
				d.runner = r
				stdout.Reset()
				stderr.Reset()
				code := run(context.Background(), []string{cmd, "-C", dir}, d)
				if code != tc.code {
					t.Fatalf("theos %s exited %d:\n%s", cmd, code, stderr)
				}
				for _, says := range tc.says {
					if !strings.Contains(stderr.String(), says) {
						t.Errorf("theos %s does not say %q:\n%s", cmd, says, stderr)
					}
				}
				if tc.code != 0 && built != 0 {
					t.Errorf("theos %s reached the host %d times before refusing", cmd, built)
				}
				want := []string{}
				if tc.helper {
					want = append(want, "git -C "+dir+" remote get-url origin")
				}
				if tc.helper && tc.code == 0 {
					want = append(want, "git credential fill")
				}
				if !slices.Equal(r.calls, want) {
					t.Errorf("theos %s ran %q, want %q", cmd, r.calls, want)
				}
				if strings.Contains(stdout.String()+stderr.String(), token) {
					t.Errorf("theos %s printed the token", cmd)
				}
			}
			if tc.code == 0 && d.hosts.(countingHosts).git.PushCount() != 4 {
				t.Errorf("%d pushes, want one per Container", d.hosts.(countingHosts).git.PushCount())
			}
		})
	}
}

// recordingTemplates records the url of each template fetch.
type recordingTemplates struct {
	checkout
	urls *[]string
}

func (f recordingTemplates) FetchTemplate(ctx context.Context, url, ref, dir string,
	creds *ports.Credentials) (scaffold.Tree, error) {
	*f.urls = append(*f.urls, url)
	return f.checkout.FetchTemplate(ctx, url, ref, dir, creds)
}

// #268: theos fetches a template over https or http only, as the CRD's
// pattern on a template's url says: a model/setup.yaml whose template url is
// a file url, a path, an ssh url or the scp form is refused when the model
// files are read, before any template is fetched or any host reached.
func TestTemplateURLIsHTTPSOrHTTP268(t *testing.T) {
	m, err := nocluster.Read(os.DirFS(webshopDir(t)))
	if err != nil {
		t.Fatal(err)
	}
	// The sample's first Framework's template url, as the setup spells it.
	url := "url: " + m.Objects.Frameworks[0].Spec.Template.URL + "\n"
	for _, form := range []string{"file:///srv/templates/python-fastapi", "/srv/templates/python-fastapi",
		"ssh://git@templates.example/python-fastapi", "git@templates.example:python-fastapi"} {
		t.Run("#268 "+form, func(t *testing.T) {
			dir := webshopDir(t)
			host := providerHost(t, dir)
			setupPath := filepath.Join(dir, nocluster.SetupFile)
			setup, err := os.ReadFile(setupPath)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(setup), url) {
				t.Fatalf("the sample's setup no longer holds %q", url)
			}
			changed := strings.Replace(string(setup), url, "url: "+form+"\n", 1)
			if err := os.WriteFile(setupPath, []byte(changed), 0o644); err != nil {
				t.Fatal(err)
			}
			token := rand.Text()
			d, stdout, stderr := testDeps(t, map[string]string{nocluster.TokenVar: token,
				nocluster.HostVar: "http://" + host})
			var fetched []string
			built := 0
			d.templates = recordingTemplates{checkout: checkout{t}, urls: &fetched}
			d.hosts = countingHosts{fakeHosts: d.hosts.(fakeHosts), built: &built}
			for _, cmd := range []string{"scaffold", "protect", "status"} {
				if code := run(context.Background(), []string{cmd, "-C", dir}, d); code != 1 {
					t.Errorf("theos %s exited %d, want 1:\n%s", cmd, code, stderr)
				}
			}
			for _, says := range []string{"spec.template.url", "^https?://"} {
				if !strings.Contains(stderr.String(), says) {
					t.Errorf("the refusal does not say %q:\n%s", says, stderr)
				}
			}
			if len(fetched) != 0 || built != 0 || d.hosts.(countingHosts).git.PushCount() != 0 {
				t.Errorf("fetched %q and built %d hosts before refusing", fetched, built)
			}
			if strings.Contains(stdout.String()+stderr.String(), token) {
				t.Error("theos printed the token")
			}
		})
	}
}

// #268: init writes the three model files once, with the release's tag a
// build names or -ref.
func TestInit268(t *testing.T) {
	d, stdout, stderr := testDeps(t, map[string]string{nocluster.TokenVar: rand.Text()})
	dir := t.TempDir()
	args := []string{"init", "-C", dir, "-project", "shop", "-remote", "https://github.com/acme/theos-shop.git"}
	if code := run(context.Background(), append(args, "-ref", ""), d); code != 1 ||
		!strings.Contains(stderr.String(), "-ref") {
		t.Errorf("init with no release exited %d:\n%s", code, stderr)
	}
	if code := run(context.Background(), append(args, "-ref", "v0.9.0"), d); code != 0 {
		t.Fatalf("init exited %d:\n%s", code, stderr)
	}
	m, err := nocluster.Read(os.DirFS(dir))
	if err != nil || m.Project != "shop" || m.Objects.GitProviders[0].Spec.Provider != "github" {
		t.Errorf("init wrote a model that reads %v", err)
	}
	if !strings.Contains(stdout.String(), "wrote "+nocluster.SetupFile) {
		t.Errorf("init said:\n%s", stdout)
	}
	if code := run(context.Background(), append(args, "-ref", "v0.9.0"), d); code != 1 {
		t.Errorf("a second init exited %d", code)
	}
	if code := run(context.Background(), []string{"rebase"}, d); code != 2 {
		t.Errorf("an unknown command exited %d", code)
	}

	// #268: an empty answer to which provider a host is gets a plain refusal.
	for _, answer := range []string{"\n", "   \n", ""} {
		stderr.Reset()
		d.stdin = strings.NewReader(answer)
		code := run(context.Background(), []string{"init", "-C", t.TempDir(), "-project", "shop", "-remote",
			"https://git.example.com/acme/theos-shop.git", "-ref", "v0.9.0"}, d)
		if code != 1 || !strings.Contains(stderr.String(), "no answer (pass -provider)") ||
			strings.Contains(stderr.String(), "%!") {
			t.Errorf("#268 init with the answer %q exited %d:\n%s", answer, code, stderr)
		}
	}
}

// recordingRunner records the commands run and answers none.
type recordingRunner struct{ calls [][]string }

func (r *recordingRunner) Run(_ context.Context, _, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	return nil, errors.New("no command is run here")
}

// #268: init checks the project name, a DNS label, before it reads origin's
// address or anything else.
func TestInitChecksTheProjectNameFirst268(t *testing.T) {
	d, _, stderr := testDeps(t, nil)
	rec := &recordingRunner{}
	d.runner = rec
	dir := t.TempDir()
	code := run(context.Background(), []string{"init", "-C", dir, "-project", "My_App", "-ref", "v0.9.0"}, d)
	if code != 1 || !strings.Contains(stderr.String(), "DNS label") {
		t.Errorf("init of My_App exited %d:\n%s", code, stderr)
	}
	if len(rec.calls) != 0 {
		t.Errorf("init of My_App ran %v, want no command", rec.calls)
	}
	if _, err := os.Stat(filepath.Join(dir, nocluster.ModelFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("init of My_App wrote %s: %v", nocluster.ModelFile, err)
	}
}

// #268: cmd/theos wires the host adapters onto internal/nocluster, as only
// cmd/main.go does for the operator, and links no controller-runtime and no
// client-go (docs/architecture.md).
func TestImports(t *testing.T) {
	const module = "github.com/JairJosafath/theos/"
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pkg.Imports {
		switch rel := strings.TrimPrefix(p, module); {
		case rel == "internal/buildinfo", rel == "internal/modelfacts", rel == "internal/nocluster",
			rel == "internal/ports", rel == "internal/model":
		// The host adapters and plain Git; an adapter that needs a cluster
		// (client-go) fails the dependency check below.
		case strings.HasPrefix(rel, "internal/adapters/"):
		case strings.HasPrefix(p, module), strings.Contains(p, "k8s.io/"):
			t.Errorf("cmd/theos imports %s", p)
		}
	}
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	for dep := range strings.FieldsSeq(string(out)) {
		if strings.HasPrefix(dep, "sigs.k8s.io/controller-runtime") || strings.HasPrefix(dep, "k8s.io/client-go") {
			t.Errorf("cmd/theos depends on %s", dep)
		}
	}
}

// #268: the GitHosts theos builds follow a redirect only on the scheme,
// host and port the request began on: a redirect to another host is not
// followed with the credential, whichever redirect status the host answers.
func TestRedirectToAnotherHost268(t *testing.T) {
	token := rand.Text()
	var reached, carried atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		if strings.Contains(r.Header.Get("Authorization"), token) {
			carried.Add(1)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer other.Close()
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, other.URL+r.URL.Path, status)
		}))
		for kind, call := range map[string]func(ports.GitHost) error{
			"": func(h ports.GitHost) error { // hostadapter's, the kind left out

				_, err := h.EnsureRepository(context.Background(), "orders-api", "")
				return err
			},
			"github": func(h ports.GitHost) error { _, err := h.Login(context.Background()); return err },
		} {
			reached.Store(0)
			carried.Store(0)
			h, err := hosts{}.GitHost(ports.Config{Kind: kind, URL: first.URL, Owner: "webshop",
				Credentials: ports.Credentials{Username: "shop-owner", Token: token}})
			if err != nil {
				t.Fatal(err)
			}
			err = call(h)
			if reached.Load() != 0 || carried.Load() != 0 {
				t.Errorf("#268 %s, %d redirect: %d requests reached the other host, %d with the credential",
					kind, status, reached.Load(), carried.Load())
			}
			if err == nil || strings.Contains(err.Error(), token) {
				t.Errorf("#268 %s, %d redirect: %v", kind, status, err)
			}
		}
		first.Close()
	}
}

// #268: a redirect is followed only on the scheme, host and port the
// request began on; a hop to http from https on the same host, to another
// port, to another name of the machine or to a host outside ASCII is not.
func TestSameOrigin268(t *testing.T) {
	via := func(raw string) []*http.Request {
		req, err := http.NewRequest(http.MethodGet, raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		return []*http.Request{req}
	}
	first := via("https://git.example.com/api/v1/user")
	for to, follows := range map[string]bool{
		"https://git.example.com/api/v1/users/x":     true,
		"https://GIT.example.com:443/api/v1/users/x": true,
		"http://git.example.com/api/v1/users/x":      false,
		"https://git.example.com:8443/x":             false,
		"https://api.git.example.com/x":              false,
		"https://localhost/x":                        false,
		"https://xn--gt-qja.example.com/x":           false,
		"https://g%C4%B0t.example.com/x":             false,
	} {
		err := sameOrigin(via(to)[0], first)
		if (err == nil) != follows {
			t.Errorf("#268 a redirect to %s: %v, want followed %v", to, err, follows)
		}
	}
	long := slices.Repeat(first, maxRedirects)
	if err := sameOrigin(via("https://git.example.com/x")[0], long); err == nil {
		t.Errorf("#268 a redirect after %d: followed", maxRedirects)
	}
}

// #293: theos plan starts a round in the project repository under -C, and
// run again applies it; a round with no change and no issue needs no host.
func TestPlan293(t *testing.T) {
	dir := webshopDir(t)
	d, stdout, stderr := testDeps(t, nil)
	if code := run(context.Background(), []string{"plan", "-C", dir}, d); code != 0 {
		t.Fatalf("theos plan exited %d:\n%s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "plan", "model.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plan", "choices.md"), []byte("- Kept the shop.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := run(context.Background(), []string{"plan", "-C", dir}, d); code != 0 {
		t.Fatalf("theos plan exited %d:\n%s", code, stderr)
	}
	for _, want := range []string{"round 1: wrote plan/model.yaml", "model/model.yaml: unchanged",
		"the round files no issues"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("theos plan does not say %q:\n%s", want, stdout)
		}
	}
	if code := run(context.Background(), []string{"plan", "-C", dir, "-new"}, d); code != 0 {
		t.Fatalf("theos plan -new exited %d:\n%s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "plan", "choices.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("theos plan -new left plan/choices.md: %v", err)
	}
}

// #295: theos update, export and import run from the command line: update
// at the Standards' refs opens nothing, export prints the stream on
// standard output and what it changed on standard error, and import writes
// the model files from it, refusing to replace them without -force.
func TestMoveCommands295(t *testing.T) {
	token := rand.Text()
	dir := webshopDir(t)
	// The webshop's Agents and its Crew, which a cluster's export holds too.
	crew, err := os.ReadFile(filepath.Join("..", "..", "config", "samples", "webshop", "crew.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	setup, err := os.ReadFile(filepath.Join(dir, nocluster.SetupFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, nocluster.SetupFile), slices.Concat(setup, []byte("\n---\n"), crew),
		0o644); err != nil {
		t.Fatal(err)
	}
	d, stdout, stderr := testDeps(t, map[string]string{nocluster.TokenVar: token,
		nocluster.HostVar: "http://" + providerHost(t, dir)})
	for _, cmd := range []string{"scaffold", "update"} {
		if code := run(context.Background(), []string{cmd, "-C", dir}, d); code != 0 {
			t.Fatalf("theos %s exited %d:\n%s", cmd, code, stderr)
		}
	}
	if !strings.Contains(stdout.String(), "orders-api: the files of the Standard python-standard are at ") {
		t.Errorf("theos update at the Standards' refs said:\n%s", stdout)
	}
	if code := run(context.Background(), []string{"update", "-C", dir, "-from", "v1.0.0"}, d); code != 1 {
		t.Errorf("theos update -from without -container exited %d", code)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{"export", "-C", dir}, d); code != 0 {
		t.Fatalf("theos export exited %d:\n%s", code, stderr)
	}
	stream := stdout.String()
	if !strings.Contains(stream, "\n  adopt: true\n") ||
		!strings.Contains(stderr.String(), "Container orders-api: adopt: true") ||
		strings.Contains(stream, "Container orders-api: adopt") {
		t.Errorf("theos export printed:\n%s\nand said:\n%s", stream, stderr)
	}

	d.stdin = strings.NewReader(stream)
	if code := run(context.Background(), []string{"import", "-C", dir}, d); code != 1 ||
		!strings.Contains(stderr.String(), "pass -force") {
		t.Errorf("theos import over the model exited %d:\n%s", code, stderr)
	}
	exported := filepath.Join(t.TempDir(), "project.yaml")
	if err := os.WriteFile(exported, []byte(stream), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := run(context.Background(), []string{"import", "-C", dir, "-f", exported, "-force"}, d); code != 0 {
		t.Fatalf("theos import -force exited %d:\n%s", code, stderr)
	}
	m, err := nocluster.Read(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Crews) != 4 {
		t.Errorf("imported %d Crews, want orders-api's and the three export added", len(m.Crews))
	}
	for _, c := range m.Containers {
		if !c.Spec.Adopt {
			t.Errorf("the imported Container %s does not adopt", c.Name)
		}
	}
	if !strings.Contains(stdout.String(), "wrote model/model.yaml") ||
		strings.Contains(stdout.String()+stderr.String(), token) {
		t.Errorf("theos import said:\n%s", stdout)
	}
}
