package gitea

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/JairJosafath/theos/internal/oci/ocitest"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// TestGiteaImageIntegration attaches a two-platform image archive, with an
// attestation, to a release in the seeded Gitea, publishes it to the
// owner's container registry, reads it back by digest and publishes it
// again as a no-op, like TestGiteaIntegration.
func TestGiteaImageIntegration(t *testing.T) {
	base := os.Getenv("THEOS_GITEA_URL")
	if base == "" {
		t.Skip("THEOS_GITEA_URL not set")
	}
	base = strings.TrimSuffix(base, "/")
	creds := ports.Credentials{Username: os.Getenv("THEOS_GITEA_USERNAME"), Token: os.Getenv("THEOS_GITEA_TOKEN")}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	p, err := Client{}.Provider(ports.Config{URL: base, Owner: "webshop", Credentials: creds})
	if err != nil {
		t.Fatal(err)
	}
	repoName := fmt.Sprintf("it-image-%d", time.Now().UnixNano())
	t.Cleanup(func() { deleteRepo(t, base, "webshop", repoName, creds.Token) })
	repo, err := p.EnsureRepository(ctx, repoName, "integration test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		packageCall(t, context.Background(), http.MethodDelete,
			base+"/api/v1/packages/webshop/container/"+url.PathEscape(repoName)+"/v0.1.0", creds)
	})
	commit, err := p.PushInitialCommit(ctx, repo, scaffold.Tree{"README.md": {Data: []byte("# image\n")}},
		"Scaffold\n", testAuthor)
	if err != nil {
		t.Fatal(err)
	}
	id := createRelease(t, base, creds.Token, repo, "v0.1.0", commit)
	archive, digest := ocitest.TwoPlatforms(t)
	uploadAsset(t, base, creds.Token, repo, id, repoName+"-v0.1.0.oci.tar", string(archive))

	got, err := p.PublishImage(ctx, repo, "v0.1.0")
	if err != nil || got.Outcome != ports.ImagePublished || got.Digest != digest ||
		got.Repository != repo.ImageRegistry+"/"+repoName || !slices.Equal(got.Platforms, ocitest.Platforms) {
		t.Fatalf("PublishImage() = %+v, %v; want Published %s at %s/%s", got, err, digest, repo.ImageRegistry, repoName)
	}

	// Read back by digest: the index with both platforms and the attestation.
	ref, err := name.NewDigest(got.Repository+"@"+digest, name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := remote.Index(ref, remote.WithContext(ctx),
		remote.WithAuth(&authn.Basic{Username: creds.Username, Password: creds.Token}))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := idx.IndexManifest()
	if err != nil || len(manifest.Manifests) != 3 {
		t.Fatalf("the index by digest = %+v, %v; want two platforms and an attestation", manifest, err)
	}
	if d, err := p.ImageDigest(ctx, repo, "v0.1.0"); err != nil || d != digest {
		t.Errorf("ImageDigest() = %q, %v; want %s", d, err, digest)
	}
	if linked := packageRepository(ctx, t, base, repoName, creds); linked != repoName {
		t.Errorf("the container package is linked to %q, want %s", linked, repoName)
	}

	again, err := p.PublishImage(ctx, repo, "v0.1.0")
	if err != nil || again.Outcome != ports.ImagePublished || again.Digest != digest {
		t.Fatalf("PublishImage() again = %+v, %v; want Published %s", again, err, digest)
	}
}

// packageRepository is the repository webshop's container package name is
// linked to, "" when none.
func packageRepository(ctx context.Context, t *testing.T, base, pkgName string, as ports.Credentials) string {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		base+"/api/v1/packages/webshop/container/"+url.PathEscape(pkgName)+"/v0.1.0", nil)
	req.Header.Set("Authorization", "token "+as.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var pkg struct {
		Repository *struct {
			Name string `json:"name"`
		} `json:"repository"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&pkg) != nil {
		t.Fatalf("reading the container package %s: HTTP %d", pkgName, resp.StatusCode)
	}
	if pkg.Repository == nil {
		return ""
	}
	return pkg.Repository.Name
}
