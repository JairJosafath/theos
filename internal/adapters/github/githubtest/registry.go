/*
Copyright 2026 The theos Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package githubtest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	stdlog "log"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"slices"
	"strings"

	"github.com/google/go-containerregistry/pkg/registry"
)

// The fake's registries, step 7 of docs/plans/github-host.md (#80). An
// Enterprise Server serves them at subdomains of its host, and so does the
// fake: Client returns an HTTP client for which containers.<host> and
// npm.<host> are the instance.
//
//	containers.<host>  /v2/...             an OCI registry, go-containerregistry's own, behind Basic auth
//	npm.<host>         GET /@scope%2Fname  the package's document with its versions, 404 without the package
//	                   PUT /@scope%2Fname  publish the versions of an npm publish document: 201; 409 for a version
//	                                       that is there; 403 for a scope that is no account's, as GitHub's registry
//	                                       takes a package under its owner's scope alone, in lower case
//
// https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry
// and .../working-with-the-npm-registry. Both take a person's token and no
// other: an installation token has no permission for packages. The npm
// registry takes it as a Bearer token, the container registry as the
// password of Basic auth.
//
// And one call of the REST API, which the adapter reads a go.mod with:
//
//	GET /repos/{o}/{r}/contents/{path}?ref=  a file at a ref: its bytes when asked for as
//	                                         application/vnd.github.raw+json, else its JSON with the content in
//	                                         base64; 404 without the file or the ref
//
// https://docs.github.com/en/rest/repos/contents.

// npmPackage is a package of the fake's npm registry: each version's
// manifest as it was published, and the newest version.
type npmPackage struct {
	versions map[string]json.RawMessage
	latest   string
}

var contentsPath = regexp.MustCompile(`^/contents/(.+)$`)

// The subdomains the registries are served at.
const (
	containersHost = "containers"
	npmHost        = "npm"
)

// newRegistries makes the fake's empty registries.
func (s *Server) newRegistries() {
	s.images = registry.New(registry.Logger(stdlog.New(io.Discard, "", 0)))
	s.npm = map[string]*npmPackage{}
}

// Client returns an HTTP client that reaches the instance and its
// registries: an address at a subdomain of the instance's host is the
// instance.
func (s *Server) Client() *http.Client {
	instance := s.srv.Listener.Addr().String()
	dialer := &net.Dialer{}
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if strings.HasSuffix(address, "."+instance) {
				address = instance
			}
			return dialer.DialContext(ctx, network, address)
		},
	}}
}

// NpmVersions returns the versions of an npm package the registry holds,
// sorted; none for a package it doesn't have.
func (s *Server) NpmVersions(name string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	pkg := s.npm[name]
	if pkg == nil {
		return nil
	}
	versions := make([]string, 0, len(pkg.versions))
	for v := range pkg.versions {
		versions = append(versions, v)
	}
	slices.Sort(versions)
	return versions
}

// NpmManifest returns the manifest an npm package's version was published
// with, nil without it.
func (s *Server) NpmManifest(name, version string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	pkg := s.npm[name]
	if pkg == nil {
		return nil
	}
	var manifest map[string]any
	if json.Unmarshal(pkg.versions[version], &manifest) != nil {
		return nil
	}
	return manifest
}

// person says whether token is a person's: one the fake takes that is no
// installation's.
func (s *Server) person(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, known := s.tokens[token]
	return known && s.grants[token] == nil
}

// serveRegistries serves a request to one of the registries' subdomains,
// and says whether r was one.
func (s *Server) serveRegistries(w http.ResponseWriter, r *http.Request) bool {
	switch sub, _, _ := strings.Cut(r.Host, "."); sub {
	case containersHost:
		if _, password, ok := r.BasicAuth(); !ok || !s.person(password) {
			w.Header().Set("WWW-Authenticate", `Basic realm="GitHub Container Registry"`)
			reply(w, http.StatusUnauthorized, struct {
				Errors []ociError `json:"errors"`
			}{[]ociError{{"UNAUTHORIZED", "authentication required"}}})
			return true
		}
		s.images.ServeHTTP(w, r)
	case npmHost:
		s.serveNpm(w, r)
	default:
		return false
	}
	return true
}

// ociError is an error of the container registry's API.
type ociError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// npmError is an error of the npm registry.
type npmError struct {
	Error string `json:"error"`
}

// npmDocument is the part of an npm publish document, and of the document
// the registry answers, that the fake keeps.
type npmDocument struct {
	Name     string                     `json:"name"`
	DistTags map[string]string          `json:"dist-tags"`
	Versions map[string]json.RawMessage `json:"versions"`
}

func (s *Server) serveNpm(w http.ResponseWriter, r *http.Request) {
	token, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !bearer || !s.person(token) {
		reply(w, http.StatusUnauthorized, npmError{"unauthenticated: User cannot be authenticated with the token provided."})
		return
	}
	name, err := url.PathUnescape(strings.TrimPrefix(r.URL.EscapedPath(), "/"))
	scope, _, scoped := strings.Cut(name, "/")
	if err != nil || !scoped || !strings.HasPrefix(scope, "@") {
		reply(w, http.StatusNotFound, npmError{"not found"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pkg := s.npm[name]
	switch r.Method {
	case http.MethodGet:
		if pkg == nil {
			reply(w, http.StatusNotFound, npmError{"npm package \"" + name + "\" does not exist"})
			return
		}
		reply(w, http.StatusOK, npmDocument{Name: name, DistTags: map[string]string{"latest": pkg.latest},
			Versions: pkg.versions})
	case http.MethodPut:
		var doc npmDocument
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil || doc.Name != name || len(doc.Versions) == 0 {
			reply(w, http.StatusBadRequest, npmError{"the publish document is not one of " + name})
			return
		}
		if !s.account(strings.TrimPrefix(scope, "@")) || name != strings.ToLower(name) {
			reply(w, http.StatusForbidden, npmError{"the scope " + scope + " is no account's the token may publish to"})
			return
		}
		if pkg == nil {
			pkg = &npmPackage{versions: map[string]json.RawMessage{}}
		}
		for version := range doc.Versions {
			if _, there := pkg.versions[version]; there {
				reply(w, http.StatusConflict, npmError{"Cannot publish over existing version"})
				return
			}
		}
		for version, manifest := range doc.Versions {
			pkg.versions[version], pkg.latest = manifest, version
		}
		s.npm[name] = pkg
		reply(w, http.StatusCreated, struct {
			OK bool `json:"ok"`
		}{true})
	default:
		reply(w, http.StatusMethodNotAllowed, npmError{"method not allowed"})
	}
}

// account says whether name, in lower case, is an organization's or a
// user's of the fake.
func (s *Server) account(name string) bool {
	for org := range s.orgs {
		if strings.ToLower(org) == name {
			return true
		}
	}
	for _, login := range s.tokens {
		if strings.ToLower(login) == name {
			return true
		}
	}
	return false
}

// getContents answers a file of the repository at the ref the query names,
// main without one.
func (s *Server) getContents(w http.ResponseWriter, r *http.Request, kept *repo, path string) {
	ref := r.URL.Query().Get("ref")
	if ref == "" {
		ref = "main"
	}
	//nolint:gosec // a test fake running git on its own temporary repositories
	data, err := exec.Command("git", "-C", s.dir(kept.owner, kept.name), "show", ref+":"+path).Output()
	if err != nil {
		reply(w, http.StatusNotFound, message("Not Found"))
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "raw") {
		w.Header().Set("Content-Type", "application/vnd.github.raw+json")
		//nolint:gosec // a test fake answering a file of its own temporary repositories
		_, _ = w.Write(data)
		return
	}
	reply(w, http.StatusOK, struct {
		Type     string `json:"type"`
		Path     string `json:"path"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}{"file", path, "base64", base64.StdEncoding.EncodeToString(data)})
}
