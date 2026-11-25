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
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/modelfacts"
	"github.com/JairJosafath/theos/internal/nocluster"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/fake"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// fakeHosts gives each GitProvider its view of one fake host.
type fakeHosts struct{ git *fake.Git }

func (h fakeHosts) GitHost(cfg ports.Config) (ports.GitHost, error) { return h.git.For(cfg), nil }

// runner is a fake command runner: it records each call and answers git
// credential fill with out and err, and git remote get-url origin with
// origin, failing as git does when origin is "".
type runner struct {
	out    string
	err    error
	origin string
	calls  []string
}

func (r *runner) Run(_ context.Context, stdin, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, strings.Join(append([]string{name}, args...), " ")+"\n"+stdin)
	if slices.Contains(args, "get-url") {
		if r.origin == "" {
			return nil, errors.New("exit status 2")
		}
		return []byte(r.origin + "\n"), nil
	}
	return []byte(r.out), r.err
}

// fills are the git credential fill calls r recorded.
func (r *runner) fills() []string {
	var out []string
	for _, c := range r.calls {
		if strings.HasPrefix(c, "git credential fill\n") {
			out = append(out, c)
		}
	}
	return out
}

// recordingHosts records the config of each GitHost it builds: no GitHost
// built, no request made.
type recordingHosts struct {
	inner   nocluster.Hosts
	configs []ports.Config
}

func (h *recordingHosts) GitHost(cfg ports.Config) (ports.GitHost, error) {
	h.configs = append(h.configs, cfg)
	return h.inner.GitHost(cfg)
}

// fetch is one template fetch, and the credential it carried, if any.
type fetch struct {
	url   string
	creds *ports.Credentials
}

// recordingTemplates records each template fetch.
type recordingTemplates struct {
	inner   modelfacts.TemplateFetcher
	fetches []fetch
}

func (f *recordingTemplates) FetchTemplate(ctx context.Context, url, ref, dir string,
	creds *ports.Credentials) (scaffold.Tree, error) {
	f.fetches = append(f.fetches, fetch{url: url, creds: creds})
	return f.inner.FetchTemplate(ctx, url, ref, dir, creds)
}

// world is a project's model on a fake host, with a credential from an
// injected environment: vars holds THEOS_GIT_TOKEN and THEOS_GIT_HOST, the
// GitProvider's host, and nothing else, and run answers for git.
type world struct {
	m         *nocluster.Model
	git       *fake.Git
	hosts     *recordingHosts
	templates *recordingTemplates
	run       *runner
	vars      map[string]string
	env       nocluster.Env
	out       *bytes.Buffer
	token     string
	now       time.Time
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{m: decode(t, readWebshop(t), ""), git: fake.New(), out: &bytes.Buffer{}, token: rand.Text(),
		now: time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC), run: &runner{}}
	w.hosts = &recordingHosts{inner: fakeHosts{w.git}}
	w.templates = &recordingTemplates{inner: templatesDir{t}}
	host, err := nocluster.Host(w.provider().Spec.URL)
	if err != nil {
		t.Fatal(err)
	}
	// The webshop sample's host is reached over http, which THEOS_GIT_HOST spells.
	w.vars = map[string]string{nocluster.TokenVar: w.token, nocluster.HostVar: "http://" + host}
	getenv := func(key string) string { return w.vars[key] }
	w.env = nocluster.Env{Hosts: w.hosts, Templates: w.templates, Out: w.out,
		Now: func() time.Time { return w.now },
		CredentialHost: func(ctx context.Context) (nocluster.Bound, error) {
			return nocluster.CredentialHost(ctx, getenv, w.run, "/project")
		},
		Credential: func(ctx context.Context, b nocluster.Bound, hostURL string) (ports.Credentials, error) {
			return nocluster.Credential(ctx, getenv, w.run, b, hostURL)
		}}
	t.Cleanup(func() {
		if w.vars[nocluster.TokenVar] != "" && len(w.run.fills()) > 0 {
			t.Errorf("git credential fill was asked although %s was set", nocluster.TokenVar)
		}
	})
	return w
}

func (w *world) provider() *c4v1beta1.GitProvider { return &w.m.Objects.GitProviders[0] }

func (w *world) repo(name string) fake.Repo { return w.git.Repo(w.provider().Spec.Owner, name) }

