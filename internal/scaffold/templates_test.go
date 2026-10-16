package scaffold

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/JairJosafath/c4factory/internal/model"
)

// templatesDir is the repository's templates/ folder, which the operator's
// catalog pins and hack/render reads.
const templatesDir = "../../templates"

type side struct{ Side, Name string }

// templateCombos renders every template in templates/ in at least one
// realistic combination of layers, the way a Container picks them.
var templateCombos = []struct {
	name   string
	typ    string
	layers []side
}{
	{"fastapi service with postgres", "service", []side{
		{model.SideFramework, "python-fastapi"},
		{model.SideStandard, "python-standard"},
		{model.SideCompute, "small"},
		{model.SideDeployment, "k8s-manifests"},
		{model.SideConnector, "postgres"},
	}},
	{"fastapi worker", "worker", []side{
		{model.SideFramework, "python-fastapi"},
		{model.SideDeployment, "k8s-manifests"},
	}},
	{"go cli", "cli", []side{
		{model.SideFramework, "go-cli"},
		{model.SideStandard, "go-standard"},
		{model.SideDeployment, "gitea-release"},
	}},
	{"python cli", "cli", []side{
		{model.SideFramework, "python-cli"},
		{model.SideStandard, "python-standard"},
		{model.SideDeployment, "gitea-release"},
	}},
	{"vscode extension", "extension", []side{
		{model.SideFramework, "vscode-extension"},
		{model.SideStandard, "ts-standard"},
		{model.SideDeployment, "gitea-release"},
	}},
	{"kubebuilder operator", "operator", []side{
		{model.SideFramework, "go-kubebuilder"},
		{model.SideStandard, "go-operator-standard"},
		{model.SideCompute, "small"},
		{model.SideDeployment, "gitea-release"},
	}},
	{"react web app", "static-site", []side{
		{model.SideFramework, "react-vite"},
		{model.SideStandard, "ts-standard"},
	}},
	{"react web app on kubernetes", "static-site", []side{
		{model.SideFramework, "react-vite"},
		{model.SideStandard, "ts-standard"},
		{model.SideCompute, "small"},
		{model.SideDeployment, "k8s-manifests"},
	}},
	{"node cli", "cli", []side{
		{model.SideFramework, "node-cli"},
		{model.SideStandard, "ts-standard"},
		{model.SideDeployment, "gitea-release"},
	}},
	{"aws lambda function", "function", []side{
		{model.SideFramework, "aws-lambda-ts"},
		{model.SideStandard, "aws-standard"},
	}},
	{"aws ec2 service", "service", []side{
		{model.SideFramework, "aws-ec2-ts"},
		{model.SideStandard, "aws-standard"},
	}},
	{"aws fargate service", "service", []side{
		{model.SideFramework, "aws-fargate-ts"},
		{model.SideStandard, "aws-standard"},
	}},
	{"aws lambda function in go", "function", []side{
		{model.SideFramework, "aws-lambda-go"},
		{model.SideStandard, "aws-standard"},
	}},
	{"aws python lambda function", "function", []side{
		{model.SideFramework, "aws-lambda-py"},
		{model.SideStandard, "aws-standard"},
	}},
	{"aws queue consumer", "worker", []side{
		{model.SideFramework, "aws-queue-ts"},
		{model.SideStandard, "aws-standard"},
	}},
	{"hello", "cli", []side{
		{model.SideFramework, "hello"},
		{model.SideStandard, "hello-standard"},
	}},
	{"hello html", "static-site", []side{
		{model.SideFramework, "hello-html"},
		{model.SideStandard, "hello-html-standard"},
	}},
}

func TestEveryTemplateRenders(t *testing.T) {
	for _, c := range templateCombos {
		t.Run(c.name, func(t *testing.T) {
			layers, result := renderCombo(t, c.typ, c.layers)
			for _, p := range result.Files.Paths() {
				if ext := path.Ext(p); ext != ".yaml" && ext != ".yml" {
					continue
				}
				if err := parseYAML(result.Files[p].Data); err != nil {
					t.Errorf("%s: %s doesn't parse as YAML: %v", ownerOf(layers, p), p, err)
				}
			}
		})
	}
}

