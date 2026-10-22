package main

import (
	"bytes"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"text/template"
)

// initPaths is every path kubebuilder 4.16.0's `kubebuilder init --plugins go/v4`
// writes, with who owns it (docs/plans/done/kubebuilder-operators.md, decision 3).
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
var data = map[string]any{
	"Project": "webshop", "SoftwareSystem": "shop", "Container": "backup-op",
	"DisplayName": "Backup Operator", "Description": "Backs up the shop's databases nightly.",
	"Standard": "go-operator-standard", "Compute": "",
}

// with returns data with the given keys changed.
func with(kv ...any) map[string]any {
	d := map[string]any{}
	for k, v := range data {
		d[k] = v
	}
	for i := 0; i < len(kv); i += 2 {
		d[kv[i].(string)] = kv[i+1]
	}
	return d
}

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

func TestAddCrossBuild(t *testing.T) {
	src := "ARG BASE_IMAGE=golang:1.26\nFROM ${BASE_IMAGE} AS builder\nARG TARGETOS\n\n" +
		"FROM gcr.io/distroless/static:nonroot\n"
	p, got, err := addCrossBuild(dockerfile, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if p != dockerfile {
		t.Errorf("path = %q, want %q", p, dockerfile)
	}
	want := "ARG BASE_IMAGE=golang:1.26\nFROM --platform=$BUILDPLATFORM ${BASE_IMAGE} AS builder\n" +
		"ARG TARGETOS\n\nFROM gcr.io/distroless/static:nonroot\n"
	if string(got) != want {
		t.Errorf("addCrossBuild = %q, want %q", got, want)
	}
	if _, _, err := addCrossBuild(dockerfile, got); err == nil {
		t.Error("want an error when the builder stage already builds on $BUILDPLATFORM")
	}
	if _, _, err := addCrossBuild(dockerfile, []byte("FROM golang:1.26 AS build\n")); err == nil {
		t.Error("want an error when the builder stage's FROM is not kubebuilder's")
	}
}

func TestGoVersion(t *testing.T) {
	got, err := goVersion([]byte("module m\n\ngo 1.26.0\n\nrequire x v1\n"))
	if err != nil || got != "1.26.0" {
		t.Errorf("goVersion = %q, %v; want 1.26.0", got, err)
	}
	if _, err := goVersion([]byte("module m\n")); err == nil {
		t.Error("want an error when go.mod has no go line")
	}
}

// statedGo is the Go version a README says to install.
var statedGo = regexp.MustCompile(`needs Go ([0-9][0-9.]*) or later`)

// checkReadme fails on a rendered README that leaves a TODO(user), states a Go
// version other than goVer, or misses one of want (#1477).
func checkReadme(t *testing.T, name, out, goVer string, want []string) {
	t.Helper()
	if strings.Contains(out, "TODO") {
		t.Errorf("%s leaves a TODO (#1477):\n%s", name, out)
	}
	if m := statedGo.FindStringSubmatch(out); m == nil || m[1] != goVer {
		t.Errorf("%s states Go %v, want go.mod's %s (#1477):\n%s", name, m, goVer, out)
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("%s lacks %q:\n%s", name, w, out)
		}
	}
}

func TestReadme(t *testing.T) {
	src := readmeFor("1.27.1")
	checkReadme(t, "README with a Standard", render(t, readme+".tmpl", src, data), "1.27.1", []string{
		"# Backup Operator\n\nBacks up the shop's databases nightly.\n\nThe `backup-op` Kubernetes operator of shop",
		"kubebuilder create api --group <group> --version v1alpha1 --kind <Kind>\nmake manifests generate\n",
		"`<group>.webshop.c4factory.local`",
		"make check ", "make install run\n", "make test-e2e",
		"## Release\n", "`make dist VERSION=<tag> IMG=<image>`", "SHA256SUMS",
		"make docker-build docker-push IMG=<some-registry>/backup-op:<tag>\n" +
			"make build-installer IMG=<some-registry>/backup-op:<tag>\n",
		"by digest on the Git host's\ncontainer registry", "kubectl apply -f install.yaml",
	})

	// Without a display name or description it names the Container; without
	// a Standard there is no Makefile, so it names no make target.
	out := render(t, readme+".tmpl", src, with("DisplayName", "", "Description", "", "Standard", ""))
	checkReadme(t, "README without a Standard", out, "1.27.1", []string{
		"# backup-op\n\nThe `backup-op` Kubernetes operator", "go test ./...", "go run ./cmd",
	})
	if strings.Contains(out, "make ") {
		t.Errorf("README without a Standard names a make target:\n%s", out)
	}
}

func TestFrameworkFilesWritesTheFactorysReadme(t *testing.T) {
	dir := t.TempDir()
	for p, s := range map[string]string{
		"go.mod":                            "module " + placeholderRepo + "\n\ngo 1.25.3\n",
		"README.md":                         "# " + placeholderContainer + "\n// TODO(user): Add simple overview of use/purpose\n",
		"config/manager/kustomization.yaml": "resources:\n- manager.yaml\n",
		".gitignore":                        "bin/\n",
	} {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files, err := frameworkFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := files[readme+".tmpl"]
	if !ok {
		t.Fatalf("no %s.tmpl; files: %v", readme, files)
	}
	checkReadme(t, readme, render(t, readme+".tmpl", f.data, data), "1.25.3", nil)
}

// TestCommittedTemplate checks templates/go-kubebuilder as committed: only
// Framework paths, every .go file a .go.tmpl, no placeholder left, and every
// template renders.
func TestCommittedTemplate(t *testing.T) {
	root := filepath.Join("..", "..", "templates", "go-kubebuilder")
	gomod, err := os.ReadFile(filepath.Join(root, "go.mod.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	committedGo, err := goVersion(gomod)
	if err != nil {
		t.Fatal(err)
	}
	seen, readmes := 0, 0
	err = filepath.WalkDir(root, func(full string, d fs.DirEntry, err error) error {
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
			out := render(t, p, src, data)
			if name == readme {
				readmes++
				checkReadme(t, p, out, committedGo, nil)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen == 0 {
		t.Fatalf("%s has no files; run make template-go-kubebuilder", root)
	}
	if readmes != 1 {
		t.Errorf("%s has %d %s.tmpl, want 1", root, readmes, readme)
	}
}
