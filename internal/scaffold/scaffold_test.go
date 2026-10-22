package scaffold

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/JairJosafath/c4factory/internal/model"
)

func tree(kv ...string) Tree {
	t := Tree{}
	for i := 0; i < len(kv); i += 2 {
		t[kv[i]] = File{Data: []byte(kv[i+1])}
	}
	return t
}

var ordersAPI = Input{
	Project:        "webshop",
	SoftwareSystem: "webshop-core",
	Container:      "orders-api",
	DisplayName:    "Orders API",
	Type:           "service",
	Connectors:     []string{"postgres"},
}

func webshopLayers() []Layer {
	return []Layer{
		{Side: model.SideFramework, Name: "python-fastapi", Ref: "v1.2.0", Files: tree(
			"pyproject.toml.tmpl", `name = "{{ .Container }}"{{ if has .Connectors "postgres" }} # psycopg{{ end }}`,
			"app/main.py", "print('{{ not a template }}')",
			".c4factory/devcontainer.json", `{"image": "python:3.12", "features": {"ghcr.io/x/uv:1": {}}, "postCreateCommand": "a && b"}`,
			".c4factory/README.md", "about the template",
		)},
		{Side: model.SideCompute, Name: "small", Ref: "v1.0.0", Files: tree(
			"deploy/k8s/compute.yaml.tmpl", "name: {{ .Container }}",
		)},
		{Side: model.SideDeployment, Name: "k8s-manifests", Ref: "v1.0.0", Files: tree(
			"Dockerfile", "FROM python",
			"deploy/k8s/service.yaml.tmpl", `{{ if eq .Type "service" }}kind: Service{{ end }}`,
		)},
		{Side: model.SideConnector, Name: "postgres", Ref: "v1.1.0", Files: tree(
			".c4factory/compose.yaml.tmpl", "services:\n  postgres:\n    image: postgres:17\n  app:\n    environment:\n      DB: {{ snake .Container }}\n",
			"deploy/k8s/connector-postgres.yaml", "kind: ConfigMap",
		)},
	}
}

