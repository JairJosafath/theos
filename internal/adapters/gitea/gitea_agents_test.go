package gitea

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/JairJosafath/c4factory/internal/ports"
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
	// botToken is the token Gitea issues for the agent's bot account.
	botToken string
	// botTokens are the names of the bot account's tokens.
	botTokens map[string]bool
}

func newAgentGitea(t *testing.T) (*agentGitea, ports.Provider) {
	t.Helper()
	g := &agentGitea{t: t, bodies: map[string]map[string]any{}, users: map[string]bool{}, rules: map[string]bool{},
		botToken: rand.Text(), botTokens: map[string]bool{}}
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	p, err := Client{}.Provider(ports.Config{URL: srv.URL, Owner: "apps", Credentials: ports.Credentials{Username: "c4admin", Token: rand.Text()}})
	if err != nil {
		t.Fatal(err)
	}
	return g, p
}

// serveBotUser reads or deletes the bot user photo-editor-agent.
func (g *agentGitea) serveBotUser(method string, reply func(int, any)) {
	switch {
	case !g.users["photo-editor-agent"]:
		reply(http.StatusNotFound, map[string]string{"message": "user does not exist"})
	case method == http.MethodDelete:
		delete(g.users, "photo-editor-agent")
		reply(http.StatusNoContent, nil)
	default:
		reply(http.StatusOK, map[string]string{"login": "photo-editor-agent"})
	}
}

