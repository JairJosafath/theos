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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/oci"
	"github.com/JairJosafath/theos/internal/ports"
)

// The Registry port on GitHub, step 7 of docs/plans/github-host.md (#80):
// the owner's images in GitHub's container registry, its npm packages in
// GitHub's npm registry, its Python packages where the GitProvider says,
// and its Go modules where they already are, in their repositories.
//
// https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry,
// .../working-with-the-npm-registry and
// /packages/learn-github-packages/connecting-a-repository-to-a-package.

// GitHub's registries on github.com.
const (
	containerRegistryHost = "ghcr.io"
	npmRegistryURL        = "https://npm.pkg.github.com/"
)

// packagePlaces is where an owner's packages are on a GitHub instance.
type packagePlaces struct {
	// images is the container registry's host; npm is the npm registry
	// the owner's packages go to, with a trailing slash, npmScope the scope
	// they are named under, and ownNpm whether that registry is GitHub's.
	images   string
	npm      string
	npmScope string
	ownNpm   bool
}

// placesOf is where owner's packages are on the instance at web, with what
// the GitProvider set of them. On github.com the registries are ghcr.io
// and npm.pkg.github.com; an Enterprise Server serves its own at
// containers.<host> and npm.<host>, as it names them with subdomain
// isolation, which its container registry needs. An instance without it
// names its npm registry in set.
//
// GitHub's npm registry takes a package under its owner's scope alone, in
// lower case: there the scope is @<owner>, and a scope set to another is an
// error, since nothing named under it could be published. A registry that
// is not GitHub's takes any scope, ports.DefaultNpmScope unless set says.
func placesOf(web *url.URL, owner string, set ports.Packages) (packagePlaces, error) {
	places := packagePlaces{images: containerRegistryHost, npm: npmRegistryURL}
	if web.String() != GitHubWebURL {
		places.images = "containers." + web.Host
		places.npm = web.Scheme + "://npm." + web.Host + "/"
	}
	own := places.npm
	if set.NpmRegistry != "" {
		places.npm = strings.TrimSuffix(set.NpmRegistry, "/") + "/"
	}
	places.ownNpm = strings.EqualFold(places.npm, own)
	ownScope := "@" + strings.ToLower(owner)
	switch {
	case !places.ownNpm:
		places.npmScope = orDefault(set.NpmScope, ports.DefaultNpmScope)
	case set.NpmScope == "" || set.NpmScope == ownScope:
		places.npmScope = ownScope
	default:
		return packagePlaces{}, fmt.Errorf("github: the npm scope %s is not %s: GitHub's npm registry takes a "+
			"package under its owner's scope and no other, so leave the scope out or name another registry",
			set.NpmScope, ownScope)
	}
	return places, nil
}

func orDefault(value, def string) string {
	if value == "" {
		return def
	}
	return value
}

// imagePath is the path of repo's images in the container registry,
// <owner>/<repository>, lower-case as a reference wants.
func imagePath(repo ports.Repository) string {
	return strings.ToLower(repo.Owner) + "/" + strings.ToLower(repo.Name)
}

// images is the owner's container registry with the operator's token, a
// personal access token (classic) that may write packages: GitHub's
// registries take no other kind of a person's token.
func (h *host) images() oci.Registry {
	return oci.Registry{Host: h.places.images, Username: h.login, Password: h.creds.Token,
		Insecure: h.web.Scheme == "http", Transport: h.http.Transport}
}

// PublishImage pushes the image archive of repo's release of tag, its
// *.oci.tar file, to GitHub's container registry as <owner>/<repository>
// under tag. A tag is pushed once and never replaced; no archive, a wrong
// one and a tag that names another image are outcomes, not errors, and a
// repeat pushes nothing new. The archive is streamed from the release,
// never held whole.
//
// GitHub links an image to a repository by the image's own
// org.opencontainers.image.source label and has no call that links one
// afterwards, so the link is the build's to make: an image without the
// label is the owner's, and no repository's.
func (h *host) PublishImage(ctx context.Context, repo ports.Repository, tag string) (ports.Image, error) {
	out := ports.Image{Repository: repo.ImageRepository(), Tag: tag}
	rel, err := h.GetRelease(ctx, repo, tag)
	if err != nil {
		return out, err
	}
	var archives []ports.ReleaseAsset
	var names []string
	if rel != nil {
		for _, a := range rel.Assets {
			if model.IsImageArchive(a.Name) {
				archives, names = append(archives, a), append(names, a.Name)
			}
		}
	}
	if len(archives) == 0 {
		out.Outcome = ports.ImageNone
		return out, nil
	}
	if len(archives) > 1 {
		out.Outcome = ports.ImageInvalid
		out.Reason = fmt.Sprintf("release %s has %d image archives, not one: %s", tag, len(archives),
			strings.Join(names, ", "))
		return out, nil
	}
	body, _, err := h.OpenReleaseAsset(ctx, repo, archives[0])
	if err != nil {
		return out, err
	}
	defer func() { _ = body.Close() }()
	pushed, err := oci.Push(ctx, h.images(), imagePath(repo), tag, body)
	out.Digest, out.Platforms = pushed.Digest, pushed.Platforms
	switch {
	case errors.Is(err, oci.ErrInvalid):
		out.Outcome, out.Reason, out.Digest, out.Platforms = ports.ImageInvalid, names[0]+": "+err.Error(), "", nil
	case errors.Is(err, oci.ErrTagTaken):
		out.Outcome, out.Reason = ports.ImageTagTaken, err.Error()
	case err != nil:
		return ports.Image{Repository: out.Repository, Tag: tag},
			fmt.Errorf("github: pushing %s of %s/%s: %w", names[0], repo.Owner, repo.Name, err)
	default:
		out.Outcome = ports.ImagePublished
	}
	return out, nil
}

