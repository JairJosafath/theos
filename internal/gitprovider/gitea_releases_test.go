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
	"slices"
	"strconv"
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

func TestListAllMilestones(t *testing.T) {
	h, p := newIssueHost(t)
	due := time.Date(2026, 10, 12, 23, 59, 59, 0, time.UTC)
	h.milestones = []hostMilestone{
		{ID: 1, Title: "v0.1.0", State: "closed", DueOn: &due, Description: "<!-- c4-brief: cel-lens/1 -->"},
		{ID: 2, Title: "Later", State: "open"},
	}
	got, err := p.ListAllMilestones(context.Background(), p.Repository("orders-api"))
	want := []Milestone{
		{ID: 1, Title: "v0.1.0", Due: due, Description: "<!-- c4-brief: cel-lens/1 -->", Closed: true},
		{ID: 2, Title: "Later"},
	}
	if err != nil || len(got) != len(want) {
		t.Fatalf("ListAllMilestones() = %+v, %v; want %+v", got, err, want)
	}
	for i := range want {
		if got[i].ID != want[i].ID || got[i].Title != want[i].Title || !got[i].Due.Equal(want[i].Due) ||
			got[i].Description != want[i].Description || got[i].Closed != want[i].Closed {
			t.Errorf("ListAllMilestones()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestListMilestonesPages(t *testing.T) {
	h, p := newIssueHost(t)
	for id := range int64(maxIssuePages*issuePageSize - 1) {
		h.milestones = append(h.milestones, hostMilestone{ID: id + 1, Title: fmt.Sprintf("v0.%d.0", id), State: "closed"})
	}
	got, err := p.ListAllMilestones(context.Background(), p.Repository("orders-api"))
	if err != nil || len(got) != len(h.milestones) || got[len(got)-1].ID != int64(len(h.milestones)) {
		t.Fatalf("ListAllMilestones() = %d milestones, %v; want all %d", len(got), err, len(h.milestones))
	}
	if h.milestonePages != maxIssuePages {
		t.Errorf("read %d pages, want %d", h.milestonePages, maxIssuePages)
	}
}

func TestListMilestonesFailsClosed(t *testing.T) {
	h, p := newIssueHost(t)
	for id := range int64(maxIssuePages*issuePageSize + 1) {
		h.milestones = append(h.milestones, hostMilestone{ID: id + 1, Title: fmt.Sprintf("v0.%d.0", id), State: "closed"})
	}
	got, err := p.ListAllMilestones(context.Background(), p.Repository("orders-api"))
	if err == nil || !strings.Contains(err.Error(), "listing all milestones of webshop/orders-api: 1000 or more") || got != nil {
		t.Fatalf("ListAllMilestones() = %d milestones, %v; want an error, not a list cut short", len(got), err)
	}
	if h.milestonePages != maxIssuePages {
		t.Errorf("read %d pages, want %d", h.milestonePages, maxIssuePages)
	}
}

func TestCreateMilestone(t *testing.T) {
	ctx := context.Background()
	due := time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)
	for _, tt := range []struct {
		name string
		due  time.Time
	}{{"dated", due}, {"undated", time.Time{}}} {
		t.Run(tt.name, func(t *testing.T) {
			h, p := newIssueHost(t)
			got, err := p.CreateMilestone(ctx, p.Repository("orders-api"), "v0.1.0", "Brief\n<!-- c4-brief: b/1 -->", tt.due)
			if err != nil || got.ID != 1 || got.Title != "v0.1.0" || got.Description != "Brief\n<!-- c4-brief: b/1 -->" ||
				!got.Due.Equal(tt.due) || got.Closed {
				t.Fatalf("CreateMilestone() = %+v, %v; want milestone 1, open, due %v", got, err, tt.due)
			}
			if m := h.milestones[0]; (m.DueOn == nil) != tt.due.IsZero() || (m.DueOn != nil && !m.DueOn.Equal(tt.due)) {
				t.Errorf("milestone sent due %v, want %v", m.DueOn, tt.due)
			}
		})
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
	due := time.Date(2026, 10, 14, 9, 30, 0, 0, time.UTC)
	h.milestones = []hostMilestone{{ID: 1, Title: "v0.1.0", State: "open", DueOn: &due}, {ID: 2, Title: "Later", State: "open"}}
	for range 2 {
		for id := int64(1); id <= 2; id++ {
			if err := p.CloseMilestone(ctx, repo, id); err != nil {
				t.Fatalf("CloseMilestone(%d) error = %v", id, err)
			}
		}
	}
	for _, m := range h.milestones {
		if m.State != "closed" {
			t.Errorf("milestone %s state = %s, want closed", m.Title, m.State)
		}
	}
	if d := h.milestones[0].DueOn; d == nil || !d.Equal(due) {
		t.Errorf("v0.1.0 due %v after closing, want %v kept", d, due)
	}
	if d := h.milestones[1].DueOn; d != nil {
		t.Errorf("Later due %v after closing, want none", d)
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
			{"id":9,"path":"release.yaml@refs/tags/v0.2.0","event":"push","head_sha":"694db57","status":"completed","conclusion":"failure","started_at":"2026-10-09T01:40:53Z","html_url":"http://gitea/webshop/orders-api/actions/runs/9"},
			{"id":8,"path":"notes.yaml@refs/tags/v0.2.0","event":"push","head_sha":"694db57","status":"queued","conclusion":"","started_at":"1970-01-01T00:00:00Z","html_url":"http://gitea/webshop/orders-api/actions/runs/8"},
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
		{ID: 9, Workflow: "release.yaml", Event: "push", Ref: "refs/tags/v0.2.0", Status: "completed", Conclusion: "failure",
			Started: time.Date(2026, 10, 9, 1, 40, 53, 0, time.UTC), URL: "http://gitea/webshop/orders-api/actions/runs/9"},
		{ID: 8, Workflow: "notes.yaml", Event: "push", Ref: "refs/tags/v0.2.0", Status: "queued",
			URL: "http://gitea/webshop/orders-api/actions/runs/8"},
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

func TestCommitRuns(t *testing.T) {
	var queries []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/repos/webshop/orders-api/actions/runs" {
			http.NotFound(w, r)
			return
		}
		queries = append(queries, r.URL.Query())
		// A full first page of old pushes, then the pull request's runs.
		var runs []string
		if r.URL.Query().Get("page") == "1" {
			for i := range issuePageSize {
				runs = append(runs, fmt.Sprintf(`{"id":%d,"path":"check.yaml@refs/heads/main","event":"push",`+
					`"head_sha":"694db57","status":"completed","conclusion":"success",`+
					`"started_at":"2026-10-08T10:00:00Z","html_url":"http://gitea/runs/%d"}`, 100+i, 100+i))
			}
		} else {
			runs = []string{
				`{"id":5,"path":"check.yaml@refs/pull/3/head","event":"pull_request","head_sha":"694db57","status":"in_progress","conclusion":"","started_at":"2026-10-09T01:40:53+02:00","html_url":"http://gitea/runs/5"}`,
				`{"id":4,"path":"lint.yaml@refs/pull/3/head","event":"pull_request","head_sha":"694db57","status":"waiting","conclusion":"","started_at":"1970-01-01T00:00:00Z","html_url":"http://gitea/runs/4"}`,
				// Another commit's run, which Gitea's filter should have left out.
				`{"id":3,"path":"check.yaml@refs/pull/2/head","event":"pull_request","head_sha":"1cedb81","status":"queued","conclusion":"","html_url":"http://gitea/runs/3"}`,
			}
		}
		_, _ = w.Write([]byte(`{"workflow_runs":[` + strings.Join(runs, ",") + `]}`))
	}))
	t.Cleanup(srv.Close)
	p, err := Client{}.Provider(Config{URL: srv.URL, Owner: "webshop", Credentials: Credentials{Token: "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	runs, err := p.CommitRuns(context.Background(), p.Repository("orders-api"), "694db57")
	if err != nil {
		t.Fatalf("CommitRuns() error = %v", err)
	}
	if len(runs) != issuePageSize+2 {
		t.Fatalf("CommitRuns() = %d runs, want %d", len(runs), issuePageSize+2)
	}
	want := []WorkflowRun{
		{ID: 5, Workflow: "check.yaml", Event: "pull_request", Ref: "refs/pull/3/head", Status: "in_progress",
			Started: time.Date(2026, 10, 8, 23, 40, 53, 0, time.UTC), URL: "http://gitea/runs/5"},
		{ID: 4, Workflow: "lint.yaml", Event: "pull_request", Ref: "refs/pull/3/head", Status: "waiting",
			URL: "http://gitea/runs/4"},
	}
	got := runs[issuePageSize:]
	if len(got) != 2 || got[0].ID != 5 || !got[0].Started.Equal(want[0].Started) {
		t.Fatalf("CommitRuns() second page = %+v, want %+v", got, want)
	}
	got[0].Started = want[0].Started
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CommitRuns() second page = %+v, want %+v", got, want)
	}
	if first := runs[0]; first.ID != 100 || first.Workflow != "check.yaml" || first.Event != "push" ||
		first.Conclusion != "success" {
		t.Errorf("CommitRuns() first = %+v, want run 100, a push of check.yaml that passed", first)
	}
	if len(queries) != 2 || queries[0].Get("head_sha") != "694db57" || queries[0].Has("event") ||
		queries[1].Get("page") != "2" {
		t.Errorf("queries = %v, want two pages of runs of any event at 694db57", queries)
	}

	if _, err := p.CommitRuns(context.Background(), p.Repository("payments"), "694db57"); err == nil ||
		!strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("CommitRuns() of a missing repository error = %v, want HTTP 404", err)
	}
}

func TestRunnersOnline(t *testing.T) {
	page := func(n int, status string, busy bool) string {
		runners := make([]string, 0, n)
		for i := range n {
			runners = append(runners, fmt.Sprintf(`{"id":%d,"name":"runner-%d","status":%q,"busy":%t}`, i, i, status, busy))
		}
		return `{"runners":[` + strings.Join(runners, ",") + `],"total_count":` + strconv.Itoa(n) + `}`
	}
	for _, tc := range []struct {
		name       string
		pages      map[string]string
		status     int
		wantOnline int
		wantKnown  bool
	}{
		{name: "none", pages: map[string]string{"1": `{"runners":[],"total_count":0}`}, wantKnown: true},
		{name: "online, idle and busy count, offline doesn't",
			pages: map[string]string{"1": `{"runners":[
				{"id":1,"status":"online","busy":false},
				{"id":2,"status":"idle","busy":false},
				{"id":3,"status":"active","busy":true},
				{"id":4,"status":"offline","busy":false}]}`},
			wantOnline: 3, wantKnown: true},
		{name: "paged", pages: map[string]string{"1": page(issuePageSize, "online", false), "2": page(2, "offline", false)},
			wantOnline: issuePageSize, wantKnown: true},
		{name: "older Gitea", status: http.StatusNotFound},
		{name: "not an admin", status: http.StatusForbidden},
		{name: "not JSON", pages: map[string]string{"1": `<html>`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var pages []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/admin/actions/runners" {
					http.NotFound(w, r)
					return
				}
				pages = append(pages, r.URL.Query().Get("page"))
				if tc.status != 0 {
					w.WriteHeader(tc.status)
					return
				}
				_, _ = w.Write([]byte(tc.pages[r.URL.Query().Get("page")]))
			}))
			t.Cleanup(srv.Close)
			p, err := Client{}.Provider(Config{URL: srv.URL, Owner: "webshop", Credentials: Credentials{Token: "secret"}})
			if err != nil {
				t.Fatal(err)
			}
			online, known := p.RunnersOnline(context.Background())
			if online != tc.wantOnline || known != tc.wantKnown {
				t.Errorf("RunnersOnline() = %d, %t; want %d, %t", online, known, tc.wantOnline, tc.wantKnown)
			}
			if tc.wantKnown && len(pages) != len(tc.pages) {
				t.Errorf("read pages %v, want %d", pages, len(tc.pages))
			}
		})
	}

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close()
	p, err := Client{}.Provider(Config{URL: srv.URL, Owner: "webshop", Credentials: Credentials{Token: "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if online, known := p.RunnersOnline(context.Background()); online != 0 || known {
		t.Errorf("RunnersOnline() of a Gitea that is down = %d, %t; want unknown", online, known)
	}
}

func TestRunnerOnline(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body       string
		wantOnline bool
		wantKnown  bool
	}{
		{name: "idle", status: http.StatusOK, body: `{"id":4,"status":"idle","busy":false}`, wantOnline: true,
			wantKnown: true},
		{name: "busy", status: http.StatusOK, body: `{"id":4,"status":"active","busy":true}`, wantOnline: true,
			wantKnown: true},
		{name: "offline", status: http.StatusOK, body: `{"id":4,"status":"offline","busy":false}`, wantKnown: true},
		{name: "offline, busy with the job it stopped in", status: http.StatusOK,
			body: `{"id":4,"status":"offline","busy":true}`, wantKnown: true},
		{name: "deleted", status: http.StatusNotFound,
			body: `{"message":"runner with id 4: resource does not exist"}`, wantKnown: true},
		{name: "older Gitea", status: http.StatusNotFound, body: `{"message":"not found"}`},
		{name: "not an admin", status: http.StatusForbidden, body: `{"message":"user should be an admin"}`},
		{name: "not JSON", status: http.StatusOK, body: `<html>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/admin/actions/runners/4" {
					http.NotFound(w, r)
					return
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			p, err := Client{}.Provider(Config{URL: srv.URL, Owner: "webshop", Credentials: Credentials{Token: "secret"}})
			if err != nil {
				t.Fatal(err)
			}
			online, known := p.RunnerOnline(context.Background(), 4)
			if online != tc.wantOnline || known != tc.wantKnown {
				t.Errorf("RunnerOnline() = %t, %t; want %t, %t", online, known, tc.wantOnline, tc.wantKnown)
			}
		})
	}
}

func TestRunJobs(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/webshop/orders-api/actions/runs/9/jobs":
			pages = append(pages, r.URL.Query().Get("page"))
			if r.URL.Query().Get("page") == "1" {
				jobs := make([]string, 0, issuePageSize)
				for i := range issuePageSize {
					jobs = append(jobs, fmt.Sprintf(`{"id":%d,"status":"completed","conclusion":"success",`+
						`"runner_id":3,"completed_at":"2026-10-16T12:00:00Z"}`, 100+i))
				}
				_, _ = w.Write([]byte(`{"jobs":[` + strings.Join(jobs, ",") + `]}`))
				return
			}
			_, _ = w.Write([]byte(`{"jobs":[
				{"id":7,"status":"completed","conclusion":"cancelled","runner_id":4,"runner_name":"gitea-runner-go-7f9",
				 "started_at":"2026-10-16T11:58:00Z","completed_at":"2026-10-16T11:59:30Z"},
				{"id":8,"status":"completed","conclusion":"cancelled","started_at":"1970-01-01T00:00:00Z",
				 "completed_at":"1970-01-01T00:00:00Z"}],"total_count":` + strconv.Itoa(issuePageSize+2) + `}`))
		case "/api/v1/repos/webshop/orders-api/actions/runs/8/jobs":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"workflow run not found"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p, err := Client{}.Provider(Config{URL: srv.URL, Owner: "webshop", Credentials: Credentials{Token: "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	repo := p.Repository("orders-api")
	jobs, err := p.RunJobs(context.Background(), repo, 9)
	if err != nil {
		t.Fatal(err)
	}
	want := []WorkflowJob{
		{ID: 7, Status: "completed", Conclusion: "cancelled", RunnerID: 4, RunnerName: "gitea-runner-go-7f9",
			Completed: time.Date(2026, 10, 16, 11, 59, 30, 0, time.UTC)},
		{ID: 8, Status: "completed", Conclusion: "cancelled"},
	}
	if len(jobs) != issuePageSize+2 || !slices.EqualFunc(jobs[issuePageSize:], want, func(a, b WorkflowJob) bool {
		return a.ID == b.ID && a.Status == b.Status && a.Conclusion == b.Conclusion && a.RunnerID == b.RunnerID &&
			a.RunnerName == b.RunnerName && a.Completed.Equal(b.Completed)
	}) {
		t.Errorf("RunJobs() = %d jobs ending %+v, want %d ending %+v", len(jobs), jobs[max(0, len(jobs)-2):],
			issuePageSize+2, want)
	}
	if !slices.Equal(pages, []string{"1", "2"}) {
		t.Errorf("read pages %v, want 1 and 2", pages)
	}
	if _, err := p.RunJobs(context.Background(), repo, 8); err == nil ||
		!strings.Contains(err.Error(), "workflow run not found") {
		t.Errorf("RunJobs() of a run Gitea doesn't have: error %v, want Gitea's message", err)
	}
}

func TestRerunFailedJobs(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/v1/repos/webshop/orders-api/actions/runs/9/rerun-failed-jobs":
			w.WriteHeader(http.StatusCreated)
		case "/api/v1/repos/webshop/orders-api/actions/runs/8/rerun-failed-jobs":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"this workflow run is not done"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	p, err := Client{}.Provider(Config{URL: srv.URL, Owner: "webshop", Credentials: Credentials{Token: "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	repo := p.Repository("orders-api")
	if err := p.RerunFailedJobs(context.Background(), repo, 9); err != nil {
		t.Fatalf("RerunFailedJobs() error = %v", err)
	}
	if err := p.RerunFailedJobs(context.Background(), repo, 8); err == nil ||
		!strings.Contains(err.Error(), "HTTP 400") || !strings.Contains(err.Error(), "not done") {
		t.Errorf("RerunFailedJobs() of a run that hasn't ended error = %v, want HTTP 400 with Gitea's message", err)
	}
	want := []string{"POST /api/v1/repos/webshop/orders-api/actions/runs/9/rerun-failed-jobs",
		"POST /api/v1/repos/webshop/orders-api/actions/runs/8/rerun-failed-jobs"}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %v, want %v", calls, want)
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
