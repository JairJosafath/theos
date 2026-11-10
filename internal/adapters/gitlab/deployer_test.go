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

package gitlab

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/JairJosafath/theos/internal/adapters/gitlab/gitlabtest"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
)

// projects numbers the fake's projects, one fresh per target.
var projects atomic.Int64

// newTarget adds a fresh project to gl, its own token "t0ken-N", and returns
// a target on it whose Secret holds token: the project's, or a wrong one.
func newTarget(gl *gitlabtest.Server, token string) (ports.Target, *gitlabtest.Project) {
	n := projects.Add(1)
	path := fmt.Sprintf("contract/orders-%d", n)
	p := gl.AddProject(path, fmt.Sprintf("t0ken-%d", n))
	if token == "t0ken" {
		token = fmt.Sprintf("t0ken-%d", n)
	}
	return ports.Target{Name: "contract/" + path, Member: model.DeployMemberGitLab,
		Settings: map[string]string{model.GitLabSettingURL: gl.URL(), model.GitLabSettingProject: path,
			model.GitLabSettingTagPrefix: "cli/"},
		Secret: map[string][]byte{model.GitLabSecretToken: []byte(token)}}, p
}

// marked counts the releases of p that the factory made for app.
func marked(p *gitlabtest.Project, app string) int {
	n := 0
	for _, r := range p.Releases() {
		if strings.Contains(r.Description, model.ShipMarkerPrefix(app)) {
			n++
		}
	}
	return n
}

func file(name, content string) ports.File {
	return ports.File{Name: name, Size: int64(len(content)),
		Open: func(context.Context) (io.ReadCloser, int64, error) {
			return io.NopCloser(strings.NewReader(content)), int64(len(content)), nil
		}}
}

// shipment is v1.0.0 of the app with files.
func shipment(files ...ports.File) ports.Shipment {
	return ports.Shipment{App: "acme/orders-cli", DisplayName: "Orders CLI", Version: "v1.0.0",
		Notes: "notes v1.0.0", Files: files}
}

// The gitlab member passes the Deployer contract against the fake GitLab:
// each target a fresh project, a wrong token refused, a deploy a marked
// release.
func TestDeployerContract(t *testing.T) {
	gl := gitlabtest.New(t)
	byPath := map[string]*gitlabtest.Project{}
	target := func(token string) ports.Target {
		tg, p := newTarget(gl, token)
		byPath[p.Path] = p
		return tg
	}
	contract.Deployer(t, contract.DeployerEnv{
		Deployer: New(nil),
		Target:   func(*testing.T) ports.Target { return target("t0ken") },
		Refused:  func(*testing.T) ports.Target { return target("wrong") },
		Deploys: func(_ *testing.T, tg ports.Target, app string) int {
			return marked(byPath[tg.Settings[model.GitLabSettingProject]], app)
		},
	})
}

// A token without the api scope can't make a release: Check refuses the
// credential, naming the scope (docs/plans/gitlab-deploy.md, Decision 2).
func TestCheckScopes(t *testing.T) {
	gl := gitlabtest.New(t)
	tg, p := newTarget(gl, "t0ken")
	p.Scopes = []string{"read_api", "write_registry"}
	err := New(nil).Check(context.Background(), tg)
	if !errors.Is(err, ports.ErrCredentialRefused) || !strings.Contains(err.Error(), "api scope") {
		t.Fatalf("Check() = %v, want ErrCredentialRefused naming the api scope", err)
	}
}

// A release of the tag that the factory didn't make is left alone: Ship fails
// with reason TagTaken, neither unreachable nor a refused credential, and
// nothing is written.
func TestShipTagTaken(t *testing.T) {
	gl := gitlabtest.New(t)
	tg, p := newTarget(gl, "t0ken")
	p.AddRelease("cli/v1.0.0", "someone else's release")
	err := New(nil).Ship(context.Background(), tg, shipment(file("orders-cli_linux.tar.gz", "bin")))
	re, ok := errors.AsType[*ports.ReleaseError](err)
	if !ok || re.Reason != model.PublishTagTaken {
		t.Fatalf("Ship() = %v, want a ReleaseError with reason TagTaken", err)
	}
	if errors.Is(err, ports.ErrUnreachable) || errors.Is(err, ports.ErrCredentialRefused) {
		t.Fatalf("Ship() = %v, want no port error", err)
	}
	if n := len(p.Files("orders-cli", "v1.0.0")); n != 0 {
		t.Errorf("files uploaded = %d, want 0", n)
	}
}

