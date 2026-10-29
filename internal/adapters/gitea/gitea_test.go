package gitea

import (
	"context"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/c4factory/internal/ports"
)

// testAuthor signs the commits the tests push.
var testAuthor = ports.Signature{Name: "Test", Email: "test@example.com", When: time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)}

// fakeGitea answers the two API calls EnsureRepository makes, each with token.
func fakeGitea(t *testing.T, token string, createStatus int, createBody string) (*httptest.Server, *[]string) {
	t.Helper()
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if got := r.Header.Get("Authorization"); got != "token "+token {
			t.Errorf("Authorization = %q, want the token", got)
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/orgs/webshop/repos":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"name":"orders-api"`) || !strings.Contains(string(body), `"auto_init":false`) {
				t.Errorf("create body = %s", body)
			}
			w.WriteHeader(createStatus)
			_, _ = io.WriteString(w, createBody)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/webshop/orders-api":
			_, _ = io.WriteString(w, `{"name":"orders-api","empty":true}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestEnsureRepository(t *testing.T) {
	for _, status := range []int{http.StatusCreated, http.StatusConflict} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			token := rand.Text()
			srv, calls := fakeGitea(t, token, status, `{}`)
			p, err := Client{}.Provider(ports.Config{URL: srv.URL + "/", Owner: "webshop", Credentials: ports.Credentials{Token: token}})
			if err != nil {
				t.Fatal(err)
			}
			repo, err := p.EnsureRepository(context.Background(), "orders-api", "Orders API")
			if err != nil {
				t.Fatalf("EnsureRepository() error = %v", err)
			}
			if repo.WebURL != srv.URL+"/webshop/orders-api" || repo.CloneURL != srv.URL+"/webshop/orders-api.git" {
				t.Errorf("repo = %+v", repo)
			}
			if len(*calls) != 2 {
				t.Errorf("calls = %v, want create then read", *calls)
			}
		})
	}
}

func TestEnsureRepositoryErrors(t *testing.T) {
	tests := []struct {
		status int
		body   string
		want   string
	}{
		{http.StatusNotFound, `{"message":"not found"}`, `organization "webshop" not found`},
		{http.StatusForbidden, `{"message":"token does not have required scope"}`, "token does not have required scope"},
		{http.StatusUnprocessableEntity, `{"message":"invalid name"}`, "HTTP 422): invalid name"},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			token := rand.Text()
			srv, _ := fakeGitea(t, token, tt.status, tt.body)
			p, err := Client{}.Provider(ports.Config{URL: srv.URL, Owner: "webshop", Credentials: ports.Credentials{Token: token}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.EnsureRepository(context.Background(), "orders-api", "")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("EnsureRepository() error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestProviderConfig(t *testing.T) {
	if _, err := (Client{}).Provider(ports.Config{Kind: "github", URL: "https://github.com", Owner: "x"}); err == nil {
		t.Error("an unsupported provider was accepted")
	}
	if _, err := (Client{}).Provider(ports.Config{URL: "gitea:3000", Owner: "x"}); err == nil {
		t.Error("a URL without scheme was accepted")
	}
	if _, err := (Client{}).Provider(ports.Config{URL: "http://gitea:3000"}); err == nil {
		t.Error("a missing owner was accepted")
	}
}

func TestRepositoryGoProxy(t *testing.T) {
	for _, tt := range []struct{ url, want string }{
		{"http://gitea.gitea.svc.cluster.local:3000", "http://gitea.gitea.svc.cluster.local:3000/api/packages/apps/go"},
		{"https://git.example.com/gitea/", "https://git.example.com/gitea/api/packages/apps/go"},
	} {
		p, err := Client{}.Provider(ports.Config{URL: tt.url, Owner: "apps", Credentials: ports.Credentials{Token: "t"}})
		if err != nil {
			t.Fatal(err)
		}
		if got := p.Repository("image-shrink").GoProxy; got != tt.want {
			t.Errorf("GoProxy for %s = %q, want %q", tt.url, got, tt.want)
		}
	}
}
