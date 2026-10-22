// Package gitlabtest is a fake GitLab over httptest, serving the REST calls a
// gitlab deploy target makes (projects, the token's scopes, releases, tags and
// the generic package registry) for the projects added to it. The adapter's
// tests and the controllers' envtest share it.
package gitlabtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// DefaultScopes are a new project's token's scopes: what the design asks of
// a gitlab target's token.
var DefaultScopes = []string{"api", "read_registry", "write_registry"}

// Server is a fake GitLab instance. Its API is under URL() + "/api/v4".
type Server struct {
	srv *httptest.Server
	mu  sync.Mutex
	// projects holds each project by its full path; nextID numbers projects,
	// releases' links, packages and files.
	projects map[string]*Project
	nextID   int64
	// clock is the time the next release is made at, a second after the last.
	clock time.Time
}

// New starts a fake GitLab, closed when t ends.
func New(t testing.TB) *Server {
	s := &Server{projects: map[string]*Project{}, clock: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

// URL is the instance's address, without /api/v4: a GitLabConfig's URL.
func (s *Server) URL() string { return s.srv.URL }

// Project is a project on the fake. Its exported fields are knobs a test sets
// before it calls the fake.
type Project struct {
	ID   int64
	Path string
	// Scopes are the scopes its token has; DefaultScopes to begin with.
	Scopes []string
	// RegistryEnabled is whether its container registry is on; true to begin
	// with.
	RegistryEnabled bool
	// PackagesEnabled is whether its package registry is on; true to begin
	// with. Off, the generic upload and the package listing are a 403.
	PackagesEnabled bool
	// DefaultBranch is its default branch, "main" to begin with, and
	// DefaultBranchHead the commit the branch is at.
	DefaultBranch     string
	DefaultBranchHead string

	s        *Server
	token    string
	releases []*Release
	tags     map[string]string
	packages []*pkg
}

// Release is a release on a project.
type Release struct {
	Tag         string
	Name        string
	Description string
	// Commit is the commit its tag names.
	Commit    string
	Links     []Link
	CreatedAt time.Time
}

// Link is a release's link.
type Link struct {
	ID              int64
	Name            string
	URL             string
	DirectAssetPath string
	LinkType        string
}

type pkg struct {
	id      int64
	name    string
	version string
	files   []*file
}

type file struct {
	id   int64
	name string
	size int64
}

// AddProject adds a project at path whose token is token: the only token it
// takes. Any other token is a 401, or a 404 when it is another project's.
func (s *Server) AddProject(path, token string) *Project {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	p := &Project{ID: s.nextID, Path: path, Scopes: slices.Clone(DefaultScopes), RegistryEnabled: true,
		PackagesEnabled: true, DefaultBranch: "main", DefaultBranchHead: fmt.Sprintf("%040x", 0xdefa017+s.nextID),
		s: s, token: token, tags: map[string]string{}}
	s.projects[path] = p
	return p
}

// Releases returns the project's releases, newest first.
func (p *Project) Releases() []Release {
	p.s.mu.Lock()
	defer p.s.mu.Unlock()
	out := make([]Release, 0, len(p.releases))
	for _, v := range slices.Backward(p.releases) {
		r := *v
		r.Links = slices.Clone(r.Links)
		out = append(out, r)
	}
	return out
}

// Files returns each file of the generic package pkg at version, by name, with
// its size: the newest file of that name.
func (p *Project) Files(name, version string) map[string]int64 {
	p.s.mu.Lock()
	defer p.s.mu.Unlock()
	out := map[string]int64{}
	for _, k := range p.packages {
		if k.name == name && k.version == version {
			for _, f := range k.files {
				out[f.name] = f.size
			}
		}
	}
	return out
}

// SetTag makes tag name name commit, as a tag pushed before the factory came.
func (p *Project) SetTag(name, commit string) {
	p.s.mu.Lock()
	defer p.s.mu.Unlock()
	p.tags[name] = commit
}

// AddRelease plants a release of tag with description, as one someone else
// made; its tag is made at the default branch's head when it is not there.
func (p *Project) AddRelease(tag, description string) {
	p.s.mu.Lock()
	defer p.s.mu.Unlock()
	if _, ok := p.tags[tag]; !ok {
		p.tags[tag] = p.DefaultBranchHead
	}
	p.releases = append(p.releases, &Release{Tag: tag, Name: tag, Description: description,
		Commit: p.tags[tag], CreatedAt: p.s.tick()})
}

func (s *Server) tick() time.Time {
	s.clock = s.clock.Add(time.Second)
	return s.clock
}

// serve routes /api/v4 calls. The path is split on its escaped form, since a
// project's id is its path with its slashes escaped.
func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rest, ok := strings.CutPrefix(r.URL.EscapedPath(), "/api/v4/")
	if !ok {
		writeJSON(w, http.StatusNotFound, errMsg(notFound))
		return
	}
	var seg []string
	for part := range strings.SplitSeq(rest, "/") {
		u, err := url.PathUnescape(part)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errMsg("bad path"))
			return
		}
		seg = append(seg, u)
	}
	token := s.tokenOwner(r.Header.Get("PRIVATE-TOKEN"))
	if token == nil {
		writeJSON(w, http.StatusUnauthorized, msg("401 Unauthorized"))
		return
	}
	switch {
	case r.Method == http.MethodGet && len(seg) == 2 && seg[0] == "personal_access_tokens" && seg[1] == "self":
		writeJSON(w, http.StatusOK, map[string]any{"id": token.ID, keyName: "c4factory", "revoked": false,
			"active": true, "scopes": token.Scopes})
	case r.Method == http.MethodGet && len(seg) == 1 && seg[0] == "user":
		writeJSON(w, http.StatusOK, map[string]any{"id": 1000 + token.ID,
			"username": fmt.Sprintf("project_%d_bot", token.ID)})
	case len(seg) >= 2 && seg[0] == "projects":
		p := s.project(seg[1])
		if p == nil || p != token {
			writeJSON(w, http.StatusNotFound, msg("404 Project Not Found"))
			return
		}
		p.serve(w, r, seg[2:])
	default:
		writeJSON(w, http.StatusNotFound, errMsg(notFound))
	}
}