func TestBuildRendersLayers(t *testing.T) {
	res, err := Build(ordersAPI, webshopLayers(), Options{})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	wantPaths := []string{
		".c4factory.yaml",
		".devcontainer/compose.postgres.yaml",
		".devcontainer/compose.yaml",
		".devcontainer/devcontainer.json",
		"Dockerfile",
		"app/main.py",
		"deploy/k8s/compute.yaml",
		"deploy/k8s/connector-postgres.yaml",
		"deploy/k8s/service.yaml",
		"pyproject.toml",
	}
	if got := res.Files.Paths(); !slices.Equal(got, wantPaths) {
		t.Fatalf("paths =\n  %v\nwant\n  %v", got, wantPaths)
	}

	checks := map[string]string{
		"pyproject.toml":                      `name = "orders-api" # psycopg`,
		"app/main.py":                         "print('{{ not a template }}')",
		"deploy/k8s/compute.yaml":             "name: orders-api",
		"deploy/k8s/service.yaml":             "kind: Service",
		".devcontainer/compose.postgres.yaml": "      DB: orders_api\n",
	}
	for p, want := range checks {
		if got := string(res.Files[p].Data); !strings.Contains(got, want) {
			t.Errorf("%s = %q, want it to contain %q", p, got, want)
		}
	}

	wantFrom := []string{"python-fastapi@v1.2.0", "small@v1.0.0", "k8s-manifests@v1.0.0", "postgres@v1.1.0"}
	if !slices.Equal(res.ScaffoldedFrom, wantFrom) {
		t.Errorf("ScaffoldedFrom = %v, want %v", res.ScaffoldedFrom, wantFrom)
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	a, err := Build(ordersAPI, webshopLayers(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(ordersAPI, webshopLayers(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Error("two builds of the same input differ")
	}
}

func TestBuildLeavesOutWhitespaceOnlyTemplates(t *testing.T) {
	worker := ordersAPI
	worker.Type = "worker"
	res, err := Build(worker, webshopLayers(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := res.Files["deploy/k8s/service.yaml"]; ok {
		t.Error("service.yaml rendered for a worker, want it left out")
	}
}

func TestBuildConflicts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]Layer) []Layer
		want   string
	}{
		{
			name: "two layers write the same path",
			mutate: func(l []Layer) []Layer {
				l[2].Files["pyproject.toml"] = File{Data: []byte("x")}
				return l
			},
			want: "python-fastapi and k8s-manifests both write pyproject.toml",
		},
		{
			name: "a template and a plain file collide after rendering",
			mutate: func(l []Layer) []Layer {
				l[3].Files["deploy/k8s/compute.yaml"] = File{Data: []byte("x")}
				return l
			},
			want: "small and postgres both write deploy/k8s/compute.yaml",
		},
		{
			name: "a layer writes into .devcontainer",
			mutate: func(l []Layer) []Layer {
				l[0].Files[".devcontainer/devcontainer.json"] = File{Data: []byte("{}")}
				return l
			},
			want: "python-fastapi and the operator both write .devcontainer/devcontainer.json",
		},
		{
			name: "a layer writes the identity file",
			mutate: func(l []Layer) []Layer {
				l[1].Files[".c4factory.yaml"] = File{Data: []byte("x")}
				return l
			},
			want: "small and the operator both write .c4factory.yaml",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Build(ordersAPI, tt.mutate(webshopLayers()), Options{})
			if _, ok := errors.AsType[*ConflictError](err); !ok {
				t.Fatalf("Build() error = %v, want *ConflictError", err)
			}
			if err.Error() != tt.want {
				t.Errorf("error = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}

func TestBuildRejectsBadInput(t *testing.T) {
	tests := []struct {
		name   string
		in     Input
		layers func() []Layer
		want   string
	}{
		{
			name: "unknown template key",
			in:   ordersAPI,
			layers: func() []Layer {
				l := webshopLayers()
				l[1].Files["x.tmpl"] = File{Data: []byte("{{ .Nope }}")}
				return l
			},
			want: "small: x.tmpl:",
		},
		{
			name: "layers out of order",
			in:   ordersAPI,
			layers: func() []Layer {
				l := webshopLayers()
				l[1], l[2] = l[2], l[1]
				return l
			},
			want: "out of order",
		},
		{
			name:   "no framework",
			in:     Input{Container: "x"},
			layers: func() []Layer { return nil },
			want:   "exactly one Framework",
		},
		{
			name:   "connector layers don't match input",
			in:     Input{Container: "orders-api", Connectors: []string{"redis"}},
			layers: webshopLayers,
			want:   "don't match",
		},
		{
			name: "path escapes the repository",
			in:   ordersAPI,
			layers: func() []Layer {
				l := webshopLayers()
				l[0].Files["../evil"] = File{Data: []byte("x")}
				return l
			},
			want: "relative and clean",
		},
		{
			name: "template writes .git",
			in:   ordersAPI,
			layers: func() []Layer {
				l := webshopLayers()
				l[0].Files[".git/config"] = File{Data: []byte("x")}
				return l
			},
			want: "can't write .git",
		},
		{
			name: "template renders to .git",
			in:   ordersAPI,
			layers: func() []Layer {
				l := webshopLayers()
				l[0].Files[".git.tmpl"] = File{Data: []byte("x")}
				return l
			},
			want: "can't write .git",
		},
		{
			name: "template writes a nested .git",
			in:   ordersAPI,
			layers: func() []Layer {
				l := webshopLayers()
				l[0].Files["app/.git/config"] = File{Data: []byte("x")}
				return l
			},
			want: "can't write .git",
		},
		{
			name: "framework devcontainer.json is null",
			in:   ordersAPI,
			layers: func() []Layer {
				l := webshopLayers()
				l[0].Files[".c4factory/devcontainer.json"] = File{Data: []byte("null")}
				return l
			},
			want: "python-fastapi: .c4factory/devcontainer.json:",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Build(tt.in, tt.layers(), Options{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Build() error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestDevcontainerWithConnectorsUsesCompose(t *testing.T) {
	res, err := Build(ordersAPI, webshopLayers(), Options{
		Features:          map[string]any{"./c4session": map[string]any{}},
		DevcontainerFiles: tree("c4session/install.sh", "#!/bin/sh"),
	})
	if err != nil {
		t.Fatal(err)
	}
	var dc map[string]any
	if err := json.Unmarshal(res.Files[".devcontainer/devcontainer.json"].Data, &dc); err != nil {
		t.Fatal(err)
	}
	if _, ok := dc["image"]; ok {
		t.Error("image should move into compose.yaml when Connectors are used")
	}
	if dc["service"] != "app" || dc["name"] != "Orders API" {
		t.Errorf("service = %v, name = %v", dc["service"], dc["name"])
	}
	files, _ := dc["dockerComposeFile"].([]any)
	if len(files) != 2 || files[0] != "compose.yaml" || files[1] != "compose.postgres.yaml" {
		t.Errorf("dockerComposeFile = %v", dc["dockerComposeFile"])
	}
	features, _ := dc["features"].(map[string]any)
	if _, ok := features["ghcr.io/x/uv:1"]; !ok {
		t.Error("base features were dropped")
	}
	if _, ok := features["./c4session"]; !ok {
		t.Error("session feature missing")
	}
	if !strings.Contains(string(res.Files[".devcontainer/devcontainer.json"].Data), `"a && b"`) {
		t.Error("devcontainer.json escapes & for HTML; people read this file")
	}
	var compose struct {
		Services map[string]struct {
			Image   string   `yaml:"image"`
			Command []string `yaml:"command"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(res.Files[".devcontainer/compose.yaml"].Data, &compose); err != nil {
		t.Fatalf("compose.yaml is not valid YAML: %v", err)
	}
	if cmd := compose.Services["app"].Command; len(cmd) != 3 || !strings.Contains(cmd[2], "pkill -TERM -x c4session") {
		t.Errorf("app command = %q, want it to stop the Session client on SIGTERM", cmd)
	}
	if !strings.Contains(string(res.Files[".devcontainer/compose.yaml"].Data), "image: python:3.12") {
		t.Error("compose.yaml doesn't carry the Framework image")
	}
	if _, ok := res.Files[".devcontainer/c4session/install.sh"]; !ok {
		t.Error("devcontainer files missing")
	}
}

func TestDevcontainerWithoutConnectorsUsesImage(t *testing.T) {
	in := ordersAPI
	in.Connectors = nil
	res, err := Build(in, webshopLayers()[:3], Options{})
	if err != nil {
		t.Fatal(err)
	}
	var dc map[string]any
	if err := json.Unmarshal(res.Files[".devcontainer/devcontainer.json"].Data, &dc); err != nil {
		t.Fatal(err)
	}
	if dc["image"] != "python:3.12" {
		t.Errorf("image = %v, want python:3.12", dc["image"])
	}
	if _, ok := res.Files[".devcontainer/compose.yaml"]; ok {
		t.Error("compose.yaml written without Connectors")
	}
}

func TestIdentityRoundTrip(t *testing.T) {
	res, err := Build(ordersAPI, webshopLayers(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	id, err := ParseIdentity(res.Files[IdentityPath].Data)
	if err != nil {
		t.Fatal(err)
	}
	want := Identity{
		Project: "webshop", SoftwareSystem: "webshop-core", Container: "orders-api", Type: "service",
		ScaffoldedFrom: []string{"python-fastapi@v1.2.0", "small@v1.0.0", "k8s-manifests@v1.0.0", "postgres@v1.1.0"},
	}
	if !reflect.DeepEqual(id, want) {
		t.Errorf("identity = %+v, want %+v", id, want)
	}

	// A Container with a Standard records its checks; empty reads as EveryChange.
	for checks, want := range map[string]string{"": "EveryChange", "BeforeRelease": "BeforeRelease"} {
		in := ordersAPI
		in.Standard, in.Checks = "python-standard", checks
		res, err := Build(in, withStandard(webshopLayers()), Options{})
		if err != nil {
			t.Fatal(err)
		}
		if id, err := ParseIdentity(res.Files[IdentityPath].Data); err != nil || id.Checks != want {
			t.Errorf("identity checks with %q = %q, %v, want %q", checks, id.Checks, err, want)
		}
	}
	if _, err := ParseIdentity([]byte("project: webshop\n")); err == nil {
		t.Error("ParseIdentity accepted a file without container")
	}
}

func TestCommitMessageRoundTrip(t *testing.T) {
	from := []string{"python-fastapi@v1.2.0", "postgres@v1.1.0"}
	rels := []model.AppliedRelationship{{Name: "orders-api-to-orders-db", DataStore: "orders-db", Connector: "postgres"}}
	info, err := ParseCommitMessage(CommitMessage(ordersAPI, from, rels))
	if err != nil {
		t.Fatal(err)
	}
	if info.Container != "webshop/orders-api" || !slices.Equal(info.ScaffoldedFrom, from) ||
		!slices.Equal(info.Relationships, rels) {
		t.Errorf("info = %+v", info)
	}

	// A commit made before targets were recorded names only the Relationship.
	old := strings.Replace(CommitMessage(ordersAPI, from, rels), " DataStore/orders-db Connector/postgres", "", 1)
	info, err = ParseCommitMessage(old)
	if err != nil || !slices.Equal(info.Relationships, []model.AppliedRelationship{{Name: "orders-api-to-orders-db"}}) {
		t.Errorf("info from a name-only commit = %+v, %v", info, err)
	}
	if info.Checks != "" {
		t.Errorf("checks of a Container without a Standard = %q, want none", info.Checks)
	}
	in := ordersAPI
	in.Standard, in.Checks = "python-standard", model.ChecksBeforeRelease
	if info, err := ParseCommitMessage(CommitMessage(in, from, rels)); err != nil || info.Checks != "BeforeRelease" {
		t.Errorf("checks = %q, %v, want BeforeRelease", info.Checks, err)
	}
	if info.Bundle != nil {
		t.Errorf("bundle of a Container naming its sides = %+v, want none", info.Bundle)
	}
	// A bundled Container's commit records the expansion it scaffolded from (#1621).
	in.Bundle, in.Framework, in.Compute, in.Deployment = "web-service", "python-fastapi", "small", "k8s-manifests"
	want := &model.Bundle{Name: "web-service", Framework: "python-fastapi", Standard: "python-standard",
		Compute: "small", Deployment: "k8s-manifests"}
	if info, err := ParseCommitMessage(CommitMessage(in, from, rels)); err != nil || !reflect.DeepEqual(info.Bundle, want) {
		t.Errorf("bundle = %+v, %v, want %+v", info.Bundle, err, want)
	}
	if _, err := ParseCommitMessage("Initial commit\n"); !errors.Is(err, ErrNotOurs) {
		t.Errorf("ParseCommitMessage(foreign) error = %v, want ErrNotOurs", err)
	}
}

// withStandard puts a Standard layer right after the Framework.
func withStandard(layers []Layer) []Layer {
	standard := Layer{Side: model.SideStandard, Name: "python-standard", Ref: "v1.0.0", Files: tree(
		"AGENTS.md.tmpl", "Rules for {{ .Container }}, checked by {{ .Standard }}",
		"Makefile", "check:\n\ttrue\n",
	)}
	return append([]Layer{layers[0], standard}, layers[1:]...)
}

func TestBuildRendersStandardSecond(t *testing.T) {
	in := ordersAPI
	in.Standard = "python-standard"
	result, err := Build(in, withStandard(webshopLayers()), Options{})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	want := []string{"python-fastapi@v1.2.0", "python-standard@v1.0.0", "small@v1.0.0", "k8s-manifests@v1.0.0", "postgres@v1.1.0"}
	if !slices.Equal(result.ScaffoldedFrom, want) {
		t.Errorf("ScaffoldedFrom = %v, want %v", result.ScaffoldedFrom, want)
	}
	if got := string(result.Files["AGENTS.md"].Data); got != "Rules for orders-api, checked by python-standard" {
		t.Errorf("AGENTS.md = %q", got)
	}
}

// A Standard tells what the Framework it renders for has from the paths the
// Framework rendered, and the Framework itself sees none (#1249).
func TestBuildTellsLaterLayersTheFrameworksFiles(t *testing.T) {
	layers := withStandard(webshopLayers())
	layers[0].Files["framework.txt.tmpl"] = File{Data: []byte(`{{ len .FrameworkFiles }}`)}
	layers[1].Files["standard.txt.tmpl"] = File{Data: []byte(
		`{{ if has .FrameworkFiles "app/main.py" }}main{{ end }}{{ if has .FrameworkFiles "infra/operations.ts" }}ops{{ end }}`)}
	in := ordersAPI
	in.Standard = "python-standard"
	result, err := Build(in, layers, Options{})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if got := string(result.Files["framework.txt"].Data); got != "0" {
		t.Errorf("the Framework sees %s FrameworkFiles, want 0", got)
	}
	if got := string(result.Files["standard.txt"].Data); got != "main" {
		t.Errorf("standard.txt = %q, want main", got)
	}
}

func TestBuildStandardConflictsWithFramework(t *testing.T) {
	layers := withStandard(webshopLayers())
	layers[1].Files["pyproject.toml"] = File{Data: []byte("x")}
	_, err := Build(ordersAPI, layers, Options{})
	if err == nil || err.Error() != "python-fastapi and python-standard both write pyproject.toml" {
		t.Fatalf("Build() error = %v", err)
	}
}

func TestBuildRejectsStandardBeforeFramework(t *testing.T) {
	layers := withStandard(webshopLayers())
	layers[0], layers[1] = layers[1], layers[0]
	_, err := Build(ordersAPI, layers, Options{})
	if err == nil || !strings.Contains(err.Error(), "out of order") {
		t.Fatalf("Build() error = %v, want out of order", err)
	}
}

func TestTSString(t *testing.T) {
	tests := []struct{ in, want string }{
		{"ops@example.com", `'ops@example.com'`},
		{"", `''`},
		{"o'neil@example.com", `"o'neil@example.com"`},
		{`a'b"c`, `'a\'b"c'`},
		{`a'b'"c`, `"a'b'\"c"`},
		{`C:\tools`, `'C:\\tools'`},
		{"Tab\there", `'Tab\u0009here'`},
	}
	for _, tt := range tests {
		if got := tsString(tt.in); got != tt.want {
			t.Errorf("tsString(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

func TestDartString(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Photo Editor", `'Photo Editor'`},
		{"Rick's Editor", `"Rick's Editor"`},
		{`Rick's "Editor"`, `'Rick\'s "Editor"'`},
		{"Pay $5", `r'Pay $5'`},
		{`Pay $5 "now" 'please'`, `'Pay \$5 "now" \'please\''`},
		{`C:\tools`, `r'C:\tools'`},
		{"Tab\there", `'Tab\u{9}here'`},
	}
	for _, tt := range tests {
		if got := dartString(tt.in); got != tt.want {
			t.Errorf("dartString(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}
