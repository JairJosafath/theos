package fake

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/module"

	"github.com/JairJosafath/c4factory/internal/ports"
)

// The fake host's package and container registries: Go modules, a release's
// npm and PyPI packages, and images.

// Package is the package a release's file holds: an npm tarball or a
// wheel or sdist, with the name and version its metadata declares.
type Package struct {
	// Ecosystem is ports.EcosystemNpm or ports.EcosystemPyPI.
	Ecosystem string
	Name      string
	Version   string
	// File is the release's file that holds it.
	File string
}

// PublishGoModule publishes the module SetGoMod gave the repository, as Gitea
// would: no go.mod, a version Go refuses and one uploaded before are outcomes.
func (g *Git) PublishGoModule(_ context.Context, repo ports.Repository, tag string) (
	ports.GoModule, error) {
	key := repo.Owner + "/" + repo.Name
	g.s.mu.Lock()
	delay := g.s.releaseState().moduleDelays[key]
	g.s.mu.Unlock()
	time.Sleep(delay)
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.releaseState()
	out := ports.GoModule{Outcome: ports.GoModuleNoGoMod, Version: tag}
	s.modulePublishes[key+"@"+tag]++
	if s.moduleFailures[key] > 0 {
		s.moduleFailures[key]--
		return out, fmt.Errorf("gitea unavailable")
	}
	path, ok := s.goMods[key]
	if !ok {
		return out, nil
	}
	out.Path = path
	if err := module.Check(path, tag); err != nil {
		out.Outcome, out.Reason = ports.GoModuleInvalid, err.Error()
		return out, nil
	}
	out.Outcome = ports.GoModuleExists
	if s.uploads[path+"@"+tag] == 0 {
		out.Outcome = ports.GoModulePublished
		s.uploads[path+"@"+tag]++
	}
	return out, nil
}

// PublishReleasePackages publishes the packages SetReleasePackages gave the
// release's files, as Gitea's adapter would: no release is ErrNoRelease; an
// npm name outside repo.NpmScope is Unscoped, a version that isn't the tag's
// Invalid, one in the registry already Exists, else it is uploaded now.
func (g *Git) PublishReleasePackages(_ context.Context, repo ports.Repository, tag string) (
	ports.ReleasePackages, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.releaseState()
	key := repo.Owner + "/" + repo.Name
	out := ports.ReleasePackages{Tag: tag}
	s.packagePublishes[key+"@"+tag]++
	if s.packageFailures[key] > 0 {
		s.packageFailures[key]--
		return out, fmt.Errorf("gitea unavailable")
	}
	if s.published[key+"@"+tag] == nil {
		return out, fmt.Errorf("%w: %s of %s", ports.ErrNoRelease, tag, key)
	}
	want := strings.TrimPrefix(tag, "v")
	for _, f := range s.packages[key+"@"+tag] {
		pkg := ports.Package{Ecosystem: f.Ecosystem, Name: f.Name, Version: f.Version, File: f.File}
		switch {
		case f.Ecosystem == ports.EcosystemNpm && !strings.HasPrefix(f.Name, repo.NpmScope+"/"):
			pkg.Outcome = ports.PackageUnscoped
			pkg.Reason = fmt.Sprintf("%s is not under the scope %s", f.Name, repo.NpmScope)
		case f.Version != want:
			pkg.Outcome = ports.PackageInvalid
			pkg.Reason = fmt.Sprintf("version %s is not the tag's, %s", f.Version, want)
		default:
			pkg.Outcome = ports.PackageExists
			upload := repo.Owner + ":" + f.Ecosystem + "/" + f.Name + "@" + f.Version
			if s.packageUploads[upload] == 0 {
				pkg.Outcome = ports.PackagePublished
				s.packageUploads[upload]++
				s.packageTokens[upload] = g.cfg.Packages.PyPIToken
				if f.Ecosystem == ports.EcosystemNpm {
					s.packageTokens[upload] = g.cfg.Packages.NpmToken
				}
			}
		}
		out.Packages = append(out.Packages, pkg)
	}
	return out, nil
}

// SetReleasePackages says which package each file of owner/name's release of
// Tag holds; the release itself is SetRelease's.
func (g *Git) SetReleasePackages(owner, name, tag string, packages ...Package) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.releaseState().packages[owner+"/"+name+"@"+tag] = packages
}

// FailPackagePublishes makes the next n PublishReleasePackages calls of
// owner/name fail.
func (g *Git) FailPackagePublishes(owner, name string, n int) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.releaseState().packageFailures[owner+"/"+name] = n
}

// PackagePublishes is how many times the Crew asked to publish the packages
// of release, owner/name@tag, as ImagePushes counts the image's pushes.
func (g *Git) PackagePublishes(release string) int {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.releaseState().packagePublishes[release]
}

// PackageUploads is how many times ecosystem package name@version was uploaded
// to owner's registry.
func (g *Git) PackageUploads(owner, ecosystem, name, version string) int {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.releaseState().packageUploads[owner+":"+ecosystem+"/"+name+"@"+version]
}

// PackageUploadToken is the registry token the upload of ecosystem package
// name@version to owner's registry carried, "" for none.
func (g *Git) PackageUploadToken(owner, ecosystem, name, version string) string {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.releaseState().packageTokens[owner+":"+ecosystem+"/"+name+"@"+version]
}

