package gitea

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
)

// TestGiteaActionsIntegration pins what gitea_actions.go assumes of Gitea's
// organization Actions API: a variable's PUT answers 404 until POST creates
// it and updates it after, a secret is created and updated by one PUT, and
// deleting either twice is no error. Like TestGiteaIntegration; the
// organization goes when the test ends.
func TestGiteaActionsIntegration(t *testing.T) {
	base := os.Getenv("THEOS_GITEA_URL")
	if base == "" {
		t.Skip("THEOS_GITEA_URL not set")
	}
	creds := ports.Credentials{Username: os.Getenv("THEOS_GITEA_USERNAME"), Token: os.Getenv("THEOS_GITEA_TOKEN")}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	org := fmt.Sprintf("it-actions-%d", time.Now().UnixNano())
	createOrg(t, base, org, creds.Token)
	p, err := Client{}.Provider(ports.Config{URL: base, Owner: org, Credentials: creds})
	if err != nil {
		t.Fatal(err)
	}
	actions, ok := p.(ports.OwnerActions)
	if !ok {
		t.Fatalf("%T has no OwnerActions", p)
	}
	api := strings.TrimSuffix(base, "/") + "/api/v1/orgs/" + org + "/actions/"
	get := func(target string, out any) int {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, api+target, nil)
		req.Header.Set("Authorization", "token "+creds.Token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if out != nil && resp.StatusCode == http.StatusOK {
			if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
				t.Fatalf("GET %s: %v", target, err)
			}
		}
		return resp.StatusCode
	}

	t.Run("a variable is created, updated and deleted", func(t *testing.T) {
		var variable struct {
			Data string `json:"data"`
		}
		for i, value := range []string{"@acme", "@acme-again"} {
			if err := actions.SetActionsVariable(ctx, "THEOS_NPM_SCOPE", value); err != nil {
				t.Fatalf("SetActionsVariable #%d: %v", i+1, err)
			}
			if status := get("variables/THEOS_NPM_SCOPE", &variable); status != http.StatusOK || variable.Data != value {
				t.Fatalf("after SetActionsVariable #%d, GET: HTTP %d, data %q; want %q", i+1, status, variable.Data, value)
			}
		}
		for i := range 2 {
			if err := actions.DeleteActionsVariable(ctx, "THEOS_NPM_SCOPE"); err != nil {
				t.Fatalf("DeleteActionsVariable #%d: %v", i+1, err)
			}
		}
		if status := get("variables/THEOS_NPM_SCOPE", nil); status != http.StatusNotFound {
			t.Errorf("after the delete, GET: HTTP %d, want 404", status)
		}
	})

	t.Run("a secret is created, updated and deleted", func(t *testing.T) {
		names := func() []string {
			var secrets []struct {
				Name string `json:"name"`
			}
			if status := get("secrets", &secrets); status != http.StatusOK {
				t.Fatalf("GET secrets: HTTP %d", status)
			}
			out := make([]string, 0, len(secrets))
			for _, s := range secrets {
				out = append(out, s.Name)
			}
			return out
		}
		for i, value := range []string{"read-token", "read-token-again"} {
			if err := actions.SetActionsSecret(ctx, "THEOS_NPM_TOKEN", value); err != nil {
				t.Fatalf("SetActionsSecret #%d: %v", i+1, err)
			}
			if got := names(); !slices.Contains(got, "THEOS_NPM_TOKEN") {
				t.Fatalf("after SetActionsSecret #%d, the secrets are %v", i+1, got)
			}
		}
		for i := range 2 {
			if err := actions.DeleteActionsSecret(ctx, "THEOS_NPM_TOKEN"); err != nil {
				t.Fatalf("DeleteActionsSecret #%d: %v", i+1, err)
			}
		}
		if got := names(); slices.Contains(got, "THEOS_NPM_TOKEN") {
			t.Errorf("after the delete, the secrets are %v", got)
		}
	})
}
