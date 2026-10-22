package gitprovider

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

const fakeGitHubRepo = "acme/cel-lens"

// fakeGitHub is GitHub's release and Git Data API for one repository. It fails
// the test on a request that isn't a githubCall, or without the token.
type fakeGitHub struct {
	errorf func(format string, args ...any)
	token  string

	mu       sync.Mutex
	releases []*githubRelease
	assets   map[int64][]githubAsset
	nextID   int64
	paths    []string
	// fail, when set, answers every request instead.
	fail func(w http.ResponseWriter)
	// uploads records each upload's Content-Length and Transfer-Encoding.
	uploads []string
	// makeLatest records each PATCH's make_latest.
	makeLatest []string
	// latest is the id of the Latest release, 0 when there is none: set as
	// make_latest says, "legacy" or absent on publishing a draft counting as
	// true, as GitHub picks the newest.
	latest int64
	// git is the repository's objects and refs.
	git *fakeGit
}

func newFakeGitHub(t *testing.T) (*fakeGitHub, ReleaseTarget) {
	t.Helper()
	f := &fakeGitHub{errorf: t.Errorf, token: rand.Text(), assets: map[int64][]githubAsset{}, nextID: 100,
		git: newFakeGit()}
	api := httptest.NewServer(f.on(false))
	upload := httptest.NewServer(f.on(true))
	t.Cleanup(api.Close)
	t.Cleanup(upload.Close)
	target, err := Client{}.GitHubReleases(GitHubConfig{Repository: fakeGitHubRepo, Token: f.token,
		APIURL: api.URL, UploadURL: upload.URL})
	if err != nil {
		t.Fatalf("GitHubReleases() error = %v", err)
	}
	return f, target
}

// releaseCall names a request by its method and path, ids as {id}.
var releaseCall = regexp.MustCompile(`/[0-9]+(/|$)`)

// on serves the API host, or the upload host, which takes asset uploads only.
func (f *fakeGitHub) on(upload bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		call := r.Method + " " + releaseCall.ReplaceAllString(r.URL.Path, "/{id}$1")
		if isUpload := call == "POST /repos/"+fakeGitHubRepo+"/releases/{id}/assets"; isUpload != upload {
			f.errorf("request %s went to the wrong host (upload host: %v)", call, upload)
		}
		f.serve(w, r)
	}
}

// githubCall reports whether a request is one the client may make: under
// /repos/{owner}/{repo}/releases, under /repos/{owner}/{repo}/git/, or GET
// /repos/{owner}/{repo} for the default branch (the plan's decision 3).
func githubCall(method, path string) bool {
	repo := "/repos/" + fakeGitHubRepo
	if strings.Contains(path, "..") || strings.Contains(path, "//") {
		return false
	}
	return path == repo+"/releases" || strings.HasPrefix(path, repo+"/releases/") ||
		strings.HasPrefix(path, repo+"/git/") || method == http.MethodGet && path == repo
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, r.Method+" "+releaseCall.ReplaceAllString(r.URL.Path, "/{id}$1"))
	if !githubCall(r.Method, r.URL.Path) {
		f.errorf("request %s %s is not a release, Git Data or default branch call", r.Method, r.URL.Path)
		http.NotFound(w, r)
		return
	}
	if got := r.Header.Get("Authorization"); got != "Bearer "+f.token {
		f.errorf("request %s %s has Authorization %q", r.Method, r.URL.Path, got)
	}
	if f.fail != nil {
		f.fail(w)
		return
	}
	prefix := "/repos/" + fakeGitHubRepo + "/releases"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		f.git.serve(f, w, r)
		return
	}
	f.serveReleases(w, r, strings.Split(strings.TrimPrefix(r.URL.Path, prefix), "/")[1:])
}

