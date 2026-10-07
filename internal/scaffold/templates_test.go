package scaffold

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
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
	{"vscode extension", "extension", []side{
		{model.SideFramework, "vscode-extension"},
		{model.SideStandard, "ts-standard"},
		{model.SideDeployment, "gitea-release"},
	}},
	{"react web app", "static-site", []side{
		{model.SideFramework, "react-vite"},
		{model.SideStandard, "ts-standard"},
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
			in := Input{
				Project:        "webshop",
				SoftwareSystem: "shop",
				Container:      "sample-app",
				DisplayName:    "Sample App",
				Type:           c.typ,
			}
			var layers []Layer
			for _, l := range c.layers {
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

			// Build names the template and file of a render error, and the
			// path and both templates of a conflict.
			result, err := Build(in, layers, Options{})
			if err != nil {
				t.Fatal(err)
			}
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