// tokenOwner is the project whose token token is, nil when none.
func (s *Server) tokenOwner(token string) *Project {
	if token == "" {
		return nil
	}
	for _, p := range s.projects {
		if p.token == token {
			return p
		}
	}
	return nil
}

// project finds a project by its path or its numeric id.
func (s *Server) project(id string) *Project {
	if p, ok := s.projects[id]; ok {
		return p
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return nil
	}
	for _, p := range s.projects {
		if p.ID == n {
			return p
		}
	}
	return nil
}

func (p *Project) serve(w http.ResponseWriter, r *http.Request, seg []string) {
	get, n := r.Method == http.MethodGet, len(seg)
	switch {
	case get && n == 0:
		p.info(w)
	case n >= 1 && seg[0] == segReleases:
		p.serveReleases(w, r, seg[1:])
	case get && n == 3 && seg[0] == "repository" && seg[1] == "tags":
		commit, ok := p.tags[seg[2]]
		if !ok {
			writeJSON(w, http.StatusNotFound, msg("404 Tag Not Found"))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{keyName: seg[2], "target": commit,
			"commit": map[string]string{"id": commit}})
	case n >= 1 && seg[0] == segPackages:
		p.servePackages(w, r, seg[1:])
	default:
		writeJSON(w, http.StatusNotFound, errMsg(notFound))
	}
}

// serveReleases serves /projects/:id/releases[/:tag].
func (p *Project) serveReleases(w http.ResponseWriter, r *http.Request, seg []string) {
	switch {
	case r.Method == http.MethodGet && len(seg) == 0:
		p.listReleases(w, r)
	case r.Method == http.MethodPost && len(seg) == 0:
		p.createRelease(w, r)
	case r.Method == http.MethodGet && len(seg) == 1:
		for _, rel := range p.releases {
			if rel.Tag == seg[0] {
				writeJSON(w, http.StatusOK, p.releaseJSON(rel))
				return
			}
		}
		writeJSON(w, http.StatusNotFound, msg(notFound))
	default:
		writeJSON(w, http.StatusNotFound, errMsg(notFound))
	}
}

