// Package contract holds the ports' contract suites: what every adapter of a
// port, and its fake, must do. Each adapter's tests call its port's suite.
package contract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// DeployerEnv is a Deployer under test and the targets it ships to.
type DeployerEnv struct {
	Deployer ports.Deployer
	// Target returns a fresh target that accepts its credential and holds no
	// deploy yet.
	Target func(t *testing.T) ports.Target
	// Refused returns a target that refuses its credential.
	Refused func(t *testing.T) ports.Target
	// Unprepared returns a target that lacks a prerequisite a person sets
	// up there; nil for a member that has none.
	Unprepared func(t *testing.T) ports.Target
	// Deploys counts the deploys of app the target's tool holds.
	Deploys func(t *testing.T, target ports.Target, app string) int
	// Files are the release files of version a member ships, nil for the
	// suite's own two (a CloudFormation member ships a cloud assembly).
	Files func(version string) []ports.File
}

// maxShips bounds the Ship calls one deploy takes: a Deployer that is the
// tool itself takes one write per call.
const maxShips = 20

// contractApp is the app the suite ships.
const contractApp = "contract/orders-cli"

// Deployer states what every Deployer does: Ship twice is one deploy; State
// after Ship reports the version once the tool says it is done, and a
// version shipped before reaches Deployed again when it is wanted; a refused
// credential is ErrCredentialRefused from Check and Ship; a release without
// the artifact the member ships (its files, or its image) is
// ErrArtifactMissing; a target that lacks a prerequisite is
// ErrPrerequisiteMissing from Ship, which ships nothing.
func Deployer(t *testing.T, env DeployerEnv) {
	t.Helper()
	ctx := context.Background()
	d := env.Deployer
	shipment := func(version string) ports.Shipment {
		s := shipment(version)
		if env.Files != nil {
			s.Files = env.Files(version)
		}
		return s
	}

	t.Run("checks a credential the target accepts", func(t *testing.T) {
		if err := d.Check(ctx, env.Target(t)); err != nil {
			t.Fatalf("Check() = %v, want nil", err)
		}
	})

	t.Run("reports nothing before the first deploy", func(t *testing.T) {
		st, err := d.State(ctx, env.Target(t), contractApp, "")
		if err != nil || st.Version != "" || st.Phase != "" {
			t.Fatalf("State() = %+v, %v; want the zero state", st, err)
		}
	})

	t.Run("ships a version once, however often Ship is called", func(t *testing.T) {
		target := env.Target(t)
		shipUntilDeployed(t, d, target, shipment("v1.0.0"))
		for range 2 {
			if err := d.Ship(ctx, target, shipment("v1.0.0")); err != nil {
				t.Fatalf("Ship() again = %v, want nil", err)
			}
		}
		if n := env.Deploys(t, target, contractApp); n != 1 {
			t.Errorf("deploys = %d after shipping v1.0.0 three times, want 1", n)
		}
		st, err := d.State(ctx, target, contractApp, "v1.0.0")
		if err != nil || st.Version != "v1.0.0" || st.Phase != ports.PhaseDeployed {
			t.Errorf("State() = %+v, %v; want v1.0.0 Deployed", st, err)
		}
	})

	t.Run("ships a newer version as a new deploy", func(t *testing.T) {
		target := env.Target(t)
		shipUntilDeployed(t, d, target, shipment("v1.0.0"))
		shipUntilDeployed(t, d, target, shipment("v1.1.0"))
		if n := env.Deploys(t, target, contractApp); n != 2 {
			t.Errorf("deploys = %d after v1.0.0 and v1.1.0, want 2", n)
		}
	})

	t.Run("a version shipped before is Deployed again when it is wanted", func(t *testing.T) {
		// A rollback: the entry pins v1.0.0 after v1.1.0 shipped. A tool
		// that runs one version starts a new deploy of it; one whose
		// versions stand side by side reports v1.0.0's. Either way State
		// reaches Deployed at v1.0.0 (#1445).
		target := env.Target(t)
		shipUntilDeployed(t, d, target, shipment("v1.0.0"))
		shipUntilDeployed(t, d, target, shipment("v1.1.0"))
		shipUntilDeployed(t, d, target, shipment("v1.0.0"))
	})

	t.Run("a refused credential is ErrCredentialRefused", func(t *testing.T) {
		target := env.Refused(t)
		if err := d.Check(ctx, target); !errors.Is(err, ports.ErrCredentialRefused) {
			t.Errorf("Check() = %v, want ErrCredentialRefused", err)
		}
		if err := d.Ship(ctx, target, shipment("v1.0.0")); !errors.Is(err, ports.ErrCredentialRefused) {
			t.Errorf("Ship() = %v, want ErrCredentialRefused", err)
		}
	})

	t.Run("a missing prerequisite is ErrPrerequisiteMissing", func(t *testing.T) {
		if env.Unprepared == nil {
			t.Skip("the member has no prerequisite")
		}
		target := env.Unprepared(t)
		if err := d.Ship(ctx, target, shipment("v1.0.0")); !errors.Is(err, ports.ErrPrerequisiteMissing) {
			t.Errorf("Ship() to a target without its prerequisite = %v, want ErrPrerequisiteMissing", err)
		}
		if n := env.Deploys(t, target, contractApp); n != 0 {
			t.Errorf("deploys = %d, want 0", n)
		}
	})

	t.Run("a missing artifact is ErrArtifactMissing", func(t *testing.T) {
		// A release with no files, no image and no source lacks what any
		// member ships: GitHub its files or source, Argo CD its image. A
		// source alone ships on GitHub (#1599), so it goes too (#1785).
		target := env.Target(t)
		s := shipment("v1.0.0")
		s.Files = nil
		s.Image = ""
		s.Source = nil
		s.ReadSource = nil
		if err := d.Ship(ctx, target, s); !errors.Is(err, ports.ErrArtifactMissing) {
			t.Errorf("Ship() of a release without files, image or source = %v, want ErrArtifactMissing", err)
		}
		if n := env.Deploys(t, target, contractApp); n != 0 {
			t.Errorf("deploys = %d, want 0", n)
		}
	})
}

