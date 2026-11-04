package gitea

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/JairJosafath/theos/internal/ports"
)

// statusHost answers the status and commit calls of repository apps/orders and
// records the POSTs to statuses.
func statusHost(t *testing.T) (ports.GitHost, *[]map[string]string) {
	t.Helper()
	var posts []map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/apps/orders/commits/abc/status":
			_, _ = io.WriteString(w, `{"state":"success","sha":"abc","total_count":1,"statuses":[{"status":"success",
"context":"check / check (pull_request)","target_url":"http://gitea/runs/1","description":"Successful"}]}`)
		case "GET /api/v1/repos/apps/orders/git/commits/def":
			_, _ = io.WriteString(w, `{"sha":"def","commit":{"tree":{"sha":"tree1"}}}`)
		case "POST /api/v1/repos/apps/orders/statuses/def":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			posts = append(posts, body)
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	p, err := Client{}.Provider(ports.Config{URL: srv.URL, Owner: "apps",
		Credentials: ports.Credentials{Username: "c4admin", Token: rand.Text()}})
	if err != nil {
		t.Fatal(err)
	}
	return p, &posts
}

// #2060: a merge commit carries the head's checks.
func TestCommitStatusesAndTree(t *testing.T) {
	p, posts := statusHost(t)
	repo := p.Repository("orders")
	ctx := context.Background()

	got, err := p.CommitStatuses(ctx, repo, "abc")
	want := []ports.CommitStatus{{Context: "check / check (pull_request)", State: "success",
		TargetURL: "http://gitea/runs/1", Description: "Successful"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("CommitStatuses() = %v, %v, want %v", got, err, want)
	}
	tree, err := p.CommitTree(ctx, repo, "def")
	if err != nil || tree != "tree1" {
		t.Errorf("CommitTree() = %q, %v", tree, err)
	}
	if _, err := p.CommitTree(ctx, repo, "missing"); err == nil {
		t.Error("CommitTree() of an unknown commit succeeded")
	}
	if err := p.SetCommitStatus(ctx, repo, "def", want[0]); err != nil {
		t.Fatalf("SetCommitStatus() error = %v", err)
	}
	if len(*posts) != 1 || (*posts)[0]["context"] != "check / check (pull_request)" || (*posts)[0]["state"] != "success" ||
		(*posts)[0]["target_url"] != "http://gitea/runs/1" || (*posts)[0]["description"] != "Successful" {
		t.Errorf("posted = %v", *posts)
	}
	if err := p.SetCommitStatus(ctx, repo, "nope", want[0]); err == nil {
		t.Error("SetCommitStatus() on an unknown commit succeeded")
	}
}
