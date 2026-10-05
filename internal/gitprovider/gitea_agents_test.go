package gitprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// agentGitea is a small stateful Gitea for the agent calls. It records every
// request and the JSON bodies it received.
type agentGitea struct {
	t        *testing.T
	mu       sync.Mutex
	calls    []string
	bodies   map[string]map[string]any
	users    map[string]bool
	rules    map[string]bool
	pulls    []giteaPull
	statuses string
	tagRules []map[string]any
	// fileQueries are the raw queries of the pull request files calls.
	fileQueries []string
	// manyFiles fills every page of pull request 7's files.
	manyFiles bool
	// files, if set, is pull request 7's one page of files.
	files string
}

func newAgentGitea(t *testing.T) (*agentGitea, Provider) {
	t.Helper()
	g := &agentGitea{t: t, bodies: map[string]map[string]any{}, users: map[string]bool{}, rules: map[string]bool{}}
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	p, err := Client{}.Provider(Config{URL: srv.URL, Owner: "apps", Credentials: Credentials{Username: "c4admin", Token: "admin"}})
	if err != nil {
		t.Fatal(err)
	}
	return g, p
}

// serveGatedPull serves pull request 7: its files, closing and merging.
func (g *agentGitea) serveGatedPull(w http.ResponseWriter, r *http.Request, call string) {
	switch {
	case strings.HasPrefix(call, "GET ") && strings.HasSuffix(call, "/files"):
		g.fileQueries = append(g.fileQueries, r.URL.RawQuery)
		if g.files != "" {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, g.files)
			return
		}
		var files []map[string]string
		count := 50
		if r.URL.Query().Get("page") == "2" && !g.manyFiles {
			count = 3
		}
		for i := 0; i < count; i++ {
			files = append(files, map[string]string{"filename": fmt.Sprintf("f%s-%d", r.URL.Query().Get("page"), i)})
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(files)
	case strings.HasPrefix(call, "PATCH "):
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "{}")
	case strings.HasPrefix(call, "POST ") && strings.HasSuffix(call, "/merge"):
		w.WriteHeader(http.StatusOK)
	default:
		g.t.Errorf("unexpected call %s", call)
		http.NotFound(w, r)
	}
}

func (g *agentGitea) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	call := r.Method + " " + r.URL.Path
	g.calls = append(g.calls, call)
	var body map[string]any
	if data, _ := io.ReadAll(r.Body); len(data) > 0 {
		_ = json.Unmarshal(data, &body)
		g.bodies[call] = body
	}
	user, _, basic := r.BasicAuth()
	reply := func(status int, v any) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	}
	repo := "/api/v1/repos/apps/photo-editor"
	switch {
	case call == "POST "+repo+"/branch_protections":
		if g.rules["main"] {
			reply(http.StatusForbidden, map[string]string{"message": "Branch protection already exist"})
			return
		}
		g.rules["main"] = true
		reply(http.StatusCreated, map[string]string{})
	case call == "PATCH "+repo+"/branch_protections/main":
		reply(http.StatusOK, map[string]string{})
	case call == "GET "+repo+"/tag_protections":
		reply(http.StatusOK, g.tagRules)
	case call == "POST "+repo+"/tag_protections":
		g.tagRules = append(g.tagRules, map[string]any{"id": 7, "name_pattern": body["name_pattern"]})
		reply(http.StatusCreated, map[string]string{})
	case call == "PATCH "+repo+"/tag_protections/7":
		reply(http.StatusOK, map[string]string{})
	case call == "GET "+repo+"/actions/jobs/27/logs":
		_, _ = io.WriteString(w, "make: *** [Makefile:21: test] Error 1\n")
	case strings.HasPrefix(call, "GET "+repo+"/commits/"):
		_, _ = io.WriteString(w, g.statuses)
	case call == "GET /api/v1/users/photo-editor-agent":
		if !g.users["photo-editor-agent"] {
			reply(http.StatusNotFound, map[string]string{"message": "user does not exist"})
			return
		}
		reply(http.StatusOK, map[string]string{"login": "photo-editor-agent"})
	case call == "POST /api/v1/admin/users":
		g.users[body["username"].(string)] = true
		reply(http.StatusCreated, map[string]string{})
	case call == "PATCH /api/v1/admin/users/photo-editor-agent":
		reply(http.StatusOK, map[string]string{})
	case call == "PUT "+repo+"/collaborators/photo-editor-agent":
		w.WriteHeader(http.StatusNoContent)
	case strings.HasPrefix(call, "DELETE /api/v1/users/photo-editor-agent/tokens/"):
		if !basic || user != "photo-editor-agent" {
			g.t.Errorf("%s without the bot's basic auth", call)
		}
		w.WriteHeader(http.StatusNotFound)
	case call == "POST /api/v1/users/photo-editor-agent/tokens":
		if !basic || user != "photo-editor-agent" {
			g.t.Errorf("%s without the bot's basic auth", call)
		}
		reply(http.StatusCreated, map[string]string{"sha1": "b0t-t0ken"})
	case call == "POST "+repo+"/pulls":
		for _, p := range g.pulls {
			if p.Head.Ref == body["head"] {
				reply(http.StatusConflict, map[string]string{"message": "pull request already exists"})
				return
			}
		}
		p := giteaPull{Number: len(g.pulls) + 1, HTMLURL: "http://gitea/apps/photo-editor/pulls/1", State: "open"}
		p.Head.Ref, p.Head.SHA = body["head"].(string), "abc123"
		g.pulls = append(g.pulls, p)
		reply(http.StatusCreated, p)
	case call == "GET "+repo+"/pulls":
		reply(http.StatusOK, g.pulls)
	case call == "GET "+repo+"/pulls/1":
		reply(http.StatusOK, g.pulls[0])
	case strings.Contains(call, "/pulls/7"):
		g.serveGatedPull(w, r, call)
	case call == "POST "+repo+"/issues/1/comments":
		reply(http.StatusCreated, map[string]string{})
	case call == "POST "+repo+"/pulls/1/merge":
		g.pulls[0].Merged = true
		w.WriteHeader(http.StatusOK)
	case call == "POST "+repo+"/pulls/2/merge":
		reply(http.StatusMethodNotAllowed, map[string]string{"message": "Please try again later"})
	default:
		g.t.Errorf("unexpected call %s", call)
		http.NotFound(w, r)
	}
}

