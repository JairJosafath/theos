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

package controller

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/adapters/gitea"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/modelfacts"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

func TestScaffoldedConnectors(t *testing.T) {
	for _, tt := range []struct {
		name    string
		applied []c4v1beta1.AppliedConnector
		want    []string
	}{
		{name: "none"},
		{name: "unique and sorted, as the scaffold passed them", applied: []c4v1beta1.AppliedConnector{
			{Relationship: "a-to-orders-db", DataStore: "orders-db", Connector: "postgres"},
			{Relationship: "b-to-cache", DataStore: "cache", Connector: "redis"},
			{Relationship: "c-to-audit-db", DataStore: "audit-db", Connector: "postgres"},
			{Relationship: "d-to-events", DataStore: "events", Connector: "kafka"},
		}, want: []string{"kafka", "postgres", "redis"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := scaffoldedConnectors(&c4v1beta1.ContainerStatus{AppliedConnectors: tt.applied})
			if !slices.Equal(got, tt.want) {
				t.Errorf("scaffoldedConnectors() = %q, want %q", got, tt.want)
			}
			// The scaffold's own list, from the same Relationships, is the same.
			var rels []model.Relationship
			connectorOf := map[string]string{}
			for _, a := range tt.applied {
				rels = append(rels, model.Relationship{Name: a.Relationship,
					Source: model.Ref{Kind: model.KindContainer, Name: "orders-api"},
					Target: model.Ref{Kind: model.KindDataStore, Name: a.DataStore}})
				connectorOf[a.DataStore] = a.Connector
			}
			if use := model.DataStoresUsedBy("orders-api", rels, connectorOf); !slices.Equal(use.Connectors, got) {
				t.Errorf("the scaffold passed %q, the update %q", use.Connectors, got)
			}
		})
	}
}

func TestScaffoldInput(t *testing.T) {
	container := &c4v1beta1.Container{
		ObjectMeta: objectMeta("webshop", "orders-api"),
		Spec: c4v1beta1.ContainerSpec{
			SoftwareSystemRef: c4v1beta1.LocalRef{Name: "webshop-core"},
			DisplayName:       "Orders API",
			Description:       "Takes and lists the shop's orders.",
			Type:              c4v1beta1.ContainerTypeService,
			FrameworkRef:      c4v1beta1.LocalRef{Name: "python-fastapi"},
			StandardRef:       &c4v1beta1.LocalRef{Name: "python-strict"},
			DeploymentRef:     &c4v1beta1.LocalRef{Name: "k8s-manifests"},
			Operations: &c4v1beta1.OperationsSpec{
				Recovery: &c4v1beta1.OperationsRecovery{RPO: "15m"},
				Alerts:   &c4v1beta1.OperationsAlerts{Email: "ops@example.com"},
			},
		},
	}
	ci, err := model.HostCI("gitea")
	if err != nil {
		t.Fatal(err)
	}
	want := scaffold.Input{
		CI:      ci,
		Project: "webshop", SoftwareSystem: "webshop-core", Container: "orders-api", DisplayName: "Orders API",
		Description: "Takes and lists the shop's orders.",
		Type:        "service", Framework: "python-fastapi", Standard: "python-strict", Deployment: "k8s-manifests",
		Connectors: []string{"postgres"}, Domains: []string{"webshop"}, Checks: "BeforeRelease",
		Operations: model.Operations{
			Backups:  model.OperationsBackups{RetentionDays: model.DefaultRetentionDays},
			Recovery: model.OperationsRecovery{RPO: "15m", RTO: model.DefaultRTO},
			Alerts:   model.OperationsAlerts{Email: "ops@example.com"},
		},
		NpmScope: "@theos",
	}
	// An empty scope (a provider that sets none) is the default (#1231).
	got := modelfacts.ScaffoldInput(container, ci, []string{"postgres"}, []string{"webshop"}, model.ChecksBeforeRelease, model.CIImages{},
		specOperations(container), "", scaffold.License{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scaffoldInput() = %+v, want %+v", got, want)
	}
	// The GitProvider's own scope is passed through (#1231).
	want.NpmScope = "@acme"
	got = modelfacts.ScaffoldInput(container, ci, []string{"postgres"}, []string{"webshop"}, model.ChecksBeforeRelease, model.CIImages{},
		specOperations(container), "@acme", scaffold.License{})
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scaffoldInput(@acme) = %+v, want %+v", got, want)
	}
	// The resolved license is carried as is (#1788).
	want.License = scaffold.License{ID: "MIT", Holder: "Web Shop", Year: 2026}
	got = modelfacts.ScaffoldInput(container, ci, []string{"postgres"}, []string{"webshop"}, model.ChecksBeforeRelease, model.CIImages{},
		specOperations(container), "@acme", want.License)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scaffoldInput(license) = %+v, want %+v", got, want)
	}
	// The toolchain images are carried as they are (#284).
	want.CIImages = model.CIImages{Go: "registry.example.com/go:1.0.0", Base: "registry.example.com/base:1.0.0"}
	got = modelfacts.ScaffoldInput(container, ci, []string{"postgres"}, []string{"webshop"}, model.ChecksBeforeRelease, want.CIImages,
		specOperations(container), "@acme", want.License)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scaffoldInput(CIImages) = %+v, want %+v", got, want)
	}
}

