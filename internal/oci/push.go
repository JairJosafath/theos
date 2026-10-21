package oci

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"path"
	"strings"
	"sync"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// MaxManifestSize is the largest manifest an archive may hold: the limit
// registries put on a manifest upload.
const MaxManifestSize = 4 << 20

// attestationType is the annotation BuildKit puts on an index's attestation
// manifests; they are no platform.
const attestationType = "attestation-manifest"

// Push reads an OCI image layout tar, as BuildKit's --output type=oci writes
// it, once from front to back, and pushes it to repository under tag.
//
// Each blob is uploaded as its entry comes, skipped when the registry has
// it, its content checked against its digest; a blob larger than
// MaxManifestSize is streamed, never held whole. Manifests are kept until
// the archive's end, when index.json has named the root: then every
// manifest the root reaches is put by digest, children first, and the root
// under tag. An archive whose content is wrong is an error wrapping
// ErrInvalid, and nothing is tagged.
//
// A tag is never moved: when it names the root already no manifest is put
// again, and when it names another digest Push puts none and returns an
// error wrapping ErrTagTaken, with the archive's Image.
func Push(ctx context.Context, r Registry, repository, tag string, archive io.Reader) (Image, error) {
	repo, err := r.repository(repository)
	if err != nil {
		return Image{}, err
	}
	pusher, err := remote.NewPusher(r.options(ctx)...)
	if err != nil {
		return Image{}, err
	}
	p := &push{
		ctx:       ctx,
		repo:      repo,
		pusher:    pusher,
		sizes:     map[v1.Hash]int64{},
		manifests: map[v1.Hash]manifest{},
		pending:   map[v1.Hash][]byte{},
		platforms: map[v1.Hash]*v1.Platform{},
	}
	if err := p.read(archive); err != nil {
		return Image{}, err
	}
	root, err := p.root()
	if err != nil {
		return Image{}, err
	}
	var order []v1.Hash
	if err := p.walk(root, map[v1.Hash]bool{}, &order); err != nil {
		return Image{}, err
	}
	img := Image{Digest: root.Digest.String(), Platforms: p.report(root)}
	held, err := Resolve(ctx, r, repository, tag)
	switch {
	case err != nil:
		return Image{}, err
	case held == img.Digest:
		return img, nil
	case held != "":
		return img, fmt.Errorf("%w: %s:%s names %s, not %s", ErrTagTaken, repo, tag, held, img.Digest)
	}
	// The root is last in order; it goes up once, under tag.
	for _, h := range order[:len(order)-1] {
		if err := p.put(repo.Digest(h.String()), h); err != nil {
			return Image{}, err
		}
	}
	if err := p.put(repo.Tag(tag), root.Digest); err != nil {
		return Image{}, err
	}
	return img, nil
}

// manifest is a manifest or index held until the archive's end.
type manifest struct {
	raw       []byte
	mediaType types.MediaType
}

func (m manifest) RawManifest() ([]byte, error)        { return m.raw, nil }
func (m manifest) MediaType() (types.MediaType, error) { return m.mediaType, nil }

type push struct {
	ctx    context.Context
	repo   name.Repository
	pusher *remote.Pusher

	index []byte
	// sizes holds every blob the archive has, by digest.
	sizes map[v1.Hash]int64
	// manifests are the blobs that read as a manifest or index.
	manifests map[v1.Hash]manifest
	// pending are manifest-looking blobs not uploaded as blobs; a
	// descriptor that names one as a blob uploads it.
	pending map[v1.Hash][]byte
	// platforms are the platforms image configs name.
	platforms map[v1.Hash]*v1.Platform
}

func (p *push) read(archive io.Reader) error {
	tr := tar.NewReader(archive)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return readErr("read the archive", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		entry := strings.TrimPrefix(path.Clean(hdr.Name), "./")
		switch {
		case entry == "index.json":
			if p.index != nil {
				return invalid("two index.json entries")
			}
			if hdr.Size > MaxManifestSize {
				return invalid("index.json is %d bytes, over %d", hdr.Size, MaxManifestSize)
			}
			if p.index, err = io.ReadAll(tr); err != nil {
				return readErr("read index.json", err)
			}
		case strings.HasPrefix(entry, "blobs/"):
			if err := p.blob(entry, hdr.Size, tr); err != nil {
				return err
			}
		}
	}
}

