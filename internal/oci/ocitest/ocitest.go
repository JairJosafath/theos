// Package ocitest makes OCI image layout archives for tests, as BuildKit's
// --output type=oci writes them.
package ocitest

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/static"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// linux is the OS of both images.
const linux = "linux"

// Platforms are the platforms TwoPlatforms builds for.
var Platforms = []string{"linux/amd64", "linux/arm64"}

// TB is what TwoPlatforms needs of a test: a *testing.T or Ginkgo's
// GinkgoT().
type TB interface {
	Helper()
	Fatal(args ...any)
}

// TwoPlatforms is the tar of an image index for linux/amd64 and
// linux/arm64, each with a random layer, and an attestation manifest for
// amd64 as BuildKit attaches one, and the index's digest. Each call makes
// another image.
func TwoPlatforms(t TB) ([]byte, string) {
	t.Helper()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	image := func(arch string) v1.Image {
		img := mutate.ConfigMediaType(mutate.MediaType(empty.Image, types.OCIManifestSchema1), types.OCIConfigJSON)
		layer, err := random.Layer(1024, types.OCILayer)
		must(err)
		img, err = mutate.AppendLayers(img, layer)
		must(err)
		cf, err := img.ConfigFile()
		must(err)
		cf = cf.DeepCopy()
		cf.OS, cf.Architecture = linux, arch
		img, err = mutate.ConfigFile(img, cf)
		must(err)
		return img
	}
	amd, arm := image("amd64"), image("arm64")
	amdDigest, err := amd.Digest()
	must(err)
	statement := mutate.ConfigMediaType(mutate.MediaType(empty.Image, types.OCIManifestSchema1), types.OCIConfigJSON)
	statement, err = mutate.AppendLayers(statement,
		static.NewLayer([]byte(`{"_type":"https://in-toto.io/Statement/v0.1"}`), "application/vnd.in-toto+json"))
	must(err)
	idx := mutate.AppendManifests(mutate.IndexMediaType(empty.Index, types.OCIImageIndex),
		mutate.IndexAddendum{Add: amd, Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: linux, Architecture: "amd64"}}},
		mutate.IndexAddendum{Add: arm, Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: linux, Architecture: "arm64"}}},
		mutate.IndexAddendum{Add: statement, Descriptor: v1.Descriptor{
			Platform: &v1.Platform{OS: "unknown", Architecture: "unknown"},
			Annotations: map[string]string{
				"vnd.docker.reference.type":   "attestation-manifest",
				"vnd.docker.reference.digest": amdDigest.String(),
			},
		}})

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	file := func(name string, data []byte) {
		must(tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}))
		_, err := tw.Write(data)
		must(err)
	}
	blob := func(h v1.Hash, data []byte) { file("blobs/"+h.Algorithm+"/"+h.Hex, data) }
	file("oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`))
	digest, err := idx.Digest()
	must(err)
	rawIndex, err := idx.RawManifest()
	must(err)
	blob(digest, rawIndex)
	manifest, err := idx.IndexManifest()
	must(err)
	for _, d := range manifest.Manifests {
		img, err := idx.Image(d.Digest)
		must(err)
		raw, err := img.RawManifest()
		must(err)
		blob(d.Digest, raw)
		config, err := img.ConfigName()
		must(err)
		raw, err = img.RawConfigFile()
		must(err)
		blob(config, raw)
		layers, err := img.Layers()
		must(err)
		for _, l := range layers {
			h, err := l.Digest()
			must(err)
			rc, err := l.Compressed()
			must(err)
			data, err := io.ReadAll(rc)
			must(err)
			blob(h, data)
		}
	}
	index, err := json.Marshal(v1.IndexManifest{SchemaVersion: 2, MediaType: types.OCIImageIndex,
		Manifests: []v1.Descriptor{{MediaType: types.OCIImageIndex, Digest: digest, Size: int64(len(rawIndex))}}})
	must(err)
	file("index.json", index)
	must(tw.Close())
	return buf.Bytes(), digest.String()
}