// A package file of another size than the release's is deleted and uploaded
// again before the release is made, and a release half shipped is Deploying.
func TestShipReplacesFile(t *testing.T) {
	ctx := context.Background()
	gl := gitlabtest.New(t)
	tg, p := newTarget(gl, "t0ken")
	d := New(nil)
	if err := d.Ship(ctx, tg, shipment(file("orders-cli_linux.tar.gz", "short"), file("SHA256SUMS", "s"))); err != nil {
		t.Fatalf("Ship() = %v", err)
	}
	if got := p.Files("orders-cli", "cli_2fv1.0.0"); got["orders-cli_linux.tar.gz"] != 5 {
		t.Fatalf("files after the first ship = %v, want the binary at 5 bytes", got)
	}
	state, err := d.State(ctx, tg, "acme/orders-cli", "v1.0.0")
	if err != nil || state.Phase != ports.PhaseDeploying || state.Version != "v1.0.0" {
		t.Fatalf("State() mid-ship = %+v, %v, want Deploying v1.0.0", state, err)
	}
	// CI rebuilt the file: it comes back longer.
	s := shipment(file("orders-cli_linux.tar.gz", "a longer binary"), file("SHA256SUMS", "s"))
	for i := 0; i < 3 && marked(p, s.App) == 0; i++ {
		if err := d.Ship(ctx, tg, s); err != nil {
			t.Fatalf("Ship() %d = %v", i, err)
		}
	}
	if got := p.Files("orders-cli", "cli_2fv1.0.0"); len(got) != 2 || got["orders-cli_linux.tar.gz"] != 15 {
		t.Fatalf("files = %v, want the binary replaced at 15 bytes and the sums", got)
	}
	rels := p.Releases()
	if len(rels) != 1 || rels[0].Tag != "cli/v1.0.0" || len(rels[0].Links) != 2 {
		t.Fatalf("releases = %+v, want one of cli/v1.0.0 with two links", rels)
	}
	if l := rels[0].Links[0]; l.LinkType != "package" || !strings.HasSuffix(l.URL,
		"/packages/generic/orders-cli/cli_2fv1.0.0/orders-cli_linux.tar.gz") || l.DirectAssetPath != "/orders-cli_linux.tar.gz" {
		t.Errorf("link = %+v, want a package link to the generic download URL", l)
	}
	state, err = d.State(ctx, tg, "acme/orders-cli", "")
	if err != nil || state.Phase != ports.PhaseDeployed || state.Version != "v1.0.0" || state.URL == "" {
		t.Fatalf("State() = %+v, %v, want Deployed v1.0.0 at its page", state, err)
	}
}

// Two Containers of one name, in different Projects and with different tag
// prefixes, ship one version to one GitLab project: each release links its
// own files, at its own package version, and State tells them apart (#1873).
func TestShipTwoAppsOfOneName(t *testing.T) {
	ctx := context.Background()
	gl := gitlabtest.New(t)
	a, p := newTarget(gl, "t0ken")
	b := a
	b.Settings = maps.Clone(a.Settings)
	b.Settings[model.GitLabSettingTagPrefix] = "b-"
	d := New(nil)
	sa := shipment(file("web_linux.tar.gz", "the first app"), file("SHA256SUMS", "s"))
	sa.App, sa.DisplayName = "ns1/web", "Web one"
	sb := shipment(file("web_linux.tar.gz", "the second app, longer"), file("SHA256SUMS", "s"))
	sb.App, sb.DisplayName = "ns2/web", "Web two"
	for i := 0; i < 4 && (marked(p, sa.App) == 0 || marked(p, sb.App) == 0); i++ {
		for _, x := range []struct {
			t ports.Target
			s ports.Shipment
		}{{a, sa}, {b, sb}} {
			if err := d.Ship(ctx, x.t, x.s); err != nil {
				t.Fatalf("Ship(%s) %d = %v", x.s.App, i, err)
			}
		}
	}
	if got := p.Files("web", "cli_2fv1.0.0"); len(got) != 2 || got["web_linux.tar.gz"] != 13 {
		t.Errorf("files of ns1/web = %v, want its binary at 13 bytes and the sums", got)
	}
	if got := p.Files("web", "b-v1.0.0"); len(got) != 2 || got["web_linux.tar.gz"] != 22 {
		t.Errorf("files of ns2/web = %v, want its binary at 22 bytes and the sums", got)
	}
	links := map[string]string{}
	for _, r := range p.Releases() {
		if len(r.Links) != 2 {
			t.Fatalf("release %s links = %+v, want two", r.Tag, r.Links)
		}
		links[r.Tag] = r.Links[0].URL
	}
	for tag, want := range map[string]string{"cli/v1.0.0": "/packages/generic/web/cli_2fv1.0.0/web_linux.tar.gz",
		"b-v1.0.0": "/packages/generic/web/b-v1.0.0/web_linux.tar.gz"} {
		if !strings.HasSuffix(links[tag], want) {
			t.Errorf("release %s links %q, want its own package file %s", tag, links[tag], want)
		}
	}
	for _, x := range []struct {
		t   ports.Target
		app string
	}{{a, "ns1/web"}, {b, "ns2/web"}} {
		state, err := d.State(ctx, x.t, x.app, "")
		if err != nil || state.Phase != ports.PhaseDeployed || state.Version != "v1.0.0" {
			t.Errorf("State(%s) = %+v, %v, want Deployed v1.0.0", x.app, state, err)
		}
	}
}

