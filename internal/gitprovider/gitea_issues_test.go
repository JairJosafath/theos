package gitprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// issueHost is an in-memory Gitea with labels, issues, dependencies and reviews,
// for repository webshop/orders-api. Exclusive scoped labels replace their
// siblings, as in Gitea.
type issueHost struct {
	mu       sync.Mutex
	labels   []giteaLabel
	scoped   map[int64]bool
	issues   map[int]*hostIssue
	reviews  []map[string]string
	comments map[int][]string
}

type hostIssue struct {
	giteaIssue
	assignees []string
	deps      []giteaIssue
}

func newIssueHost(t *testing.T) (*issueHost, Provider) {
	t.Helper()
	h := &issueHost{scoped: map[int64]bool{}, issues: map[int]*hostIssue{}, comments: map[int][]string{}}
	mux := http.NewServeMux()
	const repo = "/api/v1/repos/webshop/orders-api"
	reply := func(w http.ResponseWriter, status int, v any) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	issue := func(w http.ResponseWriter, r *http.Request) *hostIssue {
		n, _ := strconv.Atoi(r.PathValue("n"))
		i := h.issues[n]
		if i == nil {
			reply(w, http.StatusNotFound, map[string]string{"message": "no issue"})
		}
		return i
	}
	mux.HandleFunc("GET "+repo+"/labels", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, http.StatusOK, h.labels)
	})
	mux.HandleFunc("POST "+repo+"/labels", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name      string `json:"name"`
			Exclusive bool   `json:"exclusive"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		l := giteaLabel{ID: int64(len(h.labels) + 1), Name: body.Name}
		h.labels = append(h.labels, l)
		h.scoped[l.ID] = body.Exclusive
		reply(w, http.StatusCreated, l)
	})
	mux.HandleFunc("GET "+repo+"/issues", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("type") != "issues" || r.URL.Query().Get("state") != "open" {
			t.Errorf("list query = %s", r.URL.RawQuery)
		}
		want := r.URL.Query().Get("labels")
		var out []giteaIssue
		for n := 1; n <= len(h.issues); n++ {
			i := h.issues[n]
			if i != nil && i.State == "open" && slices.ContainsFunc(i.Labels, func(l giteaLabel) bool { return l.Name == want }) {
				out = append(out, i.giteaIssue)
			}
		}
		reply(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST "+repo+"/issues", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Title  string  `json:"title"`
			Body   string  `json:"body"`
			Labels []int64 `json:"labels"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		n := len(h.issues) + 1
		i := &hostIssue{giteaIssue: giteaIssue{Number: n, Title: body.Title, Body: body.Body, State: "open",
			HTMLURL: "http://gitea/webshop/orders-api/issues/" + strconv.Itoa(n)}}
		for _, id := range body.Labels {
			i.Labels = append(i.Labels, h.labels[id-1])
		}
		h.issues[n] = i
		reply(w, http.StatusCreated, i.giteaIssue)
	})
	mux.HandleFunc("GET "+repo+"/issues/{n}", func(w http.ResponseWriter, r *http.Request) {
		if i := issue(w, r); i != nil {
			reply(w, http.StatusOK, i.giteaIssue)
		}
	})
	mux.HandleFunc("PATCH "+repo+"/issues/{n}", func(w http.ResponseWriter, r *http.Request) {
		i := issue(w, r)
		if i == nil {
			return
		}
		var body struct {
			State     string   `json:"state"`
			Assignees []string `json:"assignees"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.State != "" {
			i.State = body.State
		}
		if body.Assignees != nil {
			i.assignees = body.Assignees
		}
		reply(w, http.StatusCreated, i.giteaIssue)
	})
	mux.HandleFunc("GET "+repo+"/issues/{n}/labels", func(w http.ResponseWriter, r *http.Request) {
		if i := issue(w, r); i != nil {
			reply(w, http.StatusOK, i.Labels)
		}
	})
	mux.HandleFunc("POST "+repo+"/issues/{n}/labels", func(w http.ResponseWriter, r *http.Request) {
		i := issue(w, r)
		if i == nil {
			return
		}
		var body struct {
			Labels []string `json:"labels"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, name := range body.Labels {
			idx := slices.IndexFunc(h.labels, func(l giteaLabel) bool { return l.Name == name })
			if idx < 0 {
				reply(w, http.StatusNotFound, map[string]string{"message": "no label " + name})
				return
			}
			added := h.labels[idx]
			scope, _, _ := strings.Cut(added.Name, "/")
			i.Labels = slices.DeleteFunc(i.Labels, func(l giteaLabel) bool {
				s, _, scopedName := strings.Cut(l.Name, "/")
				return l.ID == added.ID || h.scoped[added.ID] && scopedName && s == scope
			})
			i.Labels = append(i.Labels, added)
		}
		reply(w, http.StatusOK, i.Labels)
	})
	mux.HandleFunc("DELETE "+repo+"/issues/{n}/labels/{id}", func(w http.ResponseWriter, r *http.Request) {
		i := issue(w, r)
		if i == nil {
			return
		}
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		i.Labels = slices.DeleteFunc(i.Labels, func(l giteaLabel) bool { return l.ID == id })
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST "+repo+"/issues/{n}/comments", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.PathValue("n"))
		var body struct {
			Body string `json:"body"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		h.comments[n] = append(h.comments[n], body.Body)
		reply(w, http.StatusCreated, map[string]string{})
	})
	mux.HandleFunc("GET "+repo+"/issues/{n}/dependencies", func(w http.ResponseWriter, r *http.Request) {
		if i := issue(w, r); i != nil {
			reply(w, http.StatusOK, i.deps)
		}
	})
	mux.HandleFunc("POST "+repo+"/pulls/{n}/reviews", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["pull"] = r.PathValue("n")
		h.reviews = append(h.reviews, body)
		reply(w, http.StatusOK, map[string]string{})
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		defer h.mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	p, err := Client{}.Provider(Config{URL: srv.URL, Owner: "webshop", Credentials: Credentials{Token: "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	return h, p
}

func TestIssues(t *testing.T) {
	h, p := newIssueHost(t)
	ctx := context.Background()
	repo := p.Repository("orders-api")
	labels := []Label{{Name: "status/ready", Exclusive: true}, {Name: "status/in-progress", Exclusive: true}, {Name: "kind/bug"}}

	if err := p.EnsureLabels(ctx, repo, labels); err != nil {
		t.Fatalf("EnsureLabels() error = %v", err)
	}
	if err := p.EnsureLabels(ctx, repo, labels); err != nil {
		t.Fatalf("EnsureLabels() again error = %v", err)
	}
	if len(h.labels) != 3 {
		t.Fatalf("labels = %v, want each once", h.labels)
	}

	first, err := p.CreateIssue(ctx, repo, "Validate orders", "## Acceptance criteria\n- [ ] it validates", []string{"status/ready", "kind/bug"})
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}
	if first.Number != 1 || !reflect.DeepEqual(first.Labels, []string{"status/ready", "kind/bug"}) {
		t.Errorf("CreateIssue() = %+v", first)
	}
	if _, err := p.CreateIssue(ctx, repo, "Other", "", []string{"no/such"}); err == nil {
		t.Error("CreateIssue() with an unknown label succeeded")
	}
	if _, err := p.CreateIssue(ctx, repo, "Unlabelled", "body", nil); err != nil {
		t.Fatalf("CreateIssue() without labels error = %v", err)
	}

	ready, err := p.ListIssues(ctx, repo, "status/ready")
	if err != nil || len(ready) != 1 || ready[0].Number != 1 || ready[0].URL == "" {
		t.Fatalf("ListIssues() = %+v, %v; want issue 1", ready, err)
	}

	if err := p.AddIssueLabel(ctx, repo, 1, "status/in-progress"); err != nil {
		t.Fatalf("AddIssueLabel() error = %v", err)
	}
	got, err := p.GetIssue(ctx, repo, 1)
	if err != nil || !reflect.DeepEqual(got.Labels, []string{"kind/bug", "status/in-progress"}) {
		t.Errorf("after AddIssueLabel, labels = %v, %v; want the exclusive label replaced", got.Labels, err)
	}
	if err := p.RemoveIssueLabel(ctx, repo, 1, "status/in-progress"); err != nil {
		t.Fatalf("RemoveIssueLabel() error = %v", err)
	}
	if err := p.RemoveIssueLabel(ctx, repo, 1, "status/in-progress"); err != nil {
		t.Fatalf("RemoveIssueLabel() of a missing label error = %v", err)
	}
	if got, _ := p.GetIssue(ctx, repo, 1); !reflect.DeepEqual(got.Labels, []string{"kind/bug"}) {
		t.Errorf("after RemoveIssueLabel, labels = %v", got.Labels)
	}

	if err := p.AssignIssue(ctx, repo, 1, []string{"bot"}); err != nil {
		t.Fatalf("AssignIssue() error = %v", err)
	}
	if err := p.CommentIssue(ctx, repo, 1, "Picked up"); err != nil {
		t.Fatalf("CommentIssue() error = %v", err)
	}
	if err := p.CloseIssue(ctx, repo, 1); err != nil {
		t.Fatalf("CloseIssue() error = %v", err)
	}
	if got, _ := p.GetIssue(ctx, repo, 1); got.State != "closed" || !reflect.DeepEqual(h.issues[1].assignees, []string{"bot"}) ||
		!reflect.DeepEqual(h.comments[1], []string{"Picked up"}) {
		t.Errorf("issue 1 = %+v, assignees %v, comments %v", got, h.issues[1].assignees, h.comments[1])
	}
	if _, err := p.GetIssue(ctx, repo, 9); err == nil {
		t.Error("GetIssue() of a missing issue succeeded")
	}
}

func TestOpenBlockers(t *testing.T) {
	h, p := newIssueHost(t)
	ctx := context.Background()
	repo := p.Repository("orders-api")
	if _, err := p.CreateIssue(ctx, repo, "Blocked", "", nil); err != nil {
		t.Fatal(err)
	}
	dep := func(repo string, n int, state string) giteaIssue {
		d := giteaIssue{Number: n, State: state}
		d.Repository.FullName = repo
		return d
	}
	h.issues[1].deps = []giteaIssue{
		dep("webshop/orders-api", 3, "open"), dep("webshop/orders-api", 4, "closed"), dep("webshop/web", 7, "open"),
	}
	got, err := p.OpenBlockers(ctx, repo, 1)
	if err != nil || !reflect.DeepEqual(got, []string{"#3", "webshop/web#7"}) {
		t.Errorf("OpenBlockers() = %v, %v", got, err)
	}
}

func TestReviewPullRequest(t *testing.T) {
	h, p := newIssueHost(t)
	err := p.ReviewPullRequest(context.Background(), p.Repository("orders-api"), 4, "abc", ReviewRequestChanges, "Test it")
	want := []map[string]string{{"event": "REQUEST_CHANGES", "body": "Test it", "commit_id": "abc", "pull": "4"}}
	if err != nil || !reflect.DeepEqual(h.reviews, want) {
		t.Errorf("ReviewPullRequest() = %v, reviews %v", err, h.reviews)
	}
}
