package controller

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	githubdeploy "github.com/JairJosafath/c4factory/internal/adapters/github"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/oci"
	"github.com/JairJosafath/c4factory/internal/oci/ocitest"
	"github.com/JairJosafath/c4factory/internal/ports"
)

// TestGitHubDeployerImage: with a packagesToken, the github member copies
// the release's image to GHCR as a ship step, by digest, before the release
// goes out, names it in the notes and reports it; a repeat copies nothing,
// another digest under the tag is TagTaken, and without the token no image
// is copied and the release ships (#1600).
func TestGitHubDeployerImage(t *testing.T) {
	ctx := context.Background()
	owner, ghcr := newFakeRegistry(), newFakeRegistry()
	t.Cleanup(owner.server.Close)
	t.Cleanup(ghcr.server.Close)
	gh := newFakeGitHub()
	t.Cleanup(gh.server.Close)
	d := githubdeploy.New(gh, githubdeploy.WithImageRegistry(ghcr.host))

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
			CopyImage: func(ctx context.Context, to ports.ImageRegistry, repository, tag string) error {
				return oci.Copy(ctx, oci.Registry{Host: owner.host}, repo, digest,
					oci.Registry{Host: to.Host, Username: to.Username, Password: to.Password}, repository, tag)
			}}, digest
	}
	target := func(repository, tagPrefix string, packages bool) ports.Target {
		gh.addRepository(repository, "t0ken")
		secret := map[string][]byte{"token": []byte("t0ken")}
		if packages {
			secret[model.GitHubSecretPackagesToken] = []byte("pkg-" + strings.Repeat("x", 8))
		}
		return ports.Target{Name: "ns/prod", Member: model.DeployMemberGitHub, Secret: secret,
			Settings: map[string]string{model.GitHubSettingRepository: repository,
				model.GitHubSettingTagPrefix: tagPrefix}}
	}

	t.Run("the image reaches GHCR by digest, the notes name it and State reports it", func(t *testing.T) {
		tg := target("Acme/Shop", "orders-api-", true)
		s, digest := pushed(t, "ns/orders-api", "v1.0.0")
		shipUntilDeployed(t, d, tg, s)
		want := ghcr.host + "/acme/shop/orders-api@" + digest
		if got, err := oci.Resolve(ctx, oci.Registry{Host: ghcr.host}, "acme/shop/orders-api", "v1.0.0"); err != nil ||
			got != digest {
			t.Fatalf("GHCR's v1.0.0 = %q, %v; want %s", got, err, digest)
		}
		rels := gh.releases("Acme/Shop")
		if len(rels) != 1 || rels[0].Draft || !strings.Contains(rels[0].Body, "Image: `"+want+"`") {
			t.Fatalf("releases = %+v, want one published naming %s", rels, want)
		}
		st, err := d.State(ctx, tg, s.App, "v1.0.0")
		if err != nil || st.Image != want {
			t.Fatalf("State() = %+v, %v; want image %s", st, err, want)
		}
		down := newFakeRegistry()
		down.server.Close()
		st, err = githubdeploy.New(gh, githubdeploy.WithImageRegistry(down.host)).State(ctx, tg, s.App, "v1.0.0")
		if err != nil || st.Phase != model.DeployPhaseDeployed || st.Image != "" {
			t.Fatalf("State() with GHCR down = %+v, %v; want Deployed, no image", st, err)
		}
		puts := ghcr.puts.Load()
		if err := d.Ship(ctx, tg, s); err != nil {
			t.Fatalf("Ship() again = %v", err)
		}
		if got := ghcr.puts.Load(); got != puts {
			t.Errorf("a repeat Ship wrote %d manifests to GHCR, want none", got-puts)
		}
	})

	t.Run("an empty tagPrefix gives the image the repository's name", func(t *testing.T) {
		tg := target("acme/orders-web", "", true)
		s, digest := pushed(t, "ns/orders-web", "v2.0.0")
		shipUntilDeployed(t, d, tg, s)
		if got, err := oci.Resolve(ctx, oci.Registry{Host: ghcr.host}, "acme/orders-web", "v2.0.0"); err != nil ||
			got != digest {
			t.Fatalf("GHCR's v2.0.0 = %q, %v; want %s", got, err, digest)
		}
	})

	t.Run("another digest under the tag is TagTaken, naming the image", func(t *testing.T) {
		tg := target("acme/billing", "billing-", true)
		other, _ := pushed(t, "ns/billing-old", "v1.0.0")
		if err := other.CopyImage(ctx, ports.ImageRegistry{Host: ghcr.host}, "acme/billing/billing", "v1.0.0"); err != nil {
			t.Fatalf("copy: %v", err)
		}
		s, _ := pushed(t, "ns/billing", "v1.0.0")
		err := d.Ship(ctx, tg, s)
		if gitprovider.ReleaseReason(err) != gitprovider.ReasonTagTaken ||
			!strings.Contains(err.Error(), ghcr.host+"/acme/billing/billing:v1.0.0") {
			t.Fatalf("Ship() = %v, want TagTaken naming the image", err)
		}
		if rels := gh.releases("acme/billing"); len(rels) != 0 {
			t.Errorf("releases = %+v, want none", rels)
		}
	})

	t.Run("without a packagesToken no image is copied and the release ships", func(t *testing.T) {
		tg := target("acme/stock", "stock-", false)
		s, _ := pushed(t, "ns/stock", "v1.0.0")
		s.Files = []ports.File{{Name: "stock.tar.gz", Size: 3,
			Open: func(context.Context) (io.ReadCloser, int64, error) {
				return io.NopCloser(strings.NewReader("bin")), 3, nil
			}}}
		puts := ghcr.puts.Load()
		shipUntilDeployed(t, d, tg, s)
		if got := ghcr.puts.Load(); got != puts {
			t.Errorf("GHCR got %d manifests, want none", got-puts)
		}
		st, err := d.State(ctx, tg, s.App, "v1.0.0")
		if err != nil || st.Image != "" {
			t.Errorf("State() = %+v, %v; want no image", st, err)
		}
	})

	t.Run("a copy that fails to write is unreachable", func(t *testing.T) {
		tg := target("acme/refused", "x-", true)
		s, _ := pushed(t, "ns/refused", "v1.0.0")
		s.CopyImage = func(context.Context, ports.ImageRegistry, string, string) error {
			return errors.New("boom")
		}
		if err := d.Ship(ctx, tg, s); !errors.Is(err, ports.ErrUnreachable) {
			t.Errorf("Ship() with a failing copy = %v, want ErrUnreachable", err)
		}
	})
}