// serveReleases answers a release request, rest its path below /releases.
func (f *fakeGitHub) serveReleases(w http.ResponseWriter, r *http.Request, rest []string) {
	switch {
	case len(rest) == 0 && r.Method == http.MethodGet:
		out := make([]githubRelease, 0, len(f.releases))
		for _, rel := range f.releases {
			out = append(out, *rel)
		}
		f.page(w, r, out)
	case len(rest) == 0 && r.Method == http.MethodPost:
		f.createRelease(w, r)
	case len(rest) == 1 && rest[0] == "latest" && r.Method == http.MethodGet:
		if rel := f.release(strconv.FormatInt(f.latest, 10)); rel != nil && !rel.Draft {
			f.json(w, http.StatusOK, rel)
			return
		}
		f.reply(w, http.StatusNotFound, `{"message":"Not Found"}`)
	case len(rest) == 1 && r.Method == http.MethodPatch:
		f.patchRelease(w, r, rest[0])
	case len(rest) == 2 && rest[0] == "assets" && r.Method == http.MethodDelete:
		for relID, list := range f.assets {
			for i, a := range list {
				if strconv.FormatInt(a.ID, 10) == rest[1] {
					f.assets[relID] = append(list[:i], list[i+1:]...)
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
		}
		f.reply(w, http.StatusNotFound, `{"message":"Not Found"}`)
	case len(rest) == 2 && rest[1] == "assets" && r.Method == http.MethodGet:
		id, _ := strconv.ParseInt(rest[0], 10, 64)
		f.page(w, r, append([]githubAsset{}, f.assets[id]...))
	case len(rest) == 2 && rest[1] == "assets" && r.Method == http.MethodPost:
		id, _ := strconv.ParseInt(rest[0], 10, 64)
		f.uploads = append(f.uploads, fmt.Sprintf("%s length=%d encoding=%v type=%s", r.URL.Query().Get("name"),
			r.ContentLength, r.TransferEncoding, r.Header.Get("Content-Type")))
		data, err := io.ReadAll(r.Body)
		if err != nil {
			// The client gave up mid-body; GitHub keeps no asset.
			f.reply(w, http.StatusBadRequest, `{"message":"Bad Request"}`)
			return
		}
		f.nextID++
		a := githubAsset{ID: f.nextID, Name: r.URL.Query().Get("name"), Size: int64(len(data)), State: "uploaded"}
		f.assets[id] = append(f.assets[id], a)
		f.json(w, http.StatusCreated, a)
	default:
		f.errorf("unexpected request %s %s", r.Method, r.URL.Path)
		f.reply(w, http.StatusNotFound, `{"message":"Not Found"}`)
	}
}

// createRelease answers POST /releases: a draft, or 422 over a published tag.
func (f *fakeGitHub) createRelease(w http.ResponseWriter, r *http.Request) {
	var in githubRelease
	_ = json.NewDecoder(r.Body).Decode(&in)
	for _, rel := range f.releases {
		if rel.TagName == in.TagName && !rel.Draft {
			f.reply(w, http.StatusUnprocessableEntity, `{"message":"Validation Failed",
				"errors":[{"resource":"Release","code":"already_exists","field":"tag_name"}]}`)
			return
		}
	}
	f.nextID++
	if in.TargetCommitish == "" {
		in.TargetCommitish = "main"
	}
	rel := &githubRelease{ID: f.nextID, TagName: in.TagName, Name: in.Name, Body: in.Body, Draft: in.Draft,
		TargetCommitish: in.TargetCommitish, HTMLURL: "https://github.com/" + fakeGitHubRepo + "/releases/tag/untagged-" + strconv.FormatInt(f.nextID, 10)}
	f.releases = append([]*githubRelease{rel}, f.releases...)
	f.json(w, http.StatusCreated, rel)
}

// patchRelease answers PATCH /releases/{id}: retarget, publish, and Latest as
// make_latest says (true takes it; a draft published with none or legacy too).
func (f *fakeGitHub) patchRelease(w http.ResponseWriter, r *http.Request, id string) {
	rel := f.release(id)
	if rel == nil {
		f.reply(w, http.StatusNotFound, `{"message":"Not Found"}`)
		return
	}
	var in struct {
		Draft           *bool
		MakeLatest      string `json:"make_latest"`
		TargetCommitish string `json:"target_commitish"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if in.TargetCommitish != "" {
		rel.TargetCommitish = in.TargetCommitish
	}
	if in.Draft != nil {
		wasDraft := rel.Draft
		rel.Draft = *in.Draft
		f.makeLatest = append(f.makeLatest, in.MakeLatest)
		rel.HTMLURL = "https://github.com/" + fakeGitHubRepo + "/releases/tag/" + rel.TagName
		if in.MakeLatest == "true" || wasDraft && !rel.Draft && (in.MakeLatest == "" || in.MakeLatest == "legacy") {
			f.latest = rel.ID
		}
	}
	f.json(w, http.StatusOK, rel)
}

// setAssets replaces a release's assets under the lock: a handler the client
// gave up on, such as a short upload's, can still be running.
func (f *fakeGitHub) setAssets(id int64, assets []githubAsset) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.assets[id] = assets
}

func (f *fakeGitHub) release(id string) *githubRelease {
	for _, rel := range f.releases {
		if strconv.FormatInt(rel.ID, 10) == id {
			return rel
		}
	}
	return nil
}

// page answers one page of items, as per_page and page ask.
func page[T any](r *http.Request, items []T) []T {
	size, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	n, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if size <= 0 {
		size = 30
	}
	if n <= 0 {
		n = 1
	}
	start := min((n-1)*size, len(items))
	return items[start:min(start+size, len(items))]
}

func (f *fakeGitHub) page(w http.ResponseWriter, r *http.Request, items any) {
	switch v := items.(type) {
	case []githubRelease:
		f.json(w, http.StatusOK, page(r, v))
	case []githubAsset:
		f.json(w, http.StatusOK, page(r, v))
	}
}

func (f *fakeGitHub) json(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeGitHub) reply(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

const testMarker = "<!-- c4factory: shop/cel-lens v0.2.0 -->"

// TestGitHubReleasesPublish walks a release through every call: find nothing,
// draft, upload, list, replace an asset, find the draft by its marker, publish.
// Every call it makes must stay under /repos/{owner}/{repo}/releases.
func TestGitHubReleasesPublish(t *testing.T) {
	f, g := newFakeGitHub(t)
	ctx := context.Background()

	found, err := g.FindRelease(ctx, "v0.2.0", testMarker)
	if err != nil || found != nil {
		t.Fatalf("FindRelease() on no releases = %+v, %v; want nil", found, err)
	}
	draft, err := g.CreateDraft(ctx, "v0.2.0", "CEL Lens v0.2.0", "- Add a lens (#3)\n\n"+testMarker, "")
	if err != nil || !draft.Draft || draft.TagName != "v0.2.0" || draft.Name != "CEL Lens v0.2.0" || draft.ID == 0 {
		t.Fatalf("CreateDraft() = %+v, %v", draft, err)
	}

	sums := "abc  cel-lens.vsix\n"
	a, err := g.UploadAsset(ctx, draft.ID, "SHA256SUMS", int64(len(sums)), strings.NewReader(sums))
	if err != nil || a.Name != "SHA256SUMS" || a.Size != int64(len(sums)) {
		t.Fatalf("UploadAsset() = %+v, %v", a, err)
	}
	vsix, err := g.UploadAsset(ctx, draft.ID, "cel-lens.vsix", 3, strings.NewReader("old"))
	if err != nil {
		t.Fatalf("UploadAsset(cel-lens.vsix) error = %v", err)
	}
	if err := g.DeleteAsset(ctx, vsix.ID); err != nil {
		t.Fatalf("DeleteAsset() error = %v", err)
	}
	if err := g.DeleteAsset(ctx, vsix.ID); err != nil {
		t.Errorf("DeleteAsset() again error = %v; want nil, it is safe to repeat", err)
	}
	// A reader that isn't a strings.Reader, so the client can't learn its size
	// on its own: the Content-Length must come from size.
	if _, err := g.UploadAsset(ctx, draft.ID, "cel-lens.vsix", 5, io.MultiReader(strings.NewReader("new v"))); err != nil {
		t.Fatalf("UploadAsset(cel-lens.vsix) again error = %v", err)
	}
	assets, err := g.ListAssets(ctx, draft.ID)
	got := fmt.Sprint(assets)
	want := fmt.Sprint([]TargetAsset{{ID: a.ID, Name: "SHA256SUMS", Size: int64(len(sums)), Uploaded: true},
		{ID: f.nextID, Name: "cel-lens.vsix", Size: 5, Uploaded: true}})
	if err != nil || got != want {
		t.Errorf("ListAssets() = %s, %v; want %s", got, err, want)
	}
	wantUploads := []string{
		"SHA256SUMS length=19 encoding=[] type=application/octet-stream",
		"cel-lens.vsix length=3 encoding=[] type=application/octet-stream",
		"cel-lens.vsix length=5 encoding=[] type=application/octet-stream",
	}
	if !reflect.DeepEqual(f.uploads, wantUploads) {
		t.Errorf("uploads = %q, want %q", f.uploads, wantUploads)
	}

	found, err = g.FindRelease(ctx, "v0.2.0", testMarker)
	if err != nil || found == nil || found.ID != draft.ID || !found.Draft {
		t.Fatalf("FindRelease() = %+v, %v; want the draft %d", found, err, draft.ID)
	}
	listReleases(t, g, draft.ID)
	wantLatest(t, g, 0, "before any publish")
	published, err := g.PublishRelease(ctx, draft.ID, false)
	if err != nil || published.Draft || published.URL != "https://github.com/acme/cel-lens/releases/tag/v0.2.0" {
		t.Errorf("PublishRelease() = %+v, %v", published, err)
	}
	if !reflect.DeepEqual(f.makeLatest, []string{"false"}) {
		t.Errorf("make_latest sent = %q, want [false]: a string, as GitHub takes it", f.makeLatest)
	}
	wantLatest(t, g, 0, "after a publish that leaves Latest")
	latestWalk(t, g, draft.ID)
	if _, err := g.CreateDraft(ctx, "v0.2.0", "v0.2.0", "again", ""); ReleaseReason(err) != ReasonTagTaken {
		t.Errorf("CreateDraft() of a published tag error = %v, reason %s; want %s", err, ReleaseReason(err), ReasonTagTaken)
	}

	// Every release method of ReleaseTarget must be walked above, so a method
	// added to it is covered by the fake's check that every path is a release
	// path; TestGitHubSource walks those of SourceTarget.
	calls := map[string]bool{}
	for _, p := range f.paths {
		calls[p] = true
	}
	prefix := "/repos/" + fakeGitHubRepo + "/releases"
	wantCalls := map[string]bool{
		"GET " + prefix:                     true, // FindRelease
		"POST " + prefix:                    true, // CreateDraft
		"GET " + prefix + "/{id}/assets":    true, // ListAssets
		"POST " + prefix + "/{id}/assets":   true, // UploadAsset
		"DELETE " + prefix + "/assets/{id}": true, // DeleteAsset
		"PATCH " + prefix + "/{id}":         true, // PublishRelease
		"GET " + prefix + "/latest":         true, // LatestRelease
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Errorf("requests = %v, want %v", calls, wantCalls)
	}
	// RetargetDraft, walked in TestGitHubReleasesTarget, shares
	// PublishRelease's request, and ListReleases FindRelease's.
	release := reflect.TypeFor[ReleaseTarget]().NumMethod() - reflect.TypeFor[SourceTarget]().NumMethod()
	if n := release - 2; n != len(wantCalls) {
		t.Errorf("ReleaseTarget has %d methods; walk the new ones in this test and add their requests above", n)
	}
}

// latestWalk makes the published release id Latest, the same PATCH as
// publishing, and publishes a later one without Latest, which leaves it there.
func latestWalk(t *testing.T, g ReleaseTarget, id int64) {
	t.Helper()
	ctx := context.Background()
	if _, err := g.PublishRelease(ctx, id, true); err != nil {
		t.Fatalf("PublishRelease(latest) = %v", err)
	}
	wantLatest(t, g, id, "after making it Latest")
	later, err := g.CreateDraft(ctx, "v0.3.0", "v0.3.0", "later", "")
	if err != nil {
		t.Fatalf("CreateDraft(v0.3.0) = %v", err)
	}
	if _, err := g.PublishRelease(ctx, later.ID, false); err != nil {
		t.Fatalf("PublishRelease(v0.3.0) = %v", err)
	}
	wantLatest(t, g, id, "after publishing v0.3.0 without Latest")
}

// wantLatest checks LatestRelease is release id, or nil when id is 0.
func wantLatest(t *testing.T, g ReleaseTarget, id int64, when string) {
	t.Helper()
	latest, err := g.LatestRelease(context.Background())
	if err != nil || (id == 0) != (latest == nil) || latest != nil && latest.ID != id {
		t.Errorf("LatestRelease() %s = %+v, %v; want release %d (0: none)", when, latest, err, id)
	}
}

// listReleases checks ListReleases holds draft id among the releases.
func listReleases(t *testing.T, g ReleaseTarget, id int64) {
	t.Helper()
	all, err := g.ListReleases(context.Background())
	if err != nil || !slices.ContainsFunc(all, func(r TargetRelease) bool { return r.ID == id && r.Draft }) {
		t.Fatalf("ListReleases() = %+v, %v; want the draft %d among them", all, err, id)
	}
}

// TestGitHubReleasesTarget sets the commit a draft's tag is created at: none,
// the default branch's head; one at creation; and one by a later PATCH.
func TestGitHubReleasesTarget(t *testing.T) {
	_, g := newFakeGitHub(t)
	ctx := context.Background()
	draft, err := g.CreateDraft(ctx, "v0.2.0", "v0.2.0", testMarker, "")
	if err != nil {
		t.Fatal(err)
	}
	if draft.Commit != "main" {
		t.Errorf("CreateDraft() with no commit targets %q, want the default branch", draft.Commit)
	}
	const sourceCommit = "0123456789abcdef0123456789abcdef01234567"
	retargeted, err := g.RetargetDraft(ctx, draft.ID, sourceCommit)
	if err != nil || retargeted.Commit != sourceCommit || !retargeted.Draft {
		t.Errorf("RetargetDraft() = %+v, %v; want the draft at %s", retargeted, err, sourceCommit)
	}
	if _, err := g.RetargetDraft(ctx, draft.ID, ""); err == nil {
		t.Error("RetargetDraft() to no commit error = nil, want one")
	}
	at, err := g.CreateDraft(ctx, "v0.3.0", "v0.3.0", "at a commit", sourceCommit)
	if err != nil || at.Commit != sourceCommit {
		t.Errorf("CreateDraft() at a commit = %+v, %v; want target %s", at, err, sourceCommit)
	}
}

// TestGitHubReleasesStayInTheirCalls fails when the client calls GitHub
// outside /repos/{owner}/{repo}/releases, /repos/{owner}/{repo}/git/ and GET
// /repos/{owner}/{repo}: the fake every other test uses must catch it.
func TestGitHubReleasesStayInTheirCalls(t *testing.T) {
	_, g := newFakeGitHub(t)
	gh := g.(*githubReleases)
	repo := "/repos/" + fakeGitHubRepo
	if gh.releases != repo+"/releases" || gh.repo != repo {
		t.Fatalf("paths = %q, %q", gh.repo, gh.releases)
	}
	tests := []struct {
		method, path string
		allowed      bool
	}{
		{http.MethodGet, repo + "/releases", true},
		{http.MethodPatch, repo + "/releases/1", true},
		{http.MethodGet, repo + "/git/ref/heads/main", true},
		{http.MethodPost, repo + "/git/blobs", true},
		{http.MethodGet, repo, true},
		{http.MethodPatch, repo, false},
		{http.MethodDelete, repo, false},
		{http.MethodGet, repo + "/contents/idea.md", false},
		{http.MethodPut, repo + "/contents/idea.md", false},
		{http.MethodGet, repo + "/branches/main/protection", false},
		{http.MethodPost, repo + "/merges", false},
		{http.MethodGet, repo + "/gitx", false},
		{http.MethodGet, repo + "/releasesx", false},
		{http.MethodGet, repo + "/git/../contents/idea.md", false},
		{http.MethodGet, "/repos/acme/other/git/ref/heads/main", false},
		{http.MethodGet, "/user", false},
	}
	for _, tt := range tests {
		var failures []string
		f := &fakeGitHub{token: gh.token, assets: map[int64][]githubAsset{}, git: newFakeGit(),
			errorf: func(format string, args ...any) { failures = append(failures, fmt.Sprintf(format, args...)) }}
		f.fail = func(w http.ResponseWriter) { f.reply(w, http.StatusNotFound, `{"message":"Not Found"}`) }
		srv := httptest.NewServer(f.on(false))
		req, _ := http.NewRequest(tt.method, srv.URL+tt.path, nil)
		req.URL.Path = tt.path // as sent, ".." included
		req.Header.Set("Authorization", "Bearer "+gh.token)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
		srv.Close()
		if caught := len(failures) > 0; caught == tt.allowed {
			t.Errorf("%s %s: failures %q; want allowed %v", tt.method, tt.path, failures, tt.allowed)
		}
	}
}

func TestGitHubReleasesFindRelease(t *testing.T) {
	f, g := newFakeGitHub(t)
	ctx := context.Background()
	// Someone else's release of the tag, then more than a page of others before
	// the marked draft, which is on the second page.
	f.releases = append(f.releases, &githubRelease{ID: 1, TagName: "v0.2.0", Body: "by hand"})
	for i := range githubPageSize {
		f.releases = append(f.releases, &githubRelease{ID: int64(10 + i), TagName: fmt.Sprintf("v0.0.%d", i)})
	}
	got, err := g.FindRelease(ctx, "v0.2.0", testMarker)
	if err != nil || got == nil || got.ID != 1 {
		t.Errorf("FindRelease() without a marked one = %+v, %v; want release 1", got, err)
	}
	f.releases = append(f.releases, &githubRelease{ID: 9, TagName: "v0.2.0", Draft: true, Body: "notes\n" + testMarker})
	got, err = g.FindRelease(ctx, "v0.2.0", testMarker)
	if err != nil || got == nil || got.ID != 9 || !got.Draft {
		t.Errorf("FindRelease() = %+v, %v; want the marked draft 9", got, err)
	}
	// A published marked release after it comes first: a stray second draft
	// doesn't hold back a release already out.
	f.releases = append(f.releases, &githubRelease{ID: 8, TagName: "v0.2.0", Body: "notes\n" + testMarker})
	got, err = g.FindRelease(ctx, "v0.2.0", testMarker)
	if err != nil || got == nil || got.ID != 8 || got.Draft {
		t.Errorf("FindRelease() = %+v, %v; want the published release 8", got, err)
	}
	got, err = g.FindRelease(ctx, "v9.9.9", "<!-- c4factory: shop/cel-lens v9.9.9 -->")
	if err != nil || got != nil {
		t.Errorf("FindRelease(v9.9.9) = %+v, %v; want nil", got, err)
	}
}

// TestGitHubReleasesFailures maps GitHub's refusals to the plan's reasons.
func TestGitHubReleasesFailures(t *testing.T) {
	type response struct {
		status  int
		headers map[string]string
		body    string
	}
	tests := []struct {
		name string
		resp response
		want string
	}{
		{"bad token", response{401, nil, `{"message":"Bad credentials"}`}, ReasonUnauthorized},
		{"no permission", response{403, nil, `{"message":"Resource not accessible by personal access token"}`}, ReasonUnauthorized},
		{"rate limited", response{403, map[string]string{"X-RateLimit-Remaining": "0"}, `{"message":"API rate limit exceeded"}`}, ReasonGitHubError},
		{"secondary rate limit", response{403, map[string]string{"Retry-After": "60"}, `{"message":"You have exceeded a secondary rate limit"}`}, ReasonGitHubError},
		{"too many requests", response{429, nil, `{"message":"slow down"}`}, ReasonGitHubError},
		{"no repository", response{404, nil, `{"message":"Not Found"}`}, ReasonRepositoryNotFound},
		{"empty repository", response{422, nil, `{"message":"Repository is empty."}`}, ReasonEmptyRepository},
		{"empty repository conflict", response{409, nil, `{"message":"Git Repository is empty."}`}, ReasonEmptyRepository},
		{"empty repository in errors", response{422, nil, `{"message":"Validation Failed","errors":[{"resource":"Release","code":"custom","message":"Repository is empty."}]}`}, ReasonEmptyRepository},
		{"tag taken", response{422, nil, `{"message":"Validation Failed","errors":[{"resource":"Release","code":"already_exists","field":"tag_name"}]}`}, ReasonTagTaken},
		{"other validation", response{422, nil, `{"message":"Validation Failed","errors":[{"code":"invalid","field":"name"}]}`}, ReasonGitHubError},
		{"server error", response{502, nil, "<html>" + strings.Repeat("Bad Gateway ", 1000) + "</html>"}, ReasonGitHubError},
		{"long message", response{500, nil, `{"message":"` + strings.Repeat("é", 2000) + `"}`}, ReasonGitHubError},
		{"unreadable success", response{200, nil, `not json`}, ReasonGitHubError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, g := newFakeGitHub(t)
			f.fail = func(w http.ResponseWriter) {
				for k, v := range tt.resp.headers {
					w.Header().Set(k, v)
				}
				f.reply(w, tt.resp.status, tt.resp.body)
			}
			ctx := context.Background()
			calls := map[string]func() error{
				"FindRelease": func() error { _, err := g.FindRelease(ctx, "v0.2.0", testMarker); return err },
				"CreateDraft": func() error { _, err := g.CreateDraft(ctx, "v0.2.0", "v0.2.0", testMarker, ""); return err },
				"ListAssets":  func() error { _, err := g.ListAssets(ctx, 1); return err },
				"UploadAsset": func() error {
					_, err := g.UploadAsset(ctx, 1, "SHA256SUMS", 2, strings.NewReader("ab"))
					return err
				},
				"PublishRelease": func() error { _, err := g.PublishRelease(ctx, 1, true); return err },
				"DeleteAsset":    func() error { return g.DeleteAsset(ctx, 1) },
			}
			for name, call := range calls {
				err := call()
				want := tt.want
				switch {
				case name == "DeleteAsset" && tt.resp.status == http.StatusNotFound:
					want = "" // gone already
				case name == "DeleteAsset" && tt.resp.status == http.StatusOK:
					want = ReasonGitHubError // it wants 204
				}
				if want == "" {
					if err != nil {
						t.Errorf("%s() error = %v; want nil", name, err)
					}
					continue
				}
				var re *ReleaseError
				if !errors.As(err, &re) || re.Reason != want {
					t.Errorf("%s() error = %v; want reason %s", name, err, want)
				}
				if err != nil && strings.Contains(err.Error(), f.token) {
					t.Errorf("%s() error %q holds the token", name, err)
				}
				if err != nil && utf8.RuneCountInString(err.Error()) > 400 {
					t.Errorf("%s() error is %d characters long", name, utf8.RuneCountInString(err.Error()))
				}
			}
		})
	}
}

func TestGitHubReleasesNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	g, err := Client{}.GitHubReleases(GitHubConfig{Repository: fakeGitHubRepo, Token: rand.Text(),
		APIURL: srv.URL, UploadURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.FindRelease(context.Background(), "v0.2.0", testMarker); ReleaseReason(err) != ReasonGitHubError {
		t.Errorf("FindRelease() on a closed server error = %v; want reason %s", err, ReasonGitHubError)
	}
	if ReleaseReason(errors.New("other")) != ReasonGitHubError {
		t.Error("ReleaseReason() of a plain error is not GitHubError")
	}
}

func TestGitHubReleasesConfig(t *testing.T) {
	for _, cfg := range []GitHubConfig{
		{Repository: "cel-lens", Token: "t"},
		{Repository: "acme/cel-lens/extra", Token: "t"},
		{Repository: "acme/cel lens", Token: "t"},
		{Repository: "acme/cel-lens"},
		{Repository: "acme/cel-lens", Token: "t", APIURL: "not a url"},
	} {
		if _, err := (Client{}).GitHubReleases(cfg); err == nil {
			t.Errorf("GitHubReleases(%+v) error = nil, want one", cfg)
		}
	}
	g, err := Client{}.GitHubReleases(GitHubConfig{Repository: "Acme-1/cel.lens_2", Token: "t"})
	if err != nil {
		t.Fatalf("GitHubReleases() error = %v", err)
	}
	gh := g.(*githubReleases)
	if gh.api.String() != GitHubAPIURL || gh.upload.String() != GitHubUploadURL || gh.releases != "/repos/Acme-1/cel.lens_2/releases" {
		t.Errorf("GitHubReleases() = %s %s %s", gh.api, gh.upload, gh.releases)
	}
}

// TestGitHubReleasesAssets covers what a draft's assets can hold: an upload
// GitHub never finished, an empty file, a size that is unknown or wrong, and
// more than a page of them.
func TestGitHubReleasesAssets(t *testing.T) {
	f, g := newFakeGitHub(t)
	ctx := context.Background()
	draft, err := g.CreateDraft(ctx, "v0.2.0", "v0.2.0", testMarker, "")
	if err != nil {
		t.Fatal(err)
	}
	held := make([]githubAsset, 0, 1+githubPageSize)
	held = append(held, githubAsset{ID: 1, Name: "cli-linux", Size: 5, State: "starter"})
	for i := range githubPageSize {
		held = append(held, githubAsset{ID: int64(10 + i), Name: fmt.Sprint(i), State: "uploaded"})
	}
	f.setAssets(draft.ID, held)
	assets, err := g.ListAssets(ctx, draft.ID)
	if err != nil || len(assets) != githubPageSize+1 || assets[0].Uploaded || !assets[1].Uploaded {
		t.Errorf("ListAssets() = %d assets, first %+v, %v; want %d, the first not uploaded", len(assets), assets, err, githubPageSize+1)
	}

	if a, err := g.UploadAsset(ctx, draft.ID, "EMPTY", 0, io.NopCloser(strings.NewReader(""))); err != nil || a.Size != 0 {
		t.Errorf("UploadAsset() of an empty file = %+v, %v", a, err)
	}
	if _, err := g.UploadAsset(ctx, draft.ID, "unknown", -1, strings.NewReader("abc")); ReleaseReason(err) != ReasonGitHubError {
		t.Errorf("UploadAsset() of an unknown size error = %v; want reason %s", err, ReasonGitHubError)
	}
	if _, err := g.UploadAsset(ctx, draft.ID, "short", 10, io.MultiReader(strings.NewReader("abc"))); ReleaseReason(err) != ReasonGitHubError || err == nil {
		t.Errorf("UploadAsset() of fewer bytes than its size error = %v; want reason %s", err, ReasonGitHubError)
	}

	held = make([]githubAsset, 0, githubMaxPages*githubPageSize)
	for i := range githubMaxPages * githubPageSize {
		held = append(held, githubAsset{ID: int64(10 + i), Name: fmt.Sprint(i)})
	}
	f.setAssets(draft.ID, held)
	if _, err := g.ListAssets(ctx, draft.ID); ReleaseReason(err) != ReasonGitHubError || err == nil {
		t.Errorf("ListAssets() past the limit error = %v; want one", err)
	}
}
