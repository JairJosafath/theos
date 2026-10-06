package gitprovider

import (
	"context"
	"fmt"
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
