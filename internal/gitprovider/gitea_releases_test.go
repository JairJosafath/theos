package gitprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestListMilestones(t *testing.T) {
	h, p := newIssueHost(t)
	due := time.Date(2026, 10, 12, 23, 59, 59, 0, time.UTC)
	h.milestones = []hostMilestone{
		{ID: 1, Title: "v0.1.0", State: "closed", DueOn: &due},
		{ID: 2, Title: "v0.2.0", State: "open", DueOn: &due},
		{ID: 3, Title: "Later", State: "open"},
	}
	got, err := p.ListMilestones(context.Background(), p.Repository("orders-api"))
	want := []Milestone{{ID: 2, Title: "v0.2.0", Due: due}, {ID: 3, Title: "Later"}}
	if err != nil || len(got) != len(want) {
		t.Fatalf("ListMilestones() = %+v, %v; want %+v", got, err, want)
	}
	for i := range want {
		if got[i].ID != want[i].ID || got[i].Title != want[i].Title || !got[i].Due.Equal(want[i].Due) {
			t.Errorf("ListMilestones()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestMilestoneIssues(t *testing.T) {
	h, p := newIssueHost(t)
	ctx := context.Background()
	repo := p.Repository("orders-api")
	in := func(id int64) *int64 { return &id }
	h.issues[1] = &hostIssue{giteaIssue: giteaIssue{Number: 1, State: "closed"}, milestone: in(2)}
	h.issues[2] = &hostIssue{giteaIssue: giteaIssue{Number: 2, State: "open"}, milestone: in(2)}
	h.issues[3] = &hostIssue{giteaIssue: giteaIssue{Number: 3, State: "open"}, milestone: in(3)}
	h.issues[4] = &hostIssue{giteaIssue: giteaIssue{Number: 4, State: "closed"}}
	h.issues[5] = &hostIssue{giteaIssue: giteaIssue{Number: 5, State: "closed", Title: "Prepare v0.2.0",
		Labels: []giteaLabel{{Name: "kind/release"}}}, milestone: in(2)}

	issues, err := p.MilestoneIssues(ctx, repo, 2)
	got := make([]string, 0, len(issues))
	for _, i := range issues {
		got = append(got, fmt.Sprintf("#%d %s %q %v", i.Number, i.State, i.Title, i.Labels))
	}
	want := []string{`#1 closed "" []`, `#2 open "" []`, `#5 closed "Prepare v0.2.0" [kind/release]`}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("MilestoneIssues(2) = %v, %v; want %v", got, err, want)
	}
	issues, err = p.MilestoneIssues(ctx, repo, 9)
	if err != nil || len(issues) != 0 {
		t.Errorf("MilestoneIssues(9) = %v, %v; want none", issues, err)
	}
}

func TestCloseMilestone(t *testing.T) {
	h, p := newIssueHost(t)
	ctx := context.Background()
	repo := p.Repository("orders-api")
	h.milestones = []hostMilestone{{ID: 1, Title: "v0.1.0", State: "open"}}
	for range 2 {
		if err := p.CloseMilestone(ctx, repo, 1); err != nil {
			t.Fatalf("CloseMilestone() error = %v", err)
		}
	}
	if h.milestones[0].State != "closed" {
		t.Errorf("milestone state = %s, want closed", h.milestones[0].State)
	}
	if err := p.CloseMilestone(ctx, repo, 7); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("CloseMilestone() of a missing milestone error = %v, want HTTP 404", err)
	}
}

func TestTags(t *testing.T) {
	h, p := newIssueHost(t)
	ctx := context.Background()
	repo := p.Repository("orders-api")

	if sha, err := p.GetTag(ctx, repo, "v0.2.0"); err != nil || sha != "" {
		t.Fatalf("GetTag() of a missing tag = %q, %v; want none", sha, err)
	}
	if err := p.CreateTag(ctx, repo, "v0.2.0", "694db57", "Release v0.2.0"); err != nil {
		t.Fatalf("CreateTag() error = %v", err)
	}
	want := map[string]string{"tag_name": "v0.2.0", "target": "694db57", "message": "Release v0.2.0"}
	if len(h.tagPosts) != 1 || !reflect.DeepEqual(h.tagPosts[0], want) {
		t.Errorf("tag requests = %v, want [%v]", h.tagPosts, want)
	}
	if sha, err := p.GetTag(ctx, repo, "v0.2.0"); err != nil || sha != "694db57" {
		t.Errorf("GetTag() = %q, %v; want 694db57", sha, err)
	}
	if err := p.CreateTag(ctx, repo, "v0.2.0", "1cedb81", "Release v0.2.0"); err == nil || !strings.Contains(err.Error(), "HTTP 409") {
		t.Errorf("CreateTag() of an existing tag error = %v, want HTTP 409", err)
	}
	if h.tags["v0.2.0"] != "694db57" {
		t.Errorf("tag v0.2.0 = %s, want it left at 694db57", h.tags["v0.2.0"])
	}
}

func TestTagRuns(t *testing.T) {
	var queries []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/repos/webshop/orders-api/actions/runs" {
			http.NotFound(w, r)
			return
		}
		queries = append(queries, r.URL.Query())
		// Gitea lists newest first; the check run on main shares the tag's commit.
		_, _ = w.Write([]byte(`{"total_count":4,"workflow_runs":[
			{"id":9,"path":"release.yaml@refs/tags/v0.2.0","head_sha":"694db57","status":"completed","conclusion":"failure","html_url":"http://gitea/webshop/orders-api/actions/runs/9"},
			{"id":8,"path":"notes.yaml@refs/tags/v0.2.0","head_sha":"694db57","status":"queued","conclusion":"","html_url":"http://gitea/webshop/orders-api/actions/runs/8"},
			{"id":7,"path":"check.yaml@refs/heads/main","head_sha":"694db57","status":"completed","conclusion":"success","html_url":"http://gitea/webshop/orders-api/actions/runs/7"},
			{"id":6,"path":"release.yaml@refs/tags/v0.2.0","head_sha":"1cedb81","status":"completed","conclusion":"success","html_url":"http://gitea/webshop/orders-api/actions/runs/6"}]}`))
	}))
	t.Cleanup(srv.Close)
	p, err := Client{}.Provider(Config{URL: srv.URL, Owner: "webshop", Credentials: Credentials{Token: "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	runs, err := p.TagRuns(context.Background(), p.Repository("orders-api"), "v0.2.0", "694db57")
	if err != nil {
		t.Fatalf("TagRuns() error = %v", err)
	}
	want := []WorkflowRun{
		{ID: 9, Workflow: "release.yaml", Status: "completed", Conclusion: "failure",
			URL: "http://gitea/webshop/orders-api/actions/runs/9"},
		{ID: 8, Workflow: "notes.yaml", Status: "queued", URL: "http://gitea/webshop/orders-api/actions/runs/8"},
	}
	if !reflect.DeepEqual(runs, want) {
		t.Errorf("TagRuns() = %+v, want %+v", runs, want)
	}
	if len(queries) != 1 || queries[0].Get("event") != "push" || queries[0].Get("head_sha") != "694db57" {
		t.Errorf("queries = %v, want one for push runs at 694db57", queries)
	}

	if _, err := p.TagRuns(context.Background(), p.Repository("payments"), "v0.2.0", "694db57"); err == nil ||
		!strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("TagRuns() of a missing repository error = %v, want HTTP 404", err)
	}
}

func TestReleases(t *testing.T) {
	const asset = "#!/bin/sh\necho orders\n"
	var patches []map[string]string
	var downloadAuth string
	body := "Built by the Crew."
	mux := http.NewServeMux()
	const repo = "/api/v1/repos/webshop/orders-api"
	mux.HandleFunc("GET "+repo+"/releases/tags/v0.2.0", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"id":12,"tag_name":"v0.2.0","name":"v0.2.0","body":%q,"draft":false,
			"html_url":"http://gitea/webshop/orders-api/releases/tag/v0.2.0","assets":[
			{"id":1,"name":"orders","size":%d,"uuid":"c863-0fd1","browser_download_url":"http://elsewhere/x"},
			{"id":2,"name":"SHA256SUMS","size":3,"uuid":"short"},
			{"id":3,"name":"gone","size":3,"uuid":"gone"}]}`, body, len(asset))
	})
	mux.HandleFunc("PATCH "+repo+"/releases/12", func(w http.ResponseWriter, r *http.Request) {
		var p map[string]string
		_ = json.NewDecoder(r.Body).Decode(&p)
		patches = append(patches, p)
		body = p["body"]
		_, _ = w.Write([]byte(`{"id":12}`))
	})
	mux.HandleFunc("GET /attachments/c863-0fd1", func(w http.ResponseWriter, r *http.Request) {
		downloadAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(asset))
	})
	mux.HandleFunc("GET /attachments/short", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("abcdef"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	p, err := Client{}.Provider(Config{URL: srv.URL, Owner: "webshop", Credentials: Credentials{Token: "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	orders := p.Repository("orders-api")

	if rel, err := p.GetRelease(ctx, orders, "v0.1.0"); err != nil || rel != nil {
		t.Errorf("GetRelease() of a missing release = %+v, %v; want none", rel, err)
	}
	rel, err := p.GetRelease(ctx, orders, "v0.2.0")
	if err != nil || rel == nil {
		t.Fatalf("GetRelease() = %+v, %v", rel, err)
	}
	want := Release{ID: 12, Tag: "v0.2.0", Name: "v0.2.0", Body: "Built by the Crew.",
		URL: "http://gitea/webshop/orders-api/releases/tag/v0.2.0", Assets: []ReleaseAsset{
			{ID: 1, Name: "orders", Size: int64(len(asset)), UUID: "c863-0fd1"},
			{ID: 2, Name: "SHA256SUMS", Size: 3, UUID: "short"},
			{ID: 3, Name: "gone", Size: 3, UUID: "gone"}}}
	if !reflect.DeepEqual(*rel, want) {
		t.Fatalf("GetRelease() = %+v, want %+v", *rel, want)
	}

	r, size, err := p.OpenReleaseAsset(ctx, orders, rel.Assets[0])
	if err != nil {
		t.Fatalf("OpenReleaseAsset() error = %v", err)
	}
	data, _ := io.ReadAll(r)
	_ = r.Close()
	if string(data) != asset || size != int64(len(asset)) {
		t.Errorf("OpenReleaseAsset() = %q, %d; want %q, %d", data, size, asset, len(asset))
	}
	if downloadAuth != "token secret" {
		t.Errorf("download Authorization = %q, want the provider's token", downloadAuth)
	}
	if _, _, err := p.OpenReleaseAsset(ctx, orders, rel.Assets[1]); err == nil || !strings.Contains(err.Error(), "6 bytes") {
		t.Errorf("OpenReleaseAsset() of a file of another size error = %v, want its size", err)
	}
	if _, _, err := p.OpenReleaseAsset(ctx, orders, rel.Assets[2]); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("OpenReleaseAsset() of a missing file error = %v, want HTTP 404", err)
	}

	if err := p.SetReleaseBody(ctx, orders, 12, "- Add orders (#3)\n"); err != nil {
		t.Fatalf("SetReleaseBody() error = %v", err)
	}
	if len(patches) != 1 || !reflect.DeepEqual(patches[0], map[string]string{"body": "- Add orders (#3)\n"}) {
		t.Errorf("release patches = %v, want one setting only the body", patches)
	}
	if err := p.SetReleaseBody(ctx, orders, 13, "notes"); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("SetReleaseBody() of a missing release error = %v, want HTTP 404", err)
	}
}

// TestReleaseAssetWithoutLength reads downloads sent without Content-Length, as
// a gzipping host or proxy sends them: their size is the release's, and a body
// of another size fails as it is read.
func TestReleaseAssetWithoutLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.TrimPrefix(r.URL.Path, "/attachments/")))
		w.(http.Flusher).Flush() // chunked: no Content-Length
	}))
	t.Cleanup(srv.Close)
	p, err := Client{}.Provider(Config{URL: srv.URL, Owner: "webshop"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body    string
		size    int64
		wantErr string
	}{
		{body: "abcdef", size: 6},
		{body: "abc", size: 6, wantErr: "3 bytes short"},
		{body: "abcdefgh", size: 6, wantErr: "longer than the release says"},
	} {
		r, size, err := p.OpenReleaseAsset(context.Background(), p.Repository("orders-api"),
			ReleaseAsset{Name: "orders", Size: tc.size, UUID: tc.body})
		if err != nil || size != tc.size {
			t.Fatalf("OpenReleaseAsset(%s) = %d, %v; want size %d", tc.body, size, err, tc.size)
		}
		data, err := io.ReadAll(r)
		_ = r.Close()
		switch {
		case tc.wantErr == "" && (err != nil || string(data) != tc.body):
			t.Errorf("reading %s = %q, %v; want it all", tc.body, data, err)
		case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
			t.Errorf("reading %s error = %v, want %q", tc.body, err, tc.wantErr)
		}
	}
}
