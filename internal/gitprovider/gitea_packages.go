package gitprovider

import "errors"

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