// The package registry being off fails the entry with GitLab's message, not
// unreachable, so it isn't retried forever.
func TestShipPackagesOff(t *testing.T) {
	gl := gitlabtest.New(t)
	tg, p := newTarget(gl, "t0ken")
	p.PackagesEnabled = false
	err := New(nil).Ship(context.Background(), tg, shipment(file("orders-cli_linux.tar.gz", "bin")))
	re, ok := errors.AsType[*ports.ReleaseError](err)
	if !ok || re.Reason != ReasonGitLabRejected || errors.Is(err, ports.ErrUnreachable) {
		t.Fatalf("Ship() = %v, want GitLabRejected, not a port error", err)
	}
}

// A tag at another commit than the shipment's source commit, with no release,
// is someone else's too: TagTaken, and nothing is written.
func TestShipTagAtAnotherCommit(t *testing.T) {
	gl := gitlabtest.New(t)
	tg, p := newTarget(gl, "t0ken")
	p.SetTag("cli/v1.0.0", strings.Repeat("a", 40))
	s := shipment(file("orders-cli_linux.tar.gz", "bin"))
	s.Commit = strings.Repeat("b", 40)
	err := New(nil).Ship(context.Background(), tg, s)
	re, ok := errors.AsType[*ports.ReleaseError](err)
	if !ok || re.Reason != model.PublishTagTaken {
		t.Fatalf("Ship() = %v, want a ReleaseError with reason TagTaken", err)
	}
	if n := len(p.Files("orders-cli", "v1.0.0")); n != 0 {
		t.Errorf("files uploaded = %d, want 0", n)
	}
}

// racing makes the fake's release appear between Ship's read and its write,
// as another reconcile would: CreateRelease then gets GitLab's 409.
type racing struct {
	GitLabProject
	p *gitlabtest.Project
}

func (r racing) CreateRelease(ctx context.Context, rel GitLabRelease) (model.TargetRelease, error) {
	r.p.AddRelease(rel.Tag, rel.Description)
	return r.GitLabProject.CreateRelease(ctx, rel)
}

type racingProjects struct{ p *gitlabtest.Project }

func (r racingProjects) GitLab(cfg GitLabConfig) (GitLabProject, error) {
	c, err := Client{}.GitLab(cfg)
	if err != nil {
		return nil, err
	}
	return racing{GitLabProject: c, p: r.p}, nil
}

// A release made since Ship read the tag (a second reconcile) answers 409:
// Ship finds it there and is done, and the entry doesn't fail.
func TestShipReleaseRace(t *testing.T) {
	ctx := context.Background()
	gl := gitlabtest.New(t)
	tg, p := newTarget(gl, "t0ken")
	d := New(racingProjects{p: p})
	s := shipment(file("orders-cli_linux.tar.gz", "bin"))
	for i := 0; i < 3 && marked(p, s.App) == 0; i++ {
		if err := d.Ship(ctx, tg, s); err != nil {
			t.Fatalf("Ship() %d = %v, want the 409 taken as done", i, err)
		}
	}
	if rels := p.Releases(); len(rels) != 1 || marked(p, s.App) != 1 {
		t.Fatalf("releases = %+v, want the one marked release", rels)
	}
}

// An empty project has no default branch, so there is no commit to tag: Ship
// fails the entry saying so, not a 422 that reads as nothing.
func TestShipEmptyProject(t *testing.T) {
	ctx := context.Background()
	gl := gitlabtest.New(t)
	tg, p := newTarget(gl, "t0ken")
	p.DefaultBranch = ""
	d := New(nil)
	s := shipment(file("orders-cli_linux.tar.gz", "bin"))
	var err error
	for i := 0; i < 3 && err == nil; i++ {
		err = d.Ship(ctx, tg, s)
	}
	re, ok := errors.AsType[*ports.ReleaseError](err)
	if !ok || re.Reason != ReasonGitLabRejected || !strings.Contains(re.Message, "is empty") {
		t.Fatalf("Ship() = %v, want GitLabRejected saying the project is empty", err)
	}
	if errors.Is(err, ports.ErrUnreachable) || errors.Is(err, ports.ErrCredentialRefused) {
		t.Fatalf("Ship() = %v, want no port error", err)
	}
}