// TestScaffoldedGitHubCheckRunsInTheOperatorsImage284 covers #284: the
// toolchain images the operator is given (--ci-image-go and --ci-image-base,
// the chart's ciImages) reach the GitHub check of every Standard it scaffolds
// with one, so a repository falls back to the chart's registry, not to the
// fixed GHCR name; with none given it falls back to the release's.
func TestScaffoldedGitHubCheckRunsInTheOperatorsImage284(t *testing.T) {
	github, err := model.CIByName(model.CIGitHubActions)
	if err != nil {
		t.Fatal(err)
	}
	configured := model.CIImages{Go: "registry.example.com:5000/tools/go:9.9.9",
		Base: "registry.example.com:5000/tools/base:9.9.9"}
	for _, standard := range []struct{ name, variable string }{
		{"go-standard", "THEOS_CI_IMAGE_GO"}, {"hello-standard", "THEOS_CI_IMAGE_BASE"},
	} {
		files, err := scaffold.ReadTree(os.DirFS(filepath.Join("..", "..", "templates", standard.name)))
		if err != nil {
			t.Fatal(err)
		}
		layer := scaffold.Layer{Side: model.SideStandard, Name: standard.name, Ref: "v1.0.0", Files: files}
		for _, c := range []struct {
			name   string
			images model.CIImages
			want   model.CIImages
		}{
			{"the chart's images", configured, configured},
			{"none given", model.CIImages{}, model.DefaultCIImages()},
		} {
			t.Run(standard.name+", "+c.name, func(t *testing.T) {
				container := &c4v1beta1.Container{
					ObjectMeta: objectMeta("webshop", "orders-api"),
					Spec: c4v1beta1.ContainerSpec{
						SoftwareSystemRef: c4v1beta1.LocalRef{Name: "webshop-core"},
						Type:              c4v1beta1.ContainerTypeService,
						FrameworkRef:      c4v1beta1.LocalRef{Name: "go-cli"},
						StandardRef:       &c4v1beta1.LocalRef{Name: standard.name},
					},
				}
				r := &ContainerReconciler{CIImages: c.images}
				in := modelfacts.ScaffoldInput(container, github, nil, nil, model.ChecksEveryChange, r.ciImages(),
					specOperations(container), "", scaffold.License{})
				tree, err := scaffold.RenderLayer(in, layer)
				if err != nil {
					t.Fatal(err)
				}
				check, ok := tree[github.Dir()+"workflows/check.yaml"]
				if !ok {
					t.Fatalf("%s renders no %sworkflows/check.yaml", standard.name, github.Dir())
				}
				image := map[string]string{"THEOS_CI_IMAGE_GO": c.want.Go, "THEOS_CI_IMAGE_BASE": c.want.Base}[standard.variable]
				want := "container: ${{ vars." + standard.variable + " || '" + image + "' }}"
				if !strings.Contains(string(check.Data), want) {
					t.Errorf("the check does not run in %s; want the line %q in:\n%s", image, want, check.Data)
				}
			})
		}
	}
}

