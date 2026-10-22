package gitprovider

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
)

// tarGz builds a gzipped tar of files, by path.
func tarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for k := range files {
			if !yield(k) {
				return
			}
		}
	}) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(files[name])), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write(files[name])
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// npmTarball builds what npm pack makes: package/package.json and the files,
// large enough to span several of the base64 encoder's chunks.
func npmTarball(t *testing.T, name, version string) []byte {
	t.Helper()
	manifest := fmt.Sprintf(`{"name":%q,"version":%q,"description":"The shop's UI","main":"index.js",`+
		`"repository":"https://git.example.com/webshop/shop-ui.git","dependencies":{"react":"^19.0.0"}}`, name, version)
	return tarGz(t, map[string][]byte{
		"package/package.json": []byte(manifest),
		"package/index.js":     bytes.Repeat([]byte("export const ui = 1;\n"), 1500),
	})
}

// pythonMetadata is a wheel's METADATA or an sdist's PKG-INFO.
func pythonMetadata(name, version string) []byte {
	return []byte("Metadata-Version: 2.3\nName: " + name + "\nVersion: " + version +
		"\nSummary: The shop's API\nRequires-Python: >=3.12\n\nThe long description.\n")
}

// wheel builds a wheel of name at version: a zip with <name>-<version>.dist-info/METADATA.
func wheel(t *testing.T, name, version string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	dist := strings.ReplaceAll(name, "-", "_") + "-" + version + ".dist-info/"
	for path, data := range map[string][]byte{
		strings.ReplaceAll(name, "-", "_") + "/__init__.py": []byte("VERSION = " + version + "\n"),
		dist + "WHEEL":    []byte("Wheel-Version: 1.0\nRoot-Is-Purelib: true\nTag: py3-none-any\n"),
		dist + "METADATA": pythonMetadata(name, version),
		dist + "RECORD":   nil,
	} {
		f, err := w.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write(data)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// sdist builds a source distribution of name at version: a gzipped tar with
// <name>-<version>/PKG-INFO.
func sdist(t *testing.T, name, version string) []byte {
	t.Helper()
	top := strings.ReplaceAll(name, "-", "_") + "-" + version + "/"
	return tarGz(t, map[string][]byte{
		top + "PKG-INFO":       pythonMetadata(name, version),
		top + "pyproject.toml": []byte("[project]\nname = \"" + name + "\"\n"),
	})
}

// testAsset is a release file the fake host serves.
type testAsset struct {
	name string
	data []byte
}

// registryCalls is what a registry, the host's own or a custom one, received.
type registryCalls struct {
	calls []string
	// npmVersions are the published npm versions, by package name; pypiFiles
	// the published file names, by PEP 503 project name.
	npmVersions map[string][]string
	pypiFiles   map[string][]string
	// conflict makes every upload answer 409, as a race does.
	conflict bool
	// npmPut is the body of the npm publish, npmLength its Content-Length and
	// npmAuth its Authorization header; pypiForm, pypiContent and pypiAuth the
	// PyPI uploads', one per file.
	npmPut      []byte
	npmLength   int64
	npmAuth     string
	pypiForm    []map[string]string
	pypiContent [][]byte
	pypiAuth    []string
}

// serve answers the npm and PyPI routes below npm and pypi (the paths the
// registry's URLs end in). It tells whether the request was one of them.
func (c *registryCalls) serve(t *testing.T, w http.ResponseWriter, r *http.Request, npm, pypi string) bool {
	t.Helper()
	path := r.URL.EscapedPath()
	switch {
	case strings.HasPrefix(path, npm):
		name, err := unescapePath(path[len(npm):])
		if err != nil {
			t.Errorf("npm path %s: %v", path, err)
		}
		if strings.Contains(name, "/") && !strings.Contains(path, "%2F") {
			t.Errorf("npm path %s does not escape the scope's slash", path)
		}
		c.calls = append(c.calls, r.Method+" "+path)
		switch r.Method {
		case http.MethodGet:
			versions := c.npmVersions[name]
			if len(versions) == 0 {
				http.NotFound(w, r)
				return true
			}
			doc := map[string]any{"name": name, "versions": map[string]any{}}
			for _, v := range versions {
				doc["versions"].(map[string]any)[v] = map[string]any{"version": v}
			}
			_ = json.NewEncoder(w).Encode(doc)
		case http.MethodPut:
			c.npmPut, _ = io.ReadAll(r.Body)
			c.npmLength = r.ContentLength
			c.npmAuth = r.Header.Get("Authorization")
			if c.conflict {
				w.WriteHeader(http.StatusConflict)
				_, _ = io.WriteString(w, `{"message":"package version already exists"}`)
				return true
			}
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
		return true
	case r.Method == http.MethodGet && strings.HasPrefix(path, pypi+"/simple/"):
		c.calls = append(c.calls, r.Method+" "+path)
		name := strings.TrimSuffix(strings.TrimPrefix(path, pypi+"/simple/"), "/")
		files := c.pypiFiles[name]
		if len(files) == 0 {
			http.NotFound(w, r)
			return true
		}
		_, _ = io.WriteString(w, "<html><body>")
		for _, f := range files {
			_, _ = fmt.Fprintf(w, `<a href="/x/%s#sha256=0">%s</a><br/>`, f, html.EscapeString(f))
		}
		_, _ = io.WriteString(w, "</body></html>")
		return true
	case r.Method == http.MethodPost && path == pypi:
		c.calls = append(c.calls, r.Method+" "+path)
		c.pypiAuth = append(c.pypiAuth, r.Header.Get("Authorization"))
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("PyPI upload form: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return true
		}
		fields := map[string]string{}
		for k, v := range r.MultipartForm.Value {
			fields[k] = v[0]
		}
		f, hdr, err := r.FormFile("content")
		if err != nil {
			t.Errorf("PyPI upload content: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return true
		}
		data, _ := io.ReadAll(f)
		_ = f.Close()
		fields["content.filename"] = hdr.Filename
		c.pypiForm = append(c.pypiForm, fields)
		c.pypiContent = append(c.pypiContent, data)
		if c.conflict {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"message":"file already exists"}`)
			return true
		}
		w.WriteHeader(http.StatusCreated)
		return true
	}
	return false
}

func unescapePath(s string) (string, error) { return url.PathUnescape(s) }

// packageHost is a Gitea with the repository webshop/shop, one release, and
// the owner's npm and PyPI registries.
type packageHost struct {
	registryCalls
	tag    string
	assets []testAsset
	// downloads are the asset names whose files were requested.
	downloads []string
	url       string
}

func newPackageHost(t *testing.T, tag string, assets []testAsset, packages Packages) (*packageHost, Provider) {
	t.Helper()
	h := &packageHost{tag: tag, assets: assets, registryCalls: registryCalls{
		npmVersions: map[string][]string{}, pypiFiles: map[string][]string{}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token secret" {
			t.Errorf("%s %s: Authorization = %q, want the host's token", r.Method, r.URL.Path, r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if h.serve(t, w, r, "/api/packages/webshop/npm/", "/api/packages/webshop/pypi") {
			return
		}
		h.calls = append(h.calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/webshop/shop/releases/tags/"+h.tag:
			var list []string
			for i, a := range h.assets {
				list = append(list, fmt.Sprintf(`{"id":%d,"name":%q,"size":%d,"uuid":"u-%s"}`, i+1, a.name, len(a.data), a.name))
			}
			_, _ = fmt.Fprintf(w, `{"id":7,"tag_name":%q,"name":%q,"draft":false,"assets":[%s]}`, h.tag, h.tag,
				strings.Join(list, ","))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/attachments/u-"):
			name := strings.TrimPrefix(r.URL.Path, "/attachments/u-")
			h.downloads = append(h.downloads, name)
			for _, a := range h.assets {
				if a.name == name {
					_, _ = w.Write(a.data)
					return
				}
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	h.url = srv.URL
	p, err := Client{}.Provider(Config{URL: srv.URL, Owner: "webshop", Credentials: Credentials{Token: "secret"},
		Packages: packages})
	if err != nil {
		t.Fatal(err)
	}
	return h, p
}

// customRegistry is a registry on another host, as a GitProvider's
// spec.packages may name: npm below /npm/, PyPI below /pypi.
func newCustomRegistry(t *testing.T) (*registryCalls, string) {
	t.Helper()
	c := &registryCalls{npmVersions: map[string][]string{}, pypiFiles: map[string][]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "token ") {
			t.Errorf("%s %s: the host's token %q was sent to a custom registry", r.Method, r.URL.Path, auth)
		}
		if !c.serve(t, w, r, "/npm/", "/pypi") {
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return c, srv.URL
}

// npmDocument is the publish document an npm PUT carries, as far as the
// tests read it.
type npmDocument struct {
	ID          string            `json:"_id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	DistTags    map[string]string `json:"dist-tags"`
	Versions    map[string]struct {
		ID         string            `json:"_id"`
		Name       string            `json:"name"`
		Version    string            `json:"version"`
		Repository map[string]string `json:"repository"`
		Dist       struct {
			Integrity string `json:"integrity"`
			Shasum    string `json:"shasum"`
			Tarball   string `json:"tarball"`
		} `json:"dist"`
	} `json:"versions"`
	Attachments map[string]struct {
		ContentType string `json:"content_type"`
		Length      int64  `json:"length"`
		Data        string `json:"data"`
	} `json:"_attachments"`
}

func decodeNpmPut(t *testing.T, body []byte) npmDocument {
	t.Helper()
	var doc npmDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("the npm publish document is not JSON: %v\n%.200s", err, body)
	}
	return doc
}

func outcomes(pkgs []Package) []string {
	out := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		out = append(out, p.Ecosystem+" "+p.Name+"@"+p.Version+" "+string(p.Outcome))
	}
	return out
}

// TestPublishReleasePackages checks PublishReleasePackages against a fake
// Gitea (#1229, decisions 2 to 5 of docs/plans/app-packages.md).
func TestPublishReleasePackages(t *testing.T) {
	ctx := context.Background()
	ui, _, _, release := shopRelease(t)

	t.Run("a repeat publishes nothing", func(t *testing.T) {
		h, p := newPackageHost(t, "v0.1.0", release, Packages{})
		h.npmVersions["@c4factory/shop-ui"] = []string{"0.0.9", "0.1.0"}
		h.pypiFiles["shop-api"] = []string{"shop_api-0.1.0-py3-none-any.whl", "shop_api-0.1.0.tar.gz"}
		got, err := p.PublishReleasePackages(ctx, p.Repository("shop"), "v0.1.0")
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"npm @c4factory/shop-ui@0.1.0 Exists", "PyPI shop-api@0.1.0 Exists", "PyPI shop-api@0.1.0 Exists"}; !slices.Equal(outcomes(got.Packages), want) {
			t.Errorf("outcomes = %v, want %v", outcomes(got.Packages), want)
		}
		if h.npmPut != nil || len(h.pypiForm) != 0 {
			t.Errorf("uploaded again: npm %d bytes, %d PyPI uploads", len(h.npmPut), len(h.pypiForm))
		}
	})

	t.Run("a version published meanwhile exists", func(t *testing.T) {
		h, p := newPackageHost(t, "v0.1.0", release, Packages{})
		h.conflict = true
		got, err := p.PublishReleasePackages(ctx, p.Repository("shop"), "v0.1.0")
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"npm @c4factory/shop-ui@0.1.0 Exists", "PyPI shop-api@0.1.0 Exists", "PyPI shop-api@0.1.0 Exists"}; !slices.Equal(outcomes(got.Packages), want) {
			t.Errorf("outcomes = %v, want %v", outcomes(got.Packages), want)
		}
	})

	t.Run("only the wheel of a version is missing", func(t *testing.T) {
		h, p := newPackageHost(t, "v0.1.0", release, Packages{})
		h.pypiFiles["shop-api"] = []string{"shop_api-0.1.0.tar.gz"}
		got, err := p.PublishReleasePackages(ctx, p.Repository("shop"), "v0.1.0")
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"npm @c4factory/shop-ui@0.1.0 Published", "PyPI shop-api@0.1.0 Published", "PyPI shop-api@0.1.0 Exists"}; !slices.Equal(outcomes(got.Packages), want) {
			t.Errorf("outcomes = %v, want %v", outcomes(got.Packages), want)
		}
		if len(h.pypiForm) != 1 || h.pypiForm[0]["filetype"] != "bdist_wheel" {
			t.Errorf("PyPI uploads = %v, want the wheel alone", h.pypiForm)
		}
	})

	t.Run("no release is ErrNoRelease", func(t *testing.T) {
		_, p := newPackageHost(t, "v0.1.0", nil, Packages{})
		_, err := p.PublishReleasePackages(ctx, p.Repository("shop"), "v0.2.0")
		if !errors.Is(err, ErrNoRelease) {
			t.Fatalf("PublishReleasePackages() of an unreleased tag = %v, want ErrNoRelease", err)
		}
	})

	t.Run("a failing registry is an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/v1/repos/webshop/shop/releases/tags/") {
				_, _ = fmt.Fprintf(w, `{"id":7,"tag_name":"v0.1.0","assets":[{"id":1,"name":"shop-ui-0.1.0.tgz","size":%d,"uuid":"u"}]}`, len(ui))
				return
			}
			if r.URL.Path == "/attachments/u" {
				_, _ = w.Write(ui)
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
		}))
		t.Cleanup(srv.Close)
		p, _ := Client{}.Provider(Config{URL: srv.URL, Owner: "webshop"})
		if _, err := p.PublishReleasePackages(ctx, p.Repository("shop"), "v0.1.0"); err == nil ||
			!strings.Contains(err.Error(), "HTTP 500") {
			t.Fatalf("PublishReleasePackages() against a failing registry = %v, want an HTTP 500 error", err)
		}
	})
}

