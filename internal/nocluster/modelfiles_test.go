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
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"go/build"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/features"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/modelfacts"
	"github.com/JairJosafath/theos/internal/nocluster"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// webshop is the webshop sample (config/samples/webshop) as model files: the
// Project and the story in model/model.yaml, the factory's objects and the
// Bundle in model/setup.yaml.
type webshop struct{ story, setup string }

func readWebshop(t *testing.T) webshop {
	t.Helper()
	read := func(files ...string) string {
		docs := make([]string, 0, len(files))
		for _, f := range files {
			data, err := os.ReadFile(filepath.Join("..", "..", "config", "samples", "webshop", f))
			if err != nil {
				t.Fatal(err)
			}
			docs = append(docs, string(data))
		}
		return strings.Join(docs, "\n---\n")
	}
	return webshop{story: read("project.yaml", "story.yaml"), setup: read("factory.yaml", "bundle.yaml")}
}

func decode(t *testing.T, w webshop, settings string) *nocluster.Model {
	t.Helper()
	m, err := nocluster.Decode([]byte(w.story), []byte(w.setup), []byte(settings))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// templatesDir is a fetcher reading templates/<the URL's last segment, or
// the path's>, the templates a pinned ref holds in this checkout.
type templatesDir struct{ t *testing.T }

func (f templatesDir) FetchTemplate(_ context.Context, url, ref, dir string, _ *ports.Credentials) (scaffold.Tree, error) {
	if ref == "" {
		f.t.Fatalf("fetched %s with no ref", url)
	}
	name := path.Base(url)
	if dir != "" {
		name = path.Base(dir)
	}
	return scaffold.ReadTree(os.DirFS(filepath.Join("..", "..", "templates", name)))
}

// render is a Container's first commit as the Container reconciler works it
// out (internal/controller/container_scaffold.go): resolve, check, layers, CI,
// license and build, on the objects m holds.
func render(t *testing.T, m *nocluster.Model, name, token string, now time.Time) modelfacts.Render {
	t.Helper()
	objs := m.Objects
	objs.Secrets = map[string]ports.Credentials{}
	for _, gp := range objs.GitProviders {
		objs.Secrets[gp.Spec.CredentialsSecretRef.Name] = ports.Credentials{Token: token}
	}
	container := modelfacts.Find(m.Containers, name)
	system := modelfacts.Find(m.Systems, container.Spec.SoftwareSystemRef.Name)
	res, missing := modelfacts.Resolve(container, system, objs)
	if len(missing) > 0 {
		t.Fatalf("missing %v", missing)
	}
	if err := modelfacts.Check(container, res); err != nil {
		t.Fatal(err)
	}
	view := container.DeepCopy()
	view.Status.Bundle = res.Bundle.DeepCopy()
	layers, err := modelfacts.FetchLayers(context.Background(), templatesDir{t}, res)
	if err != nil {
		t.Fatal(err)
	}
	ci, err := modelfacts.CI(res.Provider)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := m.Settings.Scaffold()
	if err != nil {
		t.Fatal(err)
	}
	// The repository's npm scope is the GitProvider's, the host's own default
	// when it names none (ports.Repository.NpmScope).
	out, err := modelfacts.Build(view, res, layers, ci, modelfacts.Packages(res.Provider).NpmScope,
		modelfacts.License(container, objs.Project, now), settings)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// #268: the webshop sample's files decode, each object in the Project's
// namespace and defaulted as the API server defaults it.
func TestDecodeWebshop268(t *testing.T) {
	m := decode(t, readWebshop(t), "")
	if m.Project != "webshop" || len(m.Containers) != 4 || len(m.Systems) != 2 || len(m.Objects.GitProviders) != 1 ||
		len(m.Objects.Relationships) != 10 || len(m.Objects.Bundles) != 1 || len(m.People) != 1 {
		t.Fatalf("decoded %s: %d Containers, %d systems, %d GitProviders, %d Relationships, %d Bundles, %d People",
			m.Project, len(m.Containers), len(m.Systems), len(m.Objects.GitProviders), len(m.Objects.Relationships),
			len(m.Objects.Bundles), len(m.People))
	}
	for _, c := range m.Containers {
		if c.Namespace != "webshop" {
			t.Errorf("Container %s in namespace %q", c.Name, c.Namespace)
		}
		if c.Spec.Checks != c4v1beta1.ChecksMode(model.ChecksEveryChange) {
			t.Errorf("Container %s: checks %q, want the CRD's default", c.Name, c.Spec.Checks)
		}
	}
}

// #268: a model that leaves a defaulted field out (the GitProvider's
// provider, a Container's checks) renders the tree of one that spells it.
func TestDefaultedFieldLeftOutRendersTheSame268(t *testing.T) {
	w := readWebshop(t)
	provider := string(decode(t, w, "").Objects.GitProviders[0].Spec.Provider)
	leftOut := webshop{story: w.story, setup: strings.Replace(w.setup, "\n  provider: "+provider+"\n", "\n", 1)}
	spelled := webshop{story: strings.ReplaceAll(w.story, "\n  gitProviderRef:", "\n  checks: EveryChange\n  gitProviderRef:"),
		setup: w.setup}
	if leftOut.setup == w.setup || spelled.story == w.story {
		t.Fatal("the sample no longer spells the fields this test leaves out")
	}
	token, now := rand.Text(), time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	// The settings too: unset, and the operator's flag default spelled.
	a, b := decode(t, leftOut, ""), decode(t, spelled, "operator:\n  sessionFeature: local\n")
	if !reflect.DeepEqual(a.Objects.GitProviders, b.Objects.GitProviders) || !reflect.DeepEqual(a.Containers, b.Containers) {
		t.Fatal("the defaulted objects differ from the spelled ones")
	}
	ra, rb := render(t, a, "orders-api", token, now), render(t, b, "orders-api", token, now)
	if !reflect.DeepEqual(ra.Result.Files, rb.Result.Files) || ra.Message != rb.Message {
		t.Error("the tree rendered without the defaulted fields differs from the spelled one")
	}
}

// #268: the decode is strict and holds each kind in its file.
func TestDecodeRefuses268(t *testing.T) {
	w := readWebshop(t)
	cases := []struct {
		name  string
		files webshop
		want  string
	}{
		{"#268 an unknown field", webshop{story: strings.Replace(w.story, "\n  displayName: Webshop\n",
			"\n  displayName: Webshop\n  colour: blue\n", 1), setup: w.setup}, `unknown field "colour"`},
		{"#268 a GitProvider in the story", webshop{story: w.story + "\n---\napiVersion: theos.factory.io/v1beta1\n" +
			"kind: GitProvider\nmetadata: {name: other}\n", setup: w.setup}, "kind is a kind this file does not hold"},
		{"#268 another API", webshop{story: w.story + "\n---\napiVersion: v1\nkind: Secret\nmetadata: {name: s}\n",
			setup: w.setup}, "apiVersion is not theos.factory.io/v1beta1"},
		{"#268 another namespace", webshop{story: strings.Replace(w.story, "name: orders-api\n",
			"name: orders-api\n  namespace: shop\n", 1), setup: w.setup}, "metadata.namespace is not the Project's name"},
		{"#268 no Project", webshop{story: "", setup: w.setup}, "no Project"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := nocluster.Decode([]byte(c.files.story), []byte(c.files.setup), nil)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want one saying %s", err, c.want)
			}
		})
	}

	// The provider the enum does not list: the CRD's enum is not run without
	// a cluster, so the decode asks model.HostCI.
	provider := string(decode(t, w, "").Objects.GitProviders[0].Spec.Provider)
	setup := strings.Replace(w.setup, "\n  provider: "+provider+"\n", "\n  provider: bitbucket\n", 1)
	if _, err := nocluster.Decode([]byte(w.story), []byte(setup), nil); err == nil ||
		!strings.Contains(err.Error(), "spec.provider is not a host") ||
		!strings.Contains(err.Error(), strings.Join(model.Hosts(), ", ")) || strings.Contains(err.Error(), "bitbucket") {
		t.Errorf("#268 an unknown provider: err = %v, want one naming the field and the hosts the factory knows", err)
	}
}