// #268, the golden check: theos scaffold pushes, for each of the webshop
// sample's Containers, the tree and commit message the Container
// reconciler's functions render for the same objects and the same pinned
// templates, as one first commit with the operator's trailers, and puts the
// operator's rule on main.
func TestScaffoldIsTheReconcilers268(t *testing.T) {
	w := newWorld(t)
	if err := nocluster.Scaffold(context.Background(), w.env, w.m); err != nil {
		t.Fatal(err)
	}
	for _, c := range w.m.Containers {
		t.Run(c.Name, func(t *testing.T) {
			got := w.repo(c.Name)
			want := render(t, w.m, c.Name, w.token, w.now)
			if len(got.Commits) != 1 {
				t.Fatalf("%d commits, want the one first commit", len(got.Commits))
			}
			if !reflect.DeepEqual(got.Files, want.Result.Files) {
				t.Errorf("the pushed tree differs from the reconciler's render")
			}
			if got.Commits[0].Message != want.Message {
				t.Errorf("message %q, want the reconciler's %q", got.Commits[0].Message, want.Message)
			}
			info, err := scaffold.ParseCommitMessage(got.Commits[0].Message)
			if err != nil || info.Container != "webshop/"+c.Name || len(info.ScaffoldedFrom) == 0 {
				t.Errorf("the commit's trailers read %+v, %v", info, err)
			}

			var standard *c4v1beta1.Standard
			if c.Spec.StandardRef != nil {
				standard = modelfacts.Find(w.m.Objects.Standards, c.Spec.StandardRef.Name)
			}
			rule, protect, err := modelfacts.Protection(&c, w.provider(), standard)
			if err != nil || !protect {
				t.Fatalf("protect = %v, %v", protect, err)
			}
			owner := w.provider().Spec.Owner
			gotRule, tags := w.git.BranchRule(owner, c.Name)
			branches := w.git.ProtectedBranches(owner, c.Name)
			if !slices.Equal(branches, []string{ports.DefaultBranch}) || !reflect.DeepEqual(gotRule, rule) ||
				!slices.Equal(tags, []string{model.ReleaseTags}) {
				t.Errorf("protected %v with %+v and tags %v, want main with %+v", branches, gotRule, tags, rule)
			}
		})
	}
}

// #268: a second scaffold pushes nothing.
func TestSecondScaffoldPushesNothing268(t *testing.T) {
	w := newWorld(t)
	if err := nocluster.Scaffold(context.Background(), w.env, w.m); err != nil {
		t.Fatal(err)
	}
	pushes := w.git.PushCount()
	if pushes != len(w.m.Containers) {
		t.Fatalf("%d pushes for %d Containers", pushes, len(w.m.Containers))
	}
	w.out.Reset()
	if err := nocluster.Scaffold(context.Background(), w.env, w.m); err != nil {
		t.Fatal(err)
	}
	if n := w.git.PushCount(); n != pushes {
		t.Errorf("the second scaffold pushed %d times", n-pushes)
	}
	if !strings.Contains(w.out.String(), "orders-api: scaffolded already") {
		t.Errorf("the second scaffold said:\n%s", w.out)
	}
}