// blob takes one blobs/<algorithm>/<hex> entry.
func (p *push) blob(entry string, size int64, r io.Reader) error {
	parts := strings.Split(entry, "/")
	if len(parts) != 3 {
		return invalid("%s is not blobs/<algorithm>/<hex>", entry)
	}
	h, err := v1.NewHash(parts[1] + ":" + parts[2])
	if err != nil || h.Algorithm != "sha256" {
		return invalid("%s is not a sha256 blob", entry)
	}
	if _, ok := p.sizes[h]; ok {
		return invalid("%s twice", entry)
	}
	p.sizes[h] = size
	vr := newVerifier(r, h)
	if size > MaxManifestSize {
		err := p.pusher.Upload(p.ctx, p.repo, &streamLayer{digest: h, size: size, r: vr})
		// The transport may still read the body after Upload returns;
		// take it back before looking at what it read.
		rest := vr.take()
		if vr.err != nil {
			return vr.err
		}
		if err != nil {
			return fmt.Errorf("upload %s: %w", h, err)
		}
		// A blob the registry has is skipped, never read; reading it
		// still checks its digest.
		if _, err := io.Copy(io.Discard, rest); err != nil {
			return err
		}
		return nil
	}
	b, err := io.ReadAll(vr)
	if err != nil {
		return err
	}
	if m, ok := asManifest(b); ok {
		p.manifests[h] = m
		p.pending[h] = b
		return nil
	}
	if pl := asConfig(b); pl != nil {
		p.platforms[h] = pl
	}
	return p.upload(h, b)
}

func (p *push) upload(h v1.Hash, b []byte) error {
	l := &streamLayer{digest: h, size: int64(len(b)), b: b}
	if err := p.pusher.Upload(p.ctx, p.repo, l); err != nil {
		return fmt.Errorf("upload %s: %w", h, err)
	}
	return nil
}

// root is the one manifest index.json names.
func (p *push) root() (v1.Descriptor, error) {
	if p.index == nil {
		return v1.Descriptor{}, invalid("no index.json")
	}
	var idx v1.IndexManifest
	if err := json.Unmarshal(p.index, &idx); err != nil {
		return v1.Descriptor{}, invalid("index.json: %v", err)
	}
	// BuildKit names one root once per image name it was given.
	roots := map[v1.Hash]bool{}
	for _, d := range idx.Manifests {
		roots[d.Digest] = true
	}
	if len(roots) != 1 {
		return v1.Descriptor{}, invalid("index.json names %d manifests, not one root", len(roots))
	}
	root := idx.Manifests[0]
	if !root.MediaType.IsIndex() && !root.MediaType.IsImage() {
		return v1.Descriptor{}, invalid("index.json names a %q, not a manifest or index", root.MediaType)
	}
	return root, nil
}

// walk checks that the archive has everything d reaches, uploads manifests'
// blobs held back, and appends the manifests below d, then d, to order.
func (p *push) walk(d v1.Descriptor, seen map[v1.Hash]bool, order *[]v1.Hash) error {
	if seen[d.Digest] {
		return nil
	}
	seen[d.Digest] = true
	size, ok := p.sizes[d.Digest]
	if !ok {
		return invalid("%s names %s, which the archive lacks", d.MediaType, d.Digest)
	}
	if size != d.Size {
		return invalid("%s is %d bytes, its descriptor says %d", d.Digest, size, d.Size)
	}
	if !d.MediaType.IsIndex() && !d.MediaType.IsImage() {
		if b, ok := p.pending[d.Digest]; ok {
			delete(p.pending, d.Digest)
			return p.upload(d.Digest, b)
		}
		return nil
	}
	m, ok := p.manifests[d.Digest]
	if !ok {
		if size > MaxManifestSize {
			return invalid("manifest %s is %d bytes, over %d", d.Digest, size, MaxManifestSize)
		}
		return invalid("%s is not a manifest", d.Digest)
	}
	var own struct {
		MediaType types.MediaType `json:"mediaType"`
	}
	if err := json.Unmarshal(m.raw, &own); err == nil && own.MediaType != "" && own.MediaType != d.MediaType {
		return invalid("%s is a %q, its descriptor says %q", d.Digest, own.MediaType, d.MediaType)
	}
	var children []v1.Descriptor
	if d.MediaType.IsIndex() {
		idx, err := v1.ParseIndexManifest(bytes.NewReader(m.raw))
		if err != nil {
			return invalid("index %s: %v", d.Digest, err)
		}
		children = idx.Manifests
	} else {
		mf, err := v1.ParseManifest(bytes.NewReader(m.raw))
		if err != nil {
			return invalid("manifest %s: %v", d.Digest, err)
		}
		children = append([]v1.Descriptor{mf.Config}, mf.Layers...)
	}
	for _, c := range children {
		if err := p.walk(c, seen, order); err != nil {
			return err
		}
	}
	m.mediaType = d.MediaType
	p.manifests[d.Digest] = m
	*order = append(*order, d.Digest)
	return nil
}

func (p *push) put(ref name.Reference, h v1.Hash) error {
	if err := p.pusher.Put(p.ctx, ref, p.manifests[h]); err != nil {
		return fmt.Errorf("put %s: %w", ref, err)
	}
	return nil
}