// shopRelease is the release the tests publish: the shop's npm tarball,
// wheel and sdist, and three files that are no package.
func shopRelease(t *testing.T) (ui, whl, sd []byte, release []testAsset) {
	t.Helper()
	ui = npmTarball(t, "@c4factory/shop-ui", "0.1.0")
	whl = wheel(t, "shop-api", "0.1.0")
	sd = sdist(t, "shop-api", "0.1.0")
	release = []testAsset{
		{"shop-ui-0.1.0.tgz", ui},
		{"shop_api-0.1.0-py3-none-any.whl", whl},
		{"shop_api-0.1.0.tar.gz", sd},
		{"SHA256SUMS", []byte("abc  shop-ui-0.1.0.tgz\n")},
		{"lambda.zip", archiveZip(t, "lambda", map[string]string{"index.js": "exports.handler = 1\n"})},
		{"shop-ui.oci.tar", append([]byte("notgzip"), make([]byte, 600)...)},
	}
	return ui, whl, sd, release
}

// TestPublishReleasePackagesUploads checks what PublishReleasePackages sends:
// the npm publish document with its tarball streamed, and the PyPI upload
// form twine sends, one per file (#1229).
func TestPublishReleasePackagesUploads(t *testing.T) {
	ctx := context.Background()
	ui, whl, sd, release := shopRelease(t)
	h, p := newPackageHost(t, "v0.1.0", release, Packages{})
	got, err := p.PublishReleasePackages(ctx, p.Repository("shop"), "v0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	want := []Package{
		{Outcome: PackagePublished, Ecosystem: EcosystemNpm, Name: "@c4factory/shop-ui", Version: "0.1.0", File: "shop-ui-0.1.0.tgz"},
		{Outcome: PackagePublished, Ecosystem: EcosystemPyPI, Name: "shop-api", Version: "0.1.0", File: "shop_api-0.1.0-py3-none-any.whl"},
		{Outcome: PackagePublished, Ecosystem: EcosystemPyPI, Name: "shop-api", Version: "0.1.0", File: "shop_api-0.1.0.tar.gz"},
	}
	if got.Tag != "v0.1.0" || !slices.Equal(got.Packages, want) {
		t.Fatalf("PublishReleasePackages() = %+v\nwant packages %+v", got, want)
	}

	// Every file but the zip is opened; the one that isn't gzip is dropped.
	if wantDownloads := []string{"shop-ui-0.1.0.tgz", "shop_api-0.1.0-py3-none-any.whl", "shop_api-0.1.0.tar.gz",
		"SHA256SUMS", "shop-ui.oci.tar"}; !slices.Equal(h.downloads, wantDownloads) {
		t.Errorf("downloaded %v, want %v", h.downloads, wantDownloads)
	}

	checkNpmPublish(t, h, ui)
	checkPyPIUploads(t, h, whl, sd)
}

