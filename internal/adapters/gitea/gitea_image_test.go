package gitea

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"

	"github.com/JairJosafath/theos/internal/oci/ocitest"
	"github.com/JairJosafath/theos/internal/ports"
)

// imageHost is a Gitea serving the release of v1.0.0 of webshop/orders-api
// with its files, and a container registry that asks for a bearer token at
// the ROOT_URL http://gitea.invalid:3000/, which doesn't resolve.
type imageHost struct {
	mu sync.Mutex
	// files are the release's files by name; nil is no release.
	files map[string][]byte
	// tokens counts the token requests that reached the host; uploads the
	// blob uploads started.
	tokens, uploads int
	links           []string
}

const imageToken = "s3cret"

func newImageHost(t *testing.T) (*imageHost, ports.Provider, string) {
	t.Helper()
	h := &imageHost{}
	reg := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/repos/webshop/orders-api/releases/tags/v1.0.0", func(w http.ResponseWriter, _ *http.Request) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.files == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		rel := giteaRelease{ID: 7, TagName: "v1.0.0"}
		for _, name := range slices.Sorted(maps.Keys(h.files)) {
			rel.Assets = append(rel.Assets, struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
				Size int64  `json:"size"`
				UUID string `json:"uuid"`
			}{ID: int64(len(rel.Assets) + 1), Name: name, Size: int64(len(h.files[name])), UUID: "uuid-" + name})
		}
		_ = json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("GET /attachments/{uuid}", func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		data, ok := h.files[strings.TrimPrefix(r.PathValue("uuid"), "uuid-")]
		h.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(data)
	})
	mux.HandleFunc("GET /api/v1/packages/webshop/container/{name}/{version}", func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		defer h.mu.Unlock()
		pkg := map[string]any{"name": r.PathValue("name"), "version": r.PathValue("version")}
		if len(h.links) > 0 {
			pkg["repository"] = map[string]string{"name": r.PathValue("name")}
		}
		_ = json.NewEncoder(w).Encode(pkg)
	})
	// Gitea refuses a second link, as it does (HTTP 400).
	mux.HandleFunc("POST /api/v1/packages/webshop/container/{name}/-/link/{repo}", func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		defer h.mu.Unlock()
		if len(h.links) > 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		h.links = append(h.links, r.PathValue("name")+" -> "+r.PathValue("repo"))
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("GET /v2/token", func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		h.tokens++
		h.mu.Unlock()
		if u, p, ok := r.BasicAuth(); !ok || u != "c4admin" || p != imageToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "registry-token"})
	})
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer registry-token" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="http://gitea.invalid:3000/v2/token",service="container_registry"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/blobs/uploads/") {
			h.mu.Lock()
			h.uploads++
			h.mu.Unlock()
		}
		reg.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p, err := Client{}.Provider(ports.Config{URL: srv.URL, Owner: "webshop",
		Credentials: ports.Credentials{Username: "c4admin", Token: imageToken}})
	if err != nil {
		t.Fatal(err)
	}
	return h, p, strings.TrimPrefix(srv.URL, "http://")
}

func (h *imageHost) release(files map[string][]byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.files = files
}