// #268: the decoder applies the CRDs' create-time rules, read from the
// embedded schemas by the API server's own validators: a model file the API
// server would refuse at create is refused, naming the field and the rule.
func TestDecodeAppliesTheCRDsCreateRules268(t *testing.T) {
	w := readWebshop(t)
	inSetup := func(old, new string) webshop {
		if !strings.Contains(w.setup, old) {
			t.Fatalf("the sample's setup no longer holds %q", old)
		}
		return webshop{story: w.story, setup: strings.Replace(w.setup, old, new, 1)}
	}
	inStory := func(old, new string) webshop {
		if !strings.Contains(w.story, old) {
			t.Fatalf("the sample's story no longer holds %q", old)
		}
		return webshop{story: strings.Replace(w.story, old, new, 1), setup: w.setup}
	}
	// The sample's first Framework's template url and its GitProvider's url,
	// as the setup spells them.
	m := decode(t, w, "")
	templateURL := "url: " + m.Objects.Frameworks[0].Spec.Template.URL + "\n"
	gp := m.Objects.GitProviders[0]
	providerURL := "url: " + gp.Spec.URL + "\n"
	password := rand.Text()
	cases := []struct {
		name  string
		files webshop
		want  []string
	}{
		// A template is fetched over https or http only, as the CRD's pattern says.
		{"#268 a template url is https or http: not file", inSetup(templateURL,
			"url: file:///srv/templates/python-fastapi\n"), []string{"spec.template.url", "^https?://"}},
		{"#268 a template url is https or http: not a path", inSetup(templateURL,
			"url: /srv/templates/python-fastapi\n"), []string{"spec.template.url", "^https?://"}},
		{"#268 a template url is https or http: not ssh", inSetup(templateURL,
			"url: ssh://git@templates.example/python-fastapi\n"), []string{"spec.template.url", "^https?://"}},
		{"#268 a template url is https or http: not the scp form", inSetup(templateURL,
			"url: git@templates.example:python-fastapi\n"), []string{"spec.template.url", "^https?://"}},
		{"#268 a GitProvider's url is https or http", inSetup(providerURL, "url: git@git.example.org:webshop\n"),
			[]string{"GitProvider " + gp.Name, "spec.url", "^https?://"}},
		// The refusal names the field and the rule, never the value.
		{"#268 a refusal quotes no part of the url", inSetup(providerURL,
			"url: ftp://webshop:"+password+"@git.example.org\n"), []string{"spec.url", "^https?://"}},
		{"#268 a template's path has no .. segment", inSetup("  ref: v1.6.0\n", "  ref: v1.6.0\n    path: ../shared\n"),
			[]string{"spec.template.path", "path has no . or .. segments"}},
		{"#268 a template's ref is not empty", inSetup("  ref: v1.6.0\n", "  ref: \"\"\n"),
			[]string{"spec.template.ref"}},
		{"#268 a Container's checks is one the enum lists", inStory("  type: service\n",
			"  type: service\n  checks: Sometimes\n"), []string{"spec.checks", `supported values: "EveryChange"`}},
		{"#268 a display name is at most 100 characters", inSetup("displayName: Python, FastAPI (uv)\n",
			"displayName: "+strings.Repeat("x", 101)+"\n"), []string{"Framework python-fastapi", "spec.displayName"}},
		{"#268 a set list holds each value once", inSetup("supportedTypes: [service]\n",
			"supportedTypes: [service, service]\n"), []string{"spec.supportedTypes", "Duplicate value"}},
		{"#268 a Relationship does not point to itself", inStory(
			"  target:\n    kind: SoftwareSystem\n    name: webshop-core\n  description: Browses",
			"  target:\n    kind: Person\n    name: customer\n  description: Browses"),
			[]string{"Relationship customer-uses-webshop-core", "a Relationship cannot point to itself"}},
		{"#268 a name is a DNS subdomain", inStory("  name: orders-api\nspec:\n", "  name: orders_api\nspec:\n"),
			[]string{"a Container: metadata.name"}},
		{"#268 the Project's name is its namespace's, a DNS label", inStory("  name: webshop\n", "  name: web.shop\n"),
			[]string{"Project web.shop", "metadata.namespace"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := nocluster.Decode([]byte(c.files.story), []byte(c.files.setup), nil)
			if err == nil {
				t.Fatalf("decoded; want it refused saying %q", c.want)
			}
			for _, want := range c.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("err = %v, want one saying %s", err, want)
				}
			}
			if strings.Contains(err.Error(), password) {
				t.Error("the refusal quotes the url's password")
			}
		})
	}

	// The one rule the decoder leaves to a cluster: the provider enum, the
	// cluster's word on where it can host (docs/plans/no-cluster.md, decision
	// 2). With no cluster a GitProvider names a provider model.HostCI knows.
	provider := string(decode(t, w, "").Objects.GitProviders[0].Spec.Provider)
	for _, host := range model.Hosts() {
		setup := strings.Replace(w.setup, "\n  provider: "+provider+"\n", "\n  provider: "+host+"\n", 1)
		if _, err := nocluster.Decode([]byte(w.story), []byte(setup), nil); err != nil {
			t.Errorf("#268 a GitProvider on %s with no cluster: %v", host, err)
		}
	}
}

