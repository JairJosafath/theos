// Package ports holds the factory's swappable parts: one file per port with
// its interface and shared types, in plain Go with no Kubernetes
// (docs/architecture.md, "Ports"). Fakes are in ports/fake, contract suites in
// ports/contract, and the adapters in internal/adapters.
package ports

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/JairJosafath/c4factory/internal/model"
)

// Deployer ships a built release to a place a DeployTarget names and reads
// back what runs there. One Deployer serves one member of DeployTarget.spec;
// cmd/main.go maps each member's name to its adapter, and the controllers see
// only this port. It never builds and never runs code from the repository: CI
// built what it ships.
type Deployer interface {
	// Check says whether target accepts its credential: nil, or an error that
	// is ErrCredentialRefused or ErrUnreachable.
	Check(ctx context.Context, target Target) error
	// Ship takes the next step of shipping s to target and returns, without
	// waiting for the target's tool to finish. Repeating it for the same app
	// and version is safe and starts nothing new: it goes on with the one
	// deploy. An adapter whose tool runs the deploy (CloudFormation, Argo CD)
	// starts it in one call; one that is the tool itself (GitHub releases)
	// takes one write per call, so the caller ships again on each poll while
	// State says PhaseDeploying.
	Ship(ctx context.Context, target Target, s Shipment) error
	// State reads back the deploy of app at target: version is the version
	// the entry wants, its pin, else the newest release. An adapter whose
	// target runs one version at a time (CloudFormation, Argo CD) ignores it
	// and reports what runs; one whose versions stand side by side (GitHub
	// releases) reports version's, else the newest. The zero DeployState when
	// there is none.
	State(ctx context.Context, target Target, app, version string) (DeployState, error)
}

// Deployers are the Deployers by member name, as cmd/main.go builds them.
type Deployers map[string]Deployer

// For returns the Deployer of member.
func (d Deployers) For(member string) (Deployer, error) {
	if dep, ok := d[member]; ok && dep != nil {
		return dep, nil
	}
	return nil, fmt.Errorf("no deployer for deploy target member %q", member)
}

// Target is a DeployTarget as an adapter reads it.
type Target struct {
	// Name is the DeployTarget's namespace/name, for messages.
	Name string
	// Member is the member's name, such as github, and Settings its fields
	// (repository for github).
	Member   string
	Settings map[string]string
	// Tier is development or production.
	Tier string
	// Parameters are the target's deploy-time values, mapped by the adapter.
	Parameters map[string]string
	// Secret is the data of the target's Secret, read by the operator only.
	Secret map[string][]byte
}

// Shipment is one version of an app, as CI built it.
type Shipment struct {
	// App names the app uniquely at any target, namespace/name of its
	// Container; DisplayName is the name a person reads.
	App, DisplayName string
	// Version is the release's tag.
	Version string
	// Commit is the commit the version is tagged at where the target holds the
	// app's source, "" where it doesn't.
	Commit string
	// Repository is the clone URL of the app's repository where the target
	// holds the app's source, "" where it doesn't; a tool that pulls the
	// source itself (Argo CD) clones it at Version.
	Repository string
	// Notes are the release's notes.
	Notes string
	// Files are the release's files, streamed from the Git host.
	Files []File
	// Image is the app's image by digest, "" until CI builds one (#1069).
	Image string
	// CopyImage copies Image, with its digest, to repository at a registry
	// a target's nodes pull from, tagged tag; nil when there is no Image.
	// The owner's registry's credential stays with the caller.
	CopyImage func(ctx context.Context, to Registry, repository, tag string) error
	// Source is the release's source as the Git host holds it at Version,
	// read and hashed by the caller with the factory's own files left out;
	// nil where the target holds no source.
	Source *model.ReleaseSource
	// ReadSource streams Source's files again, each with its path and size,
	// for an adapter that sends the ones the target lacks; nil with no Source.
	ReadSource func(ctx context.Context, each func(path string, size int64, content io.Reader) error) error
}

// Registry is an OCI registry a target names and the credential from its
// Secret that writes there.
type Registry struct {
	// Host is host[:port], without a scheme.
	Host string
	// Username and Password log in; both empty is anonymous.
	Username, Password string
}

// File is a release file, opened only when the adapter sends it.
type File struct {
	Name string
	Size int64
	// Open streams the file, with its size. The caller closes the reader.
	Open func(ctx context.Context) (io.ReadCloser, int64, error)
}

// Phases of a deploy.
const (
	PhaseDeploying = "Deploying"
	PhaseDeployed  = "Deployed"
	PhaseFailed    = "Failed"
)

// Health of what runs at a target, as its tool reports it.
const (
	HealthHealthy     = "Healthy"
	HealthProgressing = "Progressing"
	HealthDegraded    = "Degraded"
	HealthUnknown     = "Unknown"
)

// DeployState is the newest deploy of an app at a target.
type DeployState struct {
	// Version is that deploy's version, "" when the app was never shipped.
	Version string
	// Phase is PhaseDeploying, PhaseDeployed or PhaseFailed, "" with no deploy.
	Phase string
	// Health is one of Health*, as the target's tool reports it.
	Health string
	// URL is where the app, or its release, answers, when the tool knows it.
	URL string
	// Reason and Message say why a deploy failed, or what it waits on.
	Reason, Message string
	// Source is the release's source as the target published it, nil until
	// its tag is there or where the target holds no source.
	Source *model.PublishedSource
}

// Errors a Deployer's calls fail with that the operator tells apart; test with
// errors.Is. An adapter wraps them with what the tool said.
var (
	// ErrCredentialRefused: the target refuses the credential, or it can't see
	// the place.
	ErrCredentialRefused = errors.New("the target refuses the credential")
	// ErrUnreachable: the call failed before the target's tool had it (the
	// network, a timeout, a 5xx); tried again on the next poll.
	ErrUnreachable = errors.New("the target can't be reached")
	// ErrArtifactMissing: the shipment lacks a file the member needs.
	ErrArtifactMissing = errors.New("the release lacks a file the target needs")
	// ErrPrerequisiteMissing: something a person sets up once at the target
	// is missing (the CDK bootstrap, a template parameter with no default
	// the target's parameters don't give); shipped again on the next poll.
	ErrPrerequisiteMissing = errors.New("the target lacks something a person sets up there")
)

// Error is an adapter's error that is one of the port's errors and keeps the
// tool's own error, whose text it has: errors.Is finds Kind, errors.As the
// cause.
type Error struct {
	Kind  error
	Cause error
}

func (e *Error) Error() string {
	if e.Cause == nil {
		return e.Kind.Error()
	}
	return e.Cause.Error()
}

// Unwrap returns Kind and Cause.
func (e *Error) Unwrap() []error {
	if e.Cause == nil {
		return []error{e.Kind}
	}
	return []error{e.Kind, e.Cause}
}