// checkNpmPublish checks the npm publish: the version looked up first, then
// the publish document PUT as npm publish does, its tarball attached.
func checkNpmPublish(t *testing.T, h *packageHost, ui []byte) {
	t.Helper()
	npmPath := "/api/packages/webshop/npm/@c4factory%2Fshop-ui"
	if i, j := slices.Index(h.calls, "GET "+npmPath), slices.Index(h.calls, "PUT "+npmPath); i < 0 || j < i {
		t.Errorf("calls = %v, want GET then PUT of %s", h.calls, npmPath)
	}
	doc := decodeNpmPut(t, h.npmPut)
	if doc.ID != "@c4factory/shop-ui" || doc.Name != "@c4factory/shop-ui" || doc.Description != "The shop's UI" ||
		doc.DistTags["latest"] != "0.1.0" {
		t.Errorf("npm document head = %+v", doc)
	}
	v, ok := doc.Versions["0.1.0"]
	if !ok || len(doc.Versions) != 1 {
		t.Fatalf("npm document versions = %v, want 0.1.0 alone", doc.Versions)
	}
	sum512 := sha512.Sum512(ui)
	if v.ID != "@c4factory/shop-ui@0.1.0" || v.Name != "@c4factory/shop-ui" || v.Version != "0.1.0" ||
		v.Repository["url"] != "https://git.example.com/webshop/shop-ui.git" || v.Repository["type"] != "git" {
		t.Errorf("npm version = %+v", v)
	}
	if v.Dist.Integrity != "sha512-"+base64.StdEncoding.EncodeToString(sum512[:]) ||
		v.Dist.Tarball != h.url+"/api/packages/webshop/npm/@c4factory/shop-ui/-/shop-ui-0.1.0.tgz" || len(v.Dist.Shasum) != 40 {
		t.Errorf("npm dist = %+v", v.Dist)
	}
	att, ok := doc.Attachments["shop-ui-0.1.0.tgz"]
	if !ok || len(doc.Attachments) != 1 {
		t.Fatalf("npm attachments = %v, want shop-ui-0.1.0.tgz alone", doc.Attachments)
	}
	data, err := base64.StdEncoding.DecodeString(att.Data)
	if err != nil || !bytes.Equal(data, ui) || att.Length != int64(len(ui)) || att.ContentType != "application/octet-stream" {
		t.Errorf("npm attachment: %d bytes (decode error %v), length %d, type %s; want the tarball's %d bytes",
			len(data), err, att.Length, att.ContentType, len(ui))
	}
	if h.npmLength != int64(len(h.npmPut)) {
		t.Errorf("npm PUT Content-Length = %d, body is %d bytes", h.npmLength, len(h.npmPut))
	}

}