// serveBotTokens lists the tokens of the bot user photo-editor-agent, or
// deletes or makes one by name.
func (g *agentGitea) serveBotTokens(call string, body map[string]any, reply func(int, any)) {
	if call == "GET /api/v1/users/photo-editor-agent/tokens" {
		list := make([]map[string]any, 0, len(g.botTokens))
		for _, name := range slices.Sorted(maps.Keys(g.botTokens)) {
			list = append(list, map[string]any{"name": name})
		}
		reply(http.StatusOK, list)
		return
	}
	if name, ok := strings.CutPrefix(call, "DELETE /api/v1/users/photo-editor-agent/tokens/"); ok {
		if !g.botTokens[name] {
			reply(http.StatusNotFound, map[string]string{"message": "token not found"})
			return
		}
		delete(g.botTokens, name)
		reply(http.StatusNoContent, nil)
		return
	}
	name, _ := body["name"].(string)
	if g.botTokens[name] {
		reply(http.StatusUnprocessableEntity, map[string]string{"message": "access token name has been used already"})
		return
	}
	g.botTokens[name] = true
	reply(http.StatusCreated, map[string]string{"sha1": g.botToken})
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
	case strings.HasPrefix(call, "GET "+repo+"/actions/jobs/"):
		serveJobLog(strings.TrimPrefix(call, "GET "+repo+"/actions/jobs/"), w, reply)
	case strings.HasPrefix(call, "GET "+repo+"/commits/"):
		_, _ = io.WriteString(w, g.statuses)
	case call == "GET /api/v1/users/photo-editor-agent", call == "DELETE /api/v1/admin/users/photo-editor-agent":
		g.serveBotUser(r.Method, reply)
	case call == "POST /api/v1/admin/users":
		g.users[body["username"].(string)] = true
		reply(http.StatusCreated, map[string]string{})
	case call == "PATCH /api/v1/admin/users/photo-editor-agent":
		if !g.users["photo-editor-agent"] {
			reply(http.StatusNotFound, map[string]string{"message": "user does not exist"})
			return
		}
		reply(http.StatusOK, map[string]string{})
	case call == "PUT "+repo+"/collaborators/photo-editor-agent":
		w.WriteHeader(http.StatusNoContent)
	case strings.HasPrefix(r.URL.Path, "/api/v1/users/photo-editor-agent/tokens"):
		if !basic || user != "photo-editor-agent" {
			g.t.Errorf("%s without the bot's basic auth", call)
		}
		g.serveBotTokens(call, body, reply)
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
	case call == "GET "+repo+"/pulls/3":
		reply(http.StatusNotFound, map[string]string{"message": "pull request does not exist"})
	case strings.Contains(call, "/pulls/7"):
		g.serveGatedPull(w, r, call)
	case call == "POST "+repo+"/issues/1/comments":
		reply(http.StatusCreated, map[string]string{})
	case call == "POST "+repo+"/pulls/1/merge":
		sha := "694db57"
		g.pulls[0].Merged, g.pulls[0].MergeCommit = true, &sha
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
		rule := ports.BranchRule{StatusContexts: []string{"check*"}, ProtectedPaths: []string{".gitea/", "Makefile"}}
		if err := p.ProtectBranch(context.Background(), repo, "main", rule); err != nil {
			t.Fatalf("ProtectBranch() error = %v", err)
		}
	}
	rule := g.bodies["POST /api/v1/repos/apps/photo-editor/branch_protections"]
	if rule["enable_push"] != false || rule["enable_status_check"] != true || rule["rule_name"] != "main" ||
		fmt.Sprint(rule["push_whitelist_usernames"]) != "[]" {
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

func TestProtectBranchOperatorPushes(t *testing.T) {
	g, p := newAgentGitea(t)
	repo := p.Repository("photo-editor")
	if err := p.ProtectBranch(context.Background(), repo, "main", ports.BranchRule{OperatorPushes: true}); err != nil {
		t.Fatalf("ProtectBranch() error = %v", err)
	}
	rule := g.bodies["POST /api/v1/repos/apps/photo-editor/branch_protections"]
	if rule["enable_push"] != true || rule["enable_push_whitelist"] != true ||
		fmt.Sprint(rule["push_whitelist_usernames"]) != "[c4admin]" {
		t.Errorf("rule = %v, want only the operator's user to push", rule)
	}
	if rule["enable_status_check"] != false {
		t.Errorf("rule = %v, want no status checks when none are named", rule)
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
		want       ports.CheckStatus
	}{
		{"no checks yet", `{"state":"pending","sha":"abc","total_count":0,"statuses":[]}`, ports.CheckStatus{Commit: "abc"}},
		{"failing check", `{"state":"failure","sha":"abc","total_count":2,"statuses":[` +
			`{"status":"success","target_url":"http://g/runs/1","context":"check / lint (pull_request)"},` +
			`{"status":"failure","target_url":"http://g/runs/2","context":"check / check (pull_request)"}]}`,
			ports.CheckStatus{Commit: "abc", State: ports.CheckFailure, URL: "http://g/runs/2", Contexts: map[string]string{
				"check / lint (pull_request)": ports.CheckSuccess, "check / check (pull_request)": ports.CheckFailure}}},
		{"passing", `{"state":"success","sha":"abc","total_count":1,"statuses":[` +
			`{"status":"success","target_url":"http://g/runs/3","context":"check / check (pull_request)"}]}`,
			ports.CheckStatus{Commit: "abc", State: ports.CheckSuccess, URL: "http://g/runs/3",
				Contexts: map[string]string{"check / check (pull_request)": ports.CheckSuccess}}},
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
		if creds != (ports.Credentials{Username: "photo-editor-agent", Token: g.botToken}) {
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

// An operator upgrade that changes the scopes makes a new token beside the one
// the operator made before, which a running sandbox of the Container may still
// use (#519).
func TestEnsureAgentAccessKeepsTokensOfOtherScopes(t *testing.T) {
	g, p := newAgentGitea(t)
	g.users["photo-editor-agent"] = true
	g.botTokens["c4factory-agent"] = true // made before the token carried read:package
	repo := p.Repository("photo-editor")
	for i := range 2 {
		if _, err := p.EnsureAgentAccess(context.Background(), repo, "photo-editor-agent"); err != nil {
			t.Fatalf("EnsureAgentAccess() #%d error = %v", i, err)
		}
	}
	if slices.Contains(g.calls, "DELETE /api/v1/users/photo-editor-agent/tokens/c4factory-agent") {
		t.Error("EnsureAgentAccess revoked the token made with other scopes")
	}
	if !g.botTokens[agentTokenName] || len(g.botTokens) != 2 {
		t.Errorf("tokens = %v, want the old one and %s", g.botTokens, agentTokenName)
	}
	if !strings.Contains(strings.Join(g.calls, "\n"), "DELETE /api/v1/users/photo-editor-agent/tokens/"+agentTokenName) {
		t.Error("the second call did not rotate the token of these scopes")
	}
}

// Once no sandbox uses them, the tokens made before an upgrade go; the current
// one and tokens the operator didn't make stay (#584).
func TestRevokeOldAgentTokens(t *testing.T) {
	g, p := newAgentGitea(t)
	ctx := context.Background()
	if err := p.RevokeOldAgentTokens(ctx, "photo-editor-agent"); err != nil {
		t.Fatalf("RevokeOldAgentTokens() of a missing user error = %v", err)
	}
	g.users["photo-editor-agent"] = true
	g.botTokens["c4factory-agent"] = true          // made before the token carried read:package
	g.botTokens["c4factory-agent-00000000"] = true // made with other scopes
	g.botTokens["someone-else"] = true
	if _, err := p.EnsureAgentAccess(ctx, p.Repository("photo-editor"), "photo-editor-agent"); err != nil {
		t.Fatal(err)
	}
	if err := p.RevokeOldAgentTokens(ctx, "photo-editor-agent"); err != nil {
		t.Fatalf("RevokeOldAgentTokens() error = %v", err)
	}
	want := []string{agentTokenName, "someone-else"}
	if got := slices.Sorted(maps.Keys(g.botTokens)); !slices.Equal(got, want) {
		t.Errorf("tokens = %v, want %v", got, want)
	}
}

func TestRemoveAgentAccess(t *testing.T) {
	g, p := newAgentGitea(t)
	ctx := context.Background()
	repo := p.Repository("photo-editor")
	if _, err := p.EnsureAgentAccess(ctx, repo, "photo-editor-agent"); err != nil {
		t.Fatal(err)
	}
	for i := range 2 { // the second finds the user gone
		if err := p.RemoveAgentAccess(ctx, repo, "photo-editor-agent"); err != nil {
			t.Fatalf("RemoveAgentAccess() #%d error = %v", i, err)
		}
	}
	if g.users["photo-editor-agent"] {
		t.Error("the bot user is still there")
	}
	if !strings.Contains(strings.Join(g.calls, "\n"), "DELETE /api/v1/admin/users/photo-editor-agent") {
		t.Error("the bot user was not deleted")
	}
}

func TestPullRequests(t *testing.T) {
	g, p := newAgentGitea(t)
	ctx := context.Background()
	repo := p.Repository("photo-editor")

	pr, err := p.OpenPullRequest(ctx, repo, "task/add-crop", "main", "Add crop", "body")
	if err != nil || pr.Number != 1 || pr.HeadSHA != "abc123" || pr.MergeCommit != "" {
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
	if err != nil || !got.Merged || got.MergeCommit != "694db57" {
		t.Errorf("GetPullRequest() = %+v, %v; want merged at 694db57", got, err)
	}
	if _, err := p.GetPullRequest(ctx, repo, 3); !errors.Is(err, ports.ErrPullRequestNotFound) {
		t.Errorf("GetPullRequest() of a missing pull request error = %v, want ErrPullRequestNotFound", err)
	}
	if err := p.MergePullRequest(ctx, repo, 2, "def456", "x", "y"); err == nil || !strings.Contains(err.Error(), "HTTP 405") {
		t.Errorf("MergePullRequest() of an unmergeable PR error = %v", err)
	}
}

// serveJobLog serves the log of a CI job: 27 failed, 28 started and has no
// log file, 29 an empty one, and any other hasn't started.
func serveJobLog(path string, w http.ResponseWriter, reply func(int, any)) {
	switch path {
	case "27/logs":
		_, _ = io.WriteString(w, "make: *** [Makefile:21: test] Error 1\n")
	case "28/logs":
		reply(http.StatusNotFound, map[string]string{"message": "logs not found"})
	case "29/logs":
	default:
		reply(http.StatusNotFound, map[string]string{"message": "job not started"})
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
	for _, job := range []string{"28", "29"} {
		if _, err := p.CheckLog(context.Background(), repo, "/apps/photo-editor/actions/runs/27/jobs/"+job); !errors.Is(err, ports.ErrNoCILog) {
			t.Errorf("CheckLog() of job %s, which started and has no log: error = %v, want ErrNoCILog", job, err)
		}
	}
	if _, err := p.CheckLog(context.Background(), repo, "/apps/photo-editor/actions/runs/27/jobs/30"); err == nil ||
		errors.Is(err, ports.ErrNoCILog) {
		t.Errorf("CheckLog() of a job not started: error = %v, want another error", err)
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
	if !errors.Is(err, ports.ErrTooManyFiles) || !strings.Contains(err.Error(), "1000 files or more") || files != nil {
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

func TestHasBranch(t *testing.T) {
	for _, tc := range []struct {
		status  int
		want    bool
		wantErr bool
	}{
		{http.StatusOK, true, false},
		{http.StatusNotFound, false, false},
		{http.StatusForbidden, false, true},
	} {
		var call string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			call = r.Method + " " + r.URL.EscapedPath()
			w.WriteHeader(tc.status)
		}))
		p, err := Client{}.Provider(ports.Config{URL: srv.URL, Owner: "apps", Credentials: ports.Credentials{Username: "c4admin", Token: rand.Text()}})
		if err != nil {
			t.Fatal(err)
		}
		got, err := p.HasBranch(context.Background(), p.Repository("photo-editor"), "standard/photo-editor-4-1")
		srv.Close()
		if got != tc.want || (err != nil) != tc.wantErr {
			t.Errorf("HTTP %d: HasBranch() = %v, %v, want %v, error %v", tc.status, got, err, tc.want, tc.wantErr)
		}
		if want := "GET /api/v1/repos/apps/photo-editor/branches/standard/photo-editor-4-1"; call != want {
			t.Errorf("call = %q, want %q", call, want)
		}
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
		p, err := Client{}.Provider(ports.Config{URL: srv.URL, Owner: "apps", Credentials: ports.Credentials{Username: "c4admin", Token: rand.Text()}})
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

func TestBranchCommit(t *testing.T) {
	for _, tc := range []struct {
		status  int
		body    string
		want    string
		wantErr bool
	}{
		{http.StatusOK, `{"name":"release-check/v0.2.0","commit":{"id":"694db57"}}`, "694db57", false},
		{http.StatusNotFound, `{"message":"branch does not exist"}`, "", false},
		{http.StatusOK, `{"name":"release-check/v0.2.0"}`, "", true},
		{http.StatusForbidden, `{"message":"no"}`, "", true},
	} {
		var call string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			call = r.Method + " " + r.URL.EscapedPath()
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		p, err := Client{}.Provider(ports.Config{URL: srv.URL, Owner: "apps", Credentials: ports.Credentials{Username: "c4admin", Token: rand.Text()}})
		if err != nil {
			t.Fatal(err)
		}
		got, err := p.BranchCommit(context.Background(), p.Repository("photo-editor"), "release-check/v0.2.0")
		srv.Close()
		if got != tc.want || (err != nil) != tc.wantErr {
			t.Errorf("HTTP %d %s: BranchCommit() = %q, %v, want %q, error %v", tc.status, tc.body, got, err, tc.want,
				tc.wantErr)
		}
		if want := "GET /api/v1/repos/apps/photo-editor/branches/release-check/v0.2.0"; call != want {
			t.Errorf("call = %q, want %q", call, want)
		}
	}
}

func TestCreateBranch(t *testing.T) {
	for _, tc := range []struct {
		status  int
		wantErr bool
	}{
		{http.StatusCreated, false},
		{http.StatusConflict, true}, // exists already
		{http.StatusNotFound, true}, // no such commit
	} {
		var call, body string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			data, _ := io.ReadAll(r.Body)
			call, body = r.Method+" "+r.URL.EscapedPath(), string(data)
			w.WriteHeader(tc.status)
		}))
		p, err := Client{}.Provider(ports.Config{URL: srv.URL, Owner: "apps", Credentials: ports.Credentials{Username: "c4admin", Token: rand.Text()}})
		if err != nil {
			t.Fatal(err)
		}
		err = p.CreateBranch(context.Background(), p.Repository("photo-editor"), "release-check/v0.2.0", "694db57")
		srv.Close()
		if (err != nil) != tc.wantErr {
			t.Errorf("HTTP %d: CreateBranch() error = %v, want error %v", tc.status, err, tc.wantErr)
		}
		if want := "POST /api/v1/repos/apps/photo-editor/branches"; call != want {
			t.Errorf("call = %q, want %q", call, want)
		}
		if want := `{"new_branch_name":"release-check/v0.2.0","old_ref_name":"694db57"}`; body != want {
			t.Errorf("body = %s, want %s", body, want)
		}
	}
}