// #268: the decoder reads a model file as the API server reads a create: a
// field written as null is read as left out, a whole number is an integer,
// status is not set by a create, a name is used once per kind, and the
// decoder's own refusals name the field and the rule, never the value.
func TestDecodeReadsACreateAsTheAPIServer268(t *testing.T) {
	w := readWebshop(t)
	replace := func(s, old, new string) string {
		t.Helper()
		if !strings.Contains(s, old) {
			t.Fatalf("the sample no longer holds %q", old)
		}
		return strings.Replace(s, old, new, 1)
	}
	inStory := func(old, new string) webshop { return webshop{story: replace(w.story, old, new), setup: w.setup} }
	inSetup := func(old, new string) webshop { return webshop{story: w.story, setup: replace(w.setup, old, new)} }
	const ordersAPI = "  name: orders-api\nspec:\n"
	const description = "  description: Keeps customers' accounts, takes and tracks carts, orders and reorders, " +
		"and starts their payments.\n"
	// orders-api's deploy, the last field of its document.
	const next = "---\n# A worker on Kubernetes"
	const deploy = "  deploy:\n    - target: kind-cluster\n" + next
	container := func(t *testing.T, m *nocluster.Model) c4v1beta1.Container {
		t.Helper()
		if m == nil {
			t.Fatal("no model")
		}
		return *modelfacts.Find(m.Containers, "orders-api")
	}
	refused := func(t *testing.T, m *nocluster.Model, err error, want ...string) {
		t.Helper()
		if err == nil || m != nil {
			t.Fatalf("decoded %v, %v; want it refused saying %q", m != nil, err, want)
		}
		for _, w := range want {
			if !strings.Contains(err.Error(), w) {
				t.Errorf("err = %v, want one saying %s", err, w)
			}
		}
	}
	value := strings.ToLower(rand.Text())
	quotes := func(t *testing.T, err error) {
		t.Helper()
		if err != nil && strings.Contains(strings.ToLower(err.Error()), value) {
			t.Errorf("the refusal quotes the value: %v", err)
		}
	}
	leftOut := decode(t, webshop{story: replace(replace(w.story, description, ""), deploy, next), setup: w.setup}, "")
	gp := decode(t, w, "").Objects.GitProviders[0]
	provider := string(gp.Spec.Provider)
	// A GitProvider whose name the rules refuse and whose provider no CI is
	// known for.
	unknownProvider := webshop{story: w.story, setup: replace(replace(w.setup, "\n  provider: "+provider+"\n",
		"\n  provider: bitbucket\n"), "  name: "+gp.Name+"\n", "  name: x_"+value+"\n")}
	cases := []struct {
		name  string
		files webshop
		check func(t *testing.T, m *nocluster.Model, err error)
	}{
		{"#268 a field written as null is read as left out", webshop{story: replace(replace(w.story, description,
			"  description:\n"), deploy, "  deploy:\n  checkCommand: ~\n  license:\n  operations: null\n"+next), setup: w.setup},
			func(t *testing.T, m *nocluster.Model, err error) {
				if err != nil {
					t.Fatal(err)
				}
				if got, want := container(t, m), container(t, leftOut); !reflect.DeepEqual(got, want) {
					t.Errorf("orders-api decoded as %+v, want it as with the fields left out, %+v", got.Spec, want.Spec)
				}
			}},
		{"#268 a field with a default written as null takes the default", inSetup("\n  provider: "+provider+"\n",
			"\n  provider: null\n"), func(t *testing.T, m *nocluster.Model, err error) {
			if err != nil {
				t.Fatal(err)
			}
			// The CRD's default, as a GitProvider that leaves provider out has it.
			want := decode(t, inSetup("\n  provider: "+provider+"\n", "\n"), "").Objects.GitProviders[0].Spec.Provider
			if got := m.Objects.GitProviders[0].Spec.Provider; got == "" || got != want {
				t.Errorf("provider %q, want the CRD's default %q", got, want)
			}
		}},
		{"#268 a required field written as null is refused", inStory(ordersAPI+"  softwareSystemRef:\n    name: "+
			"webshop-core\n", ordersAPI+"  softwareSystemRef: null\n"), func(t *testing.T, m *nocluster.Model, err error) {
			refused(t, m, err, "Container orders-api", "spec.softwareSystemRef", "Required value")
		}},
		{"#268 a whole number is an integer", inStory(next, "  operations:\n    backups:\n      retentionDays: 7\n"+next),
			func(t *testing.T, m *nocluster.Model, err error) {
				if err != nil {
					t.Fatal(err)
				}
				if b := container(t, m).Spec.Operations.Backups; b == nil || b.RetentionDays == nil || *b.RetentionDays != 7 {
					t.Errorf("backups %+v, want retentionDays 7", b)
				}
			}},
		{"#268 a create sets no status", inStory(next, "status:\n  scaffoldedFrom: [python-fastapi]\n"+next),
			func(t *testing.T, m *nocluster.Model, err error) {
				if err != nil {
					t.Fatal(err)
				}
				if s := container(t, m).Status; !reflect.DeepEqual(s, c4v1beta1.ContainerStatus{}) {
					t.Errorf("orders-api's status %+v, want none", s)
				}
			}},
		{"#268 a create reads status strictly before it drops it", inStory(next, "status:\n  bogus: 1\n"+next),
			func(t *testing.T, m *nocluster.Model, err error) {
				refused(t, m, err, "Container orders-api", `unknown field "status.bogus"`)
			}},
		{"#268 a kind with no status subresource has no status", inStory("  description: Someone who buys from the "+
			"shop.\n", "  description: Someone who buys from the shop.\nstatus:\n  seen: true\n"),
			func(t *testing.T, m *nocluster.Model, err error) { refused(t, m, err, `"status"`) }},
		{"#268 a name is used once per kind", webshop{story: w.story + "\n---\napiVersion: theos.factory.io/v1beta1\n" +
			"kind: Person\nmetadata:\n  name: customer\nspec:\n  displayName: Another\n", setup: w.setup},
			func(t *testing.T, m *nocluster.Model, err error) {
				refused(t, m, err, "Person customer", "a name is used once per kind")
			}},
		{"#268 a name may be used by two kinds", webshop{story: w.story + "\n---\napiVersion: theos.factory.io/v1beta1\n" +
			"kind: Person\nmetadata:\n  name: orders-api\nspec:\n  displayName: Orders\n", setup: w.setup},
			func(t *testing.T, m *nocluster.Model, err error) {
				if err != nil {
					t.Fatal(err)
				}
				if len(m.People) != 2 || modelfacts.Find(m.People, "orders-api") == nil {
					t.Errorf("decoded %d People", len(m.People))
				}
			}},
		{"#268 metadata.resourceVersion is the API server's", inStory(ordersAPI, "  name: orders-api\n  "+
			"resourceVersion: \"1\"\nspec:\n"), func(t *testing.T, m *nocluster.Model, err error) {
			refused(t, m, err, "Container orders-api", "metadata.resourceVersion")
		}},
		// The decoder's own refusals name the field and the rule.
		{"#268 an apiVersion is named, not quoted", webshop{story: w.story + "\n---\napiVersion: " + value +
			"/v1\nkind: Person\nmetadata: {name: p}\n", setup: w.setup}, func(t *testing.T, m *nocluster.Model, err error) {
			refused(t, m, err, "apiVersion is not theos.factory.io/v1beta1")
			quotes(t, err)
		}},
		{"#268 a kind is named, not quoted", webshop{story: w.story + "\n---\napiVersion: theos.factory.io/v1beta1\n" +
			"kind: " + value + "\nmetadata: {name: p}\n", setup: w.setup}, func(t *testing.T, m *nocluster.Model, err error) {
			refused(t, m, err, "kind is a kind this file does not hold")
			quotes(t, err)
		}},
		{"#268 a namespace is named, not quoted", inStory(ordersAPI, "  name: orders-api\n  namespace: "+value+
			"\nspec:\n"), func(t *testing.T, m *nocluster.Model, err error) {
			refused(t, m, err, "Container orders-api", "metadata.namespace is not the Project's name")
			quotes(t, err)
		}},
		{"#268 a name the rules refuse is not quoted with a spec refusal", inStory(ordersAPI+"  softwareSystemRef:",
			"  name: x_"+value+"\nspec:\n  checks: Sometimes\n  softwareSystemRef:"),
			func(t *testing.T, m *nocluster.Model, err error) {
				refused(t, m, err, "a Container: ", "spec.checks")
				quotes(t, err)
			}},
		{"#268 a name the rules refuse is not quoted", inStory(ordersAPI, "  name: x_"+value+"\nspec:\n"),
			func(t *testing.T, m *nocluster.Model, err error) {
				refused(t, m, err, "a Container: metadata.name")
				quotes(t, err)
			}},
		{"#268 a GitProvider's name the rules refuse is not quoted", unknownProvider,
			func(t *testing.T, m *nocluster.Model, err error) {
				refused(t, m, err, "a GitProvider: ", "spec.provider is not a host", strings.Join(model.Hosts(), ", "))
				quotes(t, err)
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := nocluster.Decode([]byte(c.files.story), []byte(c.files.setup), nil)
			c.check(t, m, err)
		})
	}
}