// #1573: the image-path rule lives in Repository alone; the controller and
// the fakes ask it instead of restating it.
func TestRepositoryImageRepository(t *testing.T) {
	for _, tc := range []struct {
		name string
		repo ports.Repository
		want string
	}{
		{"lower-case name below the registry",
			ports.Repository{Owner: "WebShop", Name: "Orders-API", ImageRegistry: "gitea.example/webshop"},
			"gitea.example/webshop/orders-api"},
		{"no registry, no repository", ports.Repository{Owner: "webshop", Name: "orders-api"}, ""},
	} {
		if got := tc.repo.ImageRepository(); got != tc.want {
			t.Errorf("%s: ImageRepository() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestPublishImage(t *testing.T) {
	ctx := context.Background()
	h, p, host := newImageHost(t)
	repo := p.Repository("orders-api")
	if want := host + "/webshop"; repo.ImageRegistry != want {
		t.Errorf("ImageRegistry = %q, want %q", repo.ImageRegistry, want)
	}
	if want := host + "/webshop/orders-api"; repo.ImageRepository() != want {
		t.Errorf("ImageRepository() = %q, want %q", repo.ImageRepository(), want)
	}
	archive, digest := ocitest.TwoPlatforms(t)
	h.release(map[string][]byte{"orders-api-v1.0.0.oci.tar": archive, "SHA256SUMS": []byte("abc\n")})

	got, err := p.PublishImage(ctx, repo, "v1.0.0")
	want := ports.Image{Outcome: ports.ImagePublished, Repository: host + "/webshop/orders-api", Tag: "v1.0.0", Digest: digest,
		Platforms: ocitest.Platforms}
	if err != nil || got.Outcome != want.Outcome || got.Repository != want.Repository || got.Digest != want.Digest ||
		!slices.Equal(got.Platforms, want.Platforms) {
		t.Fatalf("PublishImage() = %+v, %v; want %+v", got, err, want)
	}
	if h.tokens == 0 {
		t.Error("no token request reached the provider's address; the challenge's ROOT_URL doesn't resolve")
	}
	if !slices.Equal(h.links, []string{"orders-api -> orders-api"}) {
		t.Errorf("links = %q, want the package linked to its repository", h.links)
	}
	if d, err := p.ImageDigest(ctx, repo, "v1.0.0"); err != nil || d != digest {
		t.Errorf("ImageDigest(v1.0.0) = %q, %v; want %s", d, err, digest)
	}
	if d, err := p.ImageDigest(ctx, repo, "v2.0.0"); err != nil || d != "" {
		t.Errorf("ImageDigest(v2.0.0) = %q, %v; want none", d, err)
	}

	h.uploads = 0
	again, err := p.PublishImage(ctx, repo, "v1.0.0")
	if err != nil || again.Outcome != ports.ImagePublished || again.Digest != digest {
		t.Fatalf("PublishImage() again = %+v, %v; want Published %s", again, err, digest)
	}
	if h.uploads != 0 {
		t.Errorf("the repeat started %d blob uploads, want none", h.uploads)
	}

	other, _ := ocitest.TwoPlatforms(t)
	h.release(map[string][]byte{"orders-api-v1.0.0.oci.tar": other})
	taken, err := p.PublishImage(ctx, repo, "v1.0.0")
	if err != nil || taken.Outcome != ports.ImageTagTaken || !strings.Contains(taken.Reason, digest) ||
		taken.Digest == "" || taken.Digest == digest {
		t.Fatalf("PublishImage() of another image = %+v, %v; want TagTaken naming %s and its own digest", taken, err, digest)
	}
	if d, _ := p.ImageDigest(ctx, repo, "v1.0.0"); d != digest {
		t.Errorf("v1.0.0 moved to %s, want it to stay %s", d, digest)
	}
}

func TestPublishImageOutcomes(t *testing.T) {
	archive, _ := ocitest.TwoPlatforms(t)
	var noIndex bytes.Buffer
	tw := tar.NewWriter(&noIndex)
	layout := []byte(`{"imageLayoutVersion":"1.0.0"}`)
	_ = tw.WriteHeader(&tar.Header{Name: "oci-layout", Mode: 0o644, Size: int64(len(layout)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(layout)
	_ = tw.Close()

	for _, tt := range []struct {
		name   string
		files  map[string][]byte
		want   ports.ImageOutcome
		reason string
	}{
		{"no release", nil, ports.ImageNone, ""},
		{"a release without an archive", map[string][]byte{"SHA256SUMS": []byte("abc\n")}, ports.ImageNone, ""},
		{"two archives", map[string][]byte{"a.oci.tar": archive, "b.oci.tar": archive}, ports.ImageInvalid,
			"2 image archives, not one: a.oci.tar, b.oci.tar"},
		{"an archive the push refuses", map[string][]byte{"a.oci.tar": noIndex.Bytes()}, ports.ImageInvalid,
			"a.oci.tar: invalid image archive: no index.json"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, p, _ := newImageHost(t)
			h.release(tt.files)
			got, err := p.PublishImage(context.Background(), p.Repository("orders-api"), "v1.0.0")
			if err != nil || got.Outcome != tt.want || !strings.Contains(got.Reason, tt.reason) || got.Digest != "" {
				t.Fatalf("PublishImage() = %+v, %v; want %s saying %q", got, err, tt.want, tt.reason)
			}
			if len(h.links) != 0 {
				t.Errorf("links = %q, want none", h.links)
			}
		})
	}
}

// A registry that refuses the credential is the host's failure, retried,
// not an outcome of the archive.
func TestPublishImageRefusedIsAnError(t *testing.T) {
	h, _, host := newImageHost(t)
	archive, _ := ocitest.TwoPlatforms(t)
	h.release(map[string][]byte{"orders-api-v1.0.0.oci.tar": archive})
	p, err := Client{}.Provider(ports.Config{URL: "http://" + host, Owner: "webshop",
		Credentials: ports.Credentials{Username: "c4admin", Token: "wrong"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := p.PublishImage(context.Background(), p.Repository("orders-api"), "v1.0.0"); err == nil {
		t.Fatalf("PublishImage() with a wrong token = %+v; want an error", got)
	}
}
