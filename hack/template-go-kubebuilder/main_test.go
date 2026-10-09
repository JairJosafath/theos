package main

import (
	"bytes"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
)

// initPaths is every path kubebuilder 4.16.0's `kubebuilder init --plugins go/v4`
// writes, with who owns it (docs/plans/kubebuilder-operators.md, decision 3).
var initPaths = map[string]owner{
	".custom-gcl.yml":                 dropped,
	".devcontainer/devcontainer.json": dropped,
	".devcontainer/post-install.sh":   dropped,
	".github/workflows/lint.yml":      dropped,
	".github/workflows/test-e2e.yml":  dropped,
	".github/workflows/test.yml":      dropped,

	".golangci.yml": standard,
	"AGENTS.md":     standard,
	"Makefile":      standard,

	".dockerignore": framework,
	".gitignore":    framework,
	"Dockerfile":    framework,
	"PROJECT":       framework,
	"README.md":     framework,
	"cmd/main.go":   framework,
	"config/default/cert_metrics_manager_patch.yaml":   framework,
	"config/default/kustomization.yaml":                framework,
	"config/default/manager_metrics_patch.yaml":        framework,
	"config/default/metrics_service.yaml":              framework,
	"config/manager/kustomization.yaml":                framework,
	"config/manager/manager.yaml":                      framework,
	"config/network-policy/allow-metrics-traffic.yaml": framework,
	"config/network-policy/kustomization.yaml":         framework,
	"config/prometheus/kustomization.yaml":             framework,
	"config/prometheus/monitor.yaml":                   framework,
	"config/prometheus/monitor_tls_patch.yaml":         framework,
	"config/rbac/kustomization.yaml":                   framework,
	"config/rbac/leader_election_role.yaml":            framework,
	"config/rbac/leader_election_role_binding.yaml":    framework,
	"config/rbac/metrics_auth_role.yaml":               framework,
	"config/rbac/metrics_auth_role_binding.yaml":       framework,
	"config/rbac/metrics_reader_role.yaml":             framework,
	"config/rbac/role.yaml":                            framework,
	"config/rbac/role_binding.yaml":                    framework,
	"config/rbac/service_account.yaml":                 framework,
	"go.mod":                                           framework,
	"go.sum":                                           framework,
	"hack/boilerplate.go.txt":                          framework,
	"test/e2e/e2e_suite_test.go":                       framework,
	"test/e2e/e2e_test.go":                             framework,
	"test/utils/utils.go":                              framework,
}

