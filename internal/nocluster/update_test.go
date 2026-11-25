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

package nocluster_test

import (
	"context"
	"maps"
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/modelfacts"
	"github.com/JairJosafath/theos/internal/nocluster"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// bumpedTemplates serves inner, and the template named name (its URL's last
// segment) changed at each ref of bumps: its ruff.toml gains a line, and a
// style guide of the ref's own is added.
type bumpedTemplates struct {
	inner modelfacts.TemplateFetcher
	name  string
	bumps []string
}

func (b bumpedTemplates) FetchTemplate(ctx context.Context, url, ref, dir string,
	creds *ports.Credentials) (scaffold.Tree, error) {
	i := slices.Index(b.bumps, ref)
	if path.Base(url) != b.name || i < 0 {
		return b.inner.FetchTemplate(ctx, url, ref, dir, creds)
	}
	// The inner fetcher serves this checkout's tree at any ref.
	tree, err := b.inner.FetchTemplate(ctx, url, ref, dir, creds)
	if err != nil {
		return nil, err
	}
	tree = maps.Clone(tree)
	for _, r := range b.bumps[:i+1] {
		ruff := tree["ruff.toml"]
		ruff.Data = append(slices.Clone(ruff.Data), []byte("# "+r+"\n")...)
		tree["ruff.toml"] = ruff
		tree["docs/style/"+r+".md"] = scaffold.File{Data: []byte("# The style of " + r + "\n")}
	}
	return tree, nil
}

// pushes records each PushBranch's message, by branch.
type pushes struct {
	inner    nocluster.Hosts
	messages map[string]string
}

func (p *pushes) GitHost(cfg ports.Config) (ports.GitHost, error) {
	h, err := p.inner.GitHost(cfg)
	return recordingPush{GitHost: h, p: p}, err
}

type recordingPush struct {
	ports.GitHost
	p *pushes
}

func (r recordingPush) PushBranch(ctx context.Context, repo ports.Repository, base, branch string,
	files scaffold.Tree, removes []string, message string, author ports.Signature) (string, error) {
	r.p.messages[branch] = message
	return r.GitHost.PushBranch(ctx, repo, base, branch, files, removes, message, author)
}

// updateWorld is the webshop scaffolded on the fake host, its
// python-standard bumped at v9.9.9 and v9.9.10, and every push's message
// recorded.
func updateWorld(t *testing.T) (*world, *pushes) {
	t.Helper()
	w := newWorld(t)
	w.templates.inner = bumpedTemplates{inner: templatesDir{t}, name: "python-standard",
		bumps: []string{"v9.9.9", "v9.9.10"}}
	if err := nocluster.Scaffold(context.Background(), w.env, w.m); err != nil {
		t.Fatal(err)
	}
	p := &pushes{inner: w.env.Hosts, messages: map[string]string{}}
	w.env.Hosts = p
	return w, p
}

// bump sets python-standard's ref in the model.
func (w *world) bump(t *testing.T, ref string) {
	t.Helper()
	s := modelfacts.Find(w.m.Objects.Standards, "python-standard")
	if s == nil {
		t.Fatal("no Standard python-standard")
	}
	s.Spec.Template.Ref = ref
}

// #295: theos update plans what the operator's Standard update plans for
// the same trees: the Standard rendered from the scaffold's own input at the
// ref the scaffold pinned and at the new one, planned against main by
// scaffold's one function, pushed on the operator's branch with its commit
// message and opened under its title, the description a person's with the
// update's trailers.
func TestUpdatePlansWhatTheOperatorPlans295(t *testing.T) {
	w, p := updateWorld(t)
	ctx := context.Background()
	owner := w.provider().Spec.Owner
	scaffolded := render(t, w.m, "orders-api", w.token, w.now)
	standard := modelfacts.Find(w.m.Objects.Standards, "python-standard")
	from := model.StandardVersion{Ref: standard.Spec.Template.Ref, Checks: scaffolded.Input.Checks}.Normal()
	// The repository changed a line of ruff.toml the Standard changes too.
	mine := w.repo("orders-api").Files["ruff.toml"]
	mine.Data = append(slices.Clone(mine.Data), []byte("# ours\n")...)
	w.git.CommitToMain(owner, "orders-api", scaffold.Tree{"ruff.toml": mine})

	w.bump(t, "v9.9.9")
	if err := nocluster.Update(ctx, w.env, w.m, nocluster.UpdateOptions{Container: "orders-api"}); err != nil {
		t.Fatal(err)
	}
	to := model.StandardVersion{Ref: "v9.9.9", Checks: from.Checks}

	// What the operator plans: its update's input is the scaffold's, with the
	// paths the Framework renders at its scaffolded ref.
	in := scaffolded.Input
	var fw scaffold.Layer
	layers, err := modelfacts.FetchLayers(ctx, w.templates.inner, resolved(t, w.m, "orders-api", w.token))
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range layers {
		if l.Side == model.SideFramework {
			fw = l
		}
	}
	fwTree, err := scaffold.RenderLayer(in, fw)
	if err != nil {
		t.Fatal(err)
	}
	in.FrameworkFiles = fwTree.Paths()
	tmpl := standard.Spec.Template
	oldFiles, _ := w.templates.inner.FetchTemplate(ctx, tmpl.URL, from.Ref, tmpl.Path, nil)
	nextFiles, _ := w.templates.inner.FetchTemplate(ctx, tmpl.URL, to.Ref, tmpl.Path, nil)
	renders, err := scaffold.RenderStandardUpdate(in, "python-standard", from, to, in.CIImages, oldFiles, nextFiles)
	if err != nil {
		t.Fatal(err)
	}
	main := w.repo("orders-api").Files
	plan := renders.Plan(main, nil)
	if plan.Empty() || len(plan.LeftOut)+len(plan.Merged) == 0 {
		t.Fatalf("the operator's plan %+v changes nothing of what the test bumped", plan.Update)
	}

	branch := model.StandardUpdateBranch("python-standard", to)
	edits := w.git.BranchEdits(owner, "orders-api", branch)
	got := scaffold.Tree{}
	for p, f := range edits {
		if f != nil {
			got[p] = *f
		}
	}
	want := scaffold.Tree{}
	for p, f := range plan.Files {
		if cur, ok := main[p]; !ok || !reflect.DeepEqual(cur, f) {
			want[p] = f
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("#295 the branch %s writes %v, want the operator's plan's %v", branch, slices.Sorted(maps.Keys(got)),
			slices.Sorted(maps.Keys(want)))
	}
	if msg := p.messages[branch]; msg != scaffold.UpdateMessage(in, "python-standard", from, to, plan.Update) {
		t.Errorf("#295 the commit's message is\n%s\nwant the operator's\n%s", msg,
			scaffold.UpdateMessage(in, "python-standard", from, to, plan.Update))
	}
	pulls := w.git.PullRequests(owner, "orders-api")
	if len(pulls) != 1 {
		t.Fatalf("#295 %d pull requests, want the update's", len(pulls))
	}
	title, body := w.git.PullText(owner, "orders-api", pulls[0].Number)
	leftOut := model.LeftOut{Files: renders.LeftOut(nil, nil, main, plan.LeftOut)}
	wantBody := model.PersonStandardUpdateBody("python-standard", from, to, plan.Paths(), plan.Merged, leftOut) +
		"\n" + scaffold.UpdateTrailers("python-standard", from, to)
	if title != model.StandardUpdateTitle("python-standard", from, to) || body != wantBody {
		t.Errorf("#295 the pull request reads %q:\n%s\nwant the operator's title and a person's description", title, body)
	}
	for _, c := range []string{"catalog-api", "payments-worker", "storefront"} {
		if n := len(w.git.PullRequests(owner, c)); n != 0 {
			t.Errorf("#295 -container orders-api opened %d pull requests on %s", n, c)
		}
	}
}

// resolved is the Container name of m, resolved with token as the
// GitProvider's credential.
func resolved(t *testing.T, m *nocluster.Model, name, token string) modelfacts.Resolved {
	t.Helper()
	objs := m.Objects
	objs.Secrets = map[string]ports.Credentials{}
	for _, gp := range objs.GitProviders {
		objs.Secrets[gp.Spec.CredentialsSecretRef.Name] = ports.Credentials{Token: token}
	}
	c := modelfacts.Find(m.Containers, name)
	res, missing := modelfacts.Resolve(c, modelfacts.Find(m.Systems, c.Spec.SoftwareSystemRef.Name), objs)
	if len(missing) > 0 {
		t.Fatalf("missing %v", missing)
	}
	return res
}

// #295: the ref the files are at reads back from main's history: once a
// person merges the update on the host's pages, with the squash message the
// pull request's description gives, the next update starts at its ref; one
// at the Standard's ref opens nothing.
func TestUpdateReadsTheRefBack295(t *testing.T) {
	w, _ := updateWorld(t)
	ctx := context.Background()
	owner := w.provider().Spec.Owner
	w.bump(t, "v9.9.9")
	if err := nocluster.Update(ctx, w.env, w.m, nocluster.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"catalog-api", "orders-api", "payments-worker"} {
		pulls := w.git.PullRequests(owner, c)
		if len(pulls) != 1 {
			t.Fatalf("#295 %s: %d pull requests, want the update's", c, len(pulls))
		}
		title, body := w.git.PullText(owner, c, pulls[0].Number)
		host := w.git.For(modelfacts.Config(w.provider(), ports.Credentials{Token: w.token}))
		if err := host.ForceMergePullRequest(ctx, host.Repository(c), pulls[0].Number, pulls[0].HeadSHA, title,
			body); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(w.git.PullRequests(owner, "storefront")); n != 0 {
		t.Errorf("#295 storefront, on ts-standard, got %d pull requests", n)
	}

	w.out.Reset()
	if err := nocluster.Update(ctx, w.env, w.m, nocluster.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.out.String(), "orders-api: the files of the Standard python-standard are at v9.9.9") {
		t.Errorf("#295 an update at the Standard's ref said:\n%s", w.out)
	}
	if n := len(w.git.PullRequests(owner, "orders-api")); n != 1 {
		t.Errorf("#295 an update at the Standard's ref opened %d more pull requests", n-1)
	}

	w.bump(t, "v9.9.10")
	if err := nocluster.Update(ctx, w.env, w.m, nocluster.UpdateOptions{Container: "orders-api"}); err != nil {
		t.Fatal(err)
	}
	pulls := w.git.PullRequests(owner, "orders-api")
	if len(pulls) != 2 {
		t.Fatalf("#295 %d pull requests, want the second update's", len(pulls))
	}
	title, _ := w.git.PullText(owner, "orders-api", pulls[1].Number)
	checks := modelfacts.Find(w.m.Containers, "orders-api").Spec.Checks
	from := model.StandardVersion{Ref: "v9.9.9", Checks: string(checks)}
	to := model.StandardVersion{Ref: "v9.9.10", Checks: string(checks)}
	if title != model.StandardUpdateTitle("python-standard", from, to) {
		t.Errorf("#295 the second update is %q, want it from v9.9.9", title)
	}
	edits := w.git.BranchEdits(owner, "orders-api", model.StandardUpdateBranch("python-standard", to))
	if _, ok := edits["docs/style/v9.9.10.md"]; !ok || len(edits) != 2 {
		t.Errorf("#295 the second update writes %v, want ruff.toml and v9.9.10's guide", slices.Sorted(maps.Keys(edits)))
	}
}

// #295: a Standard the history names no ref of is not updated unless -from
// names one, and -from takes -container.
func TestUpdateWithoutARef295(t *testing.T) {
	w, _ := updateWorld(t)
	ctx := context.Background()
	owner := w.provider().Spec.Owner
	// orders-api's history names none: its repository is someone else's.
	w.git.ClearRepo(owner, "orders-api")
	w.git.SeedRepoFiles(owner, "orders-api", "Import orders-api\n", w.repo("catalog-api").Files)
	scaffoldedAt := modelfacts.Find(w.m.Objects.Standards, "python-standard").Spec.Template.Ref
	w.bump(t, "v9.9.9")
	if err := nocluster.Update(ctx, w.env, w.m, nocluster.UpdateOptions{Container: "orders-api"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.out.String(), "orders-api: AddedAfterScaffold: ") ||
		len(w.git.PullRequests(owner, "orders-api")) != 0 {
		t.Errorf("#295 an update with no ref said:\n%s", w.out)
	}
	if err := nocluster.Update(ctx, w.env, w.m, nocluster.UpdateOptions{From: "v1.11.0"}); err == nil {
		t.Error("#295 -from without -container was taken")
	}
	if err := nocluster.Update(ctx, w.env, w.m, nocluster.UpdateOptions{Container: "orders-api",
		From: scaffoldedAt}); err != nil {
		t.Fatal(err)
	}
	if n := len(w.git.PullRequests(owner, "orders-api")); n != 1 {
		t.Errorf("#295 -from opened %d pull requests, want the update", n)
	}
}
