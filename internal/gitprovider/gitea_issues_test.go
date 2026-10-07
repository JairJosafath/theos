package gitprovider

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// issueHost is an in-memory Gitea with labels, milestones, issues, dependencies,
// reviews and tags, for repository webshop/orders-api. Exclusive scoped labels replace
// their siblings, milestone lists show only open ones unless asked for all, and
// a milestone edit clears the due date unless it gives one, as in Gitea.
type issueHost struct {
	mu         sync.Mutex
	labels     []giteaLabel
	scoped     map[int64]bool
	milestones []hostMilestone
	issues     map[int]*hostIssue
	reviews    []map[string]string
	comments   map[int][]string
	depPosts   []map[string]any
	// tags maps a tag to the commit it names; tagPosts are the create requests.
	tags     map[string]string
	tagPosts []map[string]string
}

type hostIssue struct {
	giteaIssue
	assignees []string
	deps      []giteaIssue
	// milestone is the milestone the issue was filed with; nil when the request had none.
	milestone *int64
}

type hostMilestone struct {
	ID          int64      `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	State       string     `json:"state"`
	DueOn       *time.Time `json:"due_on,omitempty"`
}

func newIssueHost(t *testing.T) (*issueHost, Provider) {
	t.Helper()
	h := &issueHost{scoped: map[int64]bool{}, issues: map[int]*hostIssue{}, comments: map[int][]string{},
		tags: map[string]string{}}
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
	mux.HandleFunc("GET "+repo+"/milestones", func(w http.ResponseWriter, r *http.Request) {
		out := []hostMilestone{}
		for _, m := range h.milestones {
			if r.URL.Query().Get("state") == "all" || m.State == "open" {
				out = append(out, m)
			}
		}
		reply(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST "+repo+"/milestones", func(w http.ResponseWriter, r *http.Request) {
		var m hostMilestone
		_ = json.NewDecoder(r.Body).Decode(&m)
		m.ID, m.State = int64(len(h.milestones)+1), "open"
		h.milestones = append(h.milestones, m)
		reply(w, http.StatusCreated, m)
	})
	h.handleReleases(mux, repo, reply)
	mux.HandleFunc("GET "+repo+"/issues", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("milestones") {
			reply(w, http.StatusOK, h.milestoneIssues(t, r))
			return
		}
		if r.URL.Query().Get("type") != "issues" || r.URL.Query().Get("state") != "open" {
			t.Errorf("list query = %s", r.URL.RawQuery)
		}
		want := r.URL.Query().Get("labels")
		var out []giteaIssue
		for n := 1; n <= len(h.issues); n++ {
			i := h.issues[n]
			if i != nil && i.State == "open" && (!r.URL.Query().Has("labels") ||
				slices.ContainsFunc(i.Labels, func(l giteaLabel) bool { return l.Name == want })) {
				out = append(out, i.giteaIssue)
			}
		}
		reply(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST "+repo+"/issues", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Title     string  `json:"title"`
			Body      string  `json:"body"`
			Labels    []int64 `json:"labels"`
			Milestone *int64  `json:"milestone"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		n := len(h.issues) + 1
		i := &hostIssue{giteaIssue: giteaIssue{Number: n, Title: body.Title, Body: body.Body, State: "open",
			HTMLURL: "http://gitea/webshop/orders-api/issues/" + strconv.Itoa(n)}, milestone: body.Milestone}
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
		if i := issue(w, r); i != nil {
			i.edit(r)
			reply(w, http.StatusCreated, i.giteaIssue)
		}
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
	mux.HandleFunc("POST "+repo+"/issues/{n}/dependencies", func(w http.ResponseWriter, r *http.Request) {
		i := issue(w, r)
		if i == nil {
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		h.depPosts = append(h.depPosts, body)
		blocker, _ := body["index"].(float64)
		d := giteaIssue{Number: int(blocker), State: "open"}
		owner, _ := body["owner"].(string)
		name, _ := body["repo"].(string)
		d.Repository.FullName = owner + "/" + name
		i.deps = append(i.deps, d)
		reply(w, http.StatusCreated, i.giteaIssue)
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
	p, err := Client{}.Provider(Config{URL: srv.URL, Owner: "webshop", Credentials: Credentials{Token: rand.Text()}})
	if err != nil {
		t.Fatal(err)
	}
	return h, p
}

// handleReleases serves the milestone edits and tags of the issueHost.
// edit applies the fields an issue PATCH sets.
func (i *hostIssue) edit(r *http.Request) {
	var body struct {
		State     string   `json:"state"`
		Body      *string  `json:"body"`
		Assignees []string `json:"assignees"`
		Milestone *int64   `json:"milestone"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Body != nil {
		i.Body = *body.Body
	}
	if body.Milestone != nil {
		i.milestone = body.Milestone
	}
	if body.State != "" {
		i.State = body.State
	}
	if body.Assignees != nil {
		i.assignees = body.Assignees
	}
}

func (h *issueHost) handleReleases(mux *http.ServeMux, repo string, reply func(http.ResponseWriter, int, any)) {
	mux.HandleFunc("PATCH "+repo+"/milestones/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		idx := slices.IndexFunc(h.milestones, func(m hostMilestone) bool { return m.ID == id })
		if idx < 0 {
			reply(w, http.StatusNotFound, map[string]string{"message": "no milestone"})
			return
		}
		// As in Gitea, an edit that gives no due date clears it.
		var body struct {
			State string     `json:"state"`
			DueOn *time.Time `json:"due_on"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		h.milestones[idx].State, h.milestones[idx].DueOn = body.State, body.DueOn
		reply(w, http.StatusOK, h.milestones[idx])
	})
	mux.HandleFunc("GET "+repo+"/milestones/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		idx := slices.IndexFunc(h.milestones, func(m hostMilestone) bool { return m.ID == id })
		if idx < 0 {
			reply(w, http.StatusNotFound, map[string]string{"message": "no milestone"})
			return
		}
		reply(w, http.StatusOK, h.milestones[idx])
	})
	mux.HandleFunc("GET "+repo+"/tags/{tag}", func(w http.ResponseWriter, r *http.Request) {
		sha, ok := h.tags[r.PathValue("tag")]
		if !ok {
			reply(w, http.StatusNotFound, map[string]string{"message": "no tag"})
			return
		}
		reply(w, http.StatusOK, map[string]any{"name": r.PathValue("tag"), "id": "tagobject",
			"commit": map[string]string{"sha": sha}})
	})
	mux.HandleFunc("POST "+repo+"/tags", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		h.tagPosts = append(h.tagPosts, body)
		if _, ok := h.tags[body["tag_name"]]; ok {
			reply(w, http.StatusConflict, map[string]string{"message": "tag already exists"})
			return
		}
		h.tags[body["tag_name"]] = body["target"]
		reply(w, http.StatusCreated, map[string]any{"name": body["tag_name"]})
	})
}