// sampleModel is a model the repository ships, as the documents of its
// story and its setup, and its settings.
type sampleModel struct {
	name         string
	story, setup []map[string]any
	settings     []byte
}

// files is the model's documents as model files.
func (s sampleModel) files(t *testing.T) (story, setup []byte) {
	t.Helper()
	stream := func(docs []map[string]any) []byte {
		var b bytes.Buffer
		for _, doc := range docs {
			data, err := yaml.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			b.WriteString("---\n")
			b.Write(data)
		}
		return b.Bytes()
	}
	return stream(s.story), stream(s.setup)
}

// documents reads the YAML documents of data, those of the story's kinds
// when storyOnly is set: a sample beside a story may hold a Crew, which no
// model file holds.
func documents(t *testing.T, data []byte, storyOnly bool) []map[string]any {
	t.Helper()
	story := []string{"Project", "Person", "SoftwareSystem", "Container", "DataStore", "Relationship"}
	reader := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(data)))
	var docs []map[string]any
	for {
		doc, err := reader.Read()
		if err == io.EOF {
			return docs
		}
		if err != nil {
			t.Fatal(err)
		}
		var obj map[string]any
		if err := yaml.Unmarshal(doc, &obj); err != nil {
			t.Fatal(err)
		}
		switch kind, _ := obj["kind"].(string); {
		case obj == nil:
		case storyOnly && kind == "Crew":
		case storyOnly && !slices.Contains(story, kind):
			t.Fatalf("a sample beside a story holds a %s", kind)
		default:
			docs = append(docs, obj)
		}
	}
}

