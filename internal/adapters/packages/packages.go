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

// Package packages publishes the package files of a release, npm tarballs,
// wheels and sdists, to an npm registry and a Python index, for the Git host
// adapters: what ports.Registry.PublishReleasePackages does once an adapter
// has read the release. An adapter brings the release's files and its
// credentials; the rest is the same on every host.
//
// It is part of the adapters: it may use net/http, but must not import
// Kubernetes or the api package; see docs/architecture.md.
package packages

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5" //nolint:gosec // PyPI's md5_digest: a checksum, not a signature
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // npm's shasum: a checksum, not a signature
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"html"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/JairJosafath/theos/internal/ports"
)

// maxPackageFile is the largest release file Publish reads. A larger one is no
// package the registries would take, and is left alone.
const maxPackageFile = 512 << 20

// nameField is the field that names a package, in an npm manifest and in
// the PyPI upload form; repositoryField is the field of an npm manifest
// that names its repository.
const (
	nameField       = "name"
	repositoryField = "repository"
)

// Publisher publishes the package files of a release for one adapter, which
// sets every field.
type Publisher struct {
	// Name starts the errors, as an adapter's own do: the adapter's name.
	Name string
	// HTTP sends the registry calls; an upload runs without its timeout,
	// bounded by its context.
	HTTP *http.Client
	// Open opens the content of a release's file: an adapter's
	// OpenReleaseAsset (ports.GitHost). Publish closes the content, and does
	// not read the size.
	Open func(ctx context.Context, repo ports.Repository, asset ports.ReleaseAsset) (io.ReadCloser, int64, error)
	// Sign adds the credential to req, a request to target, the npm registry
	// or the Python index of ecosystem, ports.EcosystemNpm or
	// ports.EcosystemPyPI. It adds none where the adapter has none for that
	// address. target is the address as the repository spells it; req.URL is
	// the same, but for an empty port, which net/http drops from its host.
	Sign func(req *http.Request, target *url.URL, ecosystem string)
	// LinkNpm names the repository in the document an npm package is
	// published with when its manifest names none: a registry that files a
	// package under the repository its manifest names links it by that.
	// Unset, a manifest is published as it is.
	LinkNpm bool
}

// Publish publishes each package file among assets, the files of repo's
// release of tag, to the registries repo names: an npm tarball to
// repo.NpmRegistry, a wheel and an sdist to repo.PyPIUpload. It recognises
// each file by its content (a gzipped tar holding package/package.json or
// <name>-<version>/PKG-INFO, a .whl holding *.dist-info/METADATA), reads its
// name and version, and publishes it under those, at the tag's version. The
// outcomes are those ports.Registry.PublishReleasePackages documents: no
// package file among assets, an npm name outside repo.NpmScope, a version
// that isn't the tag's and a version the registry has already are outcomes,
// not errors; a repeat publishes nothing.
//
// Each file is downloaded to a temporary file, hashed on the way, and
// streamed from there to the registry; it is never held whole in memory.
func (p Publisher) Publish(ctx context.Context, repo ports.Repository, tag string, assets []ports.ReleaseAsset) (
	ports.ReleasePackages, error) {
	out := ports.ReleasePackages{Tag: tag}
	for _, asset := range assets {
		pkg, err := p.publishAsset(ctx, repo, asset, tag)
		if err != nil {
			return out, err
		}
		if pkg != nil {
			out.Packages = append(out.Packages, *pkg)
		}
	}
	return out, nil
}

