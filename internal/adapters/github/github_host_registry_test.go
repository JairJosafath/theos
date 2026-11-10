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
	"context"
	"fmt"
	"io"
	"log"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
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