// milestoneIssues lists the issues, open and closed, of the milestone the query names.
func (h *issueHost) milestoneIssues(t *testing.T, r *http.Request) []giteaIssue {
	if r.URL.Query().Get("type") != "issues" || r.URL.Query().Get("state") != "all" {
		t.Errorf("milestone query = %s", r.URL.RawQuery)
	}
	id, _ := strconv.ParseInt(r.URL.Query().Get("milestones"), 10, 64)
	out := []giteaIssue{}
	for n := 1; n <= len(h.issues); n++ {
		if i := h.issues[n]; i != nil && i.milestone != nil && *i.milestone == id {
			out = append(out, i.giteaIssue)
		}
	}
	return out
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

	first, err := p.CreateIssue(ctx, repo, "Validate orders", "## Acceptance criteria\n- [ ] it validates",
		[]string{"status/ready", "kind/bug"}, 0)
	if err != nil {
		t.Fatalf("CreateIssue() error = %v", err)
	}
	if first.Number != 1 || !reflect.DeepEqual(first.Labels, []string{"status/ready", "kind/bug"}) {
		t.Errorf("CreateIssue() = %+v", first)
	}
	if _, err := p.CreateIssue(ctx, repo, "Other", "", []string{"no/such"}, 0); err == nil {
		t.Error("CreateIssue() with an unknown label succeeded")
	}
	if _, err := p.CreateIssue(ctx, repo, "Unlabelled", "body", nil, 0); err != nil {
		t.Fatalf("CreateIssue() without labels error = %v", err)
	}

	ready, err := p.ListIssues(ctx, repo, "status/ready")
	if err != nil || len(ready) != 1 || ready[0].Number != 1 || ready[0].URL == "" {
		t.Fatalf("ListIssues() = %+v, %v; want issue 1", ready, err)
	}
	if all, err := p.ListIssues(ctx, repo, ""); err != nil || len(all) != 2 {
		t.Fatalf("ListIssues() without a label = %+v, %v; want both open issues", all, err)
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
	if _, err := p.GetIssue(ctx, repo, 9); !errors.Is(err, ErrIssueNotFound) {
		t.Errorf("GetIssue() of a missing issue error = %v, want ErrIssueNotFound", err)
	}
}

// Two reconcilers make sure of a new repository's labels at once, each with a
// provider of its own: a Brief filing its issues while the Crew it made polls.
// Gitea takes two labels with one name, and then filters issues by neither, so
// each label must still be made once.
func TestEnsureLabelsConcurrently(t *testing.T) {
	var mu sync.Mutex
	var labels []giteaLabel
	listing, both := 0, make(chan struct{})
	mux := http.NewServeMux()
	const path = "/api/v1/repos/webshop/orders-api/labels"
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		out := slices.Clone(labels)
		if listing++; listing == 2 {
			close(both)
		}
		mu.Unlock()
		// Reply once the other caller has listed too, or after a moment when
		// it waits for this one: then only one of them sees no labels.
		select {
		case <-both:
		case <-time.After(300 * time.Millisecond):
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("POST "+path, func(w http.ResponseWriter, r *http.Request) {
		var body giteaLabelOption
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		l := giteaLabel{ID: int64(len(labels) + 1), Name: body.Name}
		labels = append(labels, l)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(l)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	want := []Label{{Name: "status/ready", Exclusive: true}, {Name: "kind/bug"}}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		p, err := Client{}.Provider(Config{URL: srv.URL, Owner: "webshop", Credentials: Credentials{Token: rand.Text()}})
		if err != nil {
			t.Fatal(err)
		}
		wg.Go(func() { errs[i] = p.EnsureLabels(context.Background(), p.Repository("orders-api"), want) })
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("EnsureLabels() error = %v", err)
		}
	}
	if len(labels) != len(want) {
		t.Errorf("labels = %v, want each once", labels)
	}
}

func TestEnsureMilestone(t *testing.T) {
	ctx := context.Background()

	t.Run("creates the milestone once", func(t *testing.T) {
		h, p := newIssueHost(t)
		repo := p.Repository("orders-api")
		first, err := p.EnsureMilestone(ctx, repo, "Later", "Not part of any release")
		if err != nil {
			t.Fatalf("EnsureMilestone() error = %v", err)
		}
		again, err := p.EnsureMilestone(ctx, repo, "Later", "Not part of any release")
		if err != nil {
			t.Fatalf("EnsureMilestone() again error = %v", err)
		}
		want := []hostMilestone{{ID: first, Title: "Later", Description: "Not part of any release", State: "open"}}
		if first == 0 || again != first || !reflect.DeepEqual(h.milestones, want) {
			t.Errorf("EnsureMilestone() = %d, then %d; milestones %+v, want %+v", first, again, h.milestones, want)
		}
	})

	t.Run("finds a closed milestone by its exact title, without creating or reopening it", func(t *testing.T) {
		h, p := newIssueHost(t)
		h.milestones = []hostMilestone{
			{ID: 1, Title: "Later release", State: "open"},
			{ID: 2, Title: "later", State: "open"},
			{ID: 3, Title: "Later", Description: "Someday", State: "closed"},
		}
		want := slices.Clone(h.milestones)
		got, err := p.EnsureMilestone(ctx, p.Repository("orders-api"), "Later", "Not part of any release")
		if err != nil || got != 3 || !reflect.DeepEqual(h.milestones, want) {
			t.Errorf("EnsureMilestone() = %d, %v; milestones %+v, want 3 and them unchanged", got, err, h.milestones)
		}
	})
}

func TestCreateIssueMilestone(t *testing.T) {
	h, p := newIssueHost(t)
	ctx := context.Background()
	repo := p.Repository("orders-api")
	if _, err := p.CreateIssue(ctx, repo, "Validate addresses", "body", nil, 3); err != nil {
		t.Fatalf("CreateIssue() with a milestone error = %v", err)
	}
	if _, err := p.CreateIssue(ctx, repo, "Validate orders", "body", nil, 0); err != nil {
		t.Fatalf("CreateIssue() without a milestone error = %v", err)
	}
	if m := h.issues[1].milestone; m == nil || *m != 3 {
		t.Errorf("issue 1 filed with milestone %v, want 3", m)
	}
	if m := h.issues[2].milestone; m != nil {
		t.Errorf("issue 2 filed with milestone %d, want none sent", *m)
	}
}

func TestEditIssueBody(t *testing.T) {
	h, p := newIssueHost(t)
	ctx := context.Background()
	repo := p.Repository("orders-api")
	if _, err := p.CreateIssue(ctx, repo, "Validate addresses", "body", nil, 3); err != nil {
		t.Fatal(err)
	}
	if err := p.EditIssueBody(ctx, repo, 1, "body\n\n## Clarification\n\nUse the checker."); err != nil {
		t.Fatalf("EditIssueBody() error = %v", err)
	}
	if got := h.issues[1].Body; got != "body\n\n## Clarification\n\nUse the checker." {
		t.Errorf("body after EditIssueBody() = %q", got)
	}
	if m := h.issues[1].milestone; m == nil || *m != 3 {
		t.Errorf("EditIssueBody() changed the milestone to %v", m)
	}
	if err := p.EditIssueBody(ctx, repo, 9, "body"); err == nil {
		t.Error("EditIssueBody() of a missing issue succeeded")
	}
}

func TestSetIssueMilestone(t *testing.T) {
	h, p := newIssueHost(t)
	ctx := context.Background()
	repo := p.Repository("orders-api")
	if _, err := p.CreateIssue(ctx, repo, "Validate addresses", "body", nil, 3); err != nil {
		t.Fatal(err)
	}
	if err := p.SetIssueMilestone(ctx, repo, 1, 0); err != nil {
		t.Fatalf("SetIssueMilestone(0) error = %v", err)
	}
	if m := h.issues[1].milestone; m == nil || *m != 0 {
		t.Errorf("after SetIssueMilestone(0), milestone sent = %v, want 0", m)
	}
	if err := p.SetIssueMilestone(ctx, repo, 9, 0); err == nil {
		t.Error("SetIssueMilestone() of a missing issue succeeded")
	}
}

func TestOpenBlockers(t *testing.T) {
	h, p := newIssueHost(t)
	ctx := context.Background()
	repo := p.Repository("orders-api")
	if _, err := p.CreateIssue(ctx, repo, "Blocked", "", nil, 0); err != nil {
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

func TestAddIssueDependency(t *testing.T) {
	h, p := newIssueHost(t)
	ctx := context.Background()
	repo, engine := p.Repository("orders-api"), p.Repository("orders-engine")
	h.issues[4] = &hostIssue{giteaIssue: giteaIssue{Number: 4, State: "open"}}
	// An older dependency Gitea lists without its repository is of the issue's own.
	h.issues[4].deps = []giteaIssue{{Number: 2, State: "open"}}
	for range 2 {
		if err := p.AddIssueDependency(ctx, repo, 4, repo, 31); err != nil {
			t.Fatalf("AddIssueDependency() error = %v", err)
		}
		if err := p.AddIssueDependency(ctx, repo, 4, engine, 31); err != nil {
			t.Fatalf("AddIssueDependency() of another repository error = %v", err)
		}
		if err := p.AddIssueDependency(ctx, repo, 4, repo, 2); err != nil {
			t.Fatalf("AddIssueDependency() of a listed dependency error = %v", err)
		}
	}
	want := []map[string]any{
		{"owner": "webshop", "repo": "orders-api", "index": float64(31)},
		{"owner": "webshop", "repo": "orders-engine", "index": float64(31)},
	}
	if !reflect.DeepEqual(h.depPosts, want) {
		t.Errorf("dependency POSTs = %v, want each dependency posted once: %v", h.depPosts, want)
	}
}

func TestGetIssueMilestone(t *testing.T) {
	h, p := newIssueHost(t)
	ctx := context.Background()
	repo := p.Repository("orders-api")
	h.issues[1] = &hostIssue{giteaIssue: giteaIssue{Number: 1, State: "open", Milestone: &giteaMilestone{ID: 5}}}
	h.issues[2] = &hostIssue{giteaIssue: giteaIssue{Number: 2, State: "open"}}
	if got, err := p.GetIssue(ctx, repo, 1); err != nil || got.Milestone != 5 {
		t.Errorf("GetIssue(1) milestone = %d, %v; want 5", got.Milestone, err)
	}
	if got, err := p.GetIssue(ctx, repo, 2); err != nil || got.Milestone != 0 {
		t.Errorf("GetIssue(2) milestone = %d, %v; want 0", got.Milestone, err)
	}
}

func TestIssueMilestone(t *testing.T) {
	tests := []struct {
		name, milestone string
		want            Issue
	}{
		{"none", `null`, Issue{}},
		{"undated", `{"id": 9, "title": "v0.1.1", "due_on": null}`, Issue{Milestone: 9, MilestoneTitle: "v0.1.1"}},
		{"due", `{"id": 12, "title": "v0.2.0", "due_on": "2026-10-12T23:59:59Z"}`,
			Issue{Milestone: 12, MilestoneTitle: "v0.2.0", MilestoneDue: time.Date(2026, 10, 12, 23, 59, 59, 0, time.UTC)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var i giteaIssue
			if err := json.Unmarshal([]byte(`{"number": 3, "milestone": `+tt.milestone+`}`), &i); err != nil {
				t.Fatal(err)
			}
			got := i.issue()
			if got.Milestone != tt.want.Milestone || got.MilestoneTitle != tt.want.MilestoneTitle ||
				!got.MilestoneDue.Equal(tt.want.MilestoneDue) {
				t.Errorf("issue() milestone = %d %q %v, want %d %q %v", got.Milestone, got.MilestoneTitle,
					got.MilestoneDue, tt.want.Milestone, tt.want.MilestoneTitle, tt.want.MilestoneDue)
			}
		})
	}
}
