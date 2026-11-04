package gitea

import (
	"context"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/agentrunner"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// TestGiteaNpmIntegration checks what an agent's pod relies on to install the
// factory's npm packages (docs/plans/done/app-packages.md, decision 5): a scoped
// package published to the owner's npm registry installs as the restricted
// agent account through the .npmrc theos-agent writes, over plain http as the
// in-cluster Gitea is reached, and over TLS, as an external one is, where npm
// sends the agent's token. Like TestGiteaIntegration; the publish runs
// without npm, and only the install subtests skip (#1702).
func TestGiteaNpmIntegration(t *testing.T) {
	base := os.Getenv("THEOS_GITEA_URL")
	if base == "" {
		t.Skip("THEOS_GITEA_URL not set")
	}
	creds := ports.Credentials{Username: os.Getenv("THEOS_GITEA_USERNAME"), Token: os.Getenv("THEOS_GITEA_TOKEN")}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	p, err := Client{}.Provider(ports.Config{URL: base, Owner: "webshop", Credentials: creds})
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("it-npm-%d", time.Now().UnixNano())
	t.Cleanup(func() { deleteRepo(t, base, "webshop", name, creds.Token) })
	repo, err := p.EnsureRepository(ctx, name, "integration test")
	if err != nil {
		t.Fatal(err)
	}
	bot, err := p.EnsureAgentAccess(ctx, repo, name+"-agent")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.RemoveAgentAccess(context.Background(), repo, bot.Username) })

	// The package is published as a release's tarball, the way the factory
	// publishes one. It has no dependencies, so the install reaches this
	// Gitea alone. Packages are the organisation's, not the repository's: the
	// test deletes it itself.
	commit, err := p.PushInitialCommit(ctx, repo, scaffold.Tree{
		"README.md": {Data: []byte("# " + name + "\n")},
	}, "Scaffold\n", testAuthor)
	if err != nil {
		t.Fatal(err)
	}
	id := createRelease(t, base, creds.Token, repo, "v0.1.0", commit)
	npmName := ports.DefaultNpmScope + "/" + name
	uploadAsset(t, base, creds.Token, repo, id, name+"-0.1.0.tgz", string(tarGz(t, map[string][]byte{
		"package/package.json": fmt.Appendf(nil, `{"name":%q,"version":"0.1.0","main":"index.js"}`, npmName),
		"package/index.js":     []byte("export const ui = 1;\n"),
	})))
	t.Cleanup(func() {
		target := "/api/v1/packages/webshop/npm/" + url.PathEscape(npmName) + "/0.1.0"
		if status := packageCall(t, context.Background(), http.MethodDelete, strings.TrimSuffix(base, "/")+target,
			creds); status != http.StatusNoContent {
			t.Logf("DELETE %s: HTTP %d", target, status)
		}
	})
	got, err := p.PublishReleasePackages(ctx, repo, "v0.1.0")
	if err != nil {
		t.Fatalf("PublishReleasePackages() = %+v, %v", got, err)
	}
	if len(got.Packages) != 1 || got.Packages[0].Outcome != ports.PackagePublished || got.Packages[0].Name != npmName {
		t.Fatalf("PublishReleasePackages() = %+v; want %s published", got, npmName)
	}

	// An external Gitea is reached over TLS; this one is put behind a TLS
	// front that records when npm sends the agent's token.
	var authorized atomic.Int32
	upstream, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer "+bot.Token {
			authorized.Add(1)
		}
		httputil.NewSingleHostReverseProxy(upstream).ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	tlsProvider, err := Client{}.Provider(ports.Config{URL: front.URL, Owner: "webshop", Credentials: creds})
	if err != nil {
		t.Fatal(err)
	}
	tlsRepo := tlsProvider.Repository(name)
	certs := filepath.Join(t.TempDir(), "ca.pem")
	pemCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: front.Certificate().Raw})
	if err := os.WriteFile(certs, pemCert, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name string
		repo ports.Repository
	}{{"over http", repo}, {"over https", tlsRepo}} {
		t.Run("npm installs it as the agent "+tt.name, func(t *testing.T) {
			npmBin, err := exec.LookPath("npm")
			if err != nil {
				t.Skip("npm is not on PATH; the install check needs it (#1702)")
			}
			home := t.TempDir()
			if err := agentrunner.WriteNpmrc(home, agentrunner.Job{
				RepoURL: tt.repo.CloneURL, Username: bot.Username, Token: bot.Token,
				NpmScope: tt.repo.NpmScope, NpmRegistry: tt.repo.NpmRegistry,
			}); err != nil {
				t.Fatal(err)
			}
			project := filepath.Join(home, "app")
			if err := os.MkdirAll(project, 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(project, "package.json"), []byte(`{"name":"app","private":true}`),
				0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(ctx, npmBin, "install", "--save-exact", "--no-audit", "--no-fund",
				"--ignore-scripts", npmName+"@0.1.0")
			cmd.Dir = project
			cmd.Env = append(os.Environ(), "HOME="+home, "npm_config_cache="+filepath.Join(home, ".npm-cache"),
				"npm_config_update_notifier=false", "npm_config_cafile="+certs, "NODE_EXTRA_CA_CERTS="+certs)
			authorized.Store(0)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("npm install: %v\n%s", err, out)
			}
			installed, err := os.ReadFile(filepath.Join(project, "node_modules", npmName, "package.json"))
			if err != nil {
				t.Fatalf("the package is not installed: %v", err)
			}
			if !strings.Contains(string(installed), `"version":"0.1.0"`) &&
				!strings.Contains(string(installed), `"version": "0.1.0"`) {
				t.Errorf("the installed package.json is %s", installed)
			}
			if strings.HasPrefix(tt.repo.NpmRegistry, "https:") && authorized.Load() == 0 {
				t.Error("npm did not send the agent's token from its .npmrc")
			}
		})
	}
}