// Once a pull request has merged into a Container's repository, its newest
// commit is no longer the scaffold: theos scaffold refused the Container
// and theos status called it "has commits theos didn't make". The first
// commit is the scaffold however many followed: a second scaffold pushes
// nothing and puts the rule back on main, and status says scaffolded.
func TestScaffoldAfterAMerge(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	if err := nocluster.Scaffold(ctx, w.env, w.m); err != nil {
		t.Fatal(err)
	}
	owner := w.provider().Spec.Owner
	rule, _ := w.git.BranchRule(owner, "orders-api")
	host := w.git.For(ports.Config{URL: w.provider().Spec.URL, Owner: owner})
	repo := host.Repository("orders-api")
	head := w.git.MainHead(owner, "orders-api").Hash
	if _, err := host.PushBranch(ctx, repo, head, "task/orders-api-3", scaffold.Tree{"orders.go": {
		Data: []byte("package orders\n")}}, nil, "Add orders", ports.Signature{Name: "implementer"}); err != nil {
		t.Fatal(err)
	}
	pr, err := host.OpenPullRequest(ctx, repo, "task/orders-api-3", ports.DefaultBranch, "Add orders", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := host.MergePullRequest(ctx, repo, pr.Number, pr.HeadSHA, "Add orders (#3)", "task/orders-api-3"); err != nil {
		t.Fatal(err)
	}
	// A rule changed by hand since: the second scaffold protects main again.
	if err := host.ProtectBranch(ctx, repo, ports.DefaultBranch, ports.BranchRule{}); err != nil {
		t.Fatal(err)
	}
	if n := w.git.CommitCount(owner, "orders-api"); n != 2 {
		t.Fatalf("%d commits on main, want the scaffold and the merge", n)
	}

	pushes := w.git.PushCount()
	w.out.Reset()
	if err := nocluster.Scaffold(ctx, w.env, w.m); err != nil {
		t.Fatalf("the scaffold after a merge: %v", err)
	}
	if n := w.git.PushCount(); n != pushes {
		t.Errorf("the scaffold after a merge pushed %d times", n-pushes)
	}
	if !strings.Contains(w.out.String(), "orders-api: scaffolded already") {
		t.Errorf("the scaffold after a merge said:\n%s", w.out)
	}
	if got, _ := w.git.BranchRule(owner, "orders-api"); !reflect.DeepEqual(got, rule) {
		t.Errorf("main's rule is %+v after the scaffold, want %+v", got, rule)
	}

	w.out.Reset()
	if err := nocluster.Status(ctx, w.env, w.m); err != nil {
		t.Fatal(err)
	}
	if out := w.out.String(); !strings.Contains(out, "orders-api: scaffolded at ") ||
		strings.Contains(out, "orders-api: "+repo.WebURL+" has commits theos didn't make") ||
		!strings.Contains(out, "  Relationship orders-api-to-shop-db: "+c4v1beta1.ReasonAppliedAtScaffold+": ") {
		t.Errorf("status after a merge says:\n%s", out)
	}
}

// #268: status reads a Relationship added after the scaffold as
// AddedAfterScaffold, one the scaffold applied as AppliedAtScaffold, names
// the token's account from a token alone, and prints the loss table's
// reasons for the host, never the token.
func TestStatus268(t *testing.T) {
	w := newWorld(t)
	w.git.SetLogin(w.token, "shop-owner")
	if err := nocluster.Scaffold(context.Background(), w.env, w.m); err != nil {
		t.Fatal(err)
	}
	// A DataStore and a Relationship to it, added once orders-api was scaffolded.
	store := w.m.Objects.DataStores[0].DeepCopy()
	store.Name = "audit-db"
	w.m.Objects.DataStores = append(w.m.Objects.DataStores, *store)
	rel := c4v1beta1.Relationship{}
	rel.Name, rel.Namespace = "orders-api-to-audit-db", "webshop"
	rel.Spec.Source = c4v1beta1.ElementRef{Kind: c4v1beta1.ElementContainer, Name: "orders-api"}
	rel.Spec.Target = c4v1beta1.ElementRef{Kind: c4v1beta1.ElementDataStore, Name: "audit-db"}
	w.m.Objects.Relationships = append(w.m.Objects.Relationships, rel)

	w.out.Reset()
	if err := nocluster.Status(context.Background(), w.env, w.m); err != nil {
		t.Fatal(err)
	}
	out := w.out.String()
	for _, line := range []string{
		"orders-api: scaffolded at ",
		"  Relationship orders-api-to-audit-db: " + c4v1beta1.ReasonAddedAfterScaffold + ": ",
		"  Relationship orders-api-to-shop-db: " + c4v1beta1.ReasonAppliedAtScaffold + ": ",
		" as shop-owner:\n",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("status does not say %q:\n%s", line, out)
		}
	}
	for _, l := range model.NoClusterLosses(string(w.provider().Spec.Provider), true) {
		if !strings.Contains(out, "  "+l.Reason+": ") {
			t.Errorf("status leaves out %s:\n%s", l.Reason, out)
		}
	}
	for _, reason := range []string{model.LossAgentHoldsOwnerCredential, model.LossNoFence, model.LossRulesNotValidated} {
		if !strings.Contains(out, reason) {
			t.Errorf("status leaves out %s", reason)
		}
	}
	if strings.Contains(out, w.token) {
		t.Error("status printed the token")
	}
}