// sampleModels are the models the repository ships: the webshop, the first
// app, the adopted repository and each repository adopt proposes for, on the
// webshop's setup, and what init writes.
func sampleModels(t *testing.T) []sampleModel {
	t.Helper()
	read := func(file string, storyOnly bool) []map[string]any {
		data, err := os.ReadFile(filepath.Join("..", "..", "config", "samples", file))
		if err != nil {
			t.Fatal(err)
		}
		return documents(t, data, storyOnly)
	}
	setup := slices.Concat(read("webshop/factory.yaml", false), read("webshop/bundle.yaml", false))
	project := read("webshop/project.yaml", false)
	models := []sampleModel{
		{name: "webshop", story: slices.Concat(project, read("webshop/story.yaml", false)), setup: setup},
		// #333: the webshop's Crew and Agents, as the lead runtime reads them.
		{name: "webshop with its Crew", story: slices.Concat(project, read("webshop/story.yaml", false)),
			setup: slices.Concat(setup, read("webshop/crew.yaml", false))},
		{name: "first-app", story: slices.Concat(read("first-app/project.yaml", false), read("first-app/story.yaml", true)),
			setup: setup},
		{name: "adopt", story: slices.Concat(project, read("adopt/story.yaml", true)), setup: setup},
	}
	proposed, err := filepath.Glob(filepath.Join("..", "..", "config", "samples", "adopt", "proposed", "*.yaml"))
	if err != nil || len(proposed) == 0 {
		t.Fatalf("adopt's proposals: %v", err)
	}
	billing := []map[string]any{{"apiVersion": "theos.factory.io/v1beta1", "kind": "Project",
		"metadata": map[string]any{"name": "billing"}, "spec": map[string]any{"displayName": "Billing"}}}
	for _, file := range proposed {
		models = append(models, sampleModel{name: "adopt/proposed/" + filepath.Base(file),
			story: slices.Concat(billing, read("adopt/proposed/"+filepath.Base(file), true)), setup: setup})
	}
	const theosURL, tag = "https://github.com/JairJosafath/theos", "v0.9.0"
	files, err := nocluster.Init(context.Background(), chartFetcher{t: t, url: theosURL, ref: tag},
		nocluster.InitOptions{Project: "shop", Remote: "https://git.example.com/acme/theos-shop.git",
			Provider: model.Hosts()[0], TheosURL: theosURL, Ref: tag})
	if err != nil {
		t.Fatal(err)
	}
	return append(models, sampleModel{name: "init", story: documents(t, files[nocluster.ModelFile], false),
		setup: documents(t, files[nocluster.SetupFile], false), settings: files[nocluster.SettingsFile]})
}

