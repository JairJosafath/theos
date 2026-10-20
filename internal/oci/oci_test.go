package oci

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// fakeRegistry is go-containerregistry's in-memory registry on httptest,
// counting the blob uploads it starts.
type fakeRegistry struct {
	Registry
	uploads atomic.Int64
}

func newRegistry(t *testing.T, wrap func(http.Handler) http.Handler) *fakeRegistry {
	t.Helper()
	f := &fakeRegistry{}
	var h http.Handler = registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	if wrap != nil {
		h = wrap(h)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/blobs/uploads/") {
			f.uploads.Add(1)
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	f.Host = strings.TrimPrefix(srv.URL, "http://")
	return f
}

// bearer asks for a token from /token, which it hands for user:secret.
func bearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			u, p, ok := r.BasicAuth()
			if !ok || u != "user" || p != "secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "t0k3n"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer t0k3n" {
			w.Header().Set("WWW-Authenticate", `Bearer realm="http://`+r.Host+`/token",service="fake"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// must is v, or a panic that fails the test: fixtures only.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// platformImage is an OCI image for os/arch with a layer of each size.
func platformImage(t *testing.T, os, arch string, sizes ...int64) v1.Image {
	t.Helper()
	img := mutate.MediaType(empty.Image, types.OCIManifestSchema1)
	img = mutate.ConfigMediaType(img, types.OCIConfigJSON)
	for _, size := range sizes {
		img = must(mutate.AppendLayers(img, must(random.Layer(size, types.OCILayer))))
	}
	cf := must(img.ConfigFile()).DeepCopy()
	cf.OS, cf.Architecture = os, arch
	return must(mutate.ConfigFile(img, cf))
}

// attestation is a BuildKit-style attestation manifest for subject.
func attestation(t *testing.T, subject v1.Image) mutate.IndexAddendum {
	t.Helper()
	img := mutate.MediaType(empty.Image, types.OCIManifestSchema1)
	img = mutate.ConfigMediaType(img, types.OCIConfigJSON)
	statement := static.NewLayer([]byte(`{"_type":"https://in-toto.io/Statement/v0.1"}`), "application/vnd.in-toto+json")
	img = must(mutate.AppendLayers(img, statement))
	return mutate.IndexAddendum{Add: img, Descriptor: v1.Descriptor{
		Platform: &v1.Platform{OS: "unknown", Architecture: "unknown"},
		Annotations: map[string]string{
			"vnd.docker.reference.type":   attestationType,
			"vnd.docker.reference.digest": must(subject.Digest()).String(),
		},
	}}
}

// twoPlatforms is an index for linux/amd64, with a layer over
// MaxManifestSize so it streams, and linux/arm64, and amd64's attestation.
func twoPlatforms(t *testing.T) v1.ImageIndex {
	t.Helper()
	amd := platformImage(t, "linux", "amd64", 1024, MaxManifestSize+1024)
	arm := platformImage(t, "linux", "arm64", 2048)
	return addPlatforms(t, amd, arm, attestation(t, amd))
}

func addPlatforms(t *testing.T, amd, arm v1.Image, more ...mutate.IndexAddendum) v1.ImageIndex {
	t.Helper()
	idx := mutate.IndexMediaType(empty.Index, types.OCIImageIndex)
	adds := []mutate.IndexAddendum{
		{Add: amd, Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: "amd64"}}},
		{Add: arm, Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: "arm64"}}},
	}
	return mutate.AppendManifests(idx, append(adds, more...)...)
}

type entry struct {
	name string
	data []byte
}

// layout is idx as an OCI image layout's tar entries: oci-layout, every
// blob, then index.json naming idx.
func layout(t *testing.T, idx v1.ImageIndex) []entry {
	t.Helper()
	entries := []entry{{"oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`)}}
	blob := func(h v1.Hash, b []byte) {
		entries = append(entries, entry{"blobs/sha256/" + h.Hex, b})
	}
	blob(must(idx.Digest()), must(idx.RawManifest()))
	for _, d := range must(idx.IndexManifest()).Manifests {
		img := must(idx.Image(d.Digest))
		blob(d.Digest, must(img.RawManifest()))
		blob(must(img.ConfigName()), must(img.RawConfigFile()))
		for _, l := range must(img.Layers()) {
			blob(must(l.Digest()), must(io.ReadAll(must(l.Compressed()))))
		}
	}
	root := v1.Descriptor{
		MediaType: types.OCIImageIndex,
		Digest:    must(idx.Digest()),
		Size:      must(idx.Size()),
	}
	return append(entries, indexJSON(t, root))
}

func indexJSON(t *testing.T, roots ...v1.Descriptor) entry {
	t.Helper()
	b := must(json.Marshal(v1.IndexManifest{SchemaVersion: 2, MediaType: types.OCIImageIndex, Manifests: roots}))
	return entry{"index.json", b}
}