func TestPlannedFile(t *testing.T) {
	current := scaffold.Tree{"Makefile": {Data: []byte("check:\n")}}
	for _, tc := range []struct {
		name string
		plan scaffold.Update
		want string
	}{
		{"kept", scaffold.Update{}, "check:\n"},
		{"written", scaffold.Update{Files: scaffold.Tree{"Makefile": {Data: []byte("check-light:\n")}}}, "check-light:\n"},
		{"left out", scaffold.Update{LeftOut: []string{"Makefile"}}, "check:\n"},
		{"removed", scaffold.Update{Removes: []string{"Makefile"}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(plannedFile(tc.plan, current, "Makefile")); got != tc.want {
				t.Errorf("plannedFile = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNoLightCheck(t *testing.T) {
	const tail = ", which make check-light on each change needs; CI keeps running the full check on every change " +
		"until the Makefile has one"
	const head = "CI can't change to run the full check only before a release: the repository's Makefile "
	behind := &c4v1beta1.ContainerStandardStatus{Name: "go-standard", Ref: "v1.2.1",
		Behind: []c4v1beta1.StandardFile{{Path: "go.mod", Ref: "v1.0.0"}, {Path: "Makefile", Ref: "v1.1.6"}}}
	for _, tc := range []struct {
		name string
		s    *c4v1beta1.ContainerStandardStatus
		plan scaffold.Update
		want string
	}{
		{"its own", &c4v1beta1.ContainerStandardStatus{Name: "go-standard"}, scaffold.Update{},
			"has no check-light target"},
		// The reporter's case: an earlier update left the Makefile out.
		{"left out before", behind, scaffold.Update{},
			"was left out of the go-standard updates (at v1.1.6) and has no check-light target"},
		{"left out now", &c4v1beta1.ContainerStandardStatus{Name: "go-standard"},
			scaffold.Update{LeftOut: []string{"Makefile"}},
			"is left out of this go-standard update and has no check-light target"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := noLightCheck(tc.s, tc.plan); got != head+tc.want+tail {
				t.Errorf("noLightCheck =\n%s\nwant\n%s", got, head+tc.want+tail)
			}
		})
	}
}

// A Standard update renders with the Domains the Container was scaffolded
// with, from status.scaffoldedDomains, sorted and unique, as the scaffold
// passed them: the bump pull request's files name the same Domains the
// repository's docs/agents/domain/ holds (#1757, #1935).
func TestStandardUpdateRendersWithScaffoldedDomains(t *testing.T) {
	ci, err := model.HostCI("gitea")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := gitea.Client{}.Provider(ports.Config{URL: "http://gitea", Owner: "apps"})
	if err != nil {
		t.Fatal(err)
	}
	container := &c4v1beta1.Container{
		ObjectMeta: objectMeta("webshop", "orders-api"),
		Spec: c4v1beta1.ContainerSpec{
			SoftwareSystemRef: c4v1beta1.LocalRef{Name: "webshop-core"},
			DisplayName:       "Orders API",
			Type:              c4v1beta1.ContainerTypeService,
			FrameworkRef:      c4v1beta1.LocalRef{Name: "python-fastapi"},
			StandardRef:       &c4v1beta1.LocalRef{Name: "python-strict"},
		},
	}
	for _, tc := range []struct {
		name       string
		scaffolded []string
		want       []string
		agents     string
	}{
		{name: "none", agents: "# Orders API\n"},
		{name: "sorted and unique, as the scaffold passed them",
			scaffolded: []string{"webshop", "billing", "webshop"}, want: []string{"billing", "webshop"},
			agents: "# Orders API\nThis system is about billing, webshop.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := &standardUpdate{
				r:         &ContainerReconciler{},
				container: container,
				status:    &c4v1beta1.ContainerStatus{ScaffoldedDomains: tc.scaffolded},
				// The repository's license and Framework files were read; the
				// render needs no Git.
				licenseRead: true, frameworkRead: true,
			}
			git := &gitAccess{gitProvider: &c4v1beta1.GitProvider{Spec: c4v1beta1.GitProviderSpec{URL: "http://gitea"}},
				provider: provider, ci: ci}
			standard := &c4v1beta1.Standard{ObjectMeta: objectMeta("webshop", "python-strict"),
				Spec: c4v1beta1.StandardSpec{CatalogEntrySpec: c4v1beta1.CatalogEntrySpec{
					Template: c4v1beta1.TemplateSource{URL: "http://gitea/templates/python-standard"}}}}
			renders, err := u.renders(t.Context(), t.Context(), git, standard)
			if err != nil {
				t.Fatalf("renders() = %v", err)
			}
			if !slices.Equal(renders.in.Domains, tc.want) {
				t.Errorf("renders().in.Domains = %q, want %q", renders.in.Domains, tc.want)
			}
			// What the Standard's own templates see, as templates/*-standard/AGENTS.md.tmpl reads it.
			renders.fetch = func(context.Context, string) (scaffold.Tree, error) {
				return scaffold.Tree{"AGENTS.md.tmpl": {Data: []byte("# {{ .DisplayName }}\n{{ if .Domains -}}\n" +
					"This system is about {{ range $i, $d := .Domains }}{{ if $i }}, {{ end }}{{ $d }}{{ end }}.\n{{ end -}}\n")}}, nil
			}
			tree, err := renders.at(t.Context(), "v1.1.0")
			if err != nil {
				t.Fatalf("at(v1.1.0) = %v", err)
			}
			if got := string(tree["AGENTS.md"].Data); got != tc.agents {
				t.Errorf("AGENTS.md at v1.1.0 = %q, want %q", got, tc.agents)
			}
		})
	}
}
