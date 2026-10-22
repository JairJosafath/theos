// Package oci pushes, resolves and copies container images in OCI
// registries, over go-containerregistry. It knows no Kubernetes and no
// resources: callers pass plain values and get plain values back.
package oci

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

// Registry is one OCI registry and the credential that reaches it.
type Registry struct {
	// Host is the registry's host and port, without a scheme.
	Host string
	// Username and Password log in; both empty is anonymous. A registry
	// that answers with a bearer challenge gets them at its token endpoint.
	Username string
	Password string
	// Insecure speaks plain HTTP. go-containerregistry already does for
	// localhost and 127.0.0.1.
	Insecure bool
	// Transport carries every request; nil is http.DefaultTransport. A
	// caller whose registry names a token endpoint it can't reach rewrites
	// the address here.
	Transport http.RoundTripper
}

// Image is what a registry holds under a tag: the root manifest's digest
// and the platforms it runs on, attestations left out.
type Image struct {
	Digest    string
	Platforms []string
}

// ErrInvalid wraps every refusal of an archive's content, as opposed to a
// registry that can't be reached or refuses the push.
var ErrInvalid = errors.New("invalid image archive")

// ErrTagTaken is Push's and Copy's error for a tag that names another
// digest: a tag, once pushed, names one image for good.
var ErrTagTaken = errors.New("the tag names another image")

// ErrSource wraps Copy's failure to read the image it copies, as opposed to
// a failure to write the copy.
var ErrSource = errors.New("the source registry")

// ErrReadRefused wraps Copy's refusal to write a tag it may not read: the
// destination answered the tag's HEAD with 401 or 403, as a credential that
// pushes but doesn't read does (AWS's minimal ECR push policy, a token
// scoped to push alone). A tag is never moved (#1770), so the copy needs
// the read and nothing is written. The error is a refusal too (Refused),
// never transient (#1877).
var ErrReadRefused = errors.New("the registry credential may not read tags")

// readRefused is ErrReadRefused for one tag, keeping the registry's refusal.
type readRefused struct {
	ref   string
	cause *transport.Error
}

func (e *readRefused) Error() string {
	return fmt.Sprintf("%s: %s answered %d %s to the tag's read; it must read tags as well as push, as a tag "+
		"is never moved", ErrReadRefused, e.ref, e.cause.StatusCode, http.StatusText(e.cause.StatusCode))
}

func (e *readRefused) Unwrap() []error { return []error{ErrReadRefused, e.cause} }

// Refused says whether err is a registry refusing the credential: 401 or 403.
func Refused(err error) bool {
	var terr *transport.Error
	return errors.As(err, &terr) &&
		(terr.StatusCode == http.StatusUnauthorized || terr.StatusCode == http.StatusForbidden)
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func (r Registry) repository(repo string) (name.Repository, error) {
	var opts []name.Option
	if r.Insecure {
		opts = append(opts, name.Insecure)
	}
	return name.NewRepository(r.Host+"/"+repo, opts...)
}

func (r Registry) options(ctx context.Context) []remote.Option {
	auth := authn.Anonymous
	if r.Username != "" || r.Password != "" {
		auth = &authn.Basic{Username: r.Username, Password: r.Password}
	}
	opts := []remote.Option{remote.WithContext(ctx), remote.WithAuth(auth)}
	if r.Transport != nil {
		opts = append(opts, remote.WithTransport(r.Transport))
	}
	return opts
}

// Resolve returns the digest repository's tag names, or "" when the tag
// isn't there.
func Resolve(ctx context.Context, r Registry, repository, tag string) (string, error) {
	repo, err := r.repository(repository)
	if err != nil {
		return "", err
	}
	desc, err := remote.Head(repo.Tag(tag), r.options(ctx)...)
	if notFound(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("resolve %s:%s: %w", repo, tag, err)
	}
	return desc.Digest.String(), nil
}

func notFound(err error) bool {
	var terr *transport.Error
	return errors.As(err, &terr) && terr.StatusCode == http.StatusNotFound
}

// Copy copies the index or manifest at digest from one registry to another,
// with every manifest it names, attestations included, so the copy has the
// same digest. A tag also names the copy; "" leaves it untagged. A tag is
// never moved: when it names digest already nothing is written, so a
// registry whose tags are immutable takes a repeat, and when it names
// another digest Copy writes nothing and returns an error wrapping
// ErrTagTaken (#1770). A destination that refuses the tag's read refuses
// the credential: an error wrapping ErrReadRefused, nothing written (#1877).
func Copy(ctx context.Context, from Registry, fromRepository, digest string, to Registry, toRepository, tag string) error {
	src, err := from.repository(fromRepository)
	if err != nil {
		return err
	}
	dst, err := to.repository(toRepository)
	if err != nil {
		return err
	}
	if tag != "" {
		held, err := Resolve(ctx, to, toRepository, tag)
		var terr *transport.Error
		switch {
		case Refused(err) && errors.As(err, &terr):
			return &readRefused{ref: dst.Tag(tag).String(), cause: terr}
		case err != nil:
			return err
		case held == digest:
			return nil
		case held != "":
			return fmt.Errorf("%w: %s:%s names %s, not %s", ErrTagTaken, dst, tag, held, digest)
		}
	}
	desc, err := remote.Get(src.Digest(digest), from.options(ctx)...)
	if err != nil {
		return fmt.Errorf("%w: read %s@%s: %w", ErrSource, src, digest, err)
	}
	var ref name.Reference = dst.Digest(digest)
	if tag != "" {
		ref = dst.Tag(tag)
	}
	if desc.MediaType.IsIndex() {
		idx, err := desc.ImageIndex()
		if err != nil {
			return fmt.Errorf("%w: read %s@%s: %w", ErrSource, src, digest, err)
		}
		err = remote.WriteIndex(ref, idx, to.options(ctx)...)
		if err != nil {
			return fmt.Errorf("write %s: %w", ref, err)
		}
		return nil
	}
	img, err := desc.Image()
	if err != nil {
		return fmt.Errorf("%w: read %s@%s: %w", ErrSource, src, digest, err)
	}
	if err := remote.Write(ref, img, to.options(ctx)...); err != nil {
		return fmt.Errorf("write %s: %w", ref, err)
	}
	return nil
}
