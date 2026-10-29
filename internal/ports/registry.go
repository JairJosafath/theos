package ports

import (
	"context"
	"errors"
)

// ImageRegistry is an OCI registry and the credential that writes there: a
// Deployer's target names one, and the Registry port copies an image to it.
type ImageRegistry struct {
	// Host is host[:port], without a scheme.
	Host string
	// Username and Password log in; both empty is anonymous.
	Username, Password string
	// Insecure speaks plain HTTP to Host; false is TLS.
	Insecure bool
}

// GoModuleOutcome is what PublishGoModule did with a tag.
type GoModuleOutcome string

const (
	// GoModulePublished means the module's version was uploaded now.
	GoModulePublished GoModuleOutcome = "Published"
	// GoModuleExists means the registry already had the version; nothing was uploaded.
	GoModuleExists GoModuleOutcome = "Exists"
	// GoModuleNoGoMod means the repository has no go.mod at its root at the tag.
	GoModuleNoGoMod GoModuleOutcome = "NoGoMod"
	// GoModuleInvalid means the module can't be published at the tag, such as a
	// v2.0.0 whose module path lacks /v2; Reason says why. A retry won't help.
	GoModuleInvalid GoModuleOutcome = "Invalid"
)

// GoModule is the result of publishing a repository's Go module at a tag.
type GoModule struct {
	Outcome GoModuleOutcome
	// Path is the module path go.mod declares, "" when there is no go.mod.
	Path string
	// Version is the tag, the module's version.
	Version string
	// Reason says why an Invalid module can't be published.
	Reason string
}

// ErrNoRelease means the repository has no release of the tag.
var ErrNoRelease = errors.New("the repository has no release of the tag")

// PackageOutcome is what PublishReleasePackages did with one release file it
// recognised as a package.
type PackageOutcome string

const (
	// PackagePublished means the package's version was uploaded now.
	PackagePublished PackageOutcome = "Published"
	// PackageExists means the registry had the version already; nothing was uploaded.
	PackageExists PackageOutcome = "Exists"
	// PackageUnscoped means an npm tarball's name is not under the repository's
	// scope: the app's own release file, left alone. Reason names the scope.
	PackageUnscoped PackageOutcome = "Unscoped"
	// PackageInvalid means the file can't be published at the tag's version,
	// such as a version that isn't the tag's; Reason says why. A retry won't help.
	PackageInvalid PackageOutcome = "Invalid"
)

// Ecosystems of the packages a release may hold.
const (
	EcosystemNpm  = "npm"
	EcosystemPyPI = "PyPI"
)

// Package is what became of one package file of a release.
type Package struct {
	Outcome PackageOutcome
	// Ecosystem is EcosystemNpm or EcosystemPyPI.
	Ecosystem string
	// Name is the package's name as its metadata declares it, and Version its
	// version as declared, before any normalisation.
	Name    string
	Version string
	// File is the release asset's name.
	File string
	// Reason says why a package is Unscoped or Invalid.
	Reason string
}

// ReleasePackages is the result of publishing a release's package files. A
// release with no package file has no Packages.
type ReleasePackages struct {
	// Tag is the release's tag.
	Tag      string
	Packages []Package
}

// ImageOutcome is what PublishImage did with a release's image archive.
type ImageOutcome string

const (
	// ImagePublished means the registry has the image under the tag: pushed
	// now, or already there with the same digest.
	ImagePublished ImageOutcome = "Published"
	// ImageNone means the release has no image archive.
	ImageNone ImageOutcome = "NoImage"
	// ImageInvalid means the release's archive can't be pushed: there are two,
	// or its content is wrong. Reason says why; a retry won't help.
	ImageInvalid ImageOutcome = "Invalid"
	// ImageTagTaken means the tag names another digest in the registry, which
	// the operator never replaces. Reason names both.
	ImageTagTaken ImageOutcome = "TagTaken"
)

// Image is the result of publishing a release's image.
type Image struct {
	Outcome ImageOutcome
	// Repository is the image's repository, <host>/<owner>/<repository>.
	Repository string
	// Tag is the release's tag, the image's tag.
	Tag string
	// Digest is the archive's root manifest digest, also when the tag names
	// another; "" when there is no archive or it is invalid.
	Digest string
	// Platforms are the platforms the image runs on, attestations left out.
	Platforms []string
	// Reason says why an image is Invalid or TagTaken.
	Reason string
}

// Registry is the package registry port (docs/architecture.md, "Ports"): the
// owner's Go modules, npm and PyPI packages and container images, published
// from a repository's release at its tag. Where the packages go is
// Repository's addresses (GoProxy, ImageRegistry, NpmRegistry, PyPIIndex).
type Registry interface {
	// PublishGoModule publishes the Go module at the root of repo, as of tag, to
	// the owner's Go package registry, at tag's version. No go.mod, a version
	// published before and a module that can't be published at tag are
	// outcomes, not errors; a repeat publishes nothing.
	PublishGoModule(ctx context.Context, repo Repository, tag string) (GoModule, error)
	// PublishReleasePackages publishes the package files of repo's release of
	// tag, an npm tarball, a wheel and an sdist, to the registries repo names,
	// at tag's version. A release with no package file, an npm name outside
	// repo's scope, a version that isn't the tag's and a version published
	// before are outcomes, not errors; a repeat publishes nothing. No release
	// of tag is ErrNoRelease.
	PublishReleasePackages(ctx context.Context, repo Repository, tag string) (ReleasePackages, error)
	// PublishImage pushes the image archive of repo's release of tag, its
	// *.oci.tar file, to the owner's container registry under tag and links
	// it to repo. A tag is pushed once and never replaced: no archive, an
	// invalid one and a tag naming another digest are outcomes, not errors;
	// the same digest again pushes nothing. An unreachable host is an error.
	PublishImage(ctx context.Context, repo Repository, tag string) (Image, error)
	// ImageDigest returns the digest repo's image tag names in the owner's
	// container registry, or "" when there is none.
	ImageDigest(ctx context.Context, repo Repository, tag string) (string, error)
	// CopyImage copies repo's image at digest from the owner's container
	// registry to toRepository at to, tagged tag, with the same digest: an
	// argoCD target on another cluster pulls from a registry its nodes reach
	// (#1590).
	CopyImage(ctx context.Context, repo Repository, digest string, to ImageRegistry, toRepository, tag string) error
}