// #268: every model the repository ships decodes.
func TestSampleModelsDecode268(t *testing.T) {
	for _, s := range sampleModels(t) {
		story, setup := s.files(t)
		m, err := nocluster.Decode(story, setup, s.settings)
		if err != nil {
			t.Errorf("#268 %s: %v", s.name, err)
			continue
		}
		if len(m.Containers)+len(m.Systems) == 0 && s.name != "init" {
			t.Errorf("#268 %s decoded no SoftwareSystem and no Container", s.name)
		}
	}
}

// fieldPaths are the paths to each field of v, at any depth, through lists.
func fieldPaths(v any, at []any) [][]any {
	var out [][]any
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			p := append(slices.Clone(at), k)
			out = append(out, p)
			out = append(out, fieldPaths(x, p)...)
		}
	case []any:
		for i, x := range v {
			out = append(out, fieldPaths(x, append(slices.Clone(at), i))...)
		}
	}
	return out
}

// withField is a copy of doc whose field at the path field is set to null,
// or left out when null is false.
func withField(t *testing.T, doc map[string]any, field []any, null bool) map[string]any {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	var parent any = out
	for _, step := range field[:len(field)-1] {
		if i, ok := step.(int); ok {
			parent = parent.([]any)[i]
		} else {
			parent = parent.(map[string]any)[step.(string)]
		}
	}
	key := field[len(field)-1].(string)
	if null {
		parent.(map[string]any)[key] = nil
	} else {
		delete(parent.(map[string]any), key)
	}
	return out
}

