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

package scaffold

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/theos/internal/model"
)

func giteaActions(t *testing.T) model.CI {
	t.Helper()
	ci, err := model.CIByName(model.CIGiteaActions)
	if err != nil {
		t.Fatal(err)
	}
	return ci
}

// gitlabCI stands in for a CI system no template supports yet.
var gitlabCI = model.CI{Name: "gitlabCI", CheckContexts: []string{"check"}, ProtectedPaths: []string{".gitlab-ci.yml"}}

// todaysLayout moves a layer's Gitea Actions files back to the root, where
// they were before templates kept them under .theos/ci/giteaActions/.
func todaysLayout(layer Layer) Layer {
	files := Tree{}
	for p, f := range layer.Files {
		if rest, ok := strings.CutPrefix(p, ciDir+model.CIGiteaActions+"/"); ok {
			p = rest
		}
		files[p] = f
	}
	layer.Files = files
	return layer
}

func keepsCIFiles(l Layer) bool {
	return slices.ContainsFunc(l.Files.Paths(), func(p string) bool { return strings.HasPrefix(p, ciDir) })
}

// TestTemplatesRenderTheirCIFilesForGiteaActions renders every combo, so
// every Standard and gitea-release, for Gitea Actions: the files are today's,
// the CI files among them at the root, and every template that keeps CI files
// writes a workflow under .gitea/workflows/.
func TestTemplatesRenderTheirCIFilesForGiteaActions(t *testing.T) {
	for _, c := range templateCombos {
		t.Run(c.name, func(t *testing.T) {
			in, layers := loadCombo(t, c.typ, c.layers, "")
			got, err := Build(in, layers, Options{})
			if err != nil {
				t.Fatal(err)
			}
			today := make([]Layer, 0, len(layers))
			for _, l := range layers {
				today = append(today, todaysLayout(l))
			}
			want, err := Build(in, today, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Files, want.Files) {
				t.Errorf("paths = %v, want today's %v", got.Files.Paths(), want.Files.Paths())
			}
			for _, l := range layers {
				if !keepsCIFiles(l) {
					continue
				}
				files, err := RenderLayer(in, l)
				if err != nil {
					t.Fatal(err)
				}
				if !slices.ContainsFunc(files.Paths(), func(p string) bool {
					return strings.HasPrefix(p, ".gitea/workflows/")
				}) {
					t.Errorf("%s writes no workflow under .gitea/workflows/: %v", l.Name, files.Paths())
				}
				if slices.ContainsFunc(files.Paths(), func(p string) bool { return strings.HasPrefix(p, metaDir) }) {
					t.Errorf("%s writes under %s: %v", l.Name, metaDir, files.Paths())
				}
			}
		})
	}
}

// TestTemplatesRefuseACISystemTheyDontSupport renders every combo for a CI
// system no template has files for: the first layer with CI files fails it.
func TestTemplatesRefuseACISystemTheyDontSupport(t *testing.T) {
	for _, c := range templateCombos {
		t.Run(c.name, func(t *testing.T) {
			in, layers := loadCombo(t, c.typ, c.layers, "")
			in.CI = gitlabCI
			var first string
			for _, l := range layers {
				if keepsCIFiles(l) {
					first = l.Name
					break
				}
			}
			_, err := Build(in, layers, Options{})
			if first == "" {
				if err != nil {
					t.Fatalf("Build() = %v, want no error: no layer keeps CI files", err)
				}
				return
			}
			unsupported, ok := errors.AsType[*UnsupportedCIError](err)
			if !ok {
				t.Fatalf("Build() = %v, want an UnsupportedCIError", err)
			}
			want := &UnsupportedCIError{Layer: first, CI: "gitlabCI", Supported: []string{model.CIGiteaActions}}
			if !reflect.DeepEqual(unsupported, want) {
				t.Errorf("Build() = %+v, want %+v", unsupported, want)
			}
		})
	}
}

// TestTemplatesKeepingCIFiles names the templates that keep CI files, so one
// that writes a CI system's files outside .theos/ci/ can't go unnoticed.
func TestTemplatesKeepingCIFiles(t *testing.T) {
	want := []string{"aws-standard", "factory-self", "gitea-release", "go-operator-standard", "go-standard", "hello-html",
		"hello-standard", "k8s-manifests", "python-standard", "ts-standard"}
	var got []string
	for _, c := range templateCombos {
		_, layers := loadCombo(t, c.typ, c.layers, "")
		for _, l := range layers {
			for _, p := range l.Files.Paths() {
				if strings.HasPrefix(p, ".gitea/") || strings.HasPrefix(p, ".github/") {
					t.Errorf("%s keeps %s outside %s", l.Name, p, ciDir)
				}
			}
			if keepsCIFiles(l) && !slices.Contains(got, l.Name) {
				got = append(got, l.Name)
			}
		}
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("templates keeping CI files = %v, want %v", got, want)
	}
}

func TestRenderLayerCI(t *testing.T) {
	gitea := giteaActions(t)
	files := tree(
		"Makefile", "check:",
		ciDir+"giteaActions/.gitea/workflows/check.yaml.tmpl", "name: {{ .CI.Name }} for {{ .Container }}",
		ciDir+"gitlabCI/.gitlab-ci.yml", "check:",
		metaDir+"README.md", "about",
	)
	tests := []struct {
		name    string
		ci      model.CI
		files   Tree
		want    Tree
		wantErr string
	}{
		{name: "gitea actions renders its folder at the root", ci: gitea, files: files, want: tree(
			"Makefile", "check:",
			".gitea/workflows/check.yaml", "name: giteaActions for orders-api",
		)},
		{name: "another CI system renders its own folder only", ci: gitlabCI, files: files, want: tree(
			"Makefile", "check:",
			".gitlab-ci.yml", "check:",
		)},
		{name: "a layer without CI files works with any CI system", ci: gitlabCI,
			files: tree("main.go", "package main"), want: tree("main.go", "package main")},
		{name: "a CI system without a folder is refused", ci: model.CI{Name: "jenkins"}, files: files,
			wantErr: "std doesn't support jenkins; it supports giteaActions, gitlabCI"},
		{name: "no CI system is refused by a layer with CI files", files: files,
			wantErr: "std doesn't support no CI system; it supports giteaActions, gitlabCI"},
		{name: "a file beside the CI folders names no CI system", ci: gitlabCI,
			files: tree("main.go", "package main", ciDir+"README.md", "about"), want: tree("main.go", "package main")},
		{name: "CI files can't write the template's own folder", ci: gitea,
			files:   tree(ciDir+"giteaActions/.theos/x", "y"),
			wantErr: "std: .theos/ci/giteaActions/.theos/x: CI files can't write .theos/"},
		{name: "a CI file and a root file on one path conflict", ci: gitea,
			files:   tree("Makefile", "a", ciDir+"giteaActions/Makefile", "b"),
			wantErr: "std and std both write Makefile"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := ordersAPI
			in.CI = tt.ci
			got, err := RenderLayer(in, Layer{Side: model.SideStandard, Name: "std", Ref: "v1", Files: tt.files})
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("RenderLayer() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !maps.EqualFunc(got, tt.want, func(a, b File) bool { return string(a.Data) == string(b.Data) }) {
				t.Errorf("RenderLayer() = %v, want %v", got.Paths(), tt.want.Paths())
			}
		})
	}
}