// stream is entries as a tar, read through a pipe as a stream is.
func stream(t *testing.T, entries []entry) io.Reader {
	t.Helper()
	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		for _, e := range entries {
			hdr := &tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.data)), Typeflag: tar.TypeReg}
			if err := tw.WriteHeader(hdr); err != nil {
				pw.CloseWithError(err)
				return
			}
			if _, err := tw.Write(e.data); err != nil {
				pw.CloseWithError(err)
				return
			}
		}
		pw.CloseWithError(tw.Close())
	}()
	t.Cleanup(func() { _ = pr.Close() })
	return pr
}

func TestPushTwoPlatformIndexFromAStream(t *testing.T) {
	ctx := context.Background()
	reg := newRegistry(t, nil)
	idx := twoPlatforms(t)

	got, err := Push(ctx, reg.Registry, "shop/web", "v1.0.0", stream(t, layout(t, idx)))
	if err != nil {
		t.Fatal(err)
	}
	want := must(idx.Digest()).String()
	if got.Digest != want {
		t.Errorf("digest %s, want %s", got.Digest, want)
	}
	if !slices.Equal(got.Platforms, []string{"linux/amd64", "linux/arm64"}) {
		t.Errorf("platforms %v, want linux/amd64 and linux/arm64 without the attestation", got.Platforms)
	}
	if d := must(Resolve(ctx, reg.Registry, "shop/web", "v1.0.0")); d != want {
		t.Errorf("the tag resolves to %q, want %s", d, want)
	}
	assertSameIndex(t, reg.Registry, "shop/web", idx)
}

// assertSameIndex reads repository@idx's digest back and checks every
// manifest and blob the index names, attestations included.
func assertSameIndex(t *testing.T, r Registry, repository string, idx v1.ImageIndex) {
	t.Helper()
	ref := must(name.NewDigest(r.Host+"/"+repository+"@"+must(idx.Digest()).String()))
	opts := []remote.Option{remote.WithAuth(&authn.Basic{Username: r.Username, Password: r.Password})}
	got := must(remote.Index(ref, opts...))
	if !bytes.Equal(must(got.RawManifest()), must(idx.RawManifest())) {
		t.Fatal("the registry's index differs from the archive's")
	}
	for _, d := range must(idx.IndexManifest()).Manifests {
		img := must(got.Image(d.Digest))
		if must(img.Digest()) != d.Digest {
			t.Errorf("manifest %s reads back as %s", d.Digest, must(img.Digest()))
		}
		for _, l := range must(img.Layers()) {
			b := must(io.ReadAll(must(l.Compressed())))
			if int64(len(b)) != must(l.Size()) {
				t.Errorf("layer %s reads back %d bytes", must(l.Digest()), len(b))
			}
		}
	}
}

func TestPushRepeatUploadsNoBlob(t *testing.T) {
	ctx := context.Background()
	reg := newRegistry(t, nil)
	entries := layout(t, twoPlatforms(t))
	first := must(Push(ctx, reg.Registry, "shop/web", "v1.0.0", stream(t, entries)))
	if reg.uploads.Load() == 0 {
		t.Fatal("the first push uploaded no blob")
	}
	reg.uploads.Store(0)
	again := must(Push(ctx, reg.Registry, "shop/web", "v1.0.0", stream(t, entries)))
	if n := reg.uploads.Load(); n != 0 {
		t.Errorf("the repeat started %d blob uploads, want 0", n)
	}
	if again.Digest != first.Digest {
		t.Errorf("the repeat pushed %s, the first %s", again.Digest, first.Digest)
	}
}