// #268: a field written as null reads as the field left out, as on the API
// server: in every model the repository ships, each field written once as
// null decodes to what the model with that field left out decodes to, and
// is refused where leaving it out is (a required field).
func TestNullIsLeftOut268(t *testing.T) {
	for _, s := range sampleModels(t) {
		t.Run(s.name, func(t *testing.T) {
			decoded, refused := 0, 0
			for f, docs := range [][]map[string]any{s.story, s.setup} {
				for i, doc := range docs {
					for _, field := range fieldPaths(doc, nil) {
						try := func(null bool) (*nocluster.Model, error) {
							variant := s
							changed := slices.Clone(docs)
							changed[i] = withField(t, doc, field, null)
							if f == 0 {
								variant.story = changed
							} else {
								variant.setup = changed
							}
							story, setup := variant.files(t)
							return nocluster.Decode(story, setup, s.settings)
						}
						mNull, errNull := try(true)
						mOut, errOut := try(false)
						switch {
						case (errNull == nil) != (errOut == nil):
							t.Errorf("#268 %s, document %d, %v: written as null %v, left out %v", s.name, i, field,
								errNull, errOut)
						case errNull != nil:
							refused++
						case !reflect.DeepEqual(mNull, mOut):
							t.Errorf("#268 %s, document %d, %v: null decodes unlike the field left out", s.name, i, field)
						default:
							decoded++
						}
					}
				}
			}
			if decoded == 0 || refused == 0 {
				t.Errorf("#268 %s: %d nulls decoded, %d refused; want some of each", s.name, decoded, refused)
			}
			t.Logf("%s: %d fields written as null decode as left out, %d are refused as left out", s.name, decoded,
				refused)
		})
	}
}

// #268: unset settings give the operator's flag defaults, and a key no step
// reads yet is refused.
func TestSettings268(t *testing.T) {
	s, err := nocluster.DecodeSettings(nil, "webshop")
	if err != nil {
		t.Fatal(err)
	}
	want := nocluster.Settings{Repository: "theos-webshop", Lease: "1h0m0s", Grace: "5m0s", Merge: model.MergePerson,
		Operator: nocluster.OperatorSettings{SessionFeature: features.SessionFeatureLocal}}
	if !reflect.DeepEqual(s, want) {
		t.Errorf("unset settings are %+v, want %+v", s, want)
	}
	got, err := s.Scaffold()
	if err != nil {
		t.Fatal(err)
	}
	// cmd/main.go's flags default to local, no binary URL and no CI images.
	opts, err := features.ScaffoldOptions(features.SessionFeatureLocal, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, modelfacts.Settings{Options: opts}) {
		t.Errorf("unset settings render with %+v", got)
	}

	set, err := nocluster.DecodeSettings([]byte("repository: shop-lead\noperator:\n  sessionFeature: none\n"+
		"  ciImageGo: registry.example/go:1\n"), "webshop")
	if err != nil {
		t.Fatal(err)
	}
	if set.Repository != "shop-lead" || set.Operator.SessionFeature != features.SessionFeatureNone ||
		set.Operator.CIImageGo != "registry.example/go:1" {
		t.Errorf("settings read %+v", set)
	}
	if _, err := nocluster.DecodeSettings([]byte("approvals: 2\n"), "webshop"); err == nil {
		t.Error("a key no step reads was taken")
	}
	if _, err := nocluster.DecodeSettings([]byte("merge: anyone\n"), "webshop"); err == nil {
		t.Error("merge: anyone was taken")
	}
}

