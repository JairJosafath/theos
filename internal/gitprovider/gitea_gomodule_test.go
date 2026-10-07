package gitprovider

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"golang.org/x/mod/module"
)

// archiveZip builds a repository archive as Gitea makes it: every file below
// one top directory, with an entry for each directory.
func archiveZip(t *testing.T, top string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	if _, err := w.Create(top + "/"); err != nil {
		t.Fatal(err)
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		f, err := w.Create(top + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write([]byte(files[name]))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// zipNames returns the names of the files in a zip.
func zipNames(t *testing.T, data []byte) []string {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	slices.Sort(names)
	return names
}

const coreGoMod = "module c4factory.local/cel-lens/cel-lens-core\n\ngo 1.24\n"

func TestRepackModule(t *testing.T) {
	mod := module.Version{Path: "c4factory.local/cel-lens/cel-lens-core", Version: "v0.1.0"}
	for _, tt := range []struct {
		name    string
		files   map[string]string
		want    []string
		invalid string
	}{{
		name: "keeps the module's files below module@version",
		files: map[string]string{
			"go.mod": coreGoMod, "main.go": "package main\n", "eval/eval.go": "package eval\n",
			"README.md": "# core\n",
		},
		want: []string{"README.md", "eval/eval.go", "go.mod", "main.go"},
	}, {
		name: "leaves out vendored packages and a nested module",
		files: map[string]string{
			"go.mod": coreGoMod, "main.go": "package main\n",
			"vendor/example.com/dep/dep.go": "package dep\n",
			"tools/go.mod":                  "module c4factory.local/cel-lens/cel-lens-core/tools\n",
			"tools/gen/gen.go":              "package gen\n",
		},
		want: []string{"go.mod", "main.go"},
	}, {
		name:    "fails on a file name Go doesn't allow",
		files:   map[string]string{"go.mod": coreGoMod, "a:b.go": "package main\n"},
		invalid: "a:b.go",
	}} {
		t.Run(tt.name, func(t *testing.T) {
			data := archiveZip(t, "cel-lens-core", tt.files)
			archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			err = repackModule(&out, archive, mod)
			if tt.invalid != "" {
				if err == nil || !strings.Contains(err.Error(), tt.invalid) {
					t.Fatalf("repackModule() = %v, want an error naming %s", err, tt.invalid)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			for _, name := range tt.want {
				want = append(want, mod.Path+"@"+mod.Version+"/"+name)
			}
			if got := zipNames(t, out.Bytes()); !slices.Equal(got, want) {
				t.Errorf("module zip has %v, want %v", got, want)
			}
		})
	}
}

// goRegistry is a Gitea with one repository, cel-lens/cel-lens-core, its
// archive and the owner's Go registry.
type goRegistry struct {
	goMod    string // "" when the repository has none
	files    map[string]string
	versions map[string]bool // published module versions, by module@version
	conflict bool            // the upload finds the version there, as in a race
	calls    []string
	uploaded []byte
}

func newGoRegistry(t *testing.T) (*goRegistry, Provider) {
	t.Helper()
	h := &goRegistry{goMod: coreGoMod, versions: map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		h.calls = append(h.calls, r.Method+" "+r.URL.Path)
		const repo = "/api/v1/repos/cel-lens/cel-lens-core"
		const registry = "/api/packages/cel-lens/go/"
		switch {
		case r.Method == http.MethodGet && r.URL.Path == repo+"/raw/go.mod":
			if h.goMod == "" || r.URL.Query().Get("ref") == "" {
				http.NotFound(w, r)
				return
			}
			_, _ = io.WriteString(w, h.goMod)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, registry) && strings.HasSuffix(r.URL.Path, ".info"):
			at := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, registry), ".info")
			if !h.versions[strings.Replace(at, "/@v/", "@", 1)] {
				http.NotFound(w, r)
				return
			}
			_, _ = io.WriteString(w, `{"Version":"v0.1.0"}`)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, repo+"/archive/"):
			files := h.files
			if files == nil {
				files = map[string]string{"go.mod": h.goMod, "main.go": "package main\n"}
			}
			_, _ = w.Write(archiveZip(t, "cel-lens-core", files))
		case r.Method == http.MethodPut && r.URL.Path == registry+"upload":
			h.uploaded, _ = io.ReadAll(r.Body)
			if h.conflict {
				w.WriteHeader(http.StatusConflict)
				_, _ = io.WriteString(w, `{"message":"package version already exists"}`)
				return
			}
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	p, err := Client{}.Provider(Config{URL: srv.URL, Owner: "cel-lens", Credentials: Credentials{Token: "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	return h, p
}

func TestPublishGoModule(t *testing.T) {
	ctx := context.Background()
	const modPath = "c4factory.local/cel-lens/cel-lens-core"

	t.Run("publishes the module at the tag", func(t *testing.T) {
		h, p := newGoRegistry(t)
		got, err := p.PublishGoModule(ctx, p.Repository("cel-lens-core"), "v0.1.0")
		want := GoModule{Outcome: GoModulePublished, Path: modPath, Version: "v0.1.0"}
		if err != nil || got != want {
			t.Fatalf("PublishGoModule() = %+v, %v; want %+v", got, err, want)
		}
		if names := zipNames(t, h.uploaded); !slices.Equal(names,
			[]string{modPath + "@v0.1.0/go.mod", modPath + "@v0.1.0/main.go"}) {
			t.Errorf("uploaded a zip of %v", names)
		}
		if !slices.Contains(h.calls, "GET /api/v1/repos/cel-lens/cel-lens-core/archive/v0.1.0.zip") {
			t.Errorf("calls = %v, want the tag's archive downloaded", h.calls)
		}
	})

	for _, tt := range []struct {
		name  string
		setup func(h *goRegistry)
		tag   string
		want  GoModule
		// archive is whether the archive is downloaded.
		archive bool
	}{{
		name:  "no go.mod is an outcome",
		setup: func(h *goRegistry) { h.goMod = "" },
		tag:   "v0.1.0",
		want:  GoModule{Outcome: GoModuleNoGoMod, Version: "v0.1.0"},
	}, {
		name:  "a published version is left as it is",
		setup: func(h *goRegistry) { h.versions[modPath+"@v0.1.0"] = true },
		tag:   "v0.1.0",
		want:  GoModule{Outcome: GoModuleExists, Path: modPath, Version: "v0.1.0"},
	}, {
		name:    "a version published meanwhile is left as it is",
		setup:   func(h *goRegistry) { h.conflict = true },
		tag:     "v0.1.0",
		want:    GoModule{Outcome: GoModuleExists, Path: modPath, Version: "v0.1.0"},
		archive: true,
	}, {
		name:  "a v2 without /v2 is invalid",
		setup: func(*goRegistry) {},
		tag:   "v2.0.0",
		want: GoModule{Outcome: GoModuleInvalid, Path: modPath, Version: "v2.0.0",
			Reason: modPath + "@v2.0.0: invalid version: should be v0 or v1, not v2"},
	}, {
		name:  "a tag that is no semantic version is invalid",
		setup: func(*goRegistry) {},
		tag:   "release-1",
		want: GoModule{Outcome: GoModuleInvalid, Path: modPath, Version: "release-1",
			Reason: "tag release-1 is not a canonical semantic version"},
	}, {
		name:  "a go.mod without a module line is invalid",
		setup: func(h *goRegistry) { h.goMod = "go 1.24\n" },
		tag:   "v0.1.0",
		want:  GoModule{Outcome: GoModuleInvalid, Version: "v0.1.0", Reason: "go.mod declares no module path"},
	}, {
		name: "a file Go doesn't allow is invalid",
		setup: func(h *goRegistry) {
			h.files = map[string]string{"go.mod": coreGoMod, "a:b.go": "package main\n"}
		},
		tag:     "v0.1.0",
		want:    GoModule{Outcome: GoModuleInvalid, Path: modPath, Version: "v0.1.0"},
		archive: true,
	}} {
		t.Run(tt.name, func(t *testing.T) {
			h, p := newGoRegistry(t)
			tt.setup(h)
			got, err := p.PublishGoModule(ctx, p.Repository("cel-lens-core"), tt.tag)
			if err != nil {
				t.Fatal(err)
			}
			reason := got.Reason
			if tt.want.Reason == "" && got.Outcome == GoModuleInvalid {
				got.Reason = "" // checked below
			}
			if got != tt.want {
				t.Fatalf("PublishGoModule() = %+v, want %+v", got, tt.want)
			}
			if got.Outcome == GoModuleInvalid && reason == "" {
				t.Error("an invalid module has no reason")
			}
			downloaded := slices.ContainsFunc(h.calls, func(c string) bool { return strings.Contains(c, "/archive/") })
			if downloaded != tt.archive {
				t.Errorf("archive downloaded = %v, want %v (calls %v)", downloaded, tt.archive, h.calls)
			}
			if tt.want.Outcome != GoModuleExists && h.uploaded != nil {
				t.Error("uploaded a module")
			}
		})
	}

	t.Run("a failed read is an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		t.Cleanup(srv.Close)
		p, _ := Client{}.Provider(Config{URL: srv.URL, Owner: "cel-lens"})
		if _, err := p.PublishGoModule(ctx, p.Repository("cel-lens-core"), "v0.1.0"); err == nil {
			t.Fatal("PublishGoModule() succeeded against a failing host")
		}
	})
}