// #268: THEOS_GIT_TOKEN is read before git credential fill, which is asked
// for the GitProvider's host only when the variable is unset; the token is
// in no error.
func TestCredential268(t *testing.T) {
	ctx := context.Background()
	token := rand.Text()
	set := func(key string) string {
		if key == nocluster.TokenVar {
			return token
		}
		return ""
	}
	unset := func(string) string { return "" }

	run := &runner{out: "protocol=https\nhost=git.example.com\nusername=other\npassword=" + rand.Text() + "\n"}
	creds, err := nocluster.Credential(ctx, set, run, nocluster.Bound{Host: "git.example.com"},
		"https://git.example.com")
	if err != nil || creds.Token != token || creds.Username != "" || len(run.calls) != 0 {
		t.Errorf("with %s set: user %q, the token %v, %d helper calls, %v", nocluster.TokenVar, creds.Username,
			creds.Token == token, len(run.calls), err)
	}

	helper := rand.Text()
	run = &runner{out: "protocol=https\nhost=git.example.com\nusername=shop-owner\npassword=" + helper + "\n"}
	creds, err = nocluster.Credential(ctx, unset, run, nocluster.Bound{Host: "git.example.com:3000"},
		"https://git.example.com:3000")
	if err != nil || creds.Token != helper || creds.Username != "shop-owner" {
		t.Errorf("from git credential fill: user %q, the helper's token %v, %v", creds.Username, creds.Token == helper,
			err)
	}
	if want := []string{"git credential fill\nprotocol=https\nhost=git.example.com:3000\n\n"}; !slices.Equal(run.calls,
		want) {
		t.Errorf("asked %q, want %q", run.calls, want)
	}

	for name, r := range map[string]*runner{
		"no password":  {out: "username=shop-owner\n"},
		"helper fails": {out: "password=" + token + "\n", err: errors.New("exit status 128")},
	} {
		if _, err := nocluster.Credential(ctx, unset, r, nocluster.Bound{Host: "git.example.com"},
			"https://git.example.com"); err == nil ||
			strings.Contains(err.Error(), token) || !strings.Contains(err.Error(), nocluster.TokenVar) {
			t.Errorf("#268 %s: err = %v", name, err)
		}
	}
}

// #268: init writes a model that decodes: the GitProvider of the remote's
// host with its provider always written, read from a github.com address and
// asked for any other, never guessed; the chart's catalog at the release,
// each entry naming the theos repository at the release's tag with its
// template.path.
func TestInit268(t *testing.T) {
	const theosURL, tag = "https://github.com/JairJosafath/theos", "v0.9.0"
	fetch := chartFetcher{t: t, url: theosURL, ref: tag}
	ask := func(string) (string, error) {
		t.Error("init asked for the provider of github.com")
		return "", nil
	}
	files, err := nocluster.Init(context.Background(), fetch, nocluster.InitOptions{Project: "shop",
		Remote: "git@github.com:acme/theos-shop.git", Ask: ask, TheosURL: theosURL, Ref: tag})
	if err != nil {
		t.Fatal(err)
	}
	m, err := nocluster.Decode(files[nocluster.ModelFile], files[nocluster.SetupFile], files[nocluster.SettingsFile])
	if err != nil {
		t.Fatal(err)
	}
	gp := m.Objects.GitProviders
	if len(gp) != 1 || gp[0].Spec.Provider != "github" || gp[0].Spec.URL != "https://github.com" ||
		gp[0].Spec.Owner != "acme" || gp[0].Spec.CredentialsSecretRef.Name != nocluster.CredentialsSecret {
		t.Errorf("GitProvider %+v", gp)
	}
	if !bytes.Contains(files[nocluster.SetupFile], []byte("\n  provider: github\n")) {
		t.Error("the provider is not written out")
	}
	if m.Settings.Repository != "theos-shop" || len(m.Objects.Frameworks) == 0 || len(m.Objects.Standards) == 0 ||
		len(m.Objects.Domains) == 0 || len(m.Objects.Bundles) == 0 {
		t.Errorf("model: settings %+v, %d Frameworks, %d Standards, %d Domains, %d Bundles", m.Settings,
			len(m.Objects.Frameworks), len(m.Objects.Standards), len(m.Objects.Domains), len(m.Objects.Bundles))
	}
	templates := make([]c4v1beta1.TemplateSource, 0, len(m.Objects.Frameworks)+len(m.Objects.Domains))
	for _, f := range m.Objects.Frameworks {
		templates = append(templates, f.Spec.Template)
	}
	for _, d := range m.Objects.Domains {
		templates = append(templates, d.Spec.Template)
	}
	for _, tmpl := range templates {
		if tmpl.URL != theosURL || tmpl.Ref != tag || !strings.HasPrefix(tmpl.Path, "templates/") {
			t.Errorf("entry template %+v", tmpl)
			continue
		}
		if _, err := os.Stat(filepath.Join("..", "..", tmpl.Path)); err != nil {
			t.Errorf("entry template %s: %v", tmpl.Path, err)
		}
	}

	// Any other host is asked for, or named.
	var asked string
	// #268: the answer is read in any case, with spaces around it.
	answer := func(q string) (string, error) { asked = q; return " GitHub \n", nil }
	files, err = nocluster.Init(context.Background(), fetch, nocluster.InitOptions{Project: "shop",
		Remote: "https://git.example.com/acme/theos-shop.git", Ask: answer, TheosURL: theosURL, Ref: tag})
	if err != nil || !strings.Contains(asked, "git.example.com") ||
		!bytes.Contains(files[nocluster.SetupFile], []byte("url: https://git.example.com\n")) ||
		!bytes.Contains(files[nocluster.SetupFile], []byte("provider: github\n")) {
		t.Errorf("another host: asked %q, %v", asked, err)
	}
	for name, opts := range map[string]nocluster.InitOptions{
		"#268 another host, nobody to ask":  {Remote: "git@git.example.com:acme/theos-shop.git"},
		"#268 github.com as another host's": {Remote: "https://github.com/acme/theos-shop", Provider: "gitlab"},
		"#268 an unknown provider":          {Remote: "https://git.example.com/acme/x", Provider: "bitbucket"},
		"#268 no remote":                    {},
	} {
		opts.Project, opts.TheosURL, opts.Ref = "shop", theosURL, tag
		if _, err := nocluster.Init(context.Background(), fetch, opts); err == nil {
			t.Errorf("%s: init wrote a model", name)
		}
	}
}