func TestClassify(t *testing.T) {
	for p, want := range initPaths {
		got, err := classify(p)
		if err != nil {
			t.Errorf("classify(%q): %v", p, err)
			continue
		}
		if got != want {
			t.Errorf("classify(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestClassifyFailsOnAnUnknownPath(t *testing.T) {
	// A kubebuilder bump that writes a path no rule knows fails the run.
	for _, p := range []string{"Tiltfile", "hack/new.sh", "test/fixtures/a.yaml", "cmdx/main.go", "configs/a.yaml"} {
		if o, err := classify(p); err == nil {
			t.Errorf("classify(%q) = %v, want an error", p, o)
		}
	}
}

// data is what a template receives, enough for these files.
var data = map[string]any{"Project": "webshop", "Container": "backup-op", "Compute": ""}

func render(t *testing.T, name string, src []byte, d map[string]any) string {
	t.Helper()
	tmpl, err := template.New(name).Option("missingkey=error").Parse(string(src))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var b bytes.Buffer
	if err := tmpl.Execute(&b, d); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return b.String()
}

func TestTransform(t *testing.T) {
	cases := []struct {
		name, path, in string
		wantPath       string
		wantSrc        string // the template, "" when the file is copied unchanged
		wantRendered   string
	}{
		{
			name:         "module path",
			path:         "go.mod",
			in:           "module c4factory.local/placeholder-project/placeholder-container\n",
			wantPath:     "go.mod.tmpl",
			wantSrc:      "module c4factory.local/{{ .Project }}/{{ .Container }}\n",
			wantRendered: "module c4factory.local/webshop/backup-op\n",
		},
		{
			name:         "leader election ID",
			path:         "cmd/main.go",
			in:           `LeaderElectionID: "f6e0971b.placeholder-project.c4factory.local",`,
			wantPath:     "cmd/main.go.tmpl",
			wantRendered: `LeaderElectionID: "backup-op.webshop.c4factory.local",`,
		},
		{
			name:         "domain and name",
			path:         "PROJECT",
			in:           "domain: placeholder-project.c4factory.local\nprojectName: placeholder-container\n",
			wantPath:     "PROJECT.tmpl",
			wantRendered: "domain: webshop.c4factory.local\nprojectName: backup-op\n",
		},
		{
			name:         "a go file's own delimiters",
			path:         "test/e2e/e2e_test.go",
			in:           `"go-template={{ range .items }}{{ \"\\n\" }}{{ end }}" // placeholder-container`,
			wantPath:     "test/e2e/e2e_test.go.tmpl",
			wantRendered: `"go-template={{ range .items }}{{ \"\\n\" }}{{ end }}" // backup-op`,
		},
		{
			name:         "a go file without placeholders",
			path:         "test/utils/utils.go",
			in:           "/*\nCopyright 2031.\n*/\npackage utils\n",
			wantPath:     "test/utils/utils.go.tmpl",
			wantRendered: "/*\nCopyright 2026.\n*/\npackage utils\n",
		},
		{
			name:     "other files are copied, the year fixed",
			path:     "hack/boilerplate.go.txt",
			in:       "/*\nCopyright 2027.\n*/\n{{ not a template }}\n",
			wantPath: "hack/boilerplate.go.txt",
			wantSrc:  "/*\nCopyright 2026.\n*/\n{{ not a template }}\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, src, err := transform(c.path, []byte(c.in))
			if err != nil {
				t.Fatal(err)
			}
			if p != c.wantPath {
				t.Errorf("path = %q, want %q", p, c.wantPath)
			}
			if c.wantSrc != "" && string(src) != c.wantSrc {
				t.Errorf("source = %q, want %q", src, c.wantSrc)
			}
			if c.wantRendered != "" {
				if got := render(t, p, src, data); got != c.wantRendered {
					t.Errorf("rendered = %q, want %q", got, c.wantRendered)
				}
			}
		})
	}
}

func TestTransformFailsOnALeftoverPlaceholder(t *testing.T) {
	if _, _, err := transform("README.md", []byte("placeholder-other\n")); err == nil {
		t.Error("want an error for a placeholder no replacement covers")
	}
}

func TestAddComputePatch(t *testing.T) {
	p, src, err := transform(managerKustomization, []byte("resources:\n- manager.yaml\n"))
	if err != nil {
		t.Fatal(err)
	}
	p, src, err = addComputePatch(p, src)
	if err != nil {
		t.Fatal(err)
	}
	if p != managerKustomization+".tmpl" {
		t.Errorf("path = %q, want %q", p, managerKustomization+".tmpl")
	}
	if got, want := render(t, p, src, data), "resources:\n- manager.yaml\n"; got != want {
		t.Errorf("without a Compute: %q, want %q", got, want)
	}
	withCompute := map[string]any{"Project": "webshop", "Container": "backup-op", "Compute": "small"}
	want := "resources:\n- manager.yaml\npatches:\n- path: compute.yaml\n"
	if got := render(t, p, src, withCompute); got != want {
		t.Errorf("with a Compute: %q, want %q", got, want)
	}

	if _, _, err := addComputePatch(p, []byte("patches:\n- path: x.yaml\n")); err == nil {
		t.Error("want an error when the kustomization already has patches")
	}
}

func TestAddDistIgnore(t *testing.T) {
	for _, src := range []string{"bin/*\n", "bin/*"} {
		p, got, err := addDistIgnore(gitignore, []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		if p != gitignore {
			t.Errorf("path = %q, want %q", p, gitignore)
		}
		want := "bin/*\n\n# Release output: make dist\n/dist/\n"
		if string(got) != want {
			t.Errorf("addDistIgnore(%q) = %q, want %q", src, got, want)
		}
	}
	if _, _, err := addDistIgnore(gitignore, []byte("/dist/\n")); err == nil {
		t.Error("want an error when .gitignore already ignores /dist/")
	}
}

// TestCommittedTemplate checks templates/go-kubebuilder as committed: only
// Framework paths, every .go file a .go.tmpl, no placeholder left, and every
// template renders.
func TestCommittedTemplate(t *testing.T) {
	root := filepath.Join("..", "..", "templates", "go-kubebuilder")
	seen := 0
	err := filepath.WalkDir(root, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, full)
		if err != nil {
			return err
		}
		p := filepath.ToSlash(rel)
		if d.IsDir() {
			if p == ".c4factory" {
				return fs.SkipDir
			}
			return nil
		}
		seen++
		src, err := os.ReadFile(full)
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(p, ".tmpl")
		if o, err := classify(name); err != nil || o != framework {
			t.Errorf("%s: not a Framework path (%v, %v)", p, o, err)
		}
		if path.Ext(p) == ".go" {
			t.Errorf("%s: a .go file must be a .go.tmpl", p)
		}
		if bytes.Contains(src, []byte("placeholder")) {
			t.Errorf("%s: still says \"placeholder\"", p)
		}
		if p != name {
			render(t, p, src, data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen == 0 {
		t.Fatalf("%s has no files; run make template-go-kubebuilder", root)
	}
}