// TestK8sManifestsFollowsTheFramework checks that k8s-manifests writes the
// Dockerfile, .dockerignore, port and probes of the Framework's image, and no
// Dockerfile for a Framework it has none for.
func TestK8sManifestsFollowsTheFramework(t *testing.T) {
	cases := []struct {
		framework, typ string
		standard       string
		dockerfile     []string // substrings of the Dockerfile; nil: no Dockerfile
		dockerignore   []string // lines of .dockerignore
		deployment     []string // substrings of deploy/k8s/deployment.yaml
	}{
		{"python-fastapi", "service", "", []string{"ghcr.io/astral-sh/uv", "uvicorn", "EXPOSE 8000"},
			[]string{".venv", ".pytest_cache", ".ruff_cache", ".mypy_cache", ".uv", "dist"},
			[]string{"containerPort: 8000", "path: /healthz"}},
		{"react-vite", "static-site", "ts-standard",
			[]string{"npm ci", "npm run build", "nginxinc/nginx-unprivileged", "/app/dist", "USER 101", "EXPOSE 8080"},
			[]string{"node_modules", "dist", "coverage"},
			[]string{"containerPort: 8080", "path: /\n"}},
		{"go-cli", "service", "", nil, nil, []string{"containerPort: 8000"}},
	}
	for _, c := range cases {
		t.Run(c.framework, func(t *testing.T) {
			sides := []side{{model.SideFramework, c.framework}}
			if c.standard != "" {
				sides = append(sides, side{model.SideStandard, c.standard})
			}
			sides = append(sides, side{model.SideDeployment, "k8s-manifests"})
			_, result := renderCombo(t, c.typ, sides)
			for _, name := range []string{"Dockerfile", ".dockerignore"} {
				if _, ok := result.Files[name]; ok != (c.dockerfile != nil) {
					t.Errorf("%s written = %v, want %v", name, ok, c.dockerfile != nil)
				}
			}
			if c.dockerfile != nil {
				assertContains(t, "Dockerfile", string(result.Files["Dockerfile"].Data), c.dockerfile)
				lines := strings.Split(string(result.Files[".dockerignore"].Data), "\n")
				for _, w := range c.dockerignore {
					if !slices.Contains(lines, w) {
						t.Errorf(".dockerignore doesn't list %q:\n%s", w, result.Files[".dockerignore"].Data)
					}
				}
			}
			assertContains(t, "deploy/k8s/deployment.yaml", string(result.Files["deploy/k8s/deployment.yaml"].Data),
				c.deployment)
		})
	}
}