// checkPyPIUploads checks the PyPI uploads: the simple index read first, then
// the form twine sends, one per file.
func checkPyPIUploads(t *testing.T, h *packageHost, whl, sd []byte) {
	t.Helper()
	if i, j := slices.Index(h.calls, "GET /api/packages/webshop/pypi/simple/shop-api/"),
		slices.Index(h.calls, "POST /api/packages/webshop/pypi"); i < 0 || j < i {
		t.Errorf("calls = %v, want the simple index read before the upload", h.calls)
	}
	if len(h.pypiForm) != 2 {
		t.Fatalf("%d PyPI uploads, want the wheel and the sdist", len(h.pypiForm))
	}
	for i, file := range [][]byte{whl, sd} {
		form := h.pypiForm[i]
		sum := sha256.Sum256(file)
		want := map[string]string{":action": "file_upload", "protocol_version": "1", "metadata_version": "2.3",
			"name": "shop-api", "version": "0.1.0", "summary": "The shop's API", "requires_python": ">=3.12",
			"sha256_digest": hex.EncodeToString(sum[:])}
		want["filetype"], want["pyversion"], want["content.filename"] = "bdist_wheel", "py3", "shop_api-0.1.0-py3-none-any.whl"
		if i == 1 {
			want["filetype"], want["pyversion"], want["content.filename"] = "sdist", "source", "shop_api-0.1.0.tar.gz"
		}
		for k, v := range want {
			if form[k] != v {
				t.Errorf("PyPI upload %d: %s = %q, want %q", i, k, form[k], v)
			}
		}
		if len(form["md5_digest"]) != 32 {
			t.Errorf("PyPI upload %d: md5_digest = %q", i, form["md5_digest"])
		}
		if !bytes.Equal(h.pypiContent[i], file) {
			t.Errorf("PyPI upload %d: content is %d bytes, want the file's %d", i, len(h.pypiContent[i]), len(file))
		}
	}
}

