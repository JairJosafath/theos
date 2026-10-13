package controller

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"

	"github.com/JairJosafath/c4factory/internal/gitprovider"
)

// fakeGitHub is GitHub's release and issues API over httptest, for the repositories made
// with addRepository; any other repository is a 404, as GitHub answers a token
// that can't see it.
type fakeGitHub struct {
	server *httptest.Server
	mu     sync.Mutex
	// repos holds each repository's releases by owner/name, git its git
	// objects and refs, and tokens the token each accepts.
	repos  map[string][]*fakeGitHubRelease
	git    map[string]*fakeGitStore
	tokens map[string]string
	nextID int64
	is     *fakeGitHubIssues
}

type fakeGitHubRelease struct {
	ID      int64  `json:"id"`
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	Body    string `json:"body"`
	Draft   bool   `json:"draft"`
	HTMLURL string `json:"html_url"`
	// Target is the commit or branch the tag is created at when published.
	Target string `json:"target_commitish"`
	assets []fakeGitHubAsset
	// makeLatest is the make_latest it was published with.
	makeLatest string
}

type fakeGitHubAsset struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	State string `json:"state"`
	data  []byte
}

func newFakeGitHub() *fakeGitHub {
	f := &fakeGitHub{repos: map[string][]*fakeGitHubRelease{}, git: map[string]*fakeGitStore{},
		tokens: map[string]string{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

// GitHubReleases is the real client, pointed at the fake.
func (f *fakeGitHub) GitHubReleases(cfg gitprovider.GitHubConfig) (gitprovider.ReleaseTarget, error) {
	cfg.APIURL, cfg.UploadURL = f.server.URL, f.server.URL
	return gitprovider.Client{}.GitHubReleases(cfg)
}

// addRepository makes repository owner/name, which accepts token; its default
// branch main holds a README.
func (f *fakeGitHub) addRepository(repository, token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repos[repository] = []*fakeGitHubRelease{}
	f.git[repository] = newFakeGitStore(repository)
	f.tokens[repository] = token
}

// setPrivate makes GitHub report repository private.
func (f *fakeGitHub) setPrivate(repository string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.git[repository].private = true
}

// releases returns a copy of repository's releases, with their files.
func (f *fakeGitHub) releases(repository string) []fakeGitHubRelease {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeGitHubRelease, 0, len(f.repos[repository]))
	for _, r := range f.repos[repository] {
		c := *r
		c.assets = append([]fakeGitHubAsset(nil), r.assets...)
		out = append(out, c)
	}
	return out
}

func (f *fakeGitHub) serve(w http.ResponseWriter, req *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.URL.Path == "/user" && req.Method == http.MethodGet {
		// Every repository's token is the factory's account.
		reply(w, http.StatusOK, fakeGitHubUser{"c4factory-bot"})
		return
	}
	if team, ok := strings.CutPrefix(req.URL.Path, "/orgs/"); ok {
		f.serveTeam(w, req, strings.Split(team, "/"))
		return
	}
	// /repos/{owner}/{name}, or its releases[/...], issues[/...] or git/...
	parts := strings.Split(strings.TrimPrefix(req.URL.Path, "/repos/"), "/")
	whole := len(parts) == 2 && req.Method == http.MethodGet
	if !whole && (len(parts) < 3 || (parts[2] != "releases" && parts[2] != "issues" &&
		(parts[2] != "git" || len(parts) < 4))) {
		reply(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	repository := parts[0] + "/" + parts[1]
	releases, ok := f.repos[repository]
	if !ok {
		reply(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	if req.Header.Get("Authorization") != "Bearer "+f.tokens[repository] {
		reply(w, http.StatusUnauthorized, map[string]string{"message": "Bad credentials"})
		return
	}
	if whole {
		reply(w, http.StatusOK, map[string]any{"full_name": repository, "default_branch": f.git[repository].branch,
			"private": f.git[repository].private})
		return
	}
	rest := parts[3:]
	if parts[2] == "git" {
		f.git[repository].serveGit(w, req, rest)
		return
	}
	if parts[2] == "issues" {
		f.serveIssues(w, req, repository, rest)
		return
	}
	switch {
	case len(rest) == 0 && req.Method == http.MethodGet:
		if req.URL.Query().Get("page") != "1" {
			reply(w, http.StatusOK, []any{})
			return
		}
		reply(w, http.StatusOK, releases)
	case len(rest) == 0 && req.Method == http.MethodPost:
		var r fakeGitHubRelease
		_ = json.NewDecoder(req.Body).Decode(&r)
		f.nextID++
		r.ID = f.nextID
		r.HTMLURL = fmt.Sprintf("https://github.com/%s/releases/tag/%s", repository, r.TagName)
		f.repos[repository] = append(releases, &r)
		reply(w, http.StatusCreated, r)
	case len(rest) == 2 && rest[0] == "assets" && req.Method == http.MethodDelete:
		id, _ := strconv.ParseInt(rest[1], 10, 64)
		for _, r := range releases {
			for i, a := range r.assets {
				if a.ID == id {
					r.assets = append(r.assets[:i], r.assets[i+1:]...)
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
		}
		reply(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	default:
		id, _ := strconv.ParseInt(rest[0], 10, 64)
		var rel *fakeGitHubRelease
		for _, r := range releases {
			if r.ID == id {
				rel = r
			}
		}
		if rel == nil {
			reply(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
			return
		}
		f.serveRelease(w, req, f.git[repository], rel, rest[1:])
	}
}

func (f *fakeGitHub) serveRelease(w http.ResponseWriter, req *http.Request, git *fakeGitStore,
	rel *fakeGitHubRelease, rest []string) {
	switch {
	case len(rest) == 0 && req.Method == http.MethodPatch:
		var patch struct {
			Draft      *bool
			MakeLatest *string `json:"make_latest"`
			Target     *string `json:"target_commitish"`
		}
		_ = json.NewDecoder(req.Body).Decode(&patch)
		if patch.Target != nil {
			rel.Target = *patch.Target
		}
		if patch.MakeLatest != nil {
			rel.makeLatest = *patch.MakeLatest
		}
		if patch.Draft != nil {
			if rel.Draft && !*patch.Draft {
				// Publishing a draft creates its tag.
				git.tagRelease(rel.TagName, rel.Target)
			}
			rel.Draft = *patch.Draft
		}
		reply(w, http.StatusOK, rel)
	case len(rest) == 1 && rest[0] == "assets" && req.Method == http.MethodGet:
		if req.URL.Query().Get("page") != "1" {
			reply(w, http.StatusOK, []any{})
			return
		}
		reply(w, http.StatusOK, rel.assets)
	case len(rest) == 1 && rest[0] == "assets" && req.Method == http.MethodPost:
		data, err := io.ReadAll(req.Body)
		if err != nil || int64(len(data)) != req.ContentLength {
			reply(w, http.StatusBadRequest, map[string]string{"message": "short body"})
			return
		}
		f.nextID++
		a := fakeGitHubAsset{ID: f.nextID, Name: req.URL.Query().Get("name"), Size: int64(len(data)),
			State: "uploaded", data: data}
		rel.assets = append(rel.assets, a)
		reply(w, http.StatusCreated, a)
	default:
		reply(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	}
}

func reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