// A Secret without the token refuses the credential; a project setting that
// isn't a full path is the target's mistake, not the credential's.
func TestCheckConfig(t *testing.T) {
	ctx := context.Background()
	gl := gitlabtest.New(t)
	tg, _ := newTarget(gl, "t0ken")
	tg.Secret = map[string][]byte{}
	if err := New(nil).Check(ctx, tg); !errors.Is(err, ports.ErrCredentialRefused) {
		t.Fatalf("Check() without the token = %v, want ErrCredentialRefused", err)
	}
	tg, _ = newTarget(gl, "t0ken")
	tg.Settings[model.GitLabSettingProject] = "orders"
	err := New(nil).Check(ctx, tg)
	if err == nil || errors.Is(err, ports.ErrCredentialRefused) || !strings.Contains(err.Error(), "full path") {
		t.Fatalf("Check() with a one-segment project = %v, want a config error, not a refused credential", err)
	}
}

// source is a release's source of files, by path, and a ReadSource that
// streams them; a path in links is a symlink to its content.
func source(files map[string]string, links ...string) (*model.ReleaseSource, func(context.Context,
	func(string, int64, io.Reader) error) error) {
	return sourceWith(files, nil, links...)
}

// sourceWith is source with the paths in executables mode 100755.
func sourceWith(files map[string]string, executables []string, links ...string) (*model.ReleaseSource,
	func(context.Context, func(string, int64, io.Reader) error) error) {
	paths := slices.Sorted(maps.Keys(files))
	hashed := make([]model.SourceFile, 0, len(paths))
	for _, p := range paths {
		mode := model.ModeFile
		switch {
		case slices.Contains(links, p):
			mode = model.ModeSymlink
		case slices.Contains(executables, p):
			mode = model.ModeExecutable
		}
		hashed = append(hashed, model.SourceFile{Path: p, Mode: mode, Size: int64(len(files[p])),
			Blob: model.BlobSHA([]byte(files[p]))})
	}
	src, err := model.NewSource(model.CI{}, hashed, "")
	if err != nil {
		panic(err)
	}
	return src, func(_ context.Context, each func(string, int64, io.Reader) error) error {
		for _, p := range paths {
			if err := each(p, int64(len(files[p])), strings.NewReader(files[p])); err != nil {
				return err
			}
		}
		return nil
	}
}

// shipAll ships s until its release is out, at most n writes.
func shipAll(t *testing.T, d *Deployer, tg ports.Target, p *gitlabtest.Project, s ports.Shipment, n int) {
	t.Helper()
	for i := 0; i < n && marked(p, s.App) == 0; i++ {
		if err := d.Ship(context.Background(), tg, s); err != nil {
			t.Fatalf("Ship() %s %d = %v", s.Version, i, err)
		}
	}
	if marked(p, s.App) == 0 {
		t.Fatalf("no release of %s after %d writes", s.Version, n)
	}
}