// TestPublishReleasePackagesOutcomes checks the outcomes that are results,
// not errors: an unscoped name, a version that isn't the tag's, PEP 440
// normalisation, and a release with no package file (#1229).
func TestPublishReleasePackagesOutcomes(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct {
		name   string
		tag    string
		assets []testAsset
		want   []Package
		// uploads is whether anything was uploaded.
		uploads bool
	}{{
		name:   "an unscoped npm name is left alone",
		tag:    "v0.1.0",
		assets: []testAsset{{"shop-ui-0.1.0.tgz", npmTarball(t, "shop-ui", "0.1.0")}},
		want: []Package{{Outcome: PackageUnscoped, Ecosystem: EcosystemNpm, Name: "shop-ui", Version: "0.1.0",
			File: "shop-ui-0.1.0.tgz", Reason: "shop-ui is not under the scope @c4factory"}},
	}, {
		name:   "another scope is left alone",
		tag:    "v0.1.0",
		assets: []testAsset{{"shop-ui-0.1.0.tgz", npmTarball(t, "@acme/shop-ui", "0.1.0")}},
		want: []Package{{Outcome: PackageUnscoped, Ecosystem: EcosystemNpm, Name: "@acme/shop-ui", Version: "0.1.0",
			File: "shop-ui-0.1.0.tgz", Reason: "@acme/shop-ui is not under the scope @c4factory"}},
	}, {
		name: "a version that isn't the tag's is invalid",
		tag:  "v0.1.0",
		assets: []testAsset{{"shop-ui-0.2.0.tgz", npmTarball(t, "@c4factory/shop-ui", "0.2.0")},
			{"shop_api-0.2.0-py3-none-any.whl", wheel(t, "shop-api", "0.2.0")}},
		want: []Package{
			{Outcome: PackageInvalid, Ecosystem: EcosystemNpm, Name: "@c4factory/shop-ui", Version: "0.2.0",
				File: "shop-ui-0.2.0.tgz", Reason: "version 0.2.0 is not the tag's, 0.1.0"},
			{Outcome: PackageInvalid, Ecosystem: EcosystemPyPI, Name: "shop-api", Version: "0.2.0",
				File: "shop_api-0.2.0-py3-none-any.whl", Reason: "version 0.2.0 is not the tag's, 0.1.0"},
		},
	}, {
		name: "a pre-release tag is normalised for Python and exact for npm",
		tag:  "v1.0.0-rc.1",
		assets: []testAsset{{"shop-ui-1.0.0-rc.1.tgz", npmTarball(t, "@c4factory/shop-ui", "1.0.0-rc.1")},
			{"shop_api-1.0.0rc1-py3-none-any.whl", wheel(t, "shop-api", "1.0.0rc1")},
			{"shop_api-1.0.0rc1.tar.gz", sdist(t, "shop-api", "1.0.0-RC.1")}},
		want: []Package{
			{Outcome: PackagePublished, Ecosystem: EcosystemNpm, Name: "@c4factory/shop-ui", Version: "1.0.0-rc.1", File: "shop-ui-1.0.0-rc.1.tgz"},
			{Outcome: PackagePublished, Ecosystem: EcosystemPyPI, Name: "shop-api", Version: "1.0.0rc1", File: "shop_api-1.0.0rc1-py3-none-any.whl"},
			{Outcome: PackagePublished, Ecosystem: EcosystemPyPI, Name: "shop-api", Version: "1.0.0-RC.1", File: "shop_api-1.0.0rc1.tar.gz"},
		},
		uploads: true,
	}, {
		name:   "a tag that is no PEP 440 version can't publish a Python package",
		tag:    "release-1",
		assets: []testAsset{{"shop_api-0.1.0-py3-none-any.whl", wheel(t, "shop-api", "0.1.0")}},
		want: []Package{{Outcome: PackageInvalid, Ecosystem: EcosystemPyPI, Name: "shop-api", Version: "0.1.0",
			File: "shop_api-0.1.0-py3-none-any.whl", Reason: "tag release-1 is not a PEP 440 version"}},
	}, {
		name: "a release with no package file",
		tag:  "v0.1.0",
		assets: []testAsset{{"SHA256SUMS", []byte("abc\n")}, {"notes.tar.gz", tarGz(t, map[string][]byte{"notes/README": []byte("hi")})},
			{"broken.tgz", []byte{0x1f, 0x8b, 1, 2, 3}}, {"nameless.tgz", tarGz(t, map[string][]byte{"package/package.json": []byte(`{"name":"x"}`)})},
			{"empty.whl", archiveZip(t, "x", nil)}},
		want: nil,
	}} {
		t.Run(tt.name, func(t *testing.T) {
			h, p := newPackageHost(t, tt.tag, tt.assets, Packages{})
			got, err := p.PublishReleasePackages(ctx, p.Repository("shop"), tt.tag)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got.Packages, tt.want) {
				t.Errorf("PublishReleasePackages() = %+v\nwant %+v", got.Packages, tt.want)
			}
			if uploaded := h.npmPut != nil || len(h.pypiForm) > 0; uploaded != tt.uploads {
				t.Errorf("uploaded = %v, want %v (calls %v)", uploaded, tt.uploads, h.calls)
			}
			if tt.uploads && len(h.pypiForm) == 2 && (h.pypiForm[0]["version"] != "1.0.0rc1" || h.pypiForm[1]["version"] != "1.0.0-RC.1") {
				t.Errorf("PyPI uploads declare versions %q and %q, want the files' own", h.pypiForm[0]["version"], h.pypiForm[1]["version"])
			}
		})
	}
}