func TestPushRefusesAnInvalidArchive(t *testing.T) {
	big := platformImage(t, "linux", "amd64", 1024)
	big = mutate.Annotations(big, map[string]string{"padding": strings.Repeat("x", MaxManifestSize)}).(v1.Image)
	oversized := addPlatforms(t, big, platformImage(t, "linux", "arm64", 1024))

	tests := []struct {
		name string
		edit func(t *testing.T, idx v1.ImageIndex, entries []entry) []entry
		idx  v1.ImageIndex
		want string
	}{
		{
			name: "a corrupt blob",
			edit: func(t *testing.T, idx v1.ImageIndex, entries []entry) []entry {
				return corrupt(t, entries, layerDigest(t, idx, 0, 0))
			},
			want: "has content",
		},
		{
			name: "a corrupt blob that streams",
			edit: func(t *testing.T, idx v1.ImageIndex, entries []entry) []entry {
				return corrupt(t, entries, layerDigest(t, idx, 0, 1))
			},
			want: "has content",
		},
		{
			name: "a second root",
			edit: func(t *testing.T, idx v1.ImageIndex, entries []entry) []entry {
				d := must(idx.IndexManifest()).Manifests[0]
				root := v1.Descriptor{MediaType: types.OCIImageIndex, Digest: must(idx.Digest()), Size: must(idx.Size())}
				return append(entries[:len(entries)-1], indexJSON(t, root, d))
			},
			want: "names 2 manifests",
		},
		{
			name: "a missing blob",
			edit: func(t *testing.T, idx v1.ImageIndex, entries []entry) []entry {
				gone := "blobs/sha256/" + layerDigest(t, idx, 1, 0).Hex
				return slices.DeleteFunc(entries, func(e entry) bool { return e.name == gone })
			},
			want: "which the archive lacks",
		},
		{
			name: "a manifest over 4 MiB",
			idx:  oversized,
			want: "over 4194304",
		},
		{
			name: "no index.json",
			edit: func(t *testing.T, idx v1.ImageIndex, entries []entry) []entry {
				return entries[:len(entries)-1]
			},
			want: "no index.json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			reg := newRegistry(t, nil)
			idx := tt.idx
			if idx == nil {
				idx = twoPlatforms(t)
			}
			entries := layout(t, idx)
			if tt.edit != nil {
				entries = tt.edit(t, idx, entries)
			}
			_, err := Push(ctx, reg.Registry, "shop/web", "v1.0.0", stream(t, entries))
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err %v, want ErrInvalid with %q", err, tt.want)
			}
			if d := must(Resolve(ctx, reg.Registry, "shop/web", "v1.0.0")); d != "" {
				t.Errorf("a refused archive tagged %s", d)
			}
		})
	}
}

// layerDigest is the digest of the index's image i's layer l.
func layerDigest(t *testing.T, idx v1.ImageIndex, i, l int) v1.Hash {
	t.Helper()
	d := must(idx.IndexManifest()).Manifests[i]
	return must(must(idx.Image(d.Digest)).Manifest()).Layers[l].Digest
}

// corrupt flips a byte in h's blob, keeping its name and size.
func corrupt(t *testing.T, entries []entry, h v1.Hash) []entry {
	t.Helper()
	for i, e := range entries {
		if e.name == "blobs/sha256/"+h.Hex {
			b := bytes.Clone(e.data)
			b[len(b)/2] ^= 0xff
			entries[i].data = b
			return entries
		}
	}
	t.Fatalf("no blob %s", h)
	return nil
}

func TestResolveATagAndAMissingTag(t *testing.T) {
	ctx := context.Background()
	reg := newRegistry(t, nil)
	pushed := must(Push(ctx, reg.Registry, "shop/web", "v1.0.0", stream(t, layout(t, twoPlatforms(t)))))

	if d := must(Resolve(ctx, reg.Registry, "shop/web", "v1.0.0")); d != pushed.Digest {
		t.Errorf("v1.0.0 resolves to %q, want %s", d, pushed.Digest)
	}
	if d := must(Resolve(ctx, reg.Registry, "shop/web", "v2.0.0")); d != "" {
		t.Errorf("a missing tag resolves to %q, want none", d)
	}
	if d := must(Resolve(ctx, reg.Registry, "shop/nothing", "v1.0.0")); d != "" {
		t.Errorf("a missing repository resolves to %q, want none", d)
	}
}

func TestCopyKeepsTheDigestAndTheAttestations(t *testing.T) {
	ctx := context.Background()
	from := newRegistry(t, nil)
	to := newRegistry(t, bearer)
	to.Username, to.Password = "user", "secret"
	idx := twoPlatforms(t)
	pushed := must(Push(ctx, from.Registry, "shop/web", "v1.0.0", stream(t, layout(t, idx))))

	if err := Copy(ctx, from.Registry, "shop/web", pushed.Digest, to.Registry, "mirror/web", "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if d := must(Resolve(ctx, to.Registry, "mirror/web", "v1.0.0")); d != pushed.Digest {
		t.Errorf("the copy's tag resolves to %q, want %s", d, pushed.Digest)
	}
	assertSameIndex(t, to.Registry, "mirror/web", idx)
}

func TestARegistryThatAsksForABearerToken(t *testing.T) {
	ctx := context.Background()
	reg := newRegistry(t, bearer)
	entries := layout(t, twoPlatforms(t))

	anonymous := reg.Registry
	if _, err := Push(ctx, anonymous, "shop/web", "v1.0.0", stream(t, entries)); err == nil || errors.Is(err, ErrInvalid) {
		t.Fatalf("an anonymous push: err %v, want the registry's refusal", err)
	}
	reg.Username, reg.Password = "user", "secret"
	pushed := must(Push(ctx, reg.Registry, "shop/web", "v1.0.0", stream(t, entries)))
	if d := must(Resolve(ctx, reg.Registry, "shop/web", "v1.0.0")); d != pushed.Digest {
		t.Errorf("v1.0.0 resolves to %q, want %s", d, pushed.Digest)
	}
}