func TestProtectBranchCreatesThenUpdates(t *testing.T) {
	g, p := newAgentGitea(t)
	repo := p.Repository("photo-editor")
	for range 2 {
		rule := BranchRule{StatusContexts: []string{"check*"}, ProtectedPaths: []string{".gitea/", "Makefile"}}
		if err := p.ProtectBranch(context.Background(), repo, "main", rule); err != nil {
			t.Fatalf("ProtectBranch() error = %v", err)
		}
	}
	rule := g.bodies["POST /api/v1/repos/apps/photo-editor/branch_protections"]
	if rule["enable_push"] != false || rule["enable_status_check"] != true || rule["rule_name"] != "main" {
		t.Errorf("rule = %v", rule)
	}
	if rule["enable_merge_whitelist"] != true || fmt.Sprint(rule["merge_whitelist_usernames"]) != "[c4admin]" {
		t.Errorf("rule = %v, want only the operator's user to merge", rule)
	}
	if rule["protected_file_patterns"] != ".gitea/**;Makefile" {
		t.Errorf("protected_file_patterns = %v", rule["protected_file_patterns"])
	}
	if last := g.calls[len(g.calls)-1]; last != "PATCH /api/v1/repos/apps/photo-editor/branch_protections/main" {
		t.Errorf("second call = %s, want the update", last)
	}
}

func TestProtectTagsCreatesThenUpdates(t *testing.T) {
	g, p := newAgentGitea(t)
	repo := p.Repository("photo-editor")
	for range 2 {
		if err := p.ProtectTags(context.Background(), repo, "v*"); err != nil {
			t.Fatalf("ProtectTags() error = %v", err)
		}
	}
	created := g.bodies["POST /api/v1/repos/apps/photo-editor/tag_protections"]
	if created["name_pattern"] != "v*" || fmt.Sprint(created["whitelist_usernames"]) != "[c4admin]" {
		t.Errorf("tag rule = %v", created)
	}
	if last := g.calls[len(g.calls)-1]; last != "PATCH /api/v1/repos/apps/photo-editor/tag_protections/7" {
		t.Errorf("second call = %s, want the update", last)
	}
}