// A target with a source folder publishes each release's source first, as
// one commit on the default branch under the folder and a source tag at it,
// and the release is of that commit (#1805, docs/plans/gitlab-deploy.md
// Decision 4). The next release's commit updates and deletes, and State
// reports the folder at its tag.
func TestShipSource(t *testing.T) {
	ctx := context.Background()
	gl := gitlabtest.New(t)
	tg, p := newTarget(gl, "t0ken")
	tg.Settings[model.GitHubSettingSourcePath] = "orders-api"
	d := New(nil)
	s := shipment(file("orders-cli_linux.tar.gz", "bin"))
	s.Source, s.ReadSource = sourceWith(map[string]string{"go.mod": "module example.com/orders\n",
		"main.go": "package main // v1.0.0\n", "cmd/run.sh": "#!/bin/sh\n"}, []string{"cmd/run.sh"})
	shipAll(t, d, tg, p, s, 5)

	commit := p.Tag("orders-api/v1.0.0")
	if len(commit) != 40 {
		t.Fatalf("source tag names %q, want a commit", commit)
	}
	want := map[string]string{"orders-api/go.mod": "module example.com/orders\n",
		"orders-api/main.go": "package main // v1.0.0\n", "orders-api/cmd/run.sh": "#!/bin/sh\n"}
	if got := p.Folder("orders-api/v1.0.0", "orders-api"); !maps.Equal(got, want) {
		t.Errorf("folder at the source tag = %v, want %v", got, want)
	}
	if got := p.Mode("orders-api/v1.0.0", "orders-api/cmd/run.sh"); got != "100755" {
		t.Errorf("mode of cmd/run.sh = %q, want 100755: a chmod after the create", got)
	}
	if rels := p.Releases(); len(rels) != 1 || p.Tag(rels[0].Tag) != commit {
		t.Errorf("releases = %+v, want one whose tag is at the source commit %s", rels, commit)
	}
	state, err := d.State(ctx, tg, s.App, "v1.0.0")
	if err != nil || state.Phase != ports.PhaseDeployed || state.Source == nil || state.Source.Commit != commit ||
		state.Source.Tag != "orders-api/v1.0.0" ||
		state.Source.URL != gl.URL()+"/"+tg.Settings[model.GitLabSettingProject]+"/-/tree/orders-api/v1.0.0/orders-api" {
		t.Fatalf("State() = %+v, %v, want Deployed with the source at its tag", state, err)
	}

	// v1.1.0 changes main.go, drops cmd/run.sh, adds README.md and makes
	// go.mod executable (a chmod alone).
	s2 := shipment(file("orders-cli_linux.tar.gz", "bin2"))
	s2.Version, s2.Notes = "v1.1.0", "notes v1.1.0"
	s2.Source, s2.ReadSource = sourceWith(map[string]string{"go.mod": "module example.com/orders\n",
		"main.go": "package main // v1.1.0\n", "README.md": "# orders\n"}, []string{"go.mod"})
	for i := 0; i < 5 && len(p.Releases()) < 2; i++ {
		if err := d.Ship(ctx, tg, s2); err != nil {
			t.Fatalf("Ship() v1.1.0 %d = %v", i, err)
		}
	}
	want = map[string]string{"orders-api/go.mod": "module example.com/orders\n",
		"orders-api/main.go": "package main // v1.1.0\n", "orders-api/README.md": "# orders\n"}
	if got := p.Folder("orders-api/v1.1.0", "orders-api"); !maps.Equal(got, want) {
		t.Errorf("folder at orders-api/v1.1.0 = %v, want %v", got, want)
	}
	if got := p.Folder("orders-api/v1.0.0", "orders-api"); got["orders-api/main.go"] != "package main // v1.0.0\n" {
		t.Errorf("folder at orders-api/v1.0.0 = %v, want v1.0.0's still", got)
	}
	if got := p.Mode("orders-api/v1.1.0", "orders-api/go.mod"); got != "100755" {
		t.Errorf("mode of go.mod at v1.1.0 = %q, want 100755", got)
	}
	if c2 := p.Tag("orders-api/v1.1.0"); c2 == "" || c2 == commit || p.Tag("cli/v1.1.0") != c2 {
		t.Errorf("source tag v1.1.0 at %q, release tag at %q, want a new commit both name", c2, p.Tag("cli/v1.1.0"))
	}
}

// A commit that landed on the default branch between the read and the
// source commit fails BranchMoved, as GitHub's path does, so the next poll
// decides again; the stale change is not written over it.
func TestShipSourceBranchMoved(t *testing.T) {
	ctx := context.Background()
	gl := gitlabtest.New(t)
	tg, p := newTarget(gl, "t0ken")
	tg.Settings[model.GitHubSettingSourcePath] = "orders-api"
	c, err := Client{}.GitLab(GitLabConfig{URL: gl.URL(),
		Project: tg.Settings[model.GitLabSettingProject], Token: string(tg.Secret[model.GitLabSecretToken])})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateCommit(ctx, GitLabCommit{Branch: p.DefaultBranch, Message: "theirs",
		Actions: []GitLabCommitAction{{Action: "create", FilePath: "orders-api/main.go", Content: "theirs"}}}); err != nil {
		t.Fatal(err)
	}
	stale := p.DefaultBranchHead
	if _, err := c.CreateCommit(ctx, GitLabCommit{Branch: p.DefaultBranch, Message: "theirs again",
		Actions: []GitLabCommitAction{{Action: "update", FilePath: "orders-api/main.go", Content: "theirs 2"}}}); err != nil {
		t.Fatal(err)
	}
	s := shipment(file("orders-cli_linux.tar.gz", "bin"))
	s.Source, s.ReadSource = source(map[string]string{"main.go": "package main\n"})
	src, _, _ := sourceTarget(tg, s.Version)
	step := &model.SourceCommit{Path: src.Path, Message: "ours"}
	actions := model.GitLabSourceActions(s.Source.Files, []model.SourceFile{{Path: "main.go", Mode: model.ModeFile,
		Blob: model.BlobSHA([]byte("theirs"))}})
	_, err = commitSource(ctx, c, s, p.DefaultBranch, stale, step, actions)
	re, ok := errors.AsType[*ports.ReleaseError](err)
	if !ok || re.Reason != model.PublishBranchMoved || !strings.Contains(re.Message, "another commit landed") {
		t.Fatalf("commitSource on a moved branch = %v, want BranchMoved", err)
	}
	if got := p.Folder(p.DefaultBranch, "orders-api"); got["orders-api/main.go"] != "theirs 2" {
		t.Errorf("their change was written over: %v", got)
	}
}