// publishAsset publishes one release file if it is a package; nil when it is
// none, or too large to be one.
func (p Publisher) publishAsset(ctx context.Context, repo ports.Repository, asset ports.ReleaseAsset, tag string) (*ports.Package, error) {
	// A .whl is a zip the registry files by its name; any other zip, such as
	// a Lambda bundle, is never read. Everything else is a package only when
	// its content says so, so it is read, unless it can't be one.
	isWheel := strings.HasSuffix(asset.Name, ".whl")
	if asset.Size > maxPackageFile || (!isWheel && strings.HasSuffix(asset.Name, ".zip")) {
		return nil, nil
	}
	file, sums, err := p.downloadAsset(ctx, repo, asset, !isWheel)
	if err != nil || file == nil {
		return nil, err
	}
	defer removeTemp(file)
	var pkg *ports.Package
	var meta packageMeta
	if isWheel {
		pkg, meta, err = readWheel(file, asset)
	} else {
		pkg, meta, err = readTarball(file, asset)
	}
	if err != nil || pkg == nil {
		return pkg, err
	}
	switch pkg.Ecosystem {
	case ports.EcosystemNpm:
		if !strings.HasPrefix(pkg.Name, repo.NpmScope+"/") {
			pkg.Outcome, pkg.Reason = ports.PackageUnscoped, fmt.Sprintf("%s is not under the scope %s", pkg.Name, repo.NpmScope)
			return pkg, nil
		}
		if want := strings.TrimPrefix(tag, "v"); pkg.Version != want {
			pkg.Outcome, pkg.Reason = ports.PackageInvalid, fmt.Sprintf("version %s is not the tag's, %s", pkg.Version, want)
			return pkg, nil
		}
		return p.publishNpm(ctx, repo, pkg, meta, file, sums)
	default:
		if repo.PyPIIndex == "" || repo.PyPIUpload == "" {
			pkg.Outcome = ports.PackageInvalid
			pkg.Reason = "there is no Python package index to publish it to: the host has none of its own, " +
				"and the GitProvider's spec.packages.pypi names none"
			return pkg, nil
		}
		want, ok := normalizePEP440(tag)
		if !ok {
			pkg.Outcome, pkg.Reason = ports.PackageInvalid, fmt.Sprintf("tag %s is not a PEP 440 version", tag)
			return pkg, nil
		}
		if got, ok := normalizePEP440(pkg.Version); !ok || got != want {
			pkg.Outcome, pkg.Reason = ports.PackageInvalid, fmt.Sprintf("version %s is not the tag's, %s", pkg.Version, want)
			return pkg, nil
		}
		return p.publishPyPI(ctx, repo, pkg, meta, file, sums)
	}
}

// packageMeta is what a package's own metadata says, beyond its name and
// version: the fields the registry's upload wants.
type packageMeta struct {
	// manifest is an npm package's package.json, decoded.
	manifest map[string]json.RawMessage
	// metadataVersion, summary and requiresPython are a Python package's
	// Metadata-Version, Summary and Requires-Python.
	metadataVersion, summary, requiresPython string
}

// digests are the hashes of a downloaded file, which the registries check.
type digests struct {
	md5, sha1, sha256, sha512 []byte
}

// downloadAsset saves asset to a temporary file, hashing it on the way, and
// returns it open, positioned at its start; the caller closes and removes it.
// With gzipOnly, a file that doesn't start as gzip does is not saved: nil.
func (p Publisher) downloadAsset(ctx context.Context, repo ports.Repository, asset ports.ReleaseAsset, gzipOnly bool) (
	*os.File, digests, error) {
	var sums digests
	body, _, err := p.Open(ctx, repo, asset)
	if err != nil {
		return nil, sums, err
	}
	defer func() { _ = body.Close() }()
	src := bufio.NewReader(body)
	if gzipOnly {
		if magic, err := src.Peek(2); err != nil || magic[0] != 0x1f || magic[1] != 0x8b {
			return nil, sums, nil
		}
	}
	f, err := os.CreateTemp("", "c4-package-*")
	if err != nil {
		return nil, sums, err
	}
	hashes := []hash.Hash{md5.New(), sha1.New(), sha256.New(), sha512.New()} //nolint:gosec // the registries' checksums
	w := []io.Writer{f}
	for _, h := range hashes {
		w = append(w, h)
	}
	if _, err := io.Copy(io.MultiWriter(w...), src); err != nil {
		removeTemp(f)
		return nil, sums, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		removeTemp(f)
		return nil, sums, err
	}
	sums = digests{md5: hashes[0].Sum(nil), sha1: hashes[1].Sum(nil), sha256: hashes[2].Sum(nil), sha512: hashes[3].Sum(nil)}
	return f, sums, nil
}