// ImageDigest returns the digest repo's image tag names in GitHub's
// container registry, or "" when there is none.
func (h *host) ImageDigest(ctx context.Context, repo ports.Repository, tag string) (string, error) {
	digest, err := oci.Resolve(ctx, h.images(), imagePath(repo), tag)
	if err != nil {
		return "", fmt.Errorf("github: %w", err)
	}
	return digest, nil
}

// CopyImage copies repo's image at digest from GitHub's container registry
// to toRepository at to, tagged tag.
func (h *host) CopyImage(ctx context.Context, repo ports.Repository, digest string, to ports.ImageRegistry,
	toRepository, tag string) error {
	dest := oci.Registry{Host: to.Host, Username: to.Username, Password: to.Password, Insecure: to.Insecure}
	if err := oci.Copy(ctx, h.images(), imagePath(repo), digest, dest, toRepository, tag); err != nil {
		return fmt.Errorf("github: %w", err)
	}
	return nil
}

// maxGoMod is the most of a go.mod PublishGoModule reads.
const maxGoMod = 1 << 20

// PublishGoModule says whether the Go module at the root of repo can be
// fetched at tag. GitHub has no module proxy and none is needed: go reads a
// module of github.com from its repository, at the tag, so the tag is the
// publication and nothing is uploaded. What is left is what go would
// refuse: no go.mod is GoModuleNoGoMod, and a version the path doesn't
// allow, or a path that is not the repository's own, is GoModuleInvalid
// with the reason. A module go can fetch is GoModuleExists, the first
// time as every time. A tag the repository doesn't have is an error.
func (h *host) PublishGoModule(ctx context.Context, repo ports.Repository, tag string) (ports.GoModule, error) {
	out := ports.GoModule{Version: tag}
	commit, err := h.GetTag(ctx, repo, tag)
	if err != nil {
		return out, err
	}
	if commit == "" {
		return out, fmt.Errorf("github: %s/%s has no tag %s", repo.Owner, repo.Name, tag)
	}
	goMod, found, err := h.fileAt(ctx, repo, commit, "go.mod")
	if err != nil {
		return out, err
	}
	if !found {
		out.Outcome = ports.GoModuleNoGoMod
		return out, nil
	}
	out.Path = modfile.ModulePath(goMod)
	if reason := h.unfetchable(repo, out.Path, tag); reason != "" {
		out.Outcome, out.Reason = ports.GoModuleInvalid, reason
		return out, nil
	}
	out.Outcome = ports.GoModuleExists
	return out, nil
}

// modulePath is the path a Go module at the root of repo must declare for
// go to find it: where the repository is, without the scheme and without a
// port, which a module path can't hold. A version from v2 on adds its major
// to it.
func (h *host) modulePath(repo ports.Repository) string {
	return h.web.Hostname() + "/" + repo.Owner + "/" + repo.Name
}

// unfetchable says why go can't fetch the module that declares path from
// repo at version, or "" when it can.
func (h *host) unfetchable(repo ports.Repository, path, version string) string {
	if path == "" {
		return "go.mod declares no module path"
	}
	if canonical := module.CanonicalVersion(version); canonical != version {
		return fmt.Sprintf("tag %s is not a canonical semantic version", version)
	}
	if err := module.Check(path, version); err != nil {
		return err.Error()
	}
	want := h.modulePath(repo)
	if prefix, _, ok := module.SplitPathVersion(path); !ok || prefix != want {
		return fmt.Sprintf("go.mod declares %s, and go reads a module of this host from the repository its path "+
			"names: the path must be %s", path, want)
	}
	return ""
}

// fileAt reads the file at path in repo as of ref; found is false when the
// repository has no such file there.
func (h *host) fileAt(ctx context.Context, repo ports.Repository, ref, path string) (data []byte, found bool, err error) {
	what := "reading " + path + " at " + ref + " of " + repo.Owner + "/" + repo.Name
	resp, err := h.download(ctx, h.repoPath(repo)+"/contents/"+path+"?ref="+url.QueryEscape(ref),
		"application/vnd.github.raw+json")
	if err != nil {
		return nil, false, err
	}
	if resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		return nil, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, refused(what, resp)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err = io.ReadAll(io.LimitReader(resp.Body, maxGoMod))
	if err != nil {
		return nil, false, fmt.Errorf("github: %s: %w", what, err)
	}
	return data, true, nil
}