// The second release ships when the branch's head commit left alone a file it
// updates (#1950). GitLab compares an action's last_commit_id with the last
// commit that touched that file, not with the head, so a source commit that
// named the head as every file's last_commit_id was refused whenever another
// commit had touched only some other file since, and the release never shipped:
// every poll failed BranchMoved. Each action names its file's own last
// commit instead, and the fake applies GitLab's per-file rule.
func TestShipSourceAfterOutsideCommit(t *testing.T) {
	ctx := context.Background()
	gl := gitlabtest.New(t)
	tg, p := newTarget(gl, "t0ken")
	tg.Settings[model.GitHubSettingSourcePath] = "orders-api"
	d := New(nil)
	s := shipment(file("orders-cli_linux.tar.gz", "bin"))
	s.Source, s.ReadSource = source(map[string]string{"go.mod": "module example.com/orders\n",
		"main.go": "package main // v1.0.0\n"})
	shipAll(t, d, tg, p, s, 5)

	// Someone else's commit touches main.go alone: the head no longer is
	// go.mod's last commit.
	c, err := Client{}.GitLab(GitLabConfig{URL: gl.URL(),
		Project: tg.Settings[model.GitLabSettingProject], Token: string(tg.Secret[model.GitLabSecretToken])})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateCommit(ctx, GitLabCommit{Branch: p.DefaultBranch, Message: "theirs",
		Actions: []GitLabCommitAction{{Action: "update", FilePath: "orders-api/main.go",
			Content: "package main // theirs\n"}}}); err != nil {
		t.Fatal(err)
	}

	// v1.1.0 updates go.mod alone (its main.go is theirs).
	s2 := shipment(file("orders-cli_linux.tar.gz", "bin2"))
	s2.Version, s2.Notes = "v1.1.0", "notes v1.1.0"
	s2.Source, s2.ReadSource = source(map[string]string{"go.mod": "module example.com/orders/v2\n",
		"main.go": "package main // theirs\n"})
	for i := 0; i < 5 && len(p.Releases()) < 2; i++ {
		if err := d.Ship(ctx, tg, s2); err != nil {
			t.Fatalf("Ship() v1.1.0 %d = %v, want the source commit to land (#1950)", i, err)
		}
	}
	want := map[string]string{"orders-api/go.mod": "module example.com/orders/v2\n",
		"orders-api/main.go": "package main // theirs\n"}
	if got := p.Folder("orders-api/v1.1.0", "orders-api"); !maps.Equal(got, want) {
		t.Errorf("folder at orders-api/v1.1.0 = %v, want %v", got, want)
	}
	if rels := p.Releases(); len(rels) != 2 || p.Tag(rels[0].Tag) != p.Tag("orders-api/v1.1.0") {
		t.Errorf("releases = %+v, want v1.1.0 at the source tag's commit", rels)
	}
}