// #294: the lease, the grace and the claimants the markers count by: the
// defaults with the credential's login, or as set; a lease that is no
// positive duration is refused.
func TestClaimSettings294(t *testing.T) {
	s, err := nocluster.DecodeSettings(nil, "webshop")
	if err != nil {
		t.Fatal(err)
	}
	rules, err := s.ClaimRules("shop-owner")
	if err != nil || !reflect.DeepEqual(rules, model.DefaultClaimRules("shop-owner")) {
		t.Errorf("default rules %+v, %v; want %+v", rules, err, model.DefaultClaimRules("shop-owner"))
	}
	s, err = nocluster.DecodeSettings([]byte("lease: 30m\ngrace: 2m\nclaimants: [shop-owner, shop-bot]\n"),
		"webshop")
	if err != nil {
		t.Fatal(err)
	}
	rules, err = s.ClaimRules("shop-owner")
	if err != nil || rules.Lease != 30*time.Minute || rules.Grace != 2*time.Minute ||
		!slices.Equal(rules.Claimants, []string{"shop-owner", "shop-bot"}) {
		t.Errorf("rules %+v, %v", rules, err)
	}
	for _, bad := range []string{"lease: soon\n", "grace: -1m\n", "lease: 0s\n"} {
		if _, err := nocluster.DecodeSettings([]byte(bad), "webshop"); err == nil {
			t.Errorf("%q was taken", bad)
		}
	}
}

// #268: internal/nocluster is the commands' logic on ports: the API's
// types, the model files' decoder and the business rules, with nothing of
// controller-runtime, client-go or an adapter below it (docs/architecture.md).
func TestImports(t *testing.T) {
	checkImports(t, ".", "internal/nocluster", func(p string) bool {
		return oneOf(p, "api/v1beta1", "config/crd/bases", "features", "internal/model", "internal/modelfacts",
			"internal/ports", "internal/scaffold", "internal/method", "internal/harness",
			"internal/harness/claudecode", "internal/harness/codex", "internal/engine") ||
			p == "sigs.k8s.io/yaml" || strings.HasPrefix(p, "k8s.io/apimachinery/") ||
			strings.HasPrefix(p, "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions") ||
			strings.HasPrefix(p, "k8s.io/apiextensions-apiserver/pkg/apiserver/schema") ||
			// The API server's validators of a custom resource at create.
			p == "k8s.io/apiextensions-apiserver/pkg/apiserver/validation" || p == "k8s.io/apiserver/pkg/apis/cel"
	})
	// The API's types, which the decoder decodes into, too.
	noClusterDeps(t, module+"api/v1beta1")
}

const module = "github.com/JairJosafath/theos/"

// oneOf reports whether p is one of the module's packages pkgs.
func oneOf(p string, pkgs ...string) bool {
	for _, pkg := range pkgs {
		if p == module+pkg {
			return true
		}
	}
	return false
}

// checkImports fails when the package in dir imports a package of this
// module or of Kubernetes that allowed refuses, and when it depends on
// controller-runtime or client-go.
func checkImports(t *testing.T, dir, name string, allowed func(string) bool) {
	t.Helper()
	pkg, err := build.ImportDir(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Imports) == 0 {
		t.Fatal("the package's imports were not read")
	}
	for _, p := range pkg.Imports {
		if (strings.HasPrefix(p, module) || strings.Contains(p, "k8s.io/")) && !allowed(p) {
			t.Errorf("%s imports %s (docs/architecture.md)", name, p)
		}
	}
	noClusterDeps(t, dir)
}

// noClusterDeps fails when pkg depends on controller-runtime or client-go.
func noClusterDeps(t *testing.T, pkg string) {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", pkg).Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v", pkg, err)
	}
	for dep := range strings.FieldsSeq(string(out)) {
		if strings.HasPrefix(dep, "sigs.k8s.io/controller-runtime") || strings.HasPrefix(dep, "k8s.io/client-go") {
			t.Errorf("%s depends on %s", pkg, dep)
		}
	}
}
