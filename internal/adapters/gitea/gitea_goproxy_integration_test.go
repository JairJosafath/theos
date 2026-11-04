package gitea

import (
	"archive/zip"
	"bytes"
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
)

// TestGiteaGoProxyIntegration checks what an agent's pod relies on to fetch
// the factory's released Go modules: the owner's Go package registry serves a
// module to the go command as a restricted agent account, through the .netrc
// theos-agent writes, and answers a module it lacks with a 404, so the go command
// goes on to the next proxy. Uploading needs the admin's password: the seeded
// token has no package scope. Like TestGiteaIntegration.
func TestGiteaGoProxyIntegration(t *testing.T) {
	base := os.Getenv("THEOS_GITEA_URL")
	if base == "" {
		t.Skip("THEOS_GITEA_URL not set")
	}
	password := os.Getenv("THEOS_GITEA_PASSWORD")
	if password == "" {
		t.Skip("THEOS_GITEA_PASSWORD not set")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command")
	}
	creds := ports.Credentials{Username: os.Getenv("THEOS_GITEA_USERNAME"), Token: os.Getenv("THEOS_GITEA_TOKEN")}
	admin := ports.Credentials{Username: creds.Username, Token: password}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	p, err := Client{}.Provider(ports.Config{URL: base, Owner: "webshop", Credentials: creds})
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("it-mod-%d", time.Now().UnixNano())
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

	module := "theos.local/webshop/" + name
	putModule(t, ctx, repo.GoProxy+"/upload", admin, module, "v0.1.0")
	t.Cleanup(func() {
		packageCall(t, context.Background(), http.MethodDelete,
			strings.TrimSuffix(base, "/")+"/api/v1/packages/webshop/go/"+url.PathEscape(module)+"/v0.1.0", admin)
	})

	t.Run("the registry answers the agent", func(t *testing.T) {
		for _, tt := range []struct {
			module string
			want   int
		}{
			{module, http.StatusOK},
			// A 404, not a 401 or 403: only on 404 and 410 does the go command try
			// the next proxy, and it sends credentials over https after a first 4xx.
			{"theos.local/webshop/" + name + "-missing", http.StatusNotFound},
			{"golang.org/x/mod", http.StatusNotFound},
		} {
			got := packageCall(t, ctx, http.MethodGet, repo.GoProxy+"/"+tt.module+"/@v/list", bot)
			if got != tt.want {
				t.Errorf("GET %s/@v/list as the agent: HTTP %d, want %d", tt.module, got, tt.want)
			}
		}
	})

	// Go sends .netrc credentials over https only, so this Gitea is put behind
	// TLS, as an external one is; the in-cluster one is plain http, and the
	// go command fetches from it without credentials, which a public owner allows.
	var authorized atomic.Int32
	upstream, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, _, ok := r.BasicAuth(); ok && user == bot.Username {
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

	for _, tt := range []struct {
		name string
		repo ports.Repository
	}{{"over http", repo}, {"over https, with the .netrc", tlsRepo}} {
		t.Run("the go command fetches through it "+tt.name, func(t *testing.T) {
			home := t.TempDir()
			if err := agentrunner.WriteNetrc(home, agentrunner.Job{
				RepoURL: tt.repo.CloneURL, Username: bot.Username, Token: bot.Token,
			}); err != nil {
				t.Fatal(err)
			}
			certs := filepath.Join(home, "ca.pem")
			pemCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: front.Certificate().Raw})
			if err := os.WriteFile(certs, pemCert, 0o600); err != nil {
				t.Fatal(err)
			}
			goCmd := func(args ...string) (string, error) {
				cmd := exec.CommandContext(ctx, goBin, args...)
				cmd.Dir = home
				cmd.Env = append(os.Environ(), "HOME="+home, "GOPROXY="+tt.repo.GoProxy+",off",
					"GONOSUMDB=theos.local", "GOMODCACHE="+filepath.Join(home, "mod"), "GOFLAGS=-modcacherw",
					"GOTOOLCHAIN=local", "GOWORK=off", "GO111MODULE=on", "SSL_CERT_FILE="+certs)
				out, err := cmd.CombinedOutput()
				return string(out), err
			}
			authorized.Store(0)
			if out, err := goCmd("mod", "download", "-json", module+"@v0.1.0"); err != nil {
				t.Fatalf("go mod download %s@v0.1.0: %v\n%s", module, err, out)
			}
			// A module the registry lacks goes on to the next proxy, here none.
			out, err := goCmd("mod", "download", module+"-missing@v0.1.0")
			if err == nil || !strings.Contains(out, "GOPROXY=off") {
				t.Errorf("a missing module did not fall through to the next proxy: %v\n%s", err, out)
			}
			if strings.HasPrefix(tt.repo.GoProxy, "https:") && authorized.Load() == 0 {
				t.Error("the go command did not send the agent's credentials from its .netrc")
			}
		})
	}

	// What the pod's go command asks: theos-agent's proxy, which sends the factory's
	// modules to the registry and nothing of them to the public proxy, here a
	// fake that records what it is asked.
	t.Run("the go command fetches through theos-agent's proxy", func(t *testing.T) {
		var leaked atomic.Int32
		public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "theos.local") {
				leaked.Add(1)
			}
			http.NotFound(w, r)
		}))
		t.Cleanup(public.Close)
		proxy, err := agentrunner.NewGoProxy(agentrunner.Job{GoRegistry: repo.GoProxy,
			Username: bot.Username, Token: bot.Token})
		if err != nil {
			t.Fatal(err)
		}
		if proxy.Public, err = url.Parse(public.URL); err != nil {
			t.Fatal(err)
		}
		local := httptest.NewServer(proxy)
		t.Cleanup(local.Close)
		home := t.TempDir()
		goCmd := func(args ...string) (string, error) {
			cmd := exec.CommandContext(ctx, goBin, args...)
			cmd.Dir = home
			cmd.Env = append(os.Environ(), "HOME="+home, "GOPROXY="+local.URL+",off",
				"GONOSUMDB=theos.local", "GOMODCACHE="+filepath.Join(home, "mod"), "GOFLAGS=-modcacherw",
				"GOTOOLCHAIN=local", "GOWORK=off", "GO111MODULE=on")
			out, err := cmd.CombinedOutput()
			return string(out), err
		}
		if out, err := goCmd("mod", "download", "-json", module+"@v0.1.0"); err != nil {
			t.Fatalf("go mod download %s@v0.1.0: %v\n%s", module, err, out)
		}
		if out, err := goCmd("mod", "download", module+"-missing@v0.1.0"); err == nil {
			t.Errorf("a missing module was found:\n%s", out)
		}
		if n := leaked.Load(); n != 0 {
			t.Errorf("the public proxy was asked %d times for a factory module", n)
		}
	})
}

// putModule uploads a module zip of module at version to a Gitea Go registry.
func putModule(t *testing.T, ctx context.Context, upload string, as ports.Credentials, module, version string) {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for file, content := range map[string]string{
		"go.mod":     "module " + module + "\n\ngo 1.26.0\n",
		"lib/lib.go": "package lib\n\n// Answer is the answer.\nconst Answer = 42\n",
	} {
		f, err := w.Create(module + "@" + version + "/" + file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, upload, &buf)
	req.SetBasicAuth(as.Username, as.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("PUT %s: HTTP %d, want 201", upload, resp.StatusCode)
	}
}

// packageCall makes one request as the given account and returns its status.
func packageCall(t *testing.T, ctx context.Context, method, target string, as ports.Credentials) int {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, method, target, nil)
	req.SetBasicAuth(as.Username, as.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}