// #268: init takes a project name that is a DNS label, the Project's
// namespace's, and says what one is before it fetches the catalog or asks
// for the provider.
func TestInitChecksTheProjectName268(t *testing.T) {
	const theosURL, tag = "https://github.com/JairJosafath/theos", "v0.9.0"
	for _, name := range []string{"My_App", "my.app", "-app", strings.Repeat("a", 64)} {
		rec := &recordingTemplates{inner: chartFetcher{t: t, url: theosURL, ref: tag}}
		asked := false
		files, err := nocluster.Init(context.Background(), rec, nocluster.InitOptions{Project: name,
			Remote: "https://git.example.com/acme/theos-shop.git", TheosURL: theosURL, Ref: tag,
			Ask: func(string) (string, error) { asked = true; return model.Hosts()[0], nil }})
		if err == nil || files != nil || !strings.Contains(err.Error(), "DNS label") {
			t.Errorf("#268 project %q: err = %v; want it refused saying what a project's name is", name, err)
		}
		if len(rec.fetches) != 0 || asked {
			t.Errorf("#268 project %q: %d fetches, asked %v before the name was checked", name, len(rec.fetches), asked)
		}
	}
	rec := &recordingTemplates{inner: chartFetcher{t: t, url: theosURL, ref: tag}}
	if _, err := nocluster.Init(context.Background(), rec, nocluster.InitOptions{Project: strings.Repeat("a", 63),
		Remote: "https://git.example.com/acme/theos-shop.git", Provider: model.Hosts()[0], TheosURL: theosURL,
		Ref: tag}); err != nil || len(rec.fetches) != 1 {
		t.Errorf("#268 a 63-character name: %v, %d fetches", err, len(rec.fetches))
	}
}

// chartFetcher serves this checkout's chart as the theos repository's at a tag.
type chartFetcher struct {
	t        *testing.T
	url, ref string
}

func (f chartFetcher) FetchTemplate(_ context.Context, url, ref, dir string, creds *ports.Credentials) (scaffold.Tree,
	error) {
	if url != f.url || ref != f.ref || dir != "dist/chart" || creds != nil {
		f.t.Fatalf("fetched %s at %s dir %s with a credential %v", url, ref, dir, creds != nil)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "dist", "chart", "values.yaml"))
	return scaffold.Tree{"values.yaml": scaffold.File{Data: data}}, err
}
