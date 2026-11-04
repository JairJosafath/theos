package agentrunner

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// upstream is a fake proxy that records the paths and credentials it was asked
// for and answers every one with status.
type upstream struct {
	*httptest.Server
	mu    sync.Mutex
	paths []string
	auth  []string
}

func newUpstream(t *testing.T, tls bool, status int, body string) *upstream {
	u := &upstream{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _, _ := r.BasicAuth()
		u.mu.Lock()
		u.paths = append(u.paths, r.URL.Path)
		u.auth = append(u.auth, user)
		u.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	})
	if tls {
		u.Server = httptest.NewTLSServer(handler)
	} else {
		u.Server = httptest.NewServer(handler)
	}
	t.Cleanup(u.Close)
	return u
}

func (u *upstream) asked() ([]string, []string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return slices.Clone(u.paths), slices.Clone(u.auth)
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestGoProxyRoutes(t *testing.T) {
	for _, tt := range []struct {
		name, path   string
		wantRegistry string // the path the registry is asked; empty: the public proxy is asked
		wantPublic   string
	}{
		{name: "a factory module", path: "/theos.local/webshop/orders/@v/list",
			wantRegistry: "/api/packages/webshop/go/theos.local/webshop/orders/@v/list"},
		{name: "a factory module's zip", path: "/theos.local/webshop/orders/@v/v0.1.0.zip",
			wantRegistry: "/api/packages/webshop/go/theos.local/webshop/orders/@v/v0.1.0.zip"},
		{name: "the factory's prefix alone", path: "/theos.local/@v/list",
			wantRegistry: "/api/packages/webshop/go/theos.local/@v/list"},
		{name: "a public module", path: "/golang.org/x/mod/@v/list", wantPublic: "/golang.org/x/mod/@v/list"},
		{name: "a module named like the factory", path: "/theos.localhost/x/@v/list",
			wantPublic: "/theos.localhost/x/@v/list"},
		{name: "the checksum database", path: "/sumdb/sum.golang.org/supported",
			wantPublic: "/sumdb/sum.golang.org/supported"},
		{name: "a path that steps out of a public one", path: "/golang.org/../theos.local/webshop/orders/@v/list",
			wantRegistry: "/api/packages/webshop/go/theos.local/webshop/orders/@v/list"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			registry := newUpstream(t, false, http.StatusOK, "v0.1.0\n")
			public := newUpstream(t, false, http.StatusOK, "v0.2.0\n")
			proxy := &GoProxy{Registry: mustURL(t, registry.URL+"/api/packages/webshop/go"),
				Public: mustURL(t, public.URL), Username: "bot", Token: "secret"}
			req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3127/", nil)
			req.URL.Path = tt.path
			rec := httptest.NewRecorder()
			proxy.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("HTTP %d: %s", rec.Code, rec.Body)
			}
			regPaths, regAuth := registry.asked()
			pubPaths, _ := public.asked()
			if tt.wantRegistry != "" {
				if !slices.Equal(regPaths, []string{tt.wantRegistry}) || len(pubPaths) != 0 {
					t.Errorf("registry asked %v, public asked %v; want only the registry, %s", regPaths, pubPaths, tt.wantRegistry)
				}
				if regAuth[0] != "" {
					t.Errorf("credentials sent to the registry over http: %q", regAuth[0])
				}
				return
			}
			if !slices.Equal(pubPaths, []string{tt.wantPublic}) || len(regPaths) != 0 {
				t.Errorf("public asked %v, registry asked %v; want only the public proxy, %s", pubPaths, regPaths, tt.wantPublic)
			}
		})
	}
}