// shipUntilDeployed ships s until State reports it Deployed.
func shipUntilDeployed(t *testing.T, d ports.Deployer, target ports.Target, s ports.Shipment) {
	t.Helper()
	ctx := context.Background()
	for range maxShips {
		if err := d.Ship(ctx, target, s); err != nil {
			t.Fatalf("Ship(%s) = %v", s.Version, err)
		}
		st, err := d.State(ctx, target, s.App, s.Version)
		if err != nil {
			t.Fatalf("State() = %v", err)
		}
		if st.Version == s.Version && st.Phase == ports.PhaseDeployed {
			return
		}
	}
	t.Fatalf("%s isn't Deployed after %d Ship calls", s.Version, maxShips)
}

// shipment is version of the suite's app, with two files, its image by
// digest, its repository and its source of two files; an adapter whose
// target holds no source ignores the source.
func shipment(version string) ports.Shipment {
	file := func(name, content string) ports.File {
		return ports.File{Name: name, Size: int64(len(content)),
			Open: func(context.Context) (io.ReadCloser, int64, error) {
				return io.NopCloser(bytes.NewReader([]byte(content))), int64(len(content)), nil
			}}
	}
	digest := fmt.Sprintf("%064x", []byte(version))
	s := ports.Shipment{App: contractApp, DisplayName: "Orders CLI", Version: version,
		Repository: "https://git.example/contract/orders-cli.git", Notes: "- Orders can be listed",
		Files: []ports.File{file("orders-cli_linux.tar.gz", "binary "+version), file("SHA256SUMS", "sums "+version)},
		Image: "git.example/contract/orders-cli@sha256:" + digest[:64]}
	s.Source, s.ReadSource = source(map[string]string{"go.mod": "module example.com/orders-cli\n",
		"main.go": "package main // " + version + "\n"})
	return s
}

// source is a release's source of files, by path, and a ReadSource that
// streams them.
func source(files map[string]string) (*model.ReleaseSource, func(context.Context,
	func(string, int64, io.Reader) error) error) {
	paths := slices.Sorted(maps.Keys(files))
	hashed := make([]model.SourceFile, 0, len(paths))
	for _, p := range paths {
		hashed = append(hashed, model.SourceFile{Path: p, Mode: model.ModeFile, Size: int64(len(files[p])),
			Blob: model.BlobSHA([]byte(files[p]))})
	}
	src, err := model.NewSource(model.CI{}, hashed, "example.com/orders-cli")
	if err != nil {
		panic(err)
	}
	return src, func(_ context.Context, each func(string, int64, io.Reader) error) error {
		for _, p := range paths {
			if err := each(p, int64(len(files[p])), strings.NewReader(files[p])); err != nil {
				return err
			}
		}
		return nil
	}
}