// TestPublishReleasePackagesCustomRegistry checks a GitProvider's own scope
// and registries (#1229, decision 5): under scope @acme a @c4factory tarball
// is left alone, uploads go to the custom URLs with their own tokens, and the
// host's credentials never leave the host.
func TestPublishReleasePackagesCustomRegistry(t *testing.T) {
	ctx := context.Background()
	assets := []testAsset{
		{"shop-ui-0.1.0.tgz", npmTarball(t, "@acme/shop-ui", "0.1.0")},
		{"legacy-0.1.0.tgz", npmTarball(t, "@c4factory/legacy", "0.1.0")},
		{"shop_api-0.1.0-py3-none-any.whl", wheel(t, "Shop_API", "0.1.0")},
	}
	for _, tt := range []struct {
		name     string
		tokens   bool
		npmAuth  string
		pypiAuth string
	}{
		{"with the registries' tokens", true, "Bearer npm-token",
			"Basic " + base64.StdEncoding.EncodeToString([]byte("__token__:pypi-token"))},
		{"without tokens the custom registry gets no credentials", false, "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			custom, customURL := newCustomRegistry(t)
			packages := Packages{NpmScope: "@acme", NpmRegistry: customURL + "/npm/", PyPIIndex: customURL + "/pypi/simple",
				PyPIUpload: customURL + "/pypi"}
			if tt.tokens {
				packages.NpmToken, packages.PyPIToken = "npm-token", "pypi-token"
			}
			h, p := newPackageHost(t, "v0.1.0", assets, packages)
			got, err := p.PublishReleasePackages(ctx, p.Repository("shop"), "v0.1.0")
			if err != nil {
				t.Fatal(err)
			}
			want := []Package{
				{Outcome: PackagePublished, Ecosystem: EcosystemNpm, Name: "@acme/shop-ui", Version: "0.1.0", File: "shop-ui-0.1.0.tgz"},
				{Outcome: PackageUnscoped, Ecosystem: EcosystemNpm, Name: "@c4factory/legacy", Version: "0.1.0", File: "legacy-0.1.0.tgz",
					Reason: "@c4factory/legacy is not under the scope @acme"},
				{Outcome: PackagePublished, Ecosystem: EcosystemPyPI, Name: "Shop_API", Version: "0.1.0", File: "shop_api-0.1.0-py3-none-any.whl"},
			}
			if !slices.Equal(got.Packages, want) {
				t.Fatalf("PublishReleasePackages() = %+v\nwant %+v", got.Packages, want)
			}
			if wantCalls := []string{"GET /npm/@acme%2Fshop-ui", "PUT /npm/@acme%2Fshop-ui", "GET /pypi/simple/shop-api/", "POST /pypi"}; !slices.Equal(custom.calls, wantCalls) {
				t.Errorf("custom registry calls = %v, want %v", custom.calls, wantCalls)
			}
			if h.npmPut != nil || len(h.pypiForm) != 0 || slices.ContainsFunc(h.calls, func(c string) bool {
				return strings.Contains(c, "/api/packages/")
			}) {
				t.Errorf("the host's registries were used: calls %v", h.calls)
			}
			if custom.npmAuth != tt.npmAuth || len(custom.pypiAuth) != 1 || custom.pypiAuth[0] != tt.pypiAuth {
				t.Errorf("custom registry got Authorization npm %q, PyPI %v; want %q and %q", custom.npmAuth, custom.pypiAuth,
					tt.npmAuth, tt.pypiAuth)
			}
			doc := decodeNpmPut(t, custom.npmPut)
			if v := doc.Versions["0.1.0"]; v.Dist.Tarball != customURL+"/npm/@acme/shop-ui/-/shop-ui-0.1.0.tgz" {
				t.Errorf("npm tarball URL = %q, want it under the custom registry", v.Dist.Tarball)
			}
			if form := custom.pypiForm[0]; form["name"] != "Shop_API" || form["version"] != "0.1.0" {
				t.Errorf("PyPI form = %v, want the package's own name and version", form)
			}
		})
	}
}