func TestCombinedStatus(t *testing.T) {
	tests := []struct {
		name, body string
		want       CheckStatus
	}{
		{"no checks yet", `{"state":"pending","sha":"abc","total_count":0,"statuses":[]}`, CheckStatus{Commit: "abc"}},
		{"failing check", `{"state":"failure","sha":"abc","total_count":2,"statuses":[` +
			`{"status":"success","target_url":"http://g/runs/1","context":"check / lint (pull_request)"},` +
			`{"status":"failure","target_url":"http://g/runs/2","context":"check / check (pull_request)"}]}`,
			CheckStatus{Commit: "abc", State: CheckFailure, URL: "http://g/runs/2", Contexts: map[string]string{
				"check / lint (pull_request)": CheckSuccess, "check / check (pull_request)": CheckFailure}}},
		{"passing", `{"state":"success","sha":"abc","total_count":1,"statuses":[` +
			`{"status":"success","target_url":"http://g/runs/3","context":"check / check (pull_request)"}]}`,
			CheckStatus{Commit: "abc", State: CheckSuccess, URL: "http://g/runs/3",
				Contexts: map[string]string{"check / check (pull_request)": CheckSuccess}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, p := newAgentGitea(t)
			g.statuses = tt.body
			got, err := p.CombinedStatus(context.Background(), p.Repository("photo-editor"), "main")
			if err != nil {
				t.Fatalf("CombinedStatus() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CombinedStatus() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestEnsureAgentAccess(t *testing.T) {
	g, p := newAgentGitea(t)
	repo := p.Repository("photo-editor")
	for i := range 2 {
		creds, err := p.EnsureAgentAccess(context.Background(), repo, "photo-editor-agent")
		if err != nil {
			t.Fatalf("EnsureAgentAccess() #%d error = %v", i, err)
		}
		if creds != (Credentials{Username: "photo-editor-agent", Token: "b0t-t0ken"}) {
			t.Errorf("creds = %+v", creds)
		}
	}
	created := g.bodies["POST /api/v1/admin/users"]
	if created["restricted"] != true || created["must_change_password"] != false {
		t.Errorf("new user = %v, want restricted and no password change", created)
	}
	if collab := g.bodies["PUT /api/v1/repos/apps/photo-editor/collaborators/photo-editor-agent"]; collab["permission"] != "write" {
		t.Errorf("collaborator = %v", collab)
	}
	if !strings.Contains(strings.Join(g.calls, "\n"), "PATCH /api/v1/admin/users/photo-editor-agent") {
		t.Error("the second call did not reset the existing user's password")
	}
}

func TestPullRequests(t *testing.T) {
	g, p := newAgentGitea(t)
	ctx := context.Background()
	repo := p.Repository("photo-editor")

	pr, err := p.OpenPullRequest(ctx, repo, "task/add-crop", "main", "Add crop", "body")
	if err != nil || pr.Number != 1 || pr.HeadSHA != "abc123" {
		t.Fatalf("OpenPullRequest() = %+v, %v", pr, err)
	}
	again, err := p.OpenPullRequest(ctx, repo, "task/add-crop", "main", "Add crop", "body")
	if err != nil || again.Number != 1 {
		t.Fatalf("second OpenPullRequest() = %+v, %v; want the open one", again, err)
	}
	if err := p.CommentPullRequest(ctx, repo, 1, "Looks good"); err != nil {
		t.Fatalf("CommentPullRequest() error = %v", err)
	}
	if err := p.MergePullRequest(ctx, repo, 1, "abc123", "Add crop", "Task add-crop"); err != nil {
		t.Fatalf("MergePullRequest() error = %v", err)
	}
	if merge := g.bodies["POST /api/v1/repos/apps/photo-editor/pulls/1/merge"]; merge["Do"] != "squash" || merge["delete_branch_after_merge"] != true || merge["head_commit_id"] != "abc123" {
		t.Errorf("merge = %v", merge)
	}
	got, err := p.GetPullRequest(ctx, repo, 1)
	if err != nil || !got.Merged {
		t.Errorf("GetPullRequest() = %+v, %v; want merged", got, err)
	}
	if err := p.MergePullRequest(ctx, repo, 2, "def456", "x", "y"); err == nil || !strings.Contains(err.Error(), "HTTP 405") {
		t.Errorf("MergePullRequest() of an unmergeable PR error = %v", err)
	}
}

func TestCheckLog(t *testing.T) {
	_, p := newAgentGitea(t)
	repo := p.Repository("photo-editor")
	log, err := p.CheckLog(context.Background(), repo, "/apps/photo-editor/actions/runs/27/jobs/27")
	if err != nil || !strings.Contains(log, "make: ***") {
		t.Fatalf("CheckLog() = %q, %v", log, err)
	}
	if _, err := p.CheckLog(context.Background(), repo, "http://gitea/apps/photo-editor/actions/runs/27"); err == nil {
		t.Error("CheckLog() accepted a link without a job")
	}
}

func TestPullRequestFiles(t *testing.T) {
	g, p := newAgentGitea(t)
	files, err := p.PullRequestFiles(context.Background(), p.Repository("photo-editor"), 7)
	if err != nil || len(files) != 53 || files[0] != "f1-0" || files[52] != "f2-2" {
		t.Fatalf("PullRequestFiles() = %d files, %v", len(files), err)
	}
	if want := []string{"page=1&limit=50", "page=2&limit=50"}; !reflect.DeepEqual(g.fileQueries, want) {
		t.Errorf("queries = %v, want %v", g.fileQueries, want)
	}
}

func TestPullRequestFilesNamesBothPathsOfARename(t *testing.T) {
	g, p := newAgentGitea(t)
	g.files = `[{"filename":".gitea/workflows/release.yaml","previous_filename":".gitea/workflows/check.yaml",` +
		`"status":"renamed"},{"filename":"Makefile","status":"changed"},` +
		`{"filename":"README.md","previous_filename":"README.md","status":"changed"}]`
	files, err := p.PullRequestFiles(context.Background(), p.Repository("photo-editor"), 7)
	want := []string{".gitea/workflows/release.yaml", ".gitea/workflows/check.yaml", "Makefile", "README.md"}
	if err != nil || !reflect.DeepEqual(files, want) {
		t.Fatalf("PullRequestFiles() = %q, %v; want %q", files, err, want)
	}
}

func TestPullRequestFilesFailsClosed(t *testing.T) {
	g, p := newAgentGitea(t)
	g.manyFiles = true
	files, err := p.PullRequestFiles(context.Background(), p.Repository("photo-editor"), 7)
	if !errors.Is(err, ErrTooManyFiles) || !strings.Contains(err.Error(), "1000 files or more") || files != nil {
		t.Fatalf("PullRequestFiles() = %d files, %v; want ErrTooManyFiles, not a list cut short", len(files), err)
	}
	if len(g.fileQueries) != maxIssuePages {
		t.Errorf("read %d pages, want %d", len(g.fileQueries), maxIssuePages)
	}
}

func TestClosePullRequest(t *testing.T) {
	g, p := newAgentGitea(t)
	if err := p.ClosePullRequest(context.Background(), p.Repository("photo-editor"), 7); err != nil {
		t.Fatalf("ClosePullRequest() error = %v", err)
	}
	if got := g.bodies["PATCH /api/v1/repos/apps/photo-editor/pulls/7"]; got["state"] != "closed" {
		t.Errorf("body = %v", got)
	}
}

func TestForceMergePullRequest(t *testing.T) {
	g, p := newAgentGitea(t)
	if err := p.ForceMergePullRequest(context.Background(), p.Repository("photo-editor"), 7, "abc", "Add Makefile", "Task x"); err != nil {
		t.Fatalf("ForceMergePullRequest() error = %v", err)
	}
	merge := g.bodies["POST /api/v1/repos/apps/photo-editor/pulls/7/merge"]
	if merge["force_merge"] != true || merge["Do"] != "squash" || merge["head_commit_id"] != "abc" ||
		merge["merge_title_field"] != "Add Makefile" || merge["delete_branch_after_merge"] != true {
		t.Errorf("merge = %v", merge)
	}
}

func TestDeleteBranch(t *testing.T) {
	for _, tc := range []struct {
		status  int
		wantErr bool
	}{
		{http.StatusNoContent, false},
		{http.StatusNotFound, false}, // already gone
		{http.StatusForbidden, true},
	} {
		var call string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			call = r.Method + " " + r.URL.EscapedPath()
			w.WriteHeader(tc.status)
		}))
		p, err := Client{}.Provider(Config{URL: srv.URL, Owner: "apps", Credentials: Credentials{Username: "c4admin", Token: "admin"}})
		if err != nil {
			t.Fatal(err)
		}
		err = p.DeleteBranch(context.Background(), p.Repository("photo-editor"), "standard/photo-editor-4-1")
		srv.Close()
		if (err != nil) != tc.wantErr {
			t.Errorf("HTTP %d: DeleteBranch() error = %v, want error %v", tc.status, err, tc.wantErr)
		}
		if want := "DELETE /api/v1/repos/apps/photo-editor/branches/standard/photo-editor-4-1"; call != want {
			t.Errorf("call = %q, want %q", call, want)
		}
	}
}
