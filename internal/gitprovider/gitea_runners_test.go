package gitprovider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// ownerRunners is a Gitea at handler, as the webshop organization's runners.
func ownerRunners(t *testing.T, handler http.HandlerFunc) OwnerRunners {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p, err := Client{}.Provider(Config{URL: srv.URL, Owner: "webshop", Credentials: Credentials{Token: "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	runners, ok := p.(OwnerRunners)
	if !ok {
		t.Fatal("the Gitea provider has no OwnerRunners")
	}
	return runners
}

func TestRunnerRegistrationToken(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		wantToken string
		wantErr   string
	}{
		{name: "the organization's token", status: http.StatusOK, body: `{"token":"reg-123"}`, wantToken: "reg-123"},
		{name: "no organization", status: http.StatusNotFound, body: `{"message":"org not found"}`,
			wantErr: `"webshop" failed (HTTP 404): org not found`},
		{name: "not its owner", status: http.StatusForbidden, body: `{"message":"forbidden"}`, wantErr: "HTTP 403"},
		{name: "no token", status: http.StatusOK, body: `{}`, wantErr: "no token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			p := ownerRunners(t, func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization"))
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			token, err := p.RunnerRegistrationToken(context.Background())
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("RunnerRegistrationToken() error = %v, want one with %q", err, tc.wantErr)
				}
			} else if err != nil || token != tc.wantToken {
				t.Errorf("RunnerRegistrationToken() = %q, %v; want %q", token, err, tc.wantToken)
			}
			want := []string{"POST /api/v1/orgs/webshop/actions/runners/registration-token token secret"}
			if !reflect.DeepEqual(calls, want) {
				t.Errorf("calls = %q, want %q", calls, want)
			}
		})
	}
}

func TestRunners(t *testing.T) {
	page := func(n int, from int) string {
		runners := make([]string, 0, n)
		for i := range n {
			runners = append(runners, fmt.Sprintf(`{"id":%d,"name":"runner-%d","status":"offline"}`, from+i, from+i))
		}
		return `{"runners":[` + strings.Join(runners, ",") + `]}`
	}
	for _, tc := range []struct {
		name    string
		pages   map[string]string
		status  int
		want    []Runner
		wantLen int
		wantErr string
	}{
		{name: "none", pages: map[string]string{"1": `{"runners":[],"total_count":0}`}},
		{name: "their states and labels",
			pages: map[string]string{"1": `{"runners":[
				{"id":1,"name":"go-a","status":"online","busy":false,"ephemeral":true,"labels":[{"id":0,"name":"go:host","type":"custom"}]},
				{"id":2,"name":"go-b","status":"idle","busy":false,"ephemeral":true},
				{"id":3,"name":"go-c","status":"active","busy":true,"ephemeral":true},
				{"id":4,"name":"go-d","status":"offline","busy":false,"ephemeral":true},
				{"id":5,"name":"go-e","status":"offline","busy":true,"ephemeral":false}],"total_count":5}`},
			want: []Runner{
				{ID: 1, Name: "go-a", Labels: []string{"go:host"}, Ephemeral: true, Online: true},
				{ID: 2, Name: "go-b", Ephemeral: true, Online: true},
				{ID: 3, Name: "go-c", Ephemeral: true, Online: true, Busy: true},
				{ID: 4, Name: "go-d", Ephemeral: true},
				{ID: 5, Name: "go-e", Busy: true},
			}},
		{name: "paged", pages: map[string]string{"1": page(issuePageSize, 1), "2": page(2, issuePageSize+1)},
			wantLen: issuePageSize + 2},
		{name: "a runner that moved up a page between reads, once",
			pages:   map[string]string{"1": page(issuePageSize, 1), "2": page(2, issuePageSize)},
			wantLen: issuePageSize + 1},
		{name: "no organization", status: http.StatusNotFound, wantErr: "HTTP 404"},
		{name: "not its owner", status: http.StatusForbidden, wantErr: "HTTP 403"},
		{name: "not JSON", pages: map[string]string{"1": `<html>`}, wantErr: "listing the runners"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var pages []string
			p := ownerRunners(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/orgs/webshop/actions/runners" {
					http.NotFound(w, r)
					return
				}
				pages = append(pages, r.URL.Query().Get("page"))
				if tc.status != 0 {
					w.WriteHeader(tc.status)
					return
				}
				_, _ = w.Write([]byte(tc.pages[r.URL.Query().Get("page")]))
			})
			runners, err := p.Runners(context.Background())
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("Runners() error = %v, want one with %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.want != nil && !reflect.DeepEqual(runners, tc.want) {
				t.Errorf("Runners() = %+v\nwant %+v", runners, tc.want)
			}
			if tc.wantLen != 0 && len(runners) != tc.wantLen {
				t.Errorf("Runners() returned %d, want %d", len(runners), tc.wantLen)
			}
			if len(pages) != len(tc.pages) {
				t.Errorf("read pages %v, want %d", pages, len(tc.pages))
			}
		})
	}

	t.Run("stops after its last page", func(t *testing.T) {
		var pages int
		p := ownerRunners(t, func(w http.ResponseWriter, r *http.Request) {
			pages++
			_, _ = w.Write([]byte(page(issuePageSize, pages*issuePageSize)))
		})
		runners, err := p.Runners(context.Background())
		if err != nil || len(runners) != runnerPages*issuePageSize || pages != runnerPages {
			t.Errorf("Runners() = %d runners from %d pages, %v; want %d from %d", len(runners), pages, err,
				runnerPages*issuePageSize, runnerPages)
		}
	})
}

func TestDeleteRunner(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		wantErr string
	}{
		{name: "deleted", status: http.StatusNoContent},
		{name: "gone already", status: http.StatusNotFound},
		{name: "not its owner", status: http.StatusForbidden, wantErr: "deleting runner 7 of \"webshop\" failed (HTTP 403)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			p := ownerRunners(t, func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.Method+" "+r.URL.Path)
				w.WriteHeader(tc.status)
			})
			err := p.DeleteRunner(context.Background(), 7)
			if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Errorf("DeleteRunner() error = %v, want %q", err, tc.wantErr)
			}
			if want := []string{"DELETE /api/v1/orgs/webshop/actions/runners/7"}; !reflect.DeepEqual(calls, want) {
				t.Errorf("calls = %q, want %q", calls, want)
			}
		})
	}
}
