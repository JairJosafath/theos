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

package modelfacts_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"go/build"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/modelfacts"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// sample is the webshop sample (config/samples/webshop) as a caller reads it.
type sample struct {
	objs       modelfacts.Objects
	containers []c4v1beta1.Container
	systems    []c4v1beta1.SoftwareSystem
}

func readSample(t *testing.T, token string) sample {
	t.Helper()
	var s sample
	dir := filepath.Join("..", "..", "config", "samples", "webshop")
	for _, file := range []string{"project.yaml", "factory.yaml", "bundle.yaml", "story.yaml"} {
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		reader := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(data)))
		for {
			doc, err := reader.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			var head struct{ Kind string }
			if err := yaml.Unmarshal(doc, &head); err != nil {
				t.Fatal(err)
			}
			into := func(obj any) {
				if err := yaml.UnmarshalStrict(doc, obj); err != nil {
					t.Fatalf("%s %s: %v", file, head.Kind, err)
				}
			}
			switch head.Kind {
			case "Project":
				s.objs.Project = &c4v1beta1.Project{}
				into(s.objs.Project)
			case "GitProvider":
				s.objs.GitProviders = append(s.objs.GitProviders, c4v1beta1.GitProvider{})
				into(&s.objs.GitProviders[len(s.objs.GitProviders)-1])
			case "Bundle":
				s.objs.Bundles = append(s.objs.Bundles, c4v1beta1.Bundle{})
				into(&s.objs.Bundles[len(s.objs.Bundles)-1])
			case "Framework":
				s.objs.Frameworks = append(s.objs.Frameworks, c4v1beta1.Framework{})
				into(&s.objs.Frameworks[len(s.objs.Frameworks)-1])
			case "Standard":
				s.objs.Standards = append(s.objs.Standards, c4v1beta1.Standard{})
				into(&s.objs.Standards[len(s.objs.Standards)-1])
			case "Compute":
				s.objs.Computes = append(s.objs.Computes, c4v1beta1.Compute{})
				into(&s.objs.Computes[len(s.objs.Computes)-1])
			case "Deployment":
				s.objs.Deployments = append(s.objs.Deployments, c4v1beta1.Deployment{})
				into(&s.objs.Deployments[len(s.objs.Deployments)-1])
			case "Connector":
				s.objs.Connectors = append(s.objs.Connectors, c4v1beta1.Connector{})
				into(&s.objs.Connectors[len(s.objs.Connectors)-1])
			case "Domain":
				s.objs.Domains = append(s.objs.Domains, c4v1beta1.Domain{})
				into(&s.objs.Domains[len(s.objs.Domains)-1])
			case "DataStore":
				s.objs.DataStores = append(s.objs.DataStores, c4v1beta1.DataStore{})
				into(&s.objs.DataStores[len(s.objs.DataStores)-1])
			case "Relationship":
				s.objs.Relationships = append(s.objs.Relationships, c4v1beta1.Relationship{})
				into(&s.objs.Relationships[len(s.objs.Relationships)-1])
			case "Container":
				s.containers = append(s.containers, c4v1beta1.Container{})
				into(&s.containers[len(s.containers)-1])
				// The sample's kustomization sets the namespace, the Project's name.
				s.containers[len(s.containers)-1].Namespace = "webshop"
			case "SoftwareSystem":
				s.systems = append(s.systems, c4v1beta1.SoftwareSystem{})
				into(&s.systems[len(s.systems)-1])
			}
		}
	}
	secret := s.objs.GitProviders[0].Spec.CredentialsSecretRef.Name
	s.objs.Secrets = map[string]ports.Credentials{secret: {Username: "webshop", Token: token}}
	return s
}

// templatesDir is a fetcher reading templates/<the URL's last segment>, the
// templates a pinned ref holds in this checkout.
type templatesDir struct {
	t     *testing.T
	creds []*ports.Credentials
}

func (f *templatesDir) FetchTemplate(_ context.Context, url, ref, dir string, creds *ports.Credentials) (scaffold.Tree, error) {
	f.creds = append(f.creds, creds)
	if ref == "" || dir != "" {
		f.t.Fatalf("fetched %s at %q dir %q: the sample pins every ref and names no path", url, ref, dir)
	}
	return scaffold.ReadTree(os.DirFS(filepath.Join("..", "..", "templates", path.Base(url))))
}

func find[T any, PT interface {
	*T
	GetName() string
}](t *testing.T, items []T, name string) PT {
	t.Helper()
	p := modelfacts.Find[T, PT](items, name)
	if p == nil {
		t.Fatalf("the sample has no %s", name)
	}
	return p
}