// report lists the platforms the root runs on: an index's manifests but
// its attestations, or a lone manifest's config.
func (p *push) report(root v1.Descriptor) []string {
	var out []string
	add := func(pl *v1.Platform) {
		if pl != nil && pl.OS != "" && pl.OS != "unknown" {
			out = append(out, pl.String())
		}
	}
	m := p.manifests[root.Digest]
	if root.MediaType.IsIndex() {
		idx, _ := v1.ParseIndexManifest(bytes.NewReader(m.raw))
		for _, c := range idx.Manifests {
			if c.Annotations["vnd.docker.reference.type"] == attestationType {
				continue
			}
			pl := c.Platform
			if pl == nil {
				pl = p.configPlatform(c.Digest)
			}
			add(pl)
		}
		return out
	}
	pl := root.Platform
	if pl == nil {
		pl = p.configPlatform(root.Digest)
	}
	add(pl)
	return out
}

func (p *push) configPlatform(h v1.Hash) *v1.Platform {
	mf, err := v1.ParseManifest(bytes.NewReader(p.manifests[h].raw))
	if err != nil {
		return nil
	}
	return p.platforms[mf.Config.Digest]
}

// asManifest reads b as a manifest or index: a JSON object with manifests,
// or with a config and layers. Its media type comes from the descriptor
// that names it.
func asManifest(b []byte) (manifest, bool) {
	var probe struct {
		Manifests json.RawMessage `json:"manifests"`
		Config    json.RawMessage `json:"config"`
		Layers    json.RawMessage `json:"layers"`
	}
	if len(b) == 0 || b[0] != '{' || json.Unmarshal(b, &probe) != nil {
		return manifest{}, false
	}
	if probe.Manifests == nil && (probe.Config == nil || probe.Layers == nil) {
		return manifest{}, false
	}
	return manifest{raw: b}, true
}

// asConfig reads b as an image config's platform, or nil.
func asConfig(b []byte) *v1.Platform {
	if len(b) == 0 || b[0] != '{' {
		return nil
	}
	var pl v1.Platform
	if json.Unmarshal(b, &pl) != nil || pl.OS == "" || pl.Architecture == "" {
		return nil
	}
	return &pl
}

// readErr is an error reading the archive: ErrInvalid when the tar itself
// is malformed, else the reader's own error, which a retry may not see.
func readErr(what string, err error) error {
	if errors.Is(err, tar.ErrHeader) {
		return invalid("%s: %v", what, err)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// verifier hashes what it reads and fails the last read when the content
// isn't its digest.
type verifier struct {
	mu    sync.Mutex
	taken bool
	r     io.Reader
	want  v1.Hash
	h     hash.Hash
	err   error
}

func newVerifier(r io.Reader, want v1.Hash) *verifier {
	return &verifier{r: r, want: want, h: sha256.New()}
}

func (v *verifier) Read(b []byte) (int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.taken {
		return 0, errors.New("the archive's blob stream was taken back")
	}
	return v.read(b)
}

// take ends reads through Read, from any goroutine, and returns a reader of
// the rest for the caller alone.
func (v *verifier) take() io.Reader {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.taken = true
	return readerFunc(v.read)
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(b []byte) (int, error) { return f(b) }

func (v *verifier) read(b []byte) (int, error) {
	n, err := v.r.Read(b)
	v.h.Write(b[:n])
	if errors.Is(err, io.EOF) {
		if got := hex.EncodeToString(v.h.Sum(nil)); got != v.want.Hex {
			v.err = invalid("blob %s has content sha256:%s", v.want, got)
			return n, v.err
		}
	}
	return n, err
}

// streamLayer is one blob from the archive, its digest known: read once
// from r, or as often as asked from b.
type streamLayer struct {
	digest v1.Hash
	size   int64
	r      io.Reader
	b      []byte
	read   bool
}

func (l *streamLayer) Digest() (v1.Hash, error)            { return l.digest, nil }
func (l *streamLayer) DiffID() (v1.Hash, error)            { return l.digest, nil }
func (l *streamLayer) Size() (int64, error)                { return l.size, nil }
func (l *streamLayer) MediaType() (types.MediaType, error) { return types.OCILayer, nil }
func (l *streamLayer) Uncompressed() (io.ReadCloser, error) {
	return l.Compressed()
}

func (l *streamLayer) Compressed() (io.ReadCloser, error) {
	if l.b != nil {
		return io.NopCloser(bytes.NewReader(l.b)), nil
	}
	if l.read {
		return nil, errors.New("the archive's blob stream is read once")
	}
	l.read = true
	return io.NopCloser(l.r), nil
}