// servePackages serves /projects/:id/packages/...: a 403 for all of it when
// the package registry is off.
func (p *Project) servePackages(w http.ResponseWriter, r *http.Request, seg []string) {
	n := len(seg)
	switch {
	case !p.PackagesEnabled:
		writeJSON(w, http.StatusForbidden, msg("403 Forbidden - Package registry is disabled"))
	case r.Method == http.MethodGet && n == 0:
		p.listPackages(w, r)
	case r.Method == http.MethodGet && n == 2 && seg[1] == packageFiles:
		k := p.pkgByID(seg[0])
		if k == nil {
			writeJSON(w, http.StatusNotFound, msg("404 Package Not Found"))
			return
		}
		out := make([]any, 0, len(k.files))
		for _, f := range k.files {
			out = append(out, map[string]any{"id": f.id, "package_id": k.id, "file_name": f.name, "size": f.size})
		}
		writePage(w, r, out)
	case r.Method == http.MethodPut && n == 4 && seg[0] == generic:
		p.upload(w, r, seg[1], seg[2], seg[3])
	case r.Method == http.MethodDelete && n == 3 && seg[1] == packageFiles:
		p.deleteFile(w, seg[0], seg[2])
	default:
		writeJSON(w, http.StatusNotFound, errMsg(notFound))
	}
}

func (p *Project) info(w http.ResponseWriter) {
	level := "enabled"
	if !p.RegistryEnabled {
		level = "disabled"
	}
	host := strings.TrimPrefix(p.s.srv.URL, "http://")
	writeJSON(w, http.StatusOK, map[string]any{"id": p.ID, "path_with_namespace": p.Path,
		"web_url": p.s.srv.URL + "/" + p.Path, "default_branch": p.DefaultBranch,
		"container_registry_image_prefix": strings.ToLower(host + "/" + p.Path),
		"container_registry_access_level": level})
}

func (p *Project) releaseJSON(r *Release) map[string]any {
	links := make([]any, 0, len(r.Links))
	for _, l := range r.Links {
		links = append(links, map[string]any{"id": l.ID, keyName: l.Name, "url": l.URL,
			"direct_asset_url": p.s.srv.URL + "/" + p.Path + "/-/releases/" + r.Tag + "/downloads" + l.DirectAssetPath,
			"link_type":        l.LinkType})
	}
	return map[string]any{"tag_name": r.Tag, keyName: r.Name, "description": r.Description,
		"created_at": r.CreatedAt.Format(time.RFC3339), "released_at": r.CreatedAt.Format(time.RFC3339),
		"commit": map[string]string{"id": r.Commit},
		"_links": map[string]string{"self": p.s.srv.URL + "/" + p.Path + "/-/releases/" + r.Tag},
		"assets": map[string]any{"links": links}}
}

func (p *Project) listReleases(w http.ResponseWriter, r *http.Request) {
	var out []any
	for _, v := range slices.Backward(p.releases) {
		out = append(out, p.releaseJSON(v))
	}
	writePage(w, r, out)
}

var sha = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

func (p *Project) createRelease(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TagName     string `json:"tag_name"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Ref         string `json:"ref"`
		Assets      struct {
			Links []struct {
				Name            string `json:"name"`
				URL             string `json:"url"`
				DirectAssetPath string `json:"direct_asset_path"`
				LinkType        string `json:"link_type"`
			} `json:"links"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.TagName == "" {
		writeJSON(w, http.StatusBadRequest, errMsg("tag_name is missing"))
		return
	}
	for _, rel := range p.releases {
		if rel.Tag == in.TagName {
			writeJSON(w, http.StatusConflict, msg("Release already exists"))
			return
		}
	}
	commit, ok := p.tags[in.TagName]
	if !ok {
		switch {
		case in.Ref == "":
			writeJSON(w, http.StatusUnprocessableEntity, msg("Ref is not specified"))
			return
		case in.Ref == p.DefaultBranch:
			commit = p.DefaultBranchHead
		case sha.MatchString(in.Ref):
			commit = in.Ref
		default:
			writeJSON(w, http.StatusUnprocessableEntity, msg("Ref is invalid"))
			return
		}
		p.tags[in.TagName] = commit
	}
	rel := &Release{Tag: in.TagName, Name: in.Name, Description: in.Description, Commit: commit,
		CreatedAt: p.s.tick()}
	if rel.Name == "" {
		rel.Name = in.TagName
	}
	for _, l := range in.Assets.Links {
		p.s.nextID++
		rel.Links = append(rel.Links, Link{ID: p.s.nextID, Name: l.Name, URL: l.URL,
			DirectAssetPath: l.DirectAssetPath, LinkType: l.LinkType})
	}
	p.releases = append(p.releases, rel)
	writeJSON(w, http.StatusCreated, p.releaseJSON(rel))
}

