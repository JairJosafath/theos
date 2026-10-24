package controller

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	gitlabdeploy "github.com/JairJosafath/c4factory/internal/adapters/gitlab"
	"github.com/JairJosafath/c4factory/internal/adapters/gitlab/gitlabtest"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/oci"
	"github.com/JairJosafath/c4factory/internal/oci/ocitest"
	"github.com/JairJosafath/c4factory/internal/ports"
)

// TestGitLabDeployerImage: the gitlab member copies the release's image to
// the project's container registry as a ship step, by digest, before the
// release goes out, names it in the release and reports it; a repeat copies
// nothing, another digest under the tag is TagTaken, and a project whose
// registry is off, or whose token can't write it, ships without one (#1806).
func TestGitLabDeployerImage(t *testing.T) {
	ctx := context.Background()
	owner, reg := newFakeRegistry(), newFakeRegistry()
	t.Cleanup(owner.server.Close)
	t.Cleanup(reg.server.Close)
	gl := gitlabtest.New(t)
	d := gitlabdeploy.New(nil)

	// pushed is a new image in the owner's registry, and a Shipment of it
	// whose CopyImage copies it as the controller's does.
	pushed := func(t *testing.T, app, version string) (ports.Shipment, string) {
		t.Helper()
		archive, digest := ocitest.TwoPlatforms(t)
		repo := app
		if _, err := oci.Push(ctx, oci.Registry{Host: owner.host}, repo, version, bytes.NewReader(archive)); err != nil {
			t.Fatalf("push: %v", err)
		}
		return ports.Shipment{App: app, DisplayName: "Orders API", Version: version,
			Type: model.TypeService, Deployment: model.DeploymentK8sManifests,
			Image: owner.host + "/" + repo + "@" + digest,
			CopyImage: func(ctx context.Context, to ports.Registry, repository, tag string) error {
				return oci.Copy(ctx, oci.Registry{Host: owner.host}, repo, digest,
					oci.Registry{Host: to.Host, Username: to.Username, Password: to.Password}, repository, tag)
			}}, digest
	}
	// withFile gives s a release file, for a shipment the plan takes no
	// image of: a release ships something.
	withFile := func(s ports.Shipment) ports.Shipment {
		s.Files = []ports.File{{Name: "orders.tar.gz", Size: 3,
			Open: func(context.Context) (io.ReadCloser, int64, error) {
				return io.NopCloser(strings.NewReader("bin")), 3, nil
			}}}
		return s
	}
	target := func(path, tagPrefix string) (*gitlabtest.Project, ports.Target) {
		p := gl.AddProject(path, "glpat-"+path)
		p.RegistryHost = reg.host
		return p, ports.Target{Name: "ns/prod", Member: model.DeployMemberGitLab,
			Secret: map[string][]byte{model.GitLabSecretToken: []byte("glpat-" + path)},
			Settings: map[string]string{model.GitLabSettingURL: gl.URL(), model.GitLabSettingProject: path,
				model.GitLabSettingTagPrefix: tagPrefix}}
	}

	t.Run("the image reaches the project's registry by digest, the release names it and State reports it",
		func(t *testing.T) {
			p, tg := target("Acme/Shop", "orders-api-")
			s, digest := pushed(t, "ns/orders-api", "v1.0.0")
			shipUntilDeployed(t, d, tg, s)
			repo := "acme/shop/orders-api"
			want := reg.host + "/" + repo + "@" + digest
			if got, err := oci.Resolve(ctx, oci.Registry{Host: reg.host}, repo, "v1.0.0"); err != nil || got != digest {
				t.Fatalf("the registry's v1.0.0 = %q, %v; want %s", got, err, digest)
			}
			rels := p.Releases()
			if len(rels) != 1 || !strings.Contains(rels[0].Description, "Image: `"+want+"`") {
				t.Fatalf("releases = %+v, want one naming %s", rels, want)
			}
			st, err := d.State(ctx, tg, s.App, "v1.0.0")
			if err != nil || st.Image != want {
				t.Fatalf("State() = %+v, %v; want image %s", st, err, want)
			}
			puts := reg.puts.Load()
			if err := d.Ship(ctx, tg, s); err != nil {
				t.Fatalf("Ship() again = %v", err)
			}
			if got := reg.puts.Load(); got != puts {
				t.Errorf("a repeat Ship wrote %d manifests to the registry, want none", got-puts)
			}
			down := newFakeRegistry()
			down.server.Close()
			p.RegistryHost = down.host
			st, err = d.State(ctx, tg, s.App, "v1.0.0")
			if err != nil || st.Phase != model.DeployPhaseDeployed || st.Image != "" {
				t.Fatalf("State() with the registry down = %+v, %v; want Deployed, no image", st, err)
			}
		})

	t.Run("an empty tagPrefix gives the image the project's path", func(t *testing.T) {
		_, tg := target("acme/orders-web", "")
		s, digest := pushed(t, "ns/orders-web", "v2.0.0")
		shipUntilDeployed(t, d, tg, s)
		if got, err := oci.Resolve(ctx, oci.Registry{Host: reg.host}, "acme/orders-web", "v2.0.0"); err != nil ||
			got != digest {
			t.Fatalf("the registry's v2.0.0 = %q, %v; want %s", got, err, digest)
		}
	})

	t.Run("another digest under the tag is TagTaken, naming the image", func(t *testing.T) {
		p, tg := target("acme/billing", "billing-")
		other, _ := pushed(t, "ns/billing-old", "v1.0.0")
		if err := other.CopyImage(ctx, ports.Registry{Host: reg.host}, "acme/billing/billing", "v1.0.0"); err != nil {
			t.Fatalf("copy: %v", err)
		}
		s, _ := pushed(t, "ns/billing", "v1.0.0")
		err := d.Ship(ctx, tg, s)
		if gitprovider.ReleaseReason(err) != gitprovider.ReasonTagTaken ||
			!strings.Contains(err.Error(), reg.host+"/acme/billing/billing:v1.0.0") {
			t.Fatalf("Ship() = %v, want TagTaken naming the image", err)
		}
		if rels := p.Releases(); len(rels) != 0 {
			t.Errorf("releases = %+v, want none", rels)
		}
	})

	t.Run("a project with its registry off ships without an image", func(t *testing.T) {
		p, tg := target("acme/stock", "stock-")
		p.RegistryEnabled = false
		s, _ := pushed(t, "ns/stock", "v1.0.0")
		puts := reg.puts.Load()
		shipUntilDeployed(t, d, tg, withFile(s))
		if got := reg.puts.Load(); got != puts {
			t.Errorf("the registry got %d manifests, want none", got-puts)
		}
		st, err := d.State(ctx, tg, s.App, "v1.0.0")
		if err != nil || st.Image != "" {
			t.Errorf("State() = %+v, %v; want no image", st, err)
		}
	})

	t.Run("a token without write_registry ships without an image", func(t *testing.T) {
		p, tg := target("acme/tax", "tax-")
		p.Scopes = []string{"api"}
		s, _ := pushed(t, "ns/tax", "v1.0.0")
		puts := reg.puts.Load()
		shipUntilDeployed(t, d, tg, withFile(s))
		if got := reg.puts.Load(); got != puts {
			t.Errorf("the registry got %d manifests, want none", got-puts)
		}
		if rels := p.Releases(); len(rels) != 1 || strings.Contains(rels[0].Description, "Image:") {
			t.Errorf("releases = %+v, want one naming no image", rels)
		}
	})

	t.Run("a copy that fails to write is unreachable", func(t *testing.T) {
		_, tg := target("acme/refused", "x-")
		s, _ := pushed(t, "ns/refused", "v1.0.0")
		s.CopyImage = func(context.Context, ports.Registry, string, string) error {
			return errors.New("boom")
		}
		if err := d.Ship(ctx, tg, s); !errors.Is(err, ports.ErrUnreachable) {
			t.Errorf("Ship() with a failing copy = %v, want ErrUnreachable", err)
		}
	})
}
