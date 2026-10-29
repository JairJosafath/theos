package gitlab_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/c4factory/internal/adapters/gitlab"
	"github.com/JairJosafath/c4factory/internal/adapters/gitlab/gitlabtest"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/ports"
)

func gitlabReason(t *testing.T, err error, reason string) *ports.ReleaseError {
	t.Helper()
	re, ok := errors.AsType[*ports.ReleaseError](err)
	if !ok {
		t.Fatalf("got %v, want a ReleaseError %s", err, reason)
	}
	if re.Reason != reason {
		t.Fatalf("reason %s (%v), want %s", re.Reason, err, reason)
	}
	if !strings.HasPrefix(re.Message, "gitlab: ") {
		t.Errorf("message %q doesn't start with gitlab: ", re.Message)
	}
	return re
}

// TestGitLabSequence drives the client against the fake through a release's
// whole ship: read, upload, replace, release, and the refusals.
func TestGitLabSequence(t *testing.T) {
	ctx := context.Background()
	srv := gitlabtest.New(t)
	fp := srv.AddProject("shop/sub/web", "tok")
	// A package whose name only contains the wanted one is not it.
	srv.AddProject("shop/other", "tok2")

	gl, err := gitlab.Client{}.GitLab(gitlab.GitLabConfig{URL: srv.URL() + "/", Project: "shop/sub/web", Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}

	info, err := gl.Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != fp.ID || info.Path != "shop/sub/web" || info.DefaultBranch != "main" ||
		info.WebURL != srv.URL()+"/shop/sub/web" || !info.RegistryEnabled || !strings.HasSuffix(info.RegistryPrefix, "/shop/sub/web") {
		t.Errorf("project %+v", info)
	}
	scopes, err := gl.TokenScopes(ctx)
	if err != nil || !slices.Equal(scopes, gitlabtest.DefaultScopes) {
		t.Errorf("scopes %v, %v", scopes, err)
	}
	if user, err := gl.User(ctx); err != nil || user != fmt.Sprintf("project_%d_bot", fp.ID) {
		t.Errorf("user %q, %v", user, err)
	}

	if rel, err := gl.Release(ctx, "web-1.0.0"); err != nil || rel != nil {
		t.Fatalf("release before shipping: %+v, %v", rel, err)
	}
	if commit, err := gl.Tag(ctx, "web-1.0.0"); err != nil || commit != "" {
		t.Fatalf("tag before shipping: %q, %v", commit, err)
	}
	if files, err := gl.PackageFiles(ctx, "web", "1.0.0"); err != nil || files != nil {
		t.Fatalf("files before shipping: %v, %v", files, err)
	}

	gitlabShipFiles(t, gl, fp)
	gitlabShipRelease(t, srv, gl, fp)
	gitlabRefusals(t, srv, gl, fp)
}

// gitlabShipFiles uploads two files, lists them, deletes one and uploads it
// again.
func gitlabShipFiles(t *testing.T, gl gitlab.GitLabProject, fp *gitlabtest.Project) {
	ctx := context.Background()
	upload := func(pkg, name, content string) {
		t.Helper()
		if err := gl.UploadPackageFile(ctx, pkg, "1.0.0", name, int64(len(content)), strings.NewReader(content)); err != nil {
			t.Fatal(err)
		}
	}
	upload("web", "web.tar.gz", "abc")
	upload("web", "web notes.txt", "hello")
	upload("web-extra", "web.tar.gz", "longer content") // contains "web": filtered out
	files, err := gl.PackageFiles(ctx, "web", "1.0.0")
	if err != nil || len(files) != 2 {
		t.Fatalf("files %+v, %v", files, err)
	}
	if files[0].Name != "web.tar.gz" || files[0].Size != 3 || files[1].Name != "web notes.txt" || files[1].Size != 5 {
		t.Errorf("files %+v", files)
	}
	if err := gl.DeletePackageFile(ctx, files[0].PackageID, files[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := gl.DeletePackageFile(ctx, files[0].PackageID, files[0].ID); err != nil {
		t.Errorf("deleting twice: %v", err)
	}
	upload("web", "web.tar.gz", "abcd")
	if got := fp.Files("web", "1.0.0"); got["web.tar.gz"] != 4 || got["web notes.txt"] != 5 || len(got) != 2 {
		t.Errorf("fake's files %v", got)
	}

}

// gitlabShipRelease creates the release with its links and reads it back.
func gitlabShipRelease(t *testing.T, srv *gitlabtest.Server, gl gitlab.GitLabProject, fp *gitlabtest.Project) {
	ctx := context.Background()
	marker := "<!-- c4factory:web -->"
	created, err := gl.CreateRelease(ctx, gitlab.GitLabRelease{Tag: "web-1.0.0", Name: "web 1.0.0",
		Description: "notes\n" + marker, Ref: "main", Links: []gitlab.GitLabReleaseLink{
			{Name: "web.tar.gz", URL: srv.URL() + "/x", DirectAssetPath: "/web.tar.gz", LinkType: "package"}}})
	if err != nil {
		t.Fatal(err)
	}
	if created.TagName != "web-1.0.0" || created.Target != fp.DefaultBranchHead ||
		created.URL != srv.URL()+"/shop/sub/web/-/releases/web-1.0.0" {
		t.Errorf("created %+v", created)
	}
	if rels := fp.Releases(); len(rels) != 1 || len(rels[0].Links) != 1 || rels[0].Links[0].LinkType != "package" ||
		rels[0].Links[0].DirectAssetPath != "/web.tar.gz" {
		t.Errorf("fake's releases %+v", rels)
	}
	rel, err := gl.Release(ctx, "web-1.0.0")
	if err != nil || rel == nil || !strings.Contains(rel.Body, marker) || rel.Target != fp.DefaultBranchHead {
		t.Fatalf("release %+v, %v", rel, err)
	}
	if commit, err := gl.Tag(ctx, "web-1.0.0"); err != nil || commit != fp.DefaultBranchHead {
		t.Errorf("tag %q, %v", commit, err)
	}

	fp.SetTag("web-1.1.0", strings.Repeat("b", 40))
	if _, err := gl.CreateRelease(ctx, gitlab.GitLabRelease{Tag: "web-1.1.0", Description: marker,
		Ref: strings.Repeat("c", 40)}); err != nil {
		t.Fatal(err)
	}
	all, err := gl.ListReleases(ctx)
	if err != nil || len(all) != 2 || all[0].TagName != "web-1.1.0" || all[0].Target != strings.Repeat("b", 40) ||
		all[1].TagName != "web-1.0.0" {
		t.Errorf("releases %+v, %v", all, err)
	}

}

// gitlabRefusals: a second release of a tag, a wrong token, a project the
// token can't see and the package registry off.
func gitlabRefusals(t *testing.T, srv *gitlabtest.Server, gl gitlab.GitLabProject, fp *gitlabtest.Project) {
	ctx := context.Background()
	_, err := gl.CreateRelease(ctx, gitlab.GitLabRelease{Tag: "web-1.0.0", Ref: "main"})
	if re := gitlabReason(t, err, gitlab.ReasonGitLabRejected); re.Status != http.StatusConflict ||
		!strings.Contains(re.Message, "Release already exists") {
		t.Errorf("second create: %v", re)
	}

	wrong, err := gitlab.Client{}.GitLab(gitlab.GitLabConfig{URL: srv.URL(), Project: "shop/sub/web", Token: "nope"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = wrong.Project(ctx)
	_ = gitlabReason(t, err, ports.ReasonUnauthorized)
	_, err = wrong.TokenScopes(ctx)
	_ = gitlabReason(t, err, ports.ReasonUnauthorized)

	for _, project := range []string{"shop/missing", "shop/other"} {
		missing, err := gitlab.Client{}.GitLab(gitlab.GitLabConfig{URL: srv.URL(), Project: project, Token: "tok"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = missing.Project(ctx)
		if re := gitlabReason(t, err, ports.ReasonRepositoryNotFound); !strings.Contains(re.Message, "can't see project "+project) {
			t.Errorf("%s: %v", project, re)
		}
		// A 404 on the project is not "no release".
		_, err = missing.Release(ctx, "web-1.0.0")
		_ = gitlabReason(t, err, ports.ReasonRepositoryNotFound)
	}

	fp.PackagesEnabled = false
	err = gl.UploadPackageFile(ctx, "web", "1.0.1", "web.tar.gz", 1, strings.NewReader("x"))
	if re := gitlabReason(t, err, gitlab.ReasonGitLabRejected); !strings.Contains(re.Message, "Package registry is disabled") {
		t.Errorf("packages off: %v", re)
	}
	_, err = gl.PackageFiles(ctx, "web", "1.0.0")
	_ = gitlabReason(t, err, gitlab.ReasonGitLabRejected)
}

func TestGitLabConfig(t *testing.T) {
	for _, cfg := range []gitlab.GitLabConfig{
		{Project: "web", Token: "t"},
		{Project: "group/", Token: "t"},
		{Project: "/group/web", Token: "t"},
		{Project: "group/web", Token: ""},
		{Project: "group/" + strings.Repeat("a", 250), Token: "t"},
		{URL: "ftp://example.com", Project: "group/web", Token: "t"},
	} {
		if _, err := (gitlab.Client{}).GitLab(cfg); err == nil {
			t.Errorf("%+v: no error", cfg)
		}
	}
	if _, err := (gitlab.Client{}).GitLab(gitlab.GitLabConfig{Project: "group/sub/web.app", Token: "t"}); err != nil {
		t.Error(err)
	}
}

// TestGitLabFailures maps GitLab's other answers: a 5xx and a rate limit may
// pass, an object message reads as its fields, and a 404 under a relative URL
// root still knows the project.
func TestGitLabFailures(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		status int
		body   string
		reason string
		says   string
	}{
		{http.StatusInternalServerError, `<html>oops</html>`, gitlab.ReasonGitLabError, "Internal Server Error"},
		{http.StatusTooManyRequests, `{"message":"slow down"}`, gitlab.ReasonGitLabError, "slow down"},
		{http.StatusBadRequest, `{"message":{"tag_name":["is invalid"],"ref":["is missing"]}}`,
			gitlab.ReasonGitLabRejected, "ref is missing, tag_name is invalid"},
		{http.StatusBadRequest, `{"error":"tag_name is missing"}`, gitlab.ReasonGitLabRejected, "tag_name is missing"},
		{http.StatusForbidden, `{"message":"403 Forbidden"}`, ports.ReasonUnauthorized, "403 Forbidden"},
		{http.StatusNotFound, `{"message":"404 Not Found"}`, ports.ReasonRepositoryNotFound, "can't see project g/p"},
		{http.StatusOK, `not json`, gitlab.ReasonGitLabError, "unreadable"},
	}
	for _, c := range cases {
		var path string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path = r.URL.EscapedPath()
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(c.body))
		}))
		gl, err := gitlab.Client{}.GitLab(gitlab.GitLabConfig{URL: srv.URL + "/gitlab", Project: "g/p", Token: "t"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = gl.Project(ctx)
		srv.Close()
		if path != "/gitlab/api/v4/projects/g%2Fp" {
			t.Errorf("path %s", path)
		}
		if re := gitlabReason(t, err, c.reason); !strings.Contains(re.Message, c.says) {
			t.Errorf("%d %s: %q doesn't say %q", c.status, c.body, re.Message, c.says)
		}
	}
}

// A 404 on the token's own record is the token's (an instance or a token
// with no PAT record): Unauthorized, so Check refuses the credential
// (docs/plans/gitlab-deploy.md, Decision 2).
func TestGitLabTokenNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"404 Not Found"}`))
	}))
	defer srv.Close()
	gl, err := gitlab.Client{}.GitLab(gitlab.GitLabConfig{URL: srv.URL, Project: "g/p", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = gl.TokenScopes(context.Background())
	if re := gitlabReason(t, err, ports.ReasonUnauthorized); !strings.Contains(re.Message, "no record of the token") {
		t.Errorf("message %q", re.Message)
	}
}

// countingCloser counts its closes: the client never closes a reader it
// doesn't own, not even an empty one.
type countingCloser struct {
	strings.Reader
	closes int
}

func (c *countingCloser) Close() error { c.closes++; return nil }

func TestGitLabUploadEmptyFile(t *testing.T) {
	ctx := context.Background()
	srv := gitlabtest.New(t)
	fp := srv.AddProject("shop/web", "tok")
	gl, err := gitlab.Client{}.GitLab(gitlab.GitLabConfig{URL: srv.URL(), Project: "shop/web", Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	empty := &countingCloser{}
	if err := gl.UploadPackageFile(ctx, "web", "1.0.0", "empty.txt", 0, empty); err != nil {
		t.Fatal(err)
	}
	if empty.closes != 0 {
		t.Errorf("the client closed the reader %d times, want 0", empty.closes)
	}
	if files := fp.Files("web", "1.0.0"); files["empty.txt"] != 0 || len(files) != 1 {
		t.Errorf("files %v, want empty.txt at 0 bytes", files)
	}
}

// TestGitLabHosts: the API base is the model's rule, so a url with spaces or
// a trailing slash reads the same as the links Ship writes.
func TestGitLabHosts(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()
	gl, err := gitlab.Client{}.GitLab(gitlab.GitLabConfig{URL: " " + srv.URL + "/gitlab/ ", Project: "g/p", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gl.Project(context.Background()); err != nil || path != "/gitlab/api/v4/projects/g%2Fp" {
		t.Errorf("path %s, %v", path, err)
	}
}

// TestGitLabSource drives the source calls against the fake: the branch's
// head, a commit through the Commits API, the tree it leaves, and its tag.
func TestGitLabSource(t *testing.T) {
	ctx := context.Background()
	srv := gitlabtest.New(t)
	fp := srv.AddProject("shop/web", "tok")
	gl, err := gitlab.Client{}.GitLab(gitlab.GitLabConfig{URL: srv.URL(), Project: "shop/web", Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}

	head, err := gl.Branch(ctx, "main")
	if err != nil || head != fp.DefaultBranchHead {
		t.Fatalf("branch main %q, %v; want %q", head, err, fp.DefaultBranchHead)
	}
	if head, err := gl.Branch(ctx, "nope"); err != nil || head != "" {
		t.Fatalf("missing branch %q, %v", head, err)
	}
	if entries, err := gl.Tree(ctx, "main", "web"); err != nil || entries != nil {
		t.Fatalf("tree of a missing path %v, %v", entries, err)
	}
	if entries, err := gl.Tree(ctx, "main", ""); err != nil || len(entries) != 0 {
		t.Fatalf("empty tree %v, %v", entries, err)
	}

	executable := true
	commit, err := gl.CreateCommit(ctx, gitlab.GitLabCommit{Branch: "main", Message: "web 1.0.0",
		Actions: []gitlab.GitLabCommitAction{
			{Action: "create", FilePath: "web/go.mod", Content: base64.StdEncoding.EncodeToString([]byte("module web\n")),
				Encoding: "base64"},
			{Action: "create", FilePath: "web/cmd/run.sh", Content: "#!/bin/sh\n"},
			{Action: "chmod", FilePath: "web/cmd/run.sh", ExecuteFilemode: &executable},
			{Action: "create", FilePath: "README.md", Content: "hi\n"},
		}})
	if err != nil || len(commit) != 40 {
		t.Fatalf("commit %q, %v", commit, err)
	}
	if fp.DefaultBranchHead != commit {
		t.Errorf("branch head %q, want the new commit %q", fp.DefaultBranchHead, commit)
	}
	if got := fp.Folder(commit, "web"); len(got) != 2 || got["web/go.mod"] != "module web\n" || got["web/cmd/run.sh"] != "#!/bin/sh\n" {
		t.Errorf("folder web %v", got)
	}

	entries, err := gl.Tree(ctx, commit, "web")
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		paths = append(paths, e.Type+" "+e.Path+" "+e.Mode)
		if e.Type == "blob" && e.Path == "web/go.mod" && e.ID != model.BlobSHA([]byte("module web\n")) {
			t.Errorf("go.mod id %q", e.ID)
		}
	}
	slices.Sort(paths)
	if want := []string{"blob web/cmd/run.sh 100755", "blob web/go.mod 100644", "tree web/cmd 040000"}; !slices.Equal(paths, want) {
		t.Errorf("tree %v, want %v", paths, want)
	}
	if entries, err := gl.Tree(ctx, "main", "web/nope"); err != nil || entries != nil {
		t.Errorf("tree of a missing path after the commit %v, %v", entries, err)
	}
	gitlabSourceTag(t, gl, fp, commit)
}

// gitlabSourceTag tags the commit, refuses a second tag, and sees the tag
// stay put when the branch moves on.
func gitlabSourceTag(t *testing.T, gl gitlab.GitLabProject, fp *gitlabtest.Project, commit string) {
	ctx := context.Background()
	if err := gl.CreateTag(ctx, "web-1.0.0", commit); err != nil {
		t.Fatal(err)
	}
	if fp.Tag("web-1.0.0") != commit {
		t.Errorf("fake's tag %q, want %q", fp.Tag("web-1.0.0"), commit)
	}
	if got, err := gl.Tag(ctx, "web-1.0.0"); err != nil || got != commit {
		t.Errorf("tag %q, %v", got, err)
	}
	if got := fp.Folder("web-1.0.0", ""); len(got) != 3 {
		t.Errorf("folder at the tag %v", got)
	}
	err := gl.CreateTag(ctx, "web-1.0.0", commit)
	if re := gitlabReason(t, err, gitlab.ReasonGitLabRejected); re.Status != http.StatusBadRequest {
		t.Errorf("second tag: %v", err)
	}
	// A second commit updates and deletes; the tag stays at the first.
	next, err := gl.CreateCommit(ctx, gitlab.GitLabCommit{Branch: "main", Message: "web 1.1.0",
		Actions: []gitlab.GitLabCommitAction{
			{Action: "update", FilePath: "web/go.mod", Content: "module web2\n"},
			{Action: "delete", FilePath: "web/cmd/run.sh"},
		}})
	if err != nil {
		t.Fatal(err)
	}
	if got := fp.Folder("main", "web"); len(got) != 1 || got["web/go.mod"] != "module web2\n" {
		t.Errorf("folder after the second commit %v", got)
	}
	if got := fp.Folder("web-1.0.0", "web"); len(got) != 2 || fp.DefaultBranchHead != next {
		t.Errorf("tag moved: %v, head %q", got, fp.DefaultBranchHead)
	}
	_, err = gl.CreateCommit(ctx, gitlab.GitLabCommit{Branch: "main", Message: "again",
		Actions: []gitlab.GitLabCommitAction{{Action: "create", FilePath: "web/go.mod", Content: "x"}}})
	_ = gitlabReason(t, err, gitlab.ReasonGitLabRejected)
	// A last_commit_id that is not the file's own last commit is refused, as
	// GitLab does (#1950: the file's last commit, not the branch's head);
	// README.md's is still the first commit, web/go.mod's the second. Chmod
	// alone moves the mode while an update keeps it.
	executable, notExecutable := true, false
	_, err = gl.CreateCommit(ctx, gitlab.GitLabCommit{Branch: "main", Message: "stale",
		Actions: []gitlab.GitLabCommitAction{{Action: "update", FilePath: "web/go.mod", Content: "y", LastCommitID: commit}}})
	if re := gitlabReason(t, err, gitlab.ReasonGitLabRejected); re.Status != http.StatusBadRequest ||
		!strings.Contains(re.Message, "has changed since") {
		t.Errorf("stale last_commit_id: %v", err)
	}
	if _, err = gl.CreateCommit(ctx, gitlab.GitLabCommit{Branch: "main", Message: "modes",
		Actions: []gitlab.GitLabCommitAction{
			{Action: "chmod", FilePath: "web/go.mod", ExecuteFilemode: &executable, LastCommitID: next},
			{Action: "update", FilePath: "README.md", Content: "hi again\n", ExecuteFilemode: &notExecutable, LastCommitID: commit},
		}}); err != nil {
		t.Fatal(err)
	}
	if got := fp.Mode("main", "web/go.mod"); got != "100755" {
		t.Errorf("mode after chmod %q, want 100755", got)
	}
	if got := fp.Mode("web-1.0.0", "web/go.mod"); got != "100644" {
		t.Errorf("mode at the tag %q, want 100644 still", got)
	}
	if entries, err := gl.Tree(ctx, "main", "web"); err != nil || len(entries) != 1 || entries[0].Mode != "100755" {
		t.Errorf("tree after chmod %v, %v", entries, err)
	}
	_, err = gl.CreateCommit(ctx, gitlab.GitLabCommit{Branch: "main", Message: "no mode",
		Actions: []gitlab.GitLabCommitAction{{Action: "chmod", FilePath: "README.md"}}})
	_ = gitlabReason(t, err, gitlab.ReasonGitLabRejected)
}