// TestGoStandardCIKeepsFactoryModulesOffPublicProxies checks go-standard's CI:
// the factory's c4factory.local modules come from the owner's package registry
// alone, before make check, and make check never asks a proxy for them. A
// GOPROXY list of the registry, then the public proxy, would ask the public one
// for a module the registry lacks: the go command goes on after a 404.
func TestGoStandardCIKeepsFactoryModulesOffPublicProxies(t *testing.T) {
	_, result := renderCombo(t, "cli", []side{
		{model.SideFramework, "go-cli"},
		{model.SideStandard, "go-standard"},
		{model.SideDeployment, "gitea-release"},
	})
	const registry = "${{ gitea.server_url }}/api/packages/${{ gitea.repository_owner }}/go"
	var workflow struct {
		Jobs map[string]struct {
			Env   map[string]string `yaml:"env"`
			Steps []struct {
				Name string            `yaml:"name"`
				Env  map[string]string `yaml:"env"`
				Run  string            `yaml:"run"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(result.Files[".gitea/workflows/check.yaml"].Data, &workflow); err != nil {
		t.Fatal(err)
	}
	job := workflow.Jobs["check"]
	if job.Env["GOPRIVATE"] != "c4factory.local" || strings.Contains(job.Env["GOPROXY"], "/api/packages/") {
		t.Errorf("job env %v: want GOPRIVATE=c4factory.local and the public proxy only", job.Env)
	}
	fetched, checked := -1, -1
	for i, step := range job.Steps {
		if step.Env["GOPROXY"] != "" {
			// GOPRIVATE set and empty: the job's would send the paths direct.
			private, set := step.Env["GOPRIVATE"]
			if step.Env["GOPROXY"] != registry || !set || private != "" ||
				step.Env["GONOSUMDB"] != "c4factory.local" {
				t.Errorf("step %q env %v: want GOPROXY=%s alone, an empty GOPRIVATE, GONOSUMDB=c4factory.local",
					step.Name, step.Env, registry)
			}
			if !strings.Contains(step.Run, "go.sum") || !strings.Contains(step.Run, "go mod download") {
				t.Errorf("step %q doesn't download the factory's modules:\n%s", step.Name, step.Run)
			}
			fetched = i
		}
		if strings.TrimSpace(step.Run) == "make check" {
			checked = i
		}
	}
	if fetched < 0 || checked < fetched {
		t.Errorf("the registry step is #%d and make check #%d; want the registry first", fetched, checked)
	}
}

func assertContains(t *testing.T, name, got string, want []string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("%s doesn't contain %q:\n%s", name, w, got)
		}
	}
}

// renderCombo builds a Container of type typ from the templates in
// templates/, with the choices the layers make.
func renderCombo(t *testing.T, typ string, sides []side) ([]Layer, Result) {
	t.Helper()
	return renderComboChecks(t, typ, sides, "")
}

// renderComboChecks is renderCombo with the Container's effective checks.
func renderComboChecks(t *testing.T, typ string, sides []side, checks string) ([]Layer, Result) {
	t.Helper()
	in, layers := loadCombo(t, typ, sides, checks)

	// Build names the template and file of a render error, and the
	// path and both templates of a conflict.
	result, err := Build(in, layers, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return layers, result
}

// loadCombo reads the templates of sides from templates/ and returns them
// with the input of a Container of type typ on Gitea Actions.
func loadCombo(t *testing.T, typ string, sides []side, checks string) (Input, []Layer) {
	t.Helper()
	in := Input{
		Project:        "webshop",
		SoftwareSystem: "shop",
		Container:      "sample-app",
		DisplayName:    "Sample App",
		Type:           typ,
		Checks:         checks,
		// The operator always renders the resolved block: the defaults when
		// the Container has none.
		Operations: model.ResolveOperations(model.OperationsChoices{}),
		CI:         giteaActions(t),
	}
	layers := make([]Layer, 0, len(sides))
	for _, l := range sides {
		files, err := ReadTree(os.DirFS(filepath.Join(templatesDir, l.Name)))
		if err != nil {
			t.Fatalf("read template %s: %v", l.Name, err)
		}
		layers = append(layers, Layer{Side: l.Side, Name: l.Name, Ref: "local", Files: files})
		switch l.Side {
		case model.SideFramework:
			in.Framework = l.Name
		case model.SideStandard:
			in.Standard = l.Name
		case model.SideCompute:
			in.Compute = l.Name
		case model.SideDeployment:
			in.Deployment = l.Name
		case model.SideConnector:
			in.Connectors = append(in.Connectors, l.Name)
		}
	}
	return in, layers
}

// TestEveryTemplateHasACombo fails when a folder in templates/ is rendered by
// no row of templateCombos, so a new template can't go untested.
func TestEveryTemplateHasACombo(t *testing.T) {
	entries, err := os.ReadDir(templatesDir)
	if err != nil {
		t.Fatal(err)
	}
	covered := map[string]bool{}
	for _, c := range templateCombos {
		for _, l := range c.layers {
			covered[l.Name] = true
		}
	}
	for _, e := range entries {
		if e.IsDir() && !covered[e.Name()] {
			t.Errorf("templates/%s is rendered by no row of templateCombos; add a realistic combination for it", e.Name())
		}
	}
}

// parseYAML decodes every document in data.
func parseYAML(data []byte) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var doc any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// ownerOf names the layer that writes p, or the operator.
func ownerOf(layers []Layer, p string) string {
	for _, l := range layers {
		for src := range l.Files {
			if strings.TrimSuffix(src, templateSuffix) == p {
				return l.Name
			}
		}
	}
	return operatorOwner
}

// TestVSCodeExtensionGuidesAskForNoTestThatStartsVSCode checks ts-standard's
// guides for a vscode-extension: the sandbox and CI can't start VS Code, so no
// test does (#602).
func TestVSCodeExtensionGuidesAskForNoTestThatStartsVSCode(t *testing.T) {
	_, result := renderCombo(t, "extension", []side{
		{model.SideFramework, "vscode-extension"},
		{model.SideStandard, "ts-standard"},
	})
	for name, want := range map[string][]string{
		"AGENTS.md": {"No test starts VS Code",
			"`@vscode/test-electron` can't run in `make check`"},
		"docs/agents/implementer.md": {"don't write a test that starts it"},
		"docs/style/vscode.md":       {"No integration tests in a real VS Code", "can't run in `make check`"},
	} {
		file, ok := result.Files[name]
		if !ok {
			t.Errorf("%s not written", name)
			continue
		}
		assertContains(t, name, strings.Join(strings.Fields(string(file.Data)), " "), want)
	}
}
