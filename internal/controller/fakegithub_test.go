package controller

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JairJosafath/c4factory/internal/gitprovider"
)

// fakeGitHub is GitHub's release and issues API over httptest, for the repositories made
// with addRepository; any other repository is a 404, as GitHub answers a token
// that can't see it.
type fakeGitHub struct {
	server *httptest.Server
	mu     sync.Mutex
	// repos holds each repository's releases by owner/name, git its git
	// objects and refs, tokens the token each accepts, ids GitHub's id of
	// each, renamed the name GitHub follows each to, and written the
	// requests that weren't reads each took, and latest the id of each
	// one's Latest release as make_latest set it, 0 when it has none.
	written map[string]int
	latest  map[string]int64
	repos   map[string][]*fakeGitHubRelease
	git     map[string]*fakeGitStore
	tokens  map[string]string
	ids     map[string]int64
	renamed map[string]string
	nextID  int64
	is      *fakeGitHubIssues
	// apps is each GitHub App installation's App, by installation id.
	apps map[string]fakeGitHubApp
}

// fakeGitHubApp is a GitHub App installed on one repository: a token it
// mints becomes the token the repository accepts.
type fakeGitHubApp struct {
	appID      string
	repository string
	public     *rsa.PublicKey
	// mints is how many tokens it minted.
	mints int
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
		tokens: map[string]string{}, ids: map[string]int64{}, renamed: map[string]string{},
		written: map[string]int{}, latest: map[string]int64{}, apps: map[string]fakeGitHubApp{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	return f
}

// GitHubReleases is the real client, pointed at the fake.
func (f *fakeGitHub) GitHubReleases(cfg gitprovider.GitHubConfig) (gitprovider.ReleaseTarget, error) {
	cfg.APIURL, cfg.UploadURL = f.server.URL, f.server.URL
	return gitprovider.Client{}.GitHubReleases(cfg)
}

// GitHubToken is the real client's, minting a GitHub App's token at the fake.
func (f *fakeGitHub) GitHubToken(ctx context.Context, _ string, secret map[string][]byte) (string, error) {
	return gitprovider.Client{}.GitHubToken(ctx, f.server.URL, secret)
}

// addApp installs GitHub App appID, whose key public is, on repository as
// installation; installing it again replaces the App.
func (f *fakeGitHub) addApp(appID, installation, repository string, public *rsa.PublicKey) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.apps[installation] = fakeGitHubApp{appID: appID, repository: repository, public: public}
}

// appMints is how many tokens installation minted.
func (f *fakeGitHub) appMints(installation string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.apps[installation].mints
}

