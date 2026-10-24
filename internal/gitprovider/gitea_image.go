package gitprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/oci"
	"github.com/JairJosafath/c4factory/internal/ports"
)

// PublishImage pushes the image archive of repo's release of tag, its one
// file named *.oci.tar, to the owner's container registry on the Git host
// under tag, with the provider's credential, and links the image to repo.
// A tag is pushed once and never replaced; no archive, a wrong one and a
// tag that names another image are outcomes, not errors, and a repeat
// pushes nothing new. The archive is streamed from the release, never held
// whole.
func (g *gitea) PublishImage(ctx context.Context, repo Repository, tag string) (Image, error) {
	out := Image{Repository: g.base.Host + "/" + imagePath(repo), Tag: tag}
	rel, err := g.GetRelease(ctx, repo, tag)
	if err != nil {
		return out, err
	}
	var archives []ReleaseAsset
	if rel != nil {
		for _, a := range rel.Assets {
			if model.IsImageArchive(a.Name) {
				archives = append(archives, a)
			}
		}
	}
	switch len(archives) {
	case 0:
		out.Outcome = ImageNone
		return out, nil
	case 1:
	default:
		out.Outcome = ImageInvalid
		out.Reason = fmt.Sprintf("release %s has %d image archives, not one: %s", tag, len(archives),
			strings.Join(assetNames(archives), ", "))
		return out, nil
	}

	body, _, err := g.OpenReleaseAsset(ctx, repo, archives[0])
	if err != nil {
		return out, err
	}
	defer func() { _ = body.Close() }()
	pushed, err := oci.Push(ctx, g.registry(), imagePath(repo), tag, body)
	switch {
	case errors.Is(err, oci.ErrInvalid):
		out.Outcome, out.Reason = ImageInvalid, archives[0].Name+": "+err.Error()
		return out, nil
	case errors.Is(err, oci.ErrTagTaken):
		out.Outcome, out.Reason, out.Digest, out.Platforms = ImageTagTaken, err.Error(), pushed.Digest, pushed.Platforms
		return out, nil
	case err != nil:
		return out, fmt.Errorf("gitea: pushing %s of %s: %w", archives[0].Name, repoPath(repo), err)
	}
	if err := g.linkPackage(ctx, repo, "container", imageName(repo), tag); err != nil {
		return out, err
	}
	out.Outcome, out.Digest, out.Platforms = ImagePublished, pushed.Digest, pushed.Platforms
	return out, nil
}

// ImageDigest returns the digest repo's image tag names in the owner's
// container registry, or "" when there is none.
func (g *gitea) ImageDigest(ctx context.Context, repo Repository, tag string) (string, error) {
	digest, err := oci.Resolve(ctx, g.registry(), imagePath(repo), tag)
	if err != nil {
		return "", fmt.Errorf("gitea: %w", err)
	}
	return digest, nil
}

// CopyImage copies repo's image at digest from the owner's registry to
// toRepository at to, tagged tag.
func (g *gitea) CopyImage(ctx context.Context, repo Repository, digest string, to ports.ImageRegistry,
	toRepository, tag string) error {
	dest := oci.Registry{Host: to.Host, Username: to.Username, Password: to.Password, Insecure: to.Insecure}
	if err := oci.Copy(ctx, g.registry(), imagePath(repo), digest, dest, toRepository, tag); err != nil {
		return fmt.Errorf("gitea: %w", err)
	}
	return nil
}

func assetNames(assets []ReleaseAsset) []string {
	names := make([]string, 0, len(assets))
	for _, a := range assets {
		names = append(names, a.Name)
	}
	return names
}

// imageName is repo's image name below its owner: Gitea's container
// package name, which an OCI reference wants lower-case.
func imageName(repo Repository) string { return strings.ToLower(repo.Name) }

// imagePath is repo's image path in the registry, <owner>/<repository>.
func imagePath(repo Repository) string { return strings.ToLower(repo.Owner) + "/" + imageName(repo) }

// registry is the Git host's container registry, signed in with the
// provider's credential. Gitea's bearer challenge names the token endpoint
// at its ROOT_URL, which the operator may not resolve; the token is asked
// at the provider's own address, so the credential goes nowhere else.
func (g *gitea) registry() oci.Registry {
	creds := g.creds
	if g.basic != nil {
		creds = *g.basic
	}
	return oci.Registry{Host: g.base.Host, Username: creds.Username, Password: creds.Token,
		Insecure: g.base.Scheme == "http", Transport: tokenAt{base: g.base, next: g.transport()}}
}

// transport is the round tripper of the provider's HTTP client.
func (g *gitea) transport() http.RoundTripper {
	if g.http.Transport != nil {
		return g.http.Transport
	}
	return http.DefaultTransport
}

// tokenAt sends a request for Gitea's registry token endpoint, /v2/token,
// to base's scheme and host, wherever the challenge said it was.
type tokenAt struct {
	base *url.URL
	next http.RoundTripper
}

func (t tokenAt) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path == "/v2/token" && (req.URL.Host != t.base.Host || req.URL.Scheme != t.base.Scheme) {
		req = req.Clone(req.Context())
		req.URL.Scheme, req.URL.Host, req.Host = t.base.Scheme, t.base.Host, ""
	}
	return t.next.RoundTrip(req)
}

// linkPackage links version of owner's package name of type kind to repo,
// so it shows on the repository, unless it is linked to a repository
// already: Gitea refuses a second link, and one a person made elsewhere is
// theirs to keep.
func (g *gitea) linkPackage(ctx context.Context, repo Repository, kind, name, version string) error {
	pkg := "/api/v1/packages/" + url.PathEscape(repo.Owner) + "/" + kind + "/" + url.PathEscape(name)
	status, data, err := g.do(ctx, http.MethodGet, pkg+"/"+url.PathEscape(version), nil)
	if err != nil {
		return err
	}
	var linked struct {
		Repository *struct {
			Name string `json:"name"`
		} `json:"repository"`
	}
	if status != http.StatusOK || json.Unmarshal(data, &linked) != nil {
		return fmt.Errorf("gitea: reading the %s package %s %s failed (HTTP %d): %s", kind, name, version, status,
			apiMessage(data))
	}
	if linked.Repository != nil {
		// Linked here already, or by a person elsewhere: theirs to keep.
		return nil
	}
	status, data, err = g.do(ctx, http.MethodPost, pkg+"/-/link/"+url.PathEscape(repo.Name), nil)
	switch {
	case err != nil:
		return err
	case status == http.StatusCreated:
		return nil
	}
	return fmt.Errorf("gitea: linking the %s package %s to %s failed (HTTP %d): %s", kind, name, repoPath(repo),
		status, apiMessage(data))
}