// A factory module the registry lacks is a 404 to the go command, which goes on
// to direct; the public proxy is never asked. Credentials go to the registry
// over https only, and never to the public proxy.
func TestGoProxyMissingFactoryModule(t *testing.T) {
	registry := newUpstream(t, true, http.StatusNotFound, "not found")
	public := newUpstream(t, false, http.StatusOK, "v0.1.0\n")
	proxy := &GoProxy{Registry: mustURL(t, registry.URL+"/api/packages/webshop/go"),
		Public: mustURL(t, public.URL), Username: "bot", Token: "secret", Client: registry.Client()}
	for _, p := range []string{"/theos.local/webshop/orders/@v/list", "/golang.org/x/mod/@v/list"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3127"+p, nil)
		proxy.ServeHTTP(rec, req)
		want := http.StatusOK
		if IsFactoryModule(p) {
			want = http.StatusNotFound
		}
		if rec.Code != want {
			t.Errorf("GET %s: HTTP %d, want %d", p, rec.Code, want)
		}
	}
	_, regAuth := registry.asked()
	if !slices.Equal(regAuth, []string{"bot"}) {
		t.Errorf("registry got credentials %v, want the job's over https", regAuth)
	}
	pubPaths, pubAuth := public.asked()
	if !slices.Equal(pubPaths, []string{"/golang.org/x/mod/@v/list"}) || pubAuth[0] != "" {
		t.Errorf("public proxy asked %v with %v, want only the public module, without credentials", pubPaths, pubAuth)
	}
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "http://127.0.0.1:3127/theos.local/x/@v/list", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: HTTP %d, want 405", rec.Code)
	}
}

// The go command, given the proxy as GOPROXY, asks the public proxy for public
// modules and never for the factory's, even for one the registry lacks.
func TestGoProxyWithTheGoCommand(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command")
	}
	registry := newUpstream(t, false, http.StatusNotFound, "not found")
	public := newUpstream(t, false, http.StatusNotFound, "not found")
	proxy := httptest.NewServer(&GoProxy{Registry: mustURL(t, registry.URL+"/api/packages/webshop/go"),
		Public: mustURL(t, public.URL)})
	t.Cleanup(proxy.Close)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	home := t.TempDir()
	for _, module := range []string{"theos.local/webshop/orders@v0.1.0", "golang.org/x/mod@v0.1.0"} {
		cmd := exec.CommandContext(ctx, goBin, "mod", "download", module)
		cmd.Dir = home
		// off, not direct, after the proxy: the test has no network.
		cmd.Env = append(os.Environ(), "HOME="+home, "GOPROXY="+proxy.URL+",off", "GONOSUMDB=theos.local",
			"GOMODCACHE="+filepath.Join(home, "mod"), "GOFLAGS=-modcacherw", "GOTOOLCHAIN=local", "GOWORK=off",
			"GO111MODULE=on", "GOSUMDB=off")
		if out, err := cmd.CombinedOutput(); err == nil {
			t.Fatalf("go mod download %s found a module no upstream has:\n%s", module, out)
		}
	}
	regPaths, _ := registry.asked()
	pubPaths, _ := public.asked()
	for _, p := range pubPaths {
		if strings.Contains(p, "theos.local") {
			t.Errorf("the public proxy was asked for a factory module: %s", p)
		}
	}
	if !slices.ContainsFunc(regPaths, func(p string) bool { return strings.Contains(p, "theos.local/webshop/orders") }) {
		t.Errorf("the registry wasn't asked for the factory module: %v", regPaths)
	}
	if !slices.ContainsFunc(pubPaths, func(p string) bool { return strings.Contains(p, "golang.org/x/mod") }) {
		t.Errorf("the public proxy wasn't asked for the public module: %v", pubPaths)
	}
}

func TestNewGoProxy(t *testing.T) {
	if p, err := NewGoProxy(Job{}); p != nil || err != nil {
		t.Errorf("no registry: %v, %v; want no proxy", p, err)
	}
	job, err := JobFromEnv(env(map[string]string{EnvGoRegistry: "https://git.example.com/api/packages/apps/go"}))
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewGoProxy(job)
	if err != nil || p == nil {
		t.Fatalf("NewGoProxy: %v, %v", p, err)
	}
	if p.Registry.String() != "https://git.example.com/api/packages/apps/go" || p.Public.String() != PublicGoProxy ||
		p.Username != "bot" || p.Token != gitToken {
		t.Errorf("proxy %+v", p)
	}
}
