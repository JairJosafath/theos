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

package packages

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1" //nolint:gosec // npm's shasum: a checksum, not a signature
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/JairJosafath/theos/internal/ports"
)

// TestSdistDir checks which top directory makes a tar's PKG-INFO an sdist's:
// <name>-<version>, in any spelling of the name and form of the version (#1701).
func TestSdistDir(t *testing.T) {
	for _, tt := range []struct {
		dir, name, version string
		want               bool
	}{
		{"shop_api-0.1.0", "shop-api", "0.1.0", true},
		{"shop-api-0.1.0", "shop-api", "0.1.0", true},
		{"Shop.API-0.1.0", "shop_api", "0.1.0", true},
		{"shop_api-1.0.0-RC.1", "shop-api", "1.0.0rc1", true},
		{"shop_api-1.0.0rc1", "shop-api", "1.0.0-RC.1", true},
		{"docs", "shop-api", "0.1.0", false},
		{"shop_api", "shop-api", "0.1.0", false},
		{"shop_api-0.2.0", "shop-api", "0.1.0", false},
		{"other-0.1.0", "shop-api", "0.1.0", false},
		{"shop_api-0.1.0", "shop-api", "not a version", false},
	} {
		if got := sdistDir(tt.dir, tt.name, tt.version); got != tt.want {
			t.Errorf("sdistDir(%q, %q, %q) = %v, want %v", tt.dir, tt.name, tt.version, got, tt.want)
		}
	}
}

// npmPack is the tarball npm pack makes of a package whose package.json is
// manifest.
func npmPack(t *testing.T, manifest string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "package/package.json", Mode: 0o644, Size: int64(len(manifest))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(manifest)); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// published publishes tarball as the one file of a release of v1.0.0, to a
// registry that has no package and takes the upload, and returns the
// document the registry was sent and the registry's address.
func published(t *testing.T, tarball []byte) (document []byte, registry string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		document, _ = io.ReadAll(r.Body)
		if r.ContentLength != int64(len(document)) {
			t.Errorf("the upload said it is %d bytes and was %d", r.ContentLength, len(document))
		}
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)
	publisher := Publisher{Name: "test", HTTP: srv.Client(),
		Open: func(context.Context, ports.Repository, ports.ReleaseAsset) (io.ReadCloser, int64, error) {
			return io.NopCloser(bytes.NewReader(tarball)), int64(len(tarball)), nil
		},
		Sign: func(*http.Request, *url.URL, string) {}}
	repo := ports.Repository{Owner: "acme", Name: "orders", NpmScope: "@acme", NpmRegistry: srv.URL + "/"}
	got, err := publisher.Publish(context.Background(), repo, "v1.0.0",
		[]ports.ReleaseAsset{{Name: "orders.tgz", Size: int64(len(tarball))}})
	if err != nil || len(got.Packages) != 1 || got.Packages[0].Outcome != ports.PackagePublished {
		t.Fatalf("Publish() = %+v, %v; want the package Published", got, err)
	}
	return document, srv.URL + "/"
}

// TestNpmPublishDocument covers #80: the document an npm package is
// published with, byte for byte. It is marshalled whole and the tarball
// streamed into its place, so this is what keeps the two halves a document.
func TestNpmPublishDocument(t *testing.T) {
	tarball := npmPack(t, `{"name":"@acme/orders","version":"1.0.0","description":"Orders"}`)
	document, registry := published(t, tarball)
	sha512sum, sha1sum := sha512.Sum512(tarball), sha1.Sum(tarball) //nolint:gosec // npm's shasum
	want := `{"_id":"@acme/orders","name":"@acme/orders","description":"Orders","dist-tags":{"latest":"1.0.0"},` +
		`"versions":{"1.0.0":{"_id":"@acme/orders@1.0.0","description":"Orders",` +
		`"dist":{"integrity":"sha512-` + base64.StdEncoding.EncodeToString(sha512sum[:]) + `",` +
		`"shasum":"` + hex.EncodeToString(sha1sum[:]) + `","tarball":"` + registry + `@acme/orders/-/orders-1.0.0.tgz"},` +
		`"name":"@acme/orders","version":"1.0.0"}},` +
		`"_attachments":{"orders-1.0.0.tgz":{"content_type":"application/octet-stream",` +
		`"length":` + strconv.Itoa(len(tarball)) + `,"data":"` + base64.StdEncoding.EncodeToString(tarball) + `"}}}`
	if string(document) != want {
		t.Errorf("the publish document is\n%s\nwant\n%s", document, want)
	}
}

// TestNpmPublishDocumentQuotes covers #80: whatever a manifest holds, quotes
// and the document's own field names included, the document stays one JSON
// value whose attachment is the tarball.
func TestNpmPublishDocumentQuotes(t *testing.T) {
	tarball := npmPack(t, `{"name":"@acme/orders","version":"1.0.0",`+
		`"description":"says \"cut here\", then \"data\":\" and \"}}}"}`)
	document, _ := published(t, tarball)
	var doc struct {
		Description string `json:"description"`
		Attachments map[string]struct {
			Length int    `json:"length"`
			Data   string `json:"data"`
		} `json:"_attachments"`
	}
	if err := json.Unmarshal(document, &doc); err != nil {
		t.Fatalf("the publish document is no JSON: %v\n%s", err, document)
	}
	attached, err := base64.StdEncoding.DecodeString(doc.Attachments["orders-1.0.0.tgz"].Data)
	if err != nil || !bytes.Equal(attached, tarball) || doc.Attachments["orders-1.0.0.tgz"].Length != len(tarball) {
		t.Errorf("the document's attachment is not the tarball it was published with: %v", err)
	}
	if want := `says "cut here", then "data":" and "}}}`; doc.Description != want {
		t.Errorf("the document's description is %q, want %q", doc.Description, want)
	}
}
