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

package github

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"

	"github.com/JairJosafath/theos/internal/adapters/github/githubtest"
	"github.com/JairJosafath/theos/internal/oci/ocitest"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// registryHost returns the Host of owner on the fake GitHub, with the
// client for which the fake's registries are at subdomains of its host, as
// an Enterprise Server's are.
func registryHost(t *testing.T, gh *githubtest.Server, owner string, packages ports.Packages) Host {
	t.Helper()
	h, err := (Client{HTTP: gh.Client()}).Host(ports.Config{URL: gh.URL(), Owner: owner, Packages: packages,
		Credentials: ports.Credentials{Username: "ada", Token: hostToken}})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// TestRegistryContract covers #80: the GitHub adapter passes the Registry
// contract against the fake GitHub, each subtest in an organization of its
// own. A module is served from its repository, so its path is the
// repository's and nothing is uploaded.
func TestRegistryContract(t *testing.T) {
	gh := githubtest.New(t, "ada", hostToken)
	orgs := 0
	contract.Registry(t, contract.RegistryEnv{
		New: func(t *testing.T) (ports.GitHost, ports.Registry) {
			t.Helper()
			orgs++
			org := fmt.Sprintf("registry-%d", orgs)
			gh.AddOrg(org)
			h := registryHost(t, gh, org, ports.Packages{})
			return h, h
		},
		ModulePath: func(repo ports.Repository) string { return moduleHost(t, gh) + "/" + repo.Owner + "/" + repo.Name },
		NoUpload:   true,
		// GitHub can do all of it: nothing is skipped.
		Cannot: map[string]string{},
	})
	if calls := gh.Unserved(); len(calls) > 0 {
		t.Errorf("the adapter made calls the fake GitHub doesn't serve: %q", calls)
	}
}

// moduleHost is the host of a Go module's path on the fake GitHub: its
// address without the port.
func moduleHost(t *testing.T, gh *githubtest.Server) string {
	t.Helper()
	u, err := url.Parse(gh.URL())
	if err != nil {
		t.Fatal(err)
	}
	return u.Hostname()
}

// TestPackagePlaces covers #80: where an owner's packages are on github.com
// and on an Enterprise Server, and the npm scope rule: GitHub's registry
// takes the owner's scope alone, another registry any.
func TestPackagePlaces(t *testing.T) {
	const enterprise = "https://github.example.com"
	for _, c := range []struct {
		name          string
		url, owner    string
		set           ports.Packages
		images        string
		scope, npm    string
		refusedNaming []string
	}{
		{name: "github.com: ghcr.io, npm.pkg.github.com and the owner's scope",
			url: GitHubWebURL, owner: "acme",
			images: "ghcr.io/acme", scope: "@acme", npm: "https://npm.pkg.github.com/"},
		{name: "an owner in upper case has a scope and an image path in lower case",
			url: GitHubWebURL, owner: "AcmeCorp",
			images: "ghcr.io/acmecorp", scope: "@acmecorp", npm: "https://npm.pkg.github.com/"},
		{name: "an Enterprise Server: its registries at subdomains",
			url: enterprise, owner: "acme",
			images: "containers.github.example.com/acme", scope: "@acme", npm: "https://npm.github.example.com/"},
		{name: "the owner's scope, set, is the one GitHub takes",
			url: GitHubWebURL, owner: "acme", set: ports.Packages{NpmScope: "@acme"},
			images: "ghcr.io/acme", scope: "@acme", npm: "https://npm.pkg.github.com/"},
		{name: "another scope on GitHub's registry is refused, naming both",
			url: GitHubWebURL, owner: "acme", set: ports.Packages{NpmScope: "@theos"},
			refusedNaming: []string{"@theos", "@acme"}},
		{name: "GitHub's registry named outright is still GitHub's",
			url: GitHubWebURL, owner: "acme",
			set:           ports.Packages{NpmScope: "@shop", NpmRegistry: "https://npm.pkg.github.com"},
			refusedNaming: []string{"@shop", "@acme"}},
		{name: "another registry takes any scope",
			url: GitHubWebURL, owner: "acme",
			set:    ports.Packages{NpmScope: "@shop", NpmRegistry: "https://registry.example.com/npm"},
			images: "ghcr.io/acme", scope: "@shop", npm: "https://registry.example.com/npm/"},
		{name: "another registry with no scope set has the factory's",
			url: GitHubWebURL, owner: "acme", set: ports.Packages{NpmRegistry: "https://registry.example.com/npm/"},
			images: "ghcr.io/acme", scope: ports.DefaultNpmScope, npm: "https://registry.example.com/npm/"},
		{name: "an Enterprise Server without subdomains names its npm registry, and it is not GitHub's own",
			url: enterprise, owner: "acme",
			set:    ports.Packages{NpmScope: "@acme", NpmRegistry: enterprise + "/_registry/npm/"},
			images: "containers.github.example.com/acme", scope: "@acme", npm: enterprise + "/_registry/npm/"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, err := (Client{}).Host(ports.Config{URL: c.url, Owner: c.owner, Packages: c.set,
				Credentials: ports.Credentials{Token: hostToken}})
			if len(c.refusedNaming) > 0 {
				if err == nil {
					t.Fatal("Host() took the scope, want it refused")
				}
				for _, name := range c.refusedNaming {
					if !strings.Contains(err.Error(), name) {
						t.Errorf("Host() refused with %q, want it to name %s", err, name)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			repo := h.Repository("Orders")
			if repo.ImageRegistry != c.images || repo.NpmScope != c.scope || repo.NpmRegistry != c.npm {
				t.Errorf("Repository() has images at %s and npm at %s under %s, want %s, %s and %s",
					repo.ImageRegistry, repo.NpmRegistry, repo.NpmScope, c.images, c.npm, c.scope)
			}
			if want := c.images + "/orders"; repo.ImageRepository() != want {
				t.Errorf("ImageRepository() = %s, want %s", repo.ImageRepository(), want)
			}
			if repo.GoProxy != "" || repo.PyPIIndex != "" || repo.PyPIUpload != "" {
				t.Errorf("Repository() names a Go proxy %q and a Python index %q, %q; GitHub has neither",
					repo.GoProxy, repo.PyPIIndex, repo.PyPIUpload)
			}
		})
	}
	t.Run("the Python index is the one the GitProvider names", func(t *testing.T) {
		set := ports.Packages{PyPIIndex: "https://pypi.example.com/simple", PyPIUpload: "https://pypi.example.com/legacy"}
		h, err := (Client{}).Host(ports.Config{Owner: "acme", Packages: set, Credentials: ports.Credentials{Token: hostToken}})
		if err != nil {
			t.Fatal(err)
		}
		if repo := h.Repository("orders"); repo.PyPIIndex != set.PyPIIndex || repo.PyPIUpload != set.PyPIUpload {
			t.Errorf("Repository() has the Python index %q, %q, want %q, %q", repo.PyPIIndex, repo.PyPIUpload,
				set.PyPIIndex, set.PyPIUpload)
		}
	})
}

// released is a repository of the organization acme on the fake GitHub
// with a release of v1.0.0, whose files a test adds.
type released struct {
	ctx  context.Context
	gh   *githubtest.Server
	h    Host
	repo ports.Repository
	head string
}

const releasedTag = "v1.0.0"

func newReleased(t *testing.T, packages ports.Packages, files scaffold.Tree) released {
	t.Helper()
	r := released{ctx: context.Background(), gh: githubtest.New(t, "ada", hostToken)}
	r.gh.AddOrg("acme")
	r.h = registryHost(t, r.gh, "acme", packages)
	var err error
	if r.repo, err = r.h.EnsureRepository(r.ctx, "orders", ""); err != nil {
		t.Fatal(err)
	}
	if r.head, err = r.h.PushInitialCommit(r.ctx, r.repo, files, "Scaffold\n", shopAuthor); err != nil {
		t.Fatal(err)
	}
	if err := r.gh.AddRelease("acme", "orders", releasedTag, r.head, "", "notes.txt", "notes", false); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if calls := r.gh.Unserved(); len(calls) > 0 {
			t.Errorf("the adapter made calls the fake GitHub doesn't serve: %q", calls)
		}
	})
	return r
}

// TestImagesOnGitHub covers #80: a release's image archive goes to the
// container registry once, by digest, and is copied from there.
func TestImagesOnGitHub(t *testing.T) {
	archive, digest := ocitest.TwoPlatforms(t)
	r := newReleased(t, ports.Packages{}, scaffold.Tree{shopReadme: {Data: []byte("hello\n")}})
	instance := strings.TrimPrefix(r.gh.URL(), "http://")
	image := "containers." + instance + "/acme/orders"

	none := ports.Image{Outcome: ports.ImageNone, Repository: image, Tag: releasedTag}
	if got, err := r.h.PublishImage(r.ctx, r.repo, releasedTag); err != nil || !sameImage(got, none) {
		t.Fatalf("PublishImage() of a release with no archive = %+v, %v; want %+v", got, err, none)
	}
	if got, err := r.h.ImageDigest(r.ctx, r.repo, releasedTag); err != nil || got != "" {
		t.Errorf("ImageDigest() before the push = %q, %v; want none", got, err)
	}

	r.gh.AddReleaseAsset("acme", "orders", releasedTag, "orders.oci.tar", archive)
	want := ports.Image{Outcome: ports.ImagePublished, Repository: image, Tag: releasedTag, Digest: digest,
		Platforms: ocitest.Platforms}
	for range 2 {
		if got, err := r.h.PublishImage(r.ctx, r.repo, releasedTag); err != nil || !sameImage(got, want) {
			t.Fatalf("PublishImage() = %+v, %v; want %+v", got, err, want)
		}
	}
	if got, err := r.h.ImageDigest(r.ctx, r.repo, releasedTag); err != nil || got != digest {
		t.Errorf("ImageDigest() = %q, %v; want %s", got, err, digest)
	}

	t.Run("copies the image to another registry by its digest", func(t *testing.T) {
		elsewhere := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
		t.Cleanup(elsewhere.Close)
		to := ports.ImageRegistry{Host: strings.TrimPrefix(elsewhere.URL, "http://"), Insecure: true}
		if err := r.h.CopyImage(r.ctx, r.repo, digest, to, "prod/orders", releasedTag); err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(elsewhere.URL + "/v2/prod/orders/manifests/" + releasedTag)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := elsewhere.Client().Head(u.String())
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if got := resp.Header.Get("Docker-Content-Digest"); got != digest {
			t.Errorf("the copy's tag names %q, want %s", got, digest)
		}
	})

	t.Run("a second archive on the release is an outcome that names both", func(t *testing.T) {
		r.gh.AddReleaseAsset("acme", "orders", releasedTag, "worker.oci.tar", archive)
		got, err := r.h.PublishImage(r.ctx, r.repo, releasedTag)
		if err != nil || got.Outcome != ports.ImageInvalid || !strings.Contains(got.Reason, "orders.oci.tar") ||
			!strings.Contains(got.Reason, "worker.oci.tar") {
			t.Errorf("PublishImage() of a release with two archives = %+v, %v; want Invalid, naming both", got, err)
		}
	})

	t.Run("an installation token is no credential for the registry", func(t *testing.T) {
		f := newFenced(t)
		creds, err := f.h.EnsureAgentAccess(f.ctx, f.repo, "agent")
		if err != nil {
			t.Fatal(err)
		}
		agent, err := (Client{HTTP: f.gh.Client()}).Host(ports.Config{URL: f.gh.URL(), Owner: f.repo.Owner,
			Credentials: ports.Credentials{Token: creds.Token}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := agent.ImageDigest(f.ctx, f.repo, releasedTag); err == nil {
			t.Error("ImageDigest() with an agent's token succeeded, want the registry to refuse it")
		}
	})
}

func sameImage(a, b ports.Image) bool {
	return a.Outcome == b.Outcome && a.Repository == b.Repository && a.Tag == b.Tag && a.Digest == b.Digest &&
		slices.Equal(a.Platforms, b.Platforms) && a.Reason == b.Reason
}

// TestGoModulesOnGitHub covers #80: GitHub serves a module from its
// repository, so publishing one uploads nothing and says what go would
// find: the module, or why not.
func TestGoModulesOnGitHub(t *testing.T) {
	goMod := func(path string) scaffold.Tree {
		return scaffold.Tree{"go.mod": {Data: []byte("module " + path + "\n\ngo 1.24\n")}}
	}
	for _, c := range []struct {
		name string
		// path is the module path go.mod declares, with %s for the
		// repository's own; "" is no go.mod.
		path, tag string
		want      ports.GoModuleOutcome
		reason    string
	}{
		{name: "the repository's own path is fetchable", path: "%s", tag: "v1.0.0", want: ports.GoModuleExists},
		{name: "from v2 on the path ends in the major", path: "%s/v2", tag: "v2.0.0", want: ports.GoModuleExists},
		{name: "no go.mod", tag: "v1.0.0", want: ports.GoModuleNoGoMod},
		{name: "a factory path, which no host serves", path: "theos.local/webshop/orders", tag: "v1.0.0",
			want: ports.GoModuleInvalid, reason: "the path must be %s"},
		{name: "another repository's path", path: "%s-api", tag: "v1.0.0",
			want: ports.GoModuleInvalid, reason: "the path must be %s"},
		{name: "a module below the root", path: "%s/tools", tag: "v1.0.0",
			want: ports.GoModuleInvalid, reason: "the path must be %s"},
		{name: "a v2 tag on a path without the major", path: "%s", tag: "v2.0.0",
			want: ports.GoModuleInvalid, reason: "should be v0 or v1, not v2"},
		{name: "a tag that is no canonical version", path: "%s", tag: "v1.0",
			want: ports.GoModuleInvalid, reason: "not a canonical semantic version"},
	} {
		t.Run(c.name, func(t *testing.T) {
			gh := githubtest.New(t, "ada", hostToken)
			gh.AddOrg("acme")
			own := moduleHost(t, gh) + "/acme/orders"
			files := scaffold.Tree{shopReadme: {Data: []byte("hello\n")}}
			path := ""
			if c.path != "" {
				path = strings.Replace(c.path, "%s", own, 1)
				files = goMod(path)
			}
			ctx := context.Background()
			h := registryHost(t, gh, "acme", ports.Packages{})
			repo, err := h.EnsureRepository(ctx, "orders", "")
			if err != nil {
				t.Fatal(err)
			}
			head, err := h.PushInitialCommit(ctx, repo, files, "Scaffold\n", shopAuthor)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := h.PublishGoModule(ctx, repo, c.tag); err == nil {
				t.Error("PublishGoModule() of a tag the repository doesn't have succeeded, want an error")
			}
			if err := h.CreateTag(ctx, repo, c.tag, head, "Release"); err != nil {
				t.Fatal(err)
			}
			got, err := h.PublishGoModule(ctx, repo, c.tag)
			if err != nil {
				t.Fatal(err)
			}
			if got.Outcome != c.want || got.Path != path || got.Version != c.tag {
				t.Errorf("PublishGoModule() = %+v, want %s of %q at %s", got, c.want, path, c.tag)
			}
			if reason := strings.Replace(c.reason, "%s", own, 1); !strings.Contains(got.Reason, reason) {
				t.Errorf("PublishGoModule() gives the reason %q, want it to say %q", got.Reason, reason)
			}
			if calls := gh.Unserved(); len(calls) > 0 {
				t.Errorf("the adapter made calls the fake GitHub doesn't serve: %q", calls)
			}
		})
	}
}

// npmTarball is the tarball npm pack makes of a package with manifest.
func npmTarball(t *testing.T, manifest map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "package/package.json", Mode: 0o644, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// wheel is a wheel of the Python package name at version.
func wheel(t *testing.T, name, version string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name + "-" + version + ".dist-info/METADATA")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(w, "Metadata-Version: 2.1\nName: %s\nVersion: %s\n", name, version); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// elsewhere is a registry that is not GitHub's: it has no package, takes
// every upload, and keeps what each request was signed with.
type elsewhere struct {
	srv     *httptest.Server
	mu      sync.Mutex
	signed  []string
	uploads int
}

func newElsewhere(t *testing.T) *elsewhere {
	t.Helper()
	e := &elsewhere{}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		e.mu.Lock()
		defer e.mu.Unlock()
		e.signed = append(e.signed, r.Header.Get("Authorization"))
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		e.uploads++
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(e.srv.Close)
	return e
}

// requests returns how many uploads arrived and what every request was
// signed with.
func (e *elsewhere) requests() (uploads int, signed []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.uploads, slices.Clone(e.signed)
}

const (
	ordersWheel = "orders-1.0.0-py3-none-any.whl"
	noReadme    = "README.md"
)

// TestPackagesOnGitHub covers #80: a release's npm tarballs go to GitHub's
// npm registry under the owner's scope, linked to the repository; its
// Python packages go where the GitProvider says, and nowhere without that;
// and the operator's token goes to GitHub's own registry alone.
func TestPackagesOnGitHub(t *testing.T) {
	t.Run("npm packages are published once, under the owner's scope, naming the repository", ownNpm)
	t.Run("a Python package has nowhere to go until the GitProvider names an index", pythonIndex)
	t.Run("a registry that is not GitHub's never sees the operator's token", otherNpm)
}

func ownNpm(t *testing.T) {
	r := newReleased(t, ports.Packages{}, scaffold.Tree{noReadme: {Data: []byte("hello\n")}})
	if _, err := r.h.PublishReleasePackages(r.ctx, r.repo, "v9.9.9"); !errors.Is(err, ports.ErrNoRelease) {
		t.Errorf("PublishReleasePackages() of a tag with no release gave %v, want ErrNoRelease", err)
	}
	const elsewhereURL = "https://example.com/acme/web"
	for file, manifest := range map[string]map[string]any{
		"1-orders.tgz": {"name": "@acme/orders", "version": "1.0.0", "description": "Orders"},
		"2-web.tgz":    {"name": "@acme/web", "version": "1.0.0", "repository": elsewhereURL},
		"3-tool.tgz":   {"name": "@other/tool", "version": "1.0.0"},
		"4-old.tgz":    {"name": "@acme/old", "version": "0.9.0"},
	} {
		r.gh.AddReleaseAsset("acme", "orders", releasedTag, file, npmTarball(t, manifest))
	}
	outcomes := func(first ports.PackageOutcome) map[string]ports.PackageOutcome {
		return map[string]ports.PackageOutcome{"@acme/orders": first, "@acme/web": first,
			"@other/tool": ports.PackageUnscoped, "@acme/old": ports.PackageInvalid}
	}
	for _, want := range []map[string]ports.PackageOutcome{outcomes(ports.PackagePublished), outcomes(ports.PackageExists)} {
		got, err := r.h.PublishReleasePackages(r.ctx, r.repo, releasedTag)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Packages) != len(want) {
			t.Fatalf("PublishReleasePackages() = %+v, want %d packages", got, len(want))
		}
		for _, pkg := range got.Packages {
			if pkg.Outcome != want[pkg.Name] || pkg.Ecosystem != ports.EcosystemNpm {
				t.Errorf("%s is %s (%s), want %s", pkg.Name, pkg.Outcome, pkg.Reason, want[pkg.Name])
			}
		}
	}
	for name, versions := range map[string][]string{"@acme/orders": {"1.0.0"}, "@acme/web": {"1.0.0"},
		"@other/tool": nil, "@acme/old": nil} {
		if got := r.gh.NpmVersions(name); !slices.Equal(got, versions) {
			t.Errorf("the registry has %s at %q, want %q", name, got, versions)
		}
	}
	linked := func(name string) string {
		repository, _ := r.gh.NpmManifest(name, "1.0.0")["repository"].(map[string]any)
		at, _ := repository["url"].(string)
		return at
	}
	if got, want := linked("@acme/orders"), "git+"+r.repo.CloneURL; got != want {
		t.Errorf("a manifest that names no repository was published naming %q, want %s", got, want)
	}
	if got := linked("@acme/web"); got != elsewhereURL {
		t.Errorf("a manifest that names a repository was published naming %q, want its own, %s", got, elsewhereURL)
	}
}

func pythonIndex(t *testing.T) {
	files := scaffold.Tree{noReadme: {Data: []byte("hello\n")}}
	r := newReleased(t, ports.Packages{}, files)
	r.gh.AddReleaseAsset("acme", "orders", releasedTag, ordersWheel, wheel(t, "orders", "1.0.0"))
	got, err := r.h.PublishReleasePackages(r.ctx, r.repo, releasedTag)
	if err != nil || len(got.Packages) != 1 || got.Packages[0].Outcome != ports.PackageInvalid ||
		got.Packages[0].Ecosystem != ports.EcosystemPyPI || !strings.Contains(got.Packages[0].Reason, "no Python package index") {
		t.Errorf("PublishReleasePackages() of a wheel with no index named = %+v, %v; want it Invalid for want of an index",
			got, err)
	}

	index, token := newElsewhere(t), rand.Text()
	r = newReleased(t, ports.Packages{PyPIIndex: index.srv.URL + "/simple", PyPIUpload: index.srv.URL + "/legacy/",
		PyPIToken: token}, files)
	r.gh.AddReleaseAsset("acme", "orders", releasedTag, ordersWheel, wheel(t, "orders", "1.0.0"))
	got, err = r.h.PublishReleasePackages(r.ctx, r.repo, releasedTag)
	if err != nil || len(got.Packages) != 1 || got.Packages[0].Outcome != ports.PackagePublished {
		t.Fatalf("PublishReleasePackages() of a wheel with an index named = %+v, %v; want it Published", got, err)
	}
	uploads, signed := index.requests()
	if uploads != 1 {
		t.Errorf("the index took %d uploads, want one", uploads)
	}
	var probe http.Request
	probe.Header = http.Header{}
	probe.SetBasicAuth("__token__", token)
	for _, authorization := range signed {
		if authorization != probe.Header.Get("Authorization") {
			t.Error("a request to the index was not signed with the index's own token and nothing else")
		}
	}
}

func otherNpm(t *testing.T) {
	other := newElsewhere(t)
	r := newReleased(t, ports.Packages{NpmScope: "@shop", NpmRegistry: other.srv.URL},
		scaffold.Tree{noReadme: {Data: []byte("hello\n")}})
	r.gh.AddReleaseAsset("acme", "orders", releasedTag, "orders.tgz",
		npmTarball(t, map[string]any{"name": "@shop/orders", "version": "1.0.0"}))
	got, err := r.h.PublishReleasePackages(r.ctx, r.repo, releasedTag)
	if err != nil || len(got.Packages) != 1 || got.Packages[0].Outcome != ports.PackagePublished {
		t.Fatalf("PublishReleasePackages() to another registry = %+v, %v; want the package Published", got, err)
	}
	uploads, signed := other.requests()
	if uploads != 1 {
		t.Errorf("the registry took %d uploads, want one", uploads)
	}
	for _, authorization := range signed {
		if authorization != "" {
			t.Error("a request to a registry that is not GitHub's carried a credential, and the GitProvider named none for it")
		}
	}
	if got := r.gh.NpmVersions("@shop/orders"); got != nil {
		t.Errorf("GitHub's registry has @shop/orders at %q, want it nowhere but the registry named", got)
	}
}