// serveApp mints an installation token for a JWT its App signed, as POST
// /app/installations/{id}/access_tokens does; the token expires in an hour.
func (f *fakeGitHub) serveApp(w http.ResponseWriter, req *http.Request, installation string) {
	app, ok := f.apps[installation]
	if !ok || req.Method != http.MethodPost {
		reply(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	if !app.signed(strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")) {
		reply(w, http.StatusUnauthorized, map[string]string{"message": "A JSON web token could not be decoded"})
		return
	}
	app.mints++
	f.apps[installation] = app
	token := "ghs_" + rand.Text()
	f.tokens[app.repository] = token
	reply(w, http.StatusCreated, map[string]string{"token": token,
		"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
}

// newGitHubAppKey generates a GitHub App's private key, and its PEM as the
// App's Secret holds it; no test holds a key of its own.
func newGitHubAppKey() (*rsa.PrivateKey, []byte) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return key, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

// signed says whether jwt is an RS256 JWT of the App, signed by its key and
// not expired.
func (a fakeGitHubApp) signed(jwt string) bool {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err != nil || rsa.VerifyPKCS1v15(a.public, crypto.SHA256, digest[:], sig) != nil {
		return false
	}
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		ExpiresAt int64  `json:"exp"`
		Issuer    string `json:"iss"`
	}
	return json.Unmarshal(payload, &claims) == nil && claims.Issuer == a.appID &&
		claims.ExpiresAt > time.Now().Unix()
}

// addRepository makes repository owner/name, which accepts token; its default
// branch main holds a README.
func (f *fakeGitHub) addRepository(repository, token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repos[repository] = []*fakeGitHubRelease{}
	f.git[repository] = newFakeGitStore(repository)
	f.tokens[repository] = token
	f.nextID++
	f.ids[repository] = f.nextID
}

// hasRepository says whether repository was added.
func (f *fakeGitHub) hasRepository(repository string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.repos[repository]
	return ok
}

// repositoryID is GitHub's id of repository.
func (f *fakeGitHub) repositoryID(repository string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ids[repository]
}

// latestRelease is the id of repository's Latest release, 0 when it has none.
func (f *fakeGitHub) latestRelease(repository string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.latest[repository]
}

// writes is the number of requests other than reads repository took.
func (f *fakeGitHub) writes(repository string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.written[repository]
}

// rename makes GitHub answer for repository under the name to, as it follows
// a renamed repository's old name.
func (f *fakeGitHub) rename(repository, to string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renamed[repository] = to
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

// count counts a request to repository that isn't a read, under f's lock.
func (f *fakeGitHub) count(req *http.Request, repository string) {
	if req.Method != http.MethodGet {
		f.written[repository]++
	}
}

// name is the name GitHub answers for repository, under f's lock.
func (f *fakeGitHub) name(repository string) string {
	if to, ok := f.renamed[repository]; ok {
		return to
	}
	return repository
}

func (f *fakeGitHub) serve(w http.ResponseWriter, req *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.URL.Path == "/user" && req.Method == http.MethodGet {
		// Every repository's token is the factory's account.
		reply(w, http.StatusOK, fakeGitHubUser{"c4factory-bot"})
		return
	}
	if token, ok := strings.CutPrefix(req.URL.Path, "/app/installations/"); ok {
		f.serveApp(w, req, strings.TrimSuffix(token, "/access_tokens"))
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
	_, ok := f.repos[repository]
	if !ok {
		reply(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	if req.Header.Get("Authorization") != "Bearer "+f.tokens[repository] {
		reply(w, http.StatusUnauthorized, map[string]string{"message": "Bad credentials"})
		return
	}
	f.count(req, repository)
	if whole {
		reply(w, http.StatusOK, map[string]any{"id": f.ids[repository], "full_name": f.name(repository),
			"default_branch": f.git[repository].branch, "private": f.git[repository].private})
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
	f.serveReleases(w, req, repository, rest)
}

// serveReleases answers a request under repository's /releases, rest its
// path below it.
func (f *fakeGitHub) serveReleases(w http.ResponseWriter, req *http.Request, repository string, rest []string) {
	releases := f.repos[repository]
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
	case len(rest) == 1 && rest[0] == "latest" && req.Method == http.MethodGet:
		for _, r := range releases {
			if r.ID == f.latest[repository] && !r.Draft {
				reply(w, http.StatusOK, r)
				return
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
		f.serveRelease(w, req, repository, rel, rest[1:])
	}
}

func (f *fakeGitHub) serveRelease(w http.ResponseWriter, req *http.Request, repository string,
	rel *fakeGitHubRelease, rest []string) {
	git := f.git[repository]
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
		published := false
		if patch.Draft != nil {
			if rel.Draft && !*patch.Draft {
				// Publishing a draft creates its tag.
				git.tagRelease(rel.TagName, rel.Target)
				published = true
			}
			rel.Draft = *patch.Draft
		}
		// Latest moves as make_latest says: "true" takes it, "false" leaves
		// it, and "legacy" or none on a publish takes it, as GitHub picks
		// the newest.
		if patch.MakeLatest != nil && *patch.MakeLatest == "true" ||
			published && (patch.MakeLatest == nil || *patch.MakeLatest == "legacy") {
			f.latest[repository] = rel.ID
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