// #268: modelfacts gives the webshop sample's orders-api its tree from the
// objects alone; TestWebshopOrdersAPIRule gives its rule.
func TestWebshopOrdersAPITree(t *testing.T) {
	token := rand.Text()
	s := readSample(t, token)
	container := find(t, s.containers, "orders-api")
	system := find(t, s.systems, container.Spec.SoftwareSystemRef.Name)

	res, missing := modelfacts.Resolve(container, system, s.objs)
	if len(missing) > 0 {
		t.Fatalf("missing %v", missing)
	}
	if err := modelfacts.Check(container, res); err != nil {
		t.Fatalf("the sample's choices are refused: %v", err)
	}
	fetcher := &templatesDir{t: t}
	layers, err := modelfacts.FetchLayers(context.Background(), fetcher, res)
	if err != nil {
		t.Fatal(err)
	}
	for i, creds := range fetcher.creds {
		if creds == nil || creds.Token != token {
			t.Errorf("layer %d was fetched without the GitProvider's credentials, on its own host", i)
		}
	}
	ci, err := modelfacts.CI(res.Provider)
	if err != nil {
		t.Fatal(err)
	}
	license := modelfacts.License(container, s.objs.Project, time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC))
	render, err := modelfacts.Build(container, res, layers, ci, "", license, modelfacts.Settings{})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"python-fastapi@v1.6.0", "python-standard@v1.7.0", "small@v1.1.0", "k8s-manifests@v1.9.1",
		"postgres@v1.2.0", "webshop@v1.0.0"}
	if !slices.Equal(render.Result.ScaffoldedFrom, want) {
		t.Errorf("scaffolded from %v, want %v", render.Result.ScaffoldedFrom, want)
	}
	identity, ok := render.Result.Files[scaffold.IdentityPath]
	if !ok {
		t.Fatalf("no %s in the tree", scaffold.IdentityPath)
	}
	id, err := scaffold.ParseIdentity(identity.Data)
	if err != nil {
		t.Fatal(err)
	}
	if id.Project != "webshop" || id.Container != "orders-api" || !slices.Equal(id.ScaffoldedFrom, want) {
		t.Errorf("identity %+v", id)
	}
	if render.Input.NpmScope != ports.DefaultNpmScope || !slices.Equal(render.Input.Connectors, []string{"postgres"}) ||
		!slices.Equal(render.Input.Domains, []string{"webshop"}) || render.Input.Checks != model.ChecksEveryChange {
		t.Errorf("rendered with %+v", render.Input)
	}
	info, err := scaffold.ParseCommitMessage(render.Message)
	if err != nil || info.Container != "webshop/orders-api" || !slices.Equal(info.ScaffoldedFrom, want) ||
		len(info.Relationships) != 1 || info.Relationships[0].Name != "orders-api-to-shop-db" {
		t.Errorf("commit message reads %+v:\n%s", info, render.Message)
	}
	if _, ok := render.Result.Files["AGENTS.md"]; !ok {
		t.Error("the Standard's AGENTS.md is not in the tree")
	}
}

// #268: the rule the webshop sample's orders-api's main is protected with.
func TestWebshopOrdersAPIRule(t *testing.T) {
	s := readSample(t, rand.Text())
	container := find(t, s.containers, "orders-api")
	provider := find(t, s.objs.GitProviders, container.Spec.GitProviderRef.Name)
	ci, err := modelfacts.CI(provider)
	if err != nil {
		t.Fatal(err)
	}
	standard := find(t, s.objs.Standards, "python-standard")
	rule, protect, err := modelfacts.Protection(container, provider, standard)
	if err != nil || !protect {
		t.Fatalf("protect = %v, %v", protect, err)
	}
	if !slices.Equal(rule.StatusContexts, []string{"check*"}) {
		t.Errorf("rule requires %v", rule.StatusContexts)
	}
	// The host's CI files, then python-standard's own.
	for _, p := range append(slices.Clone(ci.ProtectedPaths), "AGENTS.md", "ruff.toml", "docs/agents/") {
		if !slices.Contains(rule.ProtectedPaths, p) {
			t.Errorf("rule leaves %s unprotected: %v", p, rule.ProtectedPaths)
		}
	}
	if rule.OperatorPushes {
		t.Error("the rule lets the protecting account push to main")
	}
}

// #268: what is missing is named in the order the scaffold reads it, a
// GitProvider's Secret too.
func TestResolveNamesWhatIsMissing(t *testing.T) {
	s := readSample(t, rand.Text())
	s.objs.Secrets = nil
	s.objs.Connectors = nil
	s.objs.Domains = nil
	container := find(t, s.containers, "orders-api")
	system := find(t, s.systems, container.Spec.SoftwareSystemRef.Name)
	_, missing := modelfacts.Resolve(container, system, s.objs)
	want := []string{"Secret " + s.objs.GitProviders[0].Spec.CredentialsSecretRef.Name, "Connector postgres",
		"Domain webshop"}
	if !slices.Equal(missing, want) {
		t.Errorf("missing %v, want %v", missing, want)
	}
}

// #268: a Container without a Standard has no checks to require, so its
// main is not protected.
func TestNoStandardNoRule(t *testing.T) {
	s := readSample(t, rand.Text())
	container := find(t, s.containers, "orders-api").DeepCopy()
	container.Spec.StandardRef = nil
	if _, protect, err := modelfacts.Protection(container, &s.objs.GitProviders[0], nil); err != nil || protect {
		t.Errorf("protect = %v, %v", protect, err)
	}
}

// #268: modelfacts is what the operator and the theos binary both link
// (docs/plans/no-cluster.md, decision 4), so it stays the API's types,
// apimachinery, the business rules and the ports, with nothing of
// controller-runtime or client-go below it.
func TestImports(t *testing.T) {
	const module = "github.com/JairJosafath/theos/"
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.Imports) == 0 {
		t.Fatal("the package's imports were not read")
	}
	for _, p := range pkg.Imports {
		switch {
		case p == module+"api/v1beta1", p == module+"internal/model", p == module+"internal/scaffold",
			p == module+"internal/ports", strings.HasPrefix(p, "k8s.io/apimachinery/"):
		case strings.HasPrefix(p, module), strings.Contains(p, "k8s.io/"):
			t.Errorf("internal/modelfacts imports %s: it reads the API's objects into the business rules' values, "+
				"and the theos binary links it without the operator (docs/architecture.md)", p)
		}
	}
	noClusterDeps(t, ".")
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