// readTarball recognises a gzipped tar as an npm tarball or an sdist by its
// content and reads its metadata; nil when it is neither. A gzipped tar that
// is damaged is no package either.
func readTarball(file *os.File, asset ports.ReleaseAsset) (*ports.Package, packageMeta, error) {
	var meta packageMeta
	gz, err := gzip.NewReader(file)
	if err != nil {
		return nil, meta, nil
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err != nil {
			// A tar that ends or breaks before a package's metadata is none.
			return nil, meta, nil
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA { //nolint:staticcheck // TypeRegA is what old tars write.
			continue
		}
		name := strings.TrimPrefix(h.Name, "./")
		top, rest, ok := strings.Cut(name, "/")
		if !ok || top == "" {
			continue
		}
		switch {
		case name == "package/package.json":
			data, err := io.ReadAll(io.LimitReader(tr, 1<<20))
			if err != nil {
				return nil, meta, nil
			}
			return readPackageJSON(data, asset)
		case rest == "PKG-INFO":
			data, err := io.ReadAll(io.LimitReader(tr, 1<<20))
			if err != nil {
				return nil, meta, nil
			}
			// An sdist's PKG-INFO sits in <name>-<version>/; one elsewhere is
			// some other tar's file.
			if pkg, info, err := readPKGINFO(data, asset); pkg == nil || sdistDir(top, pkg.Name, pkg.Version) {
				return pkg, info, err
			}
		}
	}
}

// readPackageJSON reads an npm package's name and version from its package.json.
func readPackageJSON(data []byte, asset ports.ReleaseAsset) (*ports.Package, packageMeta, error) {
	var meta packageMeta
	if err := json.Unmarshal(data, &meta.manifest); err != nil {
		return nil, meta, nil
	}
	var name, version string
	_ = json.Unmarshal(meta.manifest[nameField], &name)
	_ = json.Unmarshal(meta.manifest["version"], &version)
	if name == "" || version == "" {
		return nil, meta, nil
	}
	// npm publishes a repository given as a string as the object it stands for.
	var repository string
	if json.Unmarshal(meta.manifest[repositoryField], &repository) == nil && repository != "" {
		meta.manifest[repositoryField], _ = json.Marshal(map[string]string{"type": "git", "url": repository})
	}
	return &ports.Package{Ecosystem: ports.EcosystemNpm, Name: name, Version: version, File: asset.Name}, meta, nil
}

// readWheel recognises a wheel by its *.dist-info/METADATA and reads it; nil
// when the zip has none, or isn't one.
func readWheel(file *os.File, asset ports.ReleaseAsset) (*ports.Package, packageMeta, error) {
	var meta packageMeta
	info, err := file.Stat()
	if err != nil {
		return nil, meta, err
	}
	zr, err := zip.NewReader(file, info.Size())
	if err != nil {
		return nil, meta, nil
	}
	for _, f := range zr.File {
		top, rest, ok := strings.Cut(f.Name, "/")
		if !ok || rest != "METADATA" || !strings.HasSuffix(top, ".dist-info") {
			continue
		}
		r, err := f.Open()
		if err != nil {
			return nil, meta, nil
		}
		data, err := io.ReadAll(io.LimitReader(r, 1<<20))
		_ = r.Close()
		if err != nil {
			return nil, meta, nil
		}
		return readPKGINFO(data, asset)
	}
	return nil, meta, nil
}

// sdistDir reports whether dir is the top directory of name's sdist at
// version: <name>-<version>, the name in any of its spellings (shop_api or
// shop-api) and the version in any PEP 440 form.
func sdistDir(dir, name, version string) bool {
	spell := strings.NewReplacer("_", "-", ".", "-")
	prefix := strings.ToLower(spell.Replace(name)) + "-"
	if len(dir) <= len(prefix) || strings.ToLower(spell.Replace(dir[:len(prefix)])) != prefix {
		return false
	}
	got, ok := normalizePEP440(dir[len(prefix):])
	want, wantOK := normalizePEP440(version)
	return ok && wantOK && got == want
}

// readPKGINFO reads a Python package's core metadata, a PKG-INFO or METADATA
// file: headers, then a blank line and the long description.
func readPKGINFO(data []byte, asset ports.ReleaseAsset) (*ports.Package, packageMeta, error) {
	var meta packageMeta
	headers, err := textproto.NewReader(bufio.NewReader(bytes.NewReader(append(data, "\n\n"...)))).ReadMIMEHeader()
	if err != nil && len(headers) == 0 {
		return nil, meta, nil
	}
	name, version := headers.Get("Name"), headers.Get("Version")
	if name == "" || version == "" {
		return nil, meta, nil
	}
	meta.metadataVersion = headers.Get("Metadata-Version")
	meta.summary = headers.Get("Summary")
	meta.requiresPython = headers.Get("Requires-Python")
	return &ports.Package{Ecosystem: ports.EcosystemPyPI, Name: name, Version: version, File: asset.Name}, meta, nil
}

// registryRequest builds a request to target, a registry URL of ecosystem,
// which p.Sign signs.
func (p Publisher) registryRequest(ctx context.Context, method, target, ecosystem string, body io.Reader) (
	*http.Request, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, fmt.Errorf("%s: registry URL %q: %w", p.Name, target, err)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	p.Sign(req, u, ecosystem)
	return req, nil
}

// registryGet makes one GET of a registry and returns its status and body.
func (p Publisher) registryGet(ctx context.Context, target, ecosystem string) (int, []byte, error) {
	req, err := p.registryRequest(ctx, http.MethodGet, target, ecosystem, nil)
	if err != nil {
		return 0, nil, err
	}
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%s: GET %s: %w", p.Name, target, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<24))
	if err != nil {
		return 0, nil, fmt.Errorf("%s: GET %s: %w", p.Name, target, err)
	}
	return resp.StatusCode, data, nil
}

// upload sends body, of size bytes, to target and tells Published on 201 and
// Exists on 409.
func (p Publisher) upload(ctx context.Context, method, target, ecosystem, contentType string, body io.Reader,
	size int64, pkg *ports.Package) error {
	req, err := p.registryRequest(ctx, method, target, ecosystem, io.NopCloser(body))
	if err != nil {
		return err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", contentType)
	resp, err := p.uploadClient().Do(req)
	if err != nil {
		return fmt.Errorf("%s: uploading %s: %w", p.Name, pkg.File, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK:
		pkg.Outcome = ports.PackagePublished
	case http.StatusConflict:
		pkg.Outcome = ports.PackageExists
	default:
		return fmt.Errorf("%s: uploading %s, %s %s, to %s failed (HTTP %d): %s", p.Name, pkg.File, pkg.Name, pkg.Version,
			target, resp.StatusCode, apiMessage(data))
	}
	return nil
}

// uploadClient is p.HTTP without its timeout for one call: ctx bounds an
// upload, which may take longer.
func (p Publisher) uploadClient() *http.Client {
	client := *p.HTTP
	client.Timeout = 0
	return &client
}

// apiMessage pulls the message out of a registry's error body: its JSON
// message field, else the body's text.
func apiMessage(data []byte) string {
	var body struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &body) == nil && body.Message != "" {
		return body.Message
	}
	return strings.TrimSpace(string(data))
}

// removeTemp closes and removes a temporary file.
func removeTemp(f *os.File) {
	_ = f.Close()
	_ = os.Remove(f.Name())
}

// orDefault is value, or def when value is "".
func orDefault(value, def string) string {
	if value == "" {
		return def
	}
	return value
}

// publishNpm publishes an npm tarball to repo.NpmRegistry as npm publish
// does: a PUT of the package document with the tarball as its attachment,
// base64 streamed from file.
func (p Publisher) publishNpm(ctx context.Context, repo ports.Repository, pkg *ports.Package, meta packageMeta, file *os.File,
	sums digests) (*ports.Package, error) {
	target := repo.NpmRegistry + url.PathEscape(pkg.Name)
	status, data, err := p.registryGet(ctx, target, ports.EcosystemNpm)
	if err != nil {
		return pkg, err
	}
	switch status {
	case http.StatusNotFound:
	case http.StatusOK:
		var doc struct {
			Versions map[string]json.RawMessage `json:"versions"`
		}
		if json.Unmarshal(data, &doc) == nil {
			if _, ok := doc.Versions[pkg.Version]; ok {
				pkg.Outcome = ports.PackageExists
				return pkg, nil
			}
		}
	default:
		return pkg, fmt.Errorf("%s: reading %s in the npm registry %s failed (HTTP %d): %s", p.Name, pkg.Name,
			repo.NpmRegistry, status, apiMessage(data))
	}

	size, err := rewind(file)
	if err != nil {
		return pkg, err
	}
	bare := pkg.Name[strings.LastIndex(pkg.Name, "/")+1:]
	fileName := bare + "-" + pkg.Version + ".tgz"
	version := map[string]json.RawMessage{}
	maps.Copy(version, meta.manifest)
	if _, named := version[repositoryField]; !named && p.LinkNpm {
		version[repositoryField], _ = json.Marshal(map[string]string{"type": "git", "url": "git+" + repo.CloneURL})
	}
	version["_id"], _ = json.Marshal(pkg.Name + "@" + pkg.Version)
	version["dist"], _ = json.Marshal(map[string]string{
		"integrity": "sha512-" + base64.StdEncoding.EncodeToString(sums.sha512),
		"shasum":    hex.EncodeToString(sums.sha1),
		"tarball":   repo.NpmRegistry + pkg.Name + "/-/" + fileName,
	})
	// The document is marshalled whole, with a mark where the tarball goes,
	// and cut at the mark: the tarball is streamed between the two halves, in
	// base64, never held whole. The mark is made for this call, so nothing a
	// manifest holds can be taken for it.
	mark := rand.Text()
	doc, err := json.Marshal(struct {
		ID          string                                `json:"_id"`
		Name        string                                `json:"name"`
		Description json.RawMessage                       `json:"description,omitempty"`
		DistTags    map[string]string                     `json:"dist-tags"`
		Versions    map[string]map[string]json.RawMessage `json:"versions"`
		Attachments map[string]npmAttachment              `json:"_attachments"`
	}{
		ID: pkg.Name, Name: pkg.Name, Description: meta.manifest["description"],
		DistTags:    map[string]string{"latest": pkg.Version},
		Versions:    map[string]map[string]json.RawMessage{pkg.Version: version},
		Attachments: map[string]npmAttachment{fileName: {ContentType: "application/octet-stream", Length: size, Data: mark}},
	})
	if err != nil {
		return pkg, err
	}
	prefix, suffix, cut := bytes.Cut(doc, []byte(mark))
	if !cut {
		return pkg, fmt.Errorf("%s: the publish document of %s has no place for its tarball", p.Name, pkg.Name)
	}
	body := io.MultiReader(bytes.NewReader(prefix), &base64Reader{r: file}, bytes.NewReader(suffix))
	total := int64(len(prefix)) + int64(base64.StdEncoding.EncodedLen(int(size))) + int64(len(suffix))
	err = p.upload(ctx, http.MethodPut, target, ports.EcosystemNpm, "application/json", body, total, pkg)
	return pkg, err
}

// npmAttachment is the tarball in an npm publish document: its content in
// base64.
type npmAttachment struct {
	ContentType string `json:"content_type"`
	Length      int64  `json:"length"`
	Data        string `json:"data"`
}

// rewind puts file, which reading its metadata moved, back at its start, and
// returns its size: the upload streams the whole file.
func rewind(file *os.File) (int64, error) {
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// base64Reader encodes what it reads from r, standard base64, as it goes.
type base64Reader struct {
	r   io.Reader
	in  [3 * 4096]byte
	out []byte
	err error
}

func (b *base64Reader) Read(p []byte) (int, error) {
	if len(b.out) == 0 {
		if b.err != nil {
			return 0, b.err
		}
		// A full chunk is a multiple of 3 bytes, so only the last one pads.
		n, err := io.ReadFull(b.r, b.in[:])
		switch {
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
			b.err = io.EOF
		case err != nil:
			b.err = err
			return 0, err
		}
		if n == 0 {
			return 0, io.EOF
		}
		b.out = make([]byte, base64.StdEncoding.EncodedLen(n))
		base64.StdEncoding.Encode(b.out, b.in[:n])
	}
	n := copy(p, b.out)
	b.out = b.out[n:]
	return n, nil
}

// indexFiles matches the links of a PEP 503 simple index page, whose texts
// are the file names.
var indexFiles = regexp.MustCompile(`(?is)<a\b[^>]*>(.*?)</a>`)

// publishPyPI publishes a wheel or an sdist to repo.PyPIUpload with the form
// twine sends, the file streamed from file. The owner's simple index says
// first whether it has the file already: a wheel and an sdist of one version
// each publish on their own.
func (p Publisher) publishPyPI(ctx context.Context, repo ports.Repository, pkg *ports.Package, meta packageMeta, file *os.File,
	sums digests) (*ports.Package, error) {
	index := strings.TrimSuffix(repo.PyPIIndex, "/") + "/" + normalizeProjectName(pkg.Name) + "/"
	status, data, err := p.registryGet(ctx, index, ports.EcosystemPyPI)
	if err != nil {
		return pkg, err
	}
	switch status {
	case http.StatusNotFound:
	case http.StatusOK:
		for _, m := range indexFiles.FindAllStringSubmatch(string(data), -1) {
			if strings.TrimSpace(html.UnescapeString(m[1])) == pkg.File {
				pkg.Outcome = ports.PackageExists
				return pkg, nil
			}
		}
	default:
		return pkg, fmt.Errorf("%s: reading %s in the Python index %s failed (HTTP %d): %s", p.Name, pkg.Name,
			repo.PyPIIndex, status, apiMessage(data))
	}

	size, err := rewind(file)
	if err != nil {
		return pkg, err
	}
	filetype, pyversion := "sdist", "source"
	if strings.HasSuffix(pkg.File, ".whl") {
		filetype, pyversion = "bdist_wheel", wheelPythonTag(pkg.File)
	}
	// The form's version is the normalised one: what a build backend writes,
	// and all an index whose version matcher is case-sensitive accepts.
	version, _ := normalizePEP440(pkg.Version)
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	for _, field := range [][2]string{
		{":action", "file_upload"}, {"protocol_version", "1"},
		{"metadata_version", orDefault(meta.metadataVersion, "2.1")},
		{nameField, pkg.Name}, {"version", version}, {"filetype", filetype}, {"pyversion", pyversion},
		{"summary", meta.summary}, {"requires_python", meta.requiresPython},
		{"md5_digest", hex.EncodeToString(sums.md5)}, {"sha256_digest", hex.EncodeToString(sums.sha256)},
	} {
		if err := form.WriteField(field[0], field[1]); err != nil {
			return pkg, err
		}
	}
	if _, err := form.CreateFormFile("content", pkg.File); err != nil {
		return pkg, err
	}
	prefix := buf.Len()
	if err := form.Close(); err != nil {
		return pkg, err
	}
	all := buf.Bytes()
	body := io.MultiReader(bytes.NewReader(all[:prefix]), file, bytes.NewReader(all[prefix:]))
	err = p.upload(ctx, http.MethodPost, repo.PyPIUpload, ports.EcosystemPyPI, form.FormDataContentType(), body,
		int64(len(all))+size, pkg)
	return pkg, err
}

// wheelPythonTag is the Python tag of a wheel's file name,
// <name>-<version>(-<build>)?-<python>-<abi>-<platform>.whl, or py3 when the
// name isn't one.
func wheelPythonTag(file string) string {
	parts := strings.Split(strings.TrimSuffix(path.Base(file), ".whl"), "-")
	if len(parts) < 5 {
		return "py3"
	}
	return parts[len(parts)-3]
}
