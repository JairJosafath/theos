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
// same digest. A tag also names the copy; "" leaves it untagged.
func Copy(ctx context.Context, from Registry, fromRepository, digest string, to Registry, toRepository, tag string) error {
	src, err := from.repository(fromRepository)
	if err != nil {
		return err
	}
	dst, err := to.repository(toRepository)
	if err != nil {
		return err
	}
	desc, err := remote.Get(src.Digest(digest), from.options(ctx)...)
	if err != nil {
		return fmt.Errorf("read %s@%s: %w", src, digest, err)
	}
	var ref name.Reference = dst.Digest(digest)
	if tag != "" {
		ref = dst.Tag(tag)
	}
	if desc.MediaType.IsIndex() {
		idx, err := desc.ImageIndex()
		if err != nil {
			return err
		}
		err = remote.WriteIndex(ref, idx, to.options(ctx)...)
		if err != nil {
			return fmt.Errorf("write %s: %w", ref, err)
		}
		return nil
	}
	img, err := desc.Image()
	if err != nil {
		return err
	}
	if err := remote.Write(ref, img, to.options(ctx)...); err != nil {
		return fmt.Errorf("write %s: %w", ref, err)
	}
	return nil
}