// The source commit lands on the default branch, which exists, after an
// outside commit touched another file since the head was read, and fails
// BranchMoved when that commit touched a file it changes (#1979). It named
// the head as its start_sha, which GitLab reads as starting a new branch and
// refuses on one that exists; each file's own last commit, as its
// last_commit_id, is what tells GitLab the actions are stale.
func TestShipSourceOnExistingBranch(t *testing.T) {
	ctx := context.Background()
	gl := gitlabtest.New(t)
	tg, p := newTarget(gl, "t0ken")
	tg.Settings[model.GitHubSettingSourcePath] = "orders-api"
	c, err := Client{}.GitLab(GitLabConfig{URL: gl.URL(),
		Project: tg.Settings[model.GitLabSettingProject], Token: string(tg.Secret[model.GitLabSecretToken])})
	if err != nil {
		t.Fatal(err)
	}
	theirs := func(path, content string) {
		t.Helper()
		if _, err := c.CreateCommit(ctx, GitLabCommit{Branch: p.DefaultBranch, Message: "theirs",
			Actions: []GitLabCommitAction{{Action: "update", FilePath: path, Content: content}}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.CreateCommit(ctx, GitLabCommit{Branch: p.DefaultBranch, Message: "theirs",
		Actions: []GitLabCommitAction{{Action: "create", FilePath: "orders-api/go.mod", Content: "old"},
			{Action: "create", FilePath: "orders-api/main.go", Content: "old"},
			{Action: "create", FilePath: "README.md", Content: "readme"}}}); err != nil {
		t.Fatal(err)
	}
	head := p.DefaultBranchHead
	theirs("README.md", "readme 2")

	s := shipment(file("orders-cli_linux.tar.gz", "bin"))
	s.Source, s.ReadSource = source(map[string]string{"go.mod": "module example.com/orders\n", "main.go": "old"})
	src, _, _ := sourceTarget(tg, s.Version)
	step := &model.SourceCommit{Path: src.Path, Message: "ours"}
	folder := []model.SourceFile{{Path: "go.mod", Mode: model.ModeFile, Blob: model.BlobSHA([]byte("old"))},
		{Path: "main.go", Mode: model.ModeFile, Blob: model.BlobSHA([]byte("old"))}}
	actions := model.GitLabSourceActions(s.Source.Files, folder)
	if _, err := commitSource(ctx, c, s, p.DefaultBranch, head, step, actions); err != nil {
		t.Fatalf("commitSource to the existing %s = %v, want it to land (#1979)", p.DefaultBranch, err)
	}
	if got := p.Folder(p.DefaultBranch, ""); got["orders-api/go.mod"] != "module example.com/orders\n" ||
		got["README.md"] != "readme 2" {
		t.Errorf("folder = %v, want our go.mod and their README.md", got)
	}

	// Their commit touches main.go after our read: the update is stale.
	head = p.DefaultBranchHead
	theirs("orders-api/main.go", "theirs")
	s.Source, s.ReadSource = source(map[string]string{"go.mod": "module example.com/orders\n", "main.go": "ours"})
	folder[0].Blob = model.BlobSHA([]byte("module example.com/orders\n"))
	actions = model.GitLabSourceActions(s.Source.Files, folder)
	_, err = commitSource(ctx, c, s, p.DefaultBranch, head, step, actions)
	if re, ok := errors.AsType[*ports.ReleaseError](err); !ok || re.Reason != model.PublishBranchMoved {
		t.Fatalf("commitSource of a file their commit touched = %v, want BranchMoved", err)
	}
	if got := p.Folder(p.DefaultBranch, "orders-api"); got["orders-api/main.go"] != "theirs" {
		t.Errorf("their change was written over: %v", got)
	}
}

// The fake refuses start_sha and start_branch on a branch that exists, as
// GitLab does: they start a new branch (#1979).
func TestFakeStartSHAOnExistingBranch(t *testing.T) {
	ctx := context.Background()
	gl := gitlabtest.New(t)
	tg, p := newTarget(gl, "t0ken")
	c, err := Client{}.GitLab(GitLabConfig{URL: gl.URL(),
		Project: tg.Settings[model.GitLabSettingProject], Token: string(tg.Secret[model.GitLabSecretToken])})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateCommit(ctx, GitLabCommit{Branch: p.DefaultBranch, Message: "a",
		Actions: []GitLabCommitAction{{Action: "create", FilePath: "a", Content: "a"}}}); err != nil {
		t.Fatal(err)
	}
	_, err = c.CreateCommit(ctx, GitLabCommit{Branch: p.DefaultBranch, StartSHA: p.DefaultBranchHead,
		Message: "b", Actions: []GitLabCommitAction{{Action: "create", FilePath: "b", Content: "b"}}})
	if re, ok := errors.AsType[*ports.ReleaseError](err); !ok || re.Status != 400 ||
		!strings.Contains(re.Message, "already exists") {
		t.Fatalf("commit with start_sha the head = %v, want a 400: the branch exists", err)
	}
	if last, err := c.FileLastCommit(ctx, p.DefaultBranch, "a"); err != nil || last != p.DefaultBranchHead {
		t.Errorf("FileLastCommit(a) = %q, %v, want %q", last, err, p.DefaultBranchHead)
	}
	if last, err := c.FileLastCommit(ctx, p.DefaultBranch, "none"); err != nil || last != "" {
		t.Errorf("FileLastCommit(none) = %q, %v, want \"\"", last, err)
	}
}

// The fake applies GitLab's rule to last_commit_id: the last commit that
// touched the file, not the branch's head (#1950). A head that left the file
// alone is refused, the file's own last commit accepted.
func TestFakeLastCommitIDIsPerFile(t *testing.T) {
	ctx := context.Background()
	gl := gitlabtest.New(t)
	tg, p := newTarget(gl, "t0ken")
	c, err := Client{}.GitLab(GitLabConfig{URL: gl.URL(),
		Project: tg.Settings[model.GitLabSettingProject], Token: string(tg.Secret[model.GitLabSecretToken])})
	if err != nil {
		t.Fatal(err)
	}
	commit := func(msg string, actions ...GitLabCommitAction) (string, error) {
		return c.CreateCommit(ctx, GitLabCommit{Branch: p.DefaultBranch, Message: msg, Actions: actions})
	}
	both, err := commit("a and b", GitLabCommitAction{Action: "create", FilePath: "a", Content: "a"},
		GitLabCommitAction{Action: "create", FilePath: "b", Content: "b"})
	if err != nil {
		t.Fatal(err)
	}
	head, err := commit("a alone", GitLabCommitAction{Action: "update", FilePath: "a", Content: "a2"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = commit("b at the head", GitLabCommitAction{Action: "update", FilePath: "b", Content: "b2",
		LastCommitID: head})
	if re, ok := errors.AsType[*ports.ReleaseError](err); !ok || re.Status != 400 ||
		!strings.Contains(re.Message, "has changed since") {
		t.Fatalf("update of b with the head as last_commit_id = %v, want a 400: the head left b alone", err)
	}
	if _, err := commit("b at its last commit", GitLabCommitAction{Action: "update", FilePath: "b",
		Content: "b2", LastCommitID: both}); err != nil {
		t.Fatalf("update of b with its own last commit = %v, want it to land", err)
	}
	if got := p.Folder(p.DefaultBranch, ""); got["b"] != "b2" || got["a"] != "a2" {
		t.Errorf("folder = %v", got)
	}
}

// A source with a symlink fails SourceUnsupported, naming it, before
// anything is written: GitLab's Commits API carries none (#1805).
func TestShipSourceSymlink(t *testing.T) {
	gl := gitlabtest.New(t)
	tg, p := newTarget(gl, "t0ken")
	tg.Settings[model.GitHubSettingSourcePath] = "orders-api"
	s := shipment(file("orders-cli_linux.tar.gz", "bin"))
	s.Source, s.ReadSource = source(map[string]string{"main.go": "package main\n", "run": "main.go"}, "run")
	err := New(nil).Ship(context.Background(), tg, s)
	re, ok := errors.AsType[*ports.ReleaseError](err)
	if !ok || re.Reason != model.PublishSourceUnsupported || !strings.Contains(re.Message, "run is a symlink") {
		t.Fatalf("Ship() = %v, want a ReleaseError SourceUnsupported naming run", err)
	}
	if got := p.Folder(p.DefaultBranch, ""); len(got) != 0 || len(p.Files("orders-cli", "v1.0.0")) != 0 {
		t.Errorf("written: folder %v, files %v; want nothing", got, p.Files("orders-cli", "v1.0.0"))
	}
}

// Without a source folder the shipment's source is ignored: the release goes
// on the default branch and no commit is made.
func TestShipNoSourceSetting(t *testing.T) {
	gl := gitlabtest.New(t)
	tg, p := newTarget(gl, "t0ken")
	s := shipment(file("orders-cli_linux.tar.gz", "bin"))
	s.Source, s.ReadSource = source(map[string]string{"main.go": "package main\n"})
	shipAll(t, New(nil), tg, p, s, 5)
	if got := p.Folder(p.DefaultBranch, ""); len(got) != 0 || p.Tag("orders-api/v1.0.0") != "" {
		t.Errorf("folder %v, source tag %q; want the branch untouched", got, p.Tag("orders-api/v1.0.0"))
	}
	if state, err := New(nil).State(context.Background(), tg, s.App, "v1.0.0"); err != nil || state.Source != nil {
		t.Errorf("State() = %+v, %v, want no source", state, err)
	}
}

// A self-hosted GitLab under a relative URL root (#1916): the target's url
// keeps the root, so every API call, the generic package files' download
// URLs the release links and the release's page sit under it. The fake
// serves nothing outside the root, so a ship that dropped it would fail.
func TestShipUnderRoot(t *testing.T) {
	ctx := context.Background()
	gl := gitlabtest.NewUnder(t, "/gitlab")
	if !strings.HasSuffix(gl.URL(), "/gitlab") {
		t.Fatalf("URL() = %q, want it to end in the root", gl.URL())
	}
	tg, p := newTarget(gl, "t0ken")
	d := New(nil)
	if err := d.Check(ctx, tg); err != nil {
		t.Fatalf("Check() = %v", err)
	}
	s := shipment(file("orders-cli_linux.tar.gz", "binary"), file("SHA256SUMS", "s"))
	for i := 0; i < 3 && marked(p, s.App) == 0; i++ {
		if err := d.Ship(ctx, tg, s); err != nil {
			t.Fatalf("Ship() %d = %v", i, err)
		}
	}
	rels := p.Releases()
	if len(rels) != 1 || len(rels[0].Links) != 2 {
		t.Fatalf("releases = %+v, want one with two links", rels)
	}
	want := gl.URL() + "/api/v4/projects/" + url.PathEscape(p.Path) + "/packages/generic/orders-cli/" +
		model.GitLabPackageVersion("cli/v1.0.0") + "/orders-cli_linux.tar.gz"
	if got := rels[0].Links[0].URL; got != want {
		t.Errorf("package link = %q, want %q: the download URL under the root", got, want)
	}
	state, err := d.State(ctx, tg, s.App, "")
	if err != nil || state.Phase != ports.PhaseDeployed || !strings.HasPrefix(state.URL, gl.URL()+"/"+p.Path+"/") {
		t.Fatalf("State() = %+v, %v, want Deployed at a page under %s/%s", state, err, gl.URL(), p.Path)
	}
}