// listPackages filters as GitLab does: package_name matches any name
// containing it, package_type and package_version exactly.
func (p *Project) listPackages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if t := q.Get("package_type"); t != "" && t != generic {
		writePage(w, r, nil)
		return
	}
	var out []any
	for _, k := range p.packages {
		if !strings.Contains(k.name, q.Get("package_name")) {
			continue
		}
		if v := q.Get("package_version"); v != "" && v != k.version {
			continue
		}
		out = append(out, map[string]any{"id": k.id, keyName: k.name, "version": k.version,
			"package_type": generic, "status": "default"})
	}
	writePage(w, r, out)
}

func (p *Project) pkgByID(id string) *pkg {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return nil
	}
	for _, k := range p.packages {
		if k.id == n {
			return k
		}
	}
	return nil
}

// GitLab's rules for a generic package's name and version
// (lib/gitlab/regex/packages.rb): letters, digits, '.', '_', '+' and '-',
// and no ".." in a version. The file name's rule is not modelled: the
// gitprovider tests upload a name with a space to see it escaped.
var packageKeyRe = regexp.MustCompile(`^[A-Za-z0-9._+-]+$`)

// upload adds a file to the package, making the package when it is new. A
// file of a name already there is added beside it, as GitLab keeps
// duplicates. A name or version outside GitLab's rule is a 400, as GitLab
// answers (#1873: a tag prefix with '/' must not reach it as the version).
func (p *Project) upload(w http.ResponseWriter, r *http.Request, name, version, fileName string) {
	switch {
	case !packageKeyRe.MatchString(name):
		writeJSON(w, http.StatusBadRequest, errMsg("package_name is invalid"))
		return
	case !packageKeyRe.MatchString(version), strings.Contains(version, ".."):
		writeJSON(w, http.StatusBadRequest, errMsg("package_version is invalid"))
		return
	}
	size, err := io.Copy(io.Discard, r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errMsg(err.Error()))
		return
	}
	var k *pkg
	for _, c := range p.packages {
		if c.name == name && c.version == version {
			k = c
		}
	}
	if k == nil {
		p.s.nextID++
		k = &pkg{id: p.s.nextID, name: name, version: version}
		p.packages = append(p.packages, k)
	}
	p.s.nextID++
	k.files = append(k.files, &file{id: p.s.nextID, name: fileName, size: size})
	writeJSON(w, http.StatusCreated, msg("201 Created"))
}

func (p *Project) deleteFile(w http.ResponseWriter, pkgID, fileID string) {
	k := p.pkgByID(pkgID)
	n, err := strconv.ParseInt(fileID, 10, 64)
	if k == nil || err != nil {
		writeJSON(w, http.StatusNotFound, msg(notFound))
		return
	}
	i := slices.IndexFunc(k.files, func(f *file) bool { return f.id == n })
	if i < 0 {
		writeJSON(w, http.StatusNotFound, msg(notFound))
		return
	}
	k.files = slices.Delete(k.files, i, i+1)
	w.WriteHeader(http.StatusNoContent)
}

// writePage writes the page of items the request's page and per_page ask
// for, GitLab's default of 20 a page, at most 100.
func writePage(w http.ResponseWriter, r *http.Request, items []any) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if per < 1 {
		per = 20
	}
	per = min(per, 100)
	start := min((page-1)*per, len(items))
	end := min(start+per, len(items))
	if end < len(items) {
		w.Header().Set("X-Next-Page", strconv.Itoa(page+1))
	}
	out := items[start:end]
	if out == nil {
		out = []any{}
	}
	writeJSON(w, http.StatusOK, out)
}

// Path segments and keys the fake uses more than once.
const (
	segReleases  = "releases"
	segPackages  = "packages"
	packageFiles = "package_files"
	generic      = "generic"
	keyName      = "name"
	notFound     = "404 Not Found"
)

// msg is GitLab's error body with a message, errMsg with an error.
func msg(m string) map[string]string    { return map[string]string{"message": m} }
func errMsg(e string) map[string]string { return map[string]string{"error": e} }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