// PublishImage gives the outcome SetImage gave the release, after the
// failures FailImagePushes asked for and the hold HoldImagePushes put on it;
// a Published image's digest lands in the registry.
func (g *Git) PublishImage(ctx context.Context, repo ports.Repository, tag string) (
	ports.Image, error) {
	key := repo.Owner + "/" + repo.Name
	g.s.mu.Lock()
	g.s.releaseState().imagePushes[key+"@"+tag]++
	hold := g.s.releaseState().imageHolds[key]
	g.s.mu.Unlock()
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return ports.Image{}, ctx.Err()
		}
	}
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.releaseState()
	out := ports.Image{Outcome: ports.ImageNone, Tag: tag, Repository: repo.ImageRepository()}
	if s.imageFailures[key] > 0 {
		s.imageFailures[key]--
		return out, fmt.Errorf("gitea unavailable")
	}
	img, ok := s.images[key+"@"+tag]
	if !ok {
		return out, nil
	}
	img.Tag, img.Repository = tag, out.Repository
	if img.Outcome == ports.ImagePublished {
		s.registry[key+"@"+tag] = img.Digest
	}
	return img, nil
}

// ImageDigest is the digest tag names in the owner's registry, "" when none.
func (g *Git) ImageDigest(_ context.Context, repo ports.Repository, tag string) (string, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.releaseState().registry[repo.Owner+"/"+repo.Name+"@"+tag], nil
}

// ImageCopier copies image digest of repo, from the owner's registry, to
// toRepository at to under tag, as CopyImage does for real.
type ImageCopier func(ctx context.Context, repo ports.Repository, digest string, to ports.ImageRegistry,
	toRepository, tag string) error

// SetImageCopier makes CopyImage copy with copier after it records the copy;
// nil, the default, only records it.
func (g *Git) SetImageCopier(copier ImageCopier) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.copyImage = copier
}

// CopyImage records the copy of digest to toRepository at to under tag, then
// copies it with the ImageCopier SetImageCopier gave, if any.
func (g *Git) CopyImage(ctx context.Context, repo ports.Repository, digest string, to ports.ImageRegistry,
	toRepository, tag string) error {
	g.s.mu.Lock()
	state := g.s.releaseState()
	if state.imageCopies == nil {
		state.imageCopies = map[string]string{}
	}
	state.imageCopies[to.Host+"/"+toRepository+":"+tag] = digest
	copier := g.s.copyImage
	g.s.mu.Unlock()
	if copier == nil {
		return nil
	}
	return copier(ctx, repo, digest, to, toRepository, tag)
}

// ImageCopy is the digest CopyImage copied to ref, host/repository:tag.
func (g *Git) ImageCopy(ref string) string {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.releaseState().imageCopies[ref]
}

// SetImage makes PublishImage of owner/name's release of tag give img.
func (g *Git) SetImage(owner, name, tag string, img ports.Image) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.releaseState().images[owner+"/"+name+"@"+tag] = img
}

// SetRegistryImage puts digest in the owner's registry under owner/name's tag.
func (g *Git) SetRegistryImage(owner, name, tag, digest string) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.releaseState().registry[owner+"/"+name+"@"+tag] = digest
}

// FailImagePushes makes the next n PublishImage calls of owner/name fail.
func (g *Git) FailImagePushes(owner, name string, n int) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.releaseState().imageFailures[owner+"/"+name] = n
}

// HoldImagePushes holds every PublishImage call of owner/name open until
// the returned func releases them.
func (g *Git) HoldImagePushes(owner, name string) (release func()) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	hold := make(chan struct{})
	g.s.releaseState().imageHolds[owner+"/"+name] = hold
	return sync.OnceFunc(func() { close(hold) })
}

// ImagePushes is how many times the Container asked to push the image
// owner/name@tag.
func (g *Git) ImagePushes(image string) int {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.releaseState().imagePushes[image]
}

// SetGoMod gives owner/name a go.mod declaring module path.
func (g *Git) SetGoMod(owner, name, path string) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.releaseState().goMods[owner+"/"+name] = path
}

// FailModulePublishes makes the next n PublishGoModule calls of owner/name fail.
func (g *Git) FailModulePublishes(owner, name string, n int) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.releaseState().moduleFailures[owner+"/"+name] = n
}

// SlowModulePublishes makes each PublishGoModule call of owner/name take d, as
// a busy Gitea would.
func (g *Git) SlowModulePublishes(owner, name string, d time.Duration) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.releaseState().moduleDelays[owner+"/"+name] = d
}

// ModulePublishes is how many times the Crew asked to publish owner/name's
// module at tag.
func (g *Git) ModulePublishes(owner, name, tag string) int {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.releaseState().modulePublishes[owner+"/"+name+"@"+tag]
}

// ModulePublishFailuresLeft is how many PublishGoModule calls of owner/name
// still fail.
func (g *Git) ModulePublishFailuresLeft(owner, name string) int {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.releaseState().moduleFailures[owner+"/"+name]
}

// ModuleUploads is how many times module path@version was uploaded.
func (g *Git) ModuleUploads(path, version string) int {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.releaseState().uploads[path+"@"+version]
}
