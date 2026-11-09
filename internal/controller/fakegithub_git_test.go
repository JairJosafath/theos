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

package controller

import (
	"crypto/sha1" //nolint:gosec // git names its objects by SHA-1
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports/fake"
)

// The fake GitHub's Git Data API: a repository's blobs, trees, commits and
// refs, named by git's own SHA-1s so the operator's hashes match them.

type fakeGitStore struct {
	branch string
	// private is what GitHub says of the repository's visibility.
	private bool
	blobs   map[string][]byte
	trees   map[string][]fakeTreeEntry
	commits map[string]fakeGitCommit
	// refs is each ref's commit, by heads/... or tags/...
	refs map[string]string
	// landFirst is a file another app commits to the default branch just
	// before the next update of it, which then isn't a fast-forward.
	landFirst *fake.SourceFile
}

type fakeTreeEntry struct {
	name, mode, typ, sha string
}

type fakeGitCommit struct {
	tree, message string
	parents       []string
}

// fakeFlat is a file of a tree, flattened: its mode and blob.
type fakeFlat struct{ mode, sha string }

// newFakeGitStore makes a repository whose default branch main holds a README.
func newFakeGitStore(repository string) *fakeGitStore {
	s := &fakeGitStore{branch: "main", blobs: map[string][]byte{}, trees: map[string][]fakeTreeEntry{},
		commits: map[string]fakeGitCommit{}, refs: map[string]string{}}
	readme := s.blob([]byte("# " + repository + "\n"))
	tree := s.buildTree(map[string]fakeFlat{"README.md": {"100644", readme}})
	s.refs["heads/main"] = s.commit("First commit", tree, nil)
	return s
}

func (s *fakeGitStore) blob(data []byte) string {
	sha := model.BlobSHA(data)
	s.blobs[sha] = data
	return sha
}

// buildTree stores the trees of files, by path, and returns the root's.
func (s *fakeGitStore) buildTree(files map[string]fakeFlat) string {
	var entries []fakeTreeEntry
	folders := map[string]map[string]fakeFlat{}
	for path, f := range files {
		name, rest, inFolder := strings.Cut(path, "/")
		if !inFolder {
			entries = append(entries, fakeTreeEntry{name, f.mode, "blob", f.sha})
			continue
		}
		if folders[name] == nil {
			folders[name] = map[string]fakeFlat{}
		}
		folders[name][rest] = f
	}
	for name, sub := range folders {
		entries = append(entries, fakeTreeEntry{name, "040000", "tree", s.buildTree(sub)})
	}
	// git sorts a folder as if its name ended in "/".
	key := func(e fakeTreeEntry) string {
		if e.typ == "tree" {
			return e.name + "/"
		}
		return e.name
	}
	slices.SortFunc(entries, func(a, b fakeTreeEntry) int { return strings.Compare(key(a), key(b)) })
	var body []byte
	for _, e := range entries {
		raw, _ := hex.DecodeString(e.sha)
		body = append(body, strings.TrimPrefix(e.mode, "0")+" "+e.name+"\x00"...)
		body = append(body, raw...)
	}
	h := sha1.New() //nolint:gosec // git's object name
	h.Write(fmt.Appendf(nil, "tree %d\x00", len(body)))
	h.Write(body)
	sha := hex.EncodeToString(h.Sum(nil))
	s.trees[sha] = entries
	return sha
}

// flatten adds the files of tree sha to out, under prefix.
func (s *fakeGitStore) flatten(sha, prefix string, out map[string]fakeFlat) {
	for _, e := range s.trees[sha] {
		if e.typ == "tree" {
			s.flatten(e.sha, prefix+e.name+"/", out)
		} else {
			out[prefix+e.name] = fakeFlat{e.mode, e.sha}
		}
	}
}

func (s *fakeGitStore) commit(message, tree string, parents []string) string {
	h := sha1.New() //nolint:gosec // a commit's name, unique enough for a fake
	h.Write(fmt.Appendf(nil, "%s\x00%s\x00%s", tree, strings.Join(parents, ","), message))
	sha := hex.EncodeToString(h.Sum(nil))
	s.commits[sha] = fakeGitCommit{tree: tree, message: message, parents: parents}
	return sha
}

// descends says commit sha has ancestor among its ancestors, or is it.
func (s *fakeGitStore) descends(sha, ancestor string) bool {
	if sha == ancestor {
		return true
	}
	return slices.ContainsFunc(s.commits[sha].parents, func(p string) bool { return s.descends(p, ancestor) })
}

// putFile commits path with data on the default branch.
func (s *fakeGitStore) putFile(path, data string) {
	head := s.refs["heads/"+s.branch]
	files := map[string]fakeFlat{}
	s.flatten(s.commits[head].tree, "", files)
	files[path] = fakeFlat{"100644", s.blob([]byte(data))}
	s.refs["heads/"+s.branch] = s.commit("Add "+path, s.buildTree(files), []string{head})
}

// serveGit answers /repos/{owner}/{repo}/git/{rest}.
func (s *fakeGitStore) serveGit(w http.ResponseWriter, req *http.Request, rest []string) {
	notFound := map[string]string{"message": "Not Found"}
	what := strings.Join(rest[1:], "/")
	switch {
	case rest[0] == "ref" && req.Method == http.MethodGet:
		sha, ok := s.refs[what]
		if !ok {
			reply(w, http.StatusNotFound, notFound)
			return
		}
		reply(w, http.StatusOK, map[string]any{"ref": "refs/" + what,
			"object": map[string]string{"sha": sha, "type": "commit"}})
	case rest[0] == "commits" && req.Method == http.MethodGet:
		c, ok := s.commits[what]
		if !ok {
			reply(w, http.StatusNotFound, notFound)
			return
		}
		parents := []map[string]string{}
		for _, p := range c.parents {
			parents = append(parents, map[string]string{"sha": p})
		}
		reply(w, http.StatusOK, map[string]any{"sha": what, "message": c.message,
			"tree": map[string]string{"sha": c.tree}, "parents": parents})
	case rest[0] == "trees" && req.Method == http.MethodGet:
		if _, ok := s.trees[what]; !ok {
			reply(w, http.StatusNotFound, notFound)
			return
		}
		reply(w, http.StatusOK, map[string]any{"sha": what, "truncated": false,
			"tree": s.listTree(what, "", req.URL.Query().Get("recursive") != "")})
	case rest[0] == "blobs" && req.Method == http.MethodPost:
		var in struct{ Encoding, Content string }
		_ = json.NewDecoder(req.Body).Decode(&in)
		data, err := base64.StdEncoding.DecodeString(in.Content)
		if err != nil || in.Encoding != "base64" {
			reply(w, http.StatusBadRequest, map[string]string{"message": "bad blob"})
			return
		}
		reply(w, http.StatusCreated, map[string]string{"sha": s.blob(data)})
	case rest[0] == "trees" && req.Method == http.MethodPost:
		s.createTree(w, req)
	case rest[0] == "commits" && req.Method == http.MethodPost:
		var in struct {
			Message, Tree string
			Parents       []string
		}
		_ = json.NewDecoder(req.Body).Decode(&in)
		if _, ok := s.trees[in.Tree]; !ok {
			reply(w, http.StatusUnprocessableEntity, map[string]string{"message": "Tree SHA does not exist"})
			return
		}
		reply(w, http.StatusCreated, map[string]string{"sha": s.commit(in.Message, in.Tree, in.Parents)})
	case rest[0] == "refs" && len(rest) == 1 && req.Method == http.MethodPost:
		var in struct{ Ref, SHA string }
		_ = json.NewDecoder(req.Body).Decode(&in)
		ref := strings.TrimPrefix(in.Ref, "refs/")
		if _, ok := s.refs[ref]; ok {
			reply(w, http.StatusUnprocessableEntity, map[string]string{"message": "Reference already exists"})
			return
		}
		s.refs[ref] = in.SHA
		reply(w, http.StatusCreated, map[string]any{"ref": in.Ref, "object": map[string]string{"sha": in.SHA}})
	case rest[0] == "refs" && req.Method == http.MethodPatch:
		var in struct {
			SHA   string
			Force bool
		}
		_ = json.NewDecoder(req.Body).Decode(&in)
		if what == "heads/"+s.branch && s.landFirst != nil {
			f := s.landFirst
			s.landFirst = nil
			s.putFile(f.Path, f.Data)
		}
		if !in.Force && !s.descends(in.SHA, s.refs[what]) {
			reply(w, http.StatusUnprocessableEntity, map[string]string{"message": "Update is not a fast forward"})
			return
		}
		s.refs[what] = in.SHA
		reply(w, http.StatusOK, map[string]any{"ref": "refs/" + what, "object": map[string]string{"sha": in.SHA}})
	default:
		reply(w, http.StatusNotFound, notFound)
	}
}

// listTree is the entries of tree sha, with every path below it when
// recursive.
func (s *fakeGitStore) listTree(sha, prefix string, recursive bool) []map[string]string {
	out := []map[string]string{}
	for _, e := range s.trees[sha] {
		out = append(out, map[string]string{"path": prefix + e.name, "mode": e.mode, "type": e.typ, "sha": e.sha})
		if recursive && e.typ == "tree" {
			out = append(out, s.listTree(e.sha, prefix+e.name+"/", true)...)
		}
	}
	return out
}

// createTree makes a tree of the entries on the base tree: an entry replaces
// its path, a tree's whole, or removes it when its SHA is null.
func (s *fakeGitStore) createTree(w http.ResponseWriter, req *http.Request) {
	var in struct {
		BaseTree string `json:"base_tree"`
		Tree     []struct {
			Path, Mode, Type string
			SHA              *string
		}
	}
	_ = json.NewDecoder(req.Body).Decode(&in)
	files := map[string]fakeFlat{}
	if in.BaseTree != "" {
		s.flatten(in.BaseTree, "", files)
	}
	for _, e := range in.Tree {
		for path := range files {
			if path == e.Path || strings.HasPrefix(path, e.Path+"/") {
				delete(files, path)
			}
		}
		switch {
		case e.SHA == nil:
		case e.Type == "tree":
			if _, ok := s.trees[*e.SHA]; !ok {
				reply(w, http.StatusUnprocessableEntity, map[string]string{"message": "tree not found"})
				return
			}
			s.flatten(*e.SHA, e.Path+"/", files)
		default:
			if _, ok := s.blobs[*e.SHA]; !ok {
				reply(w, http.StatusUnprocessableEntity, map[string]string{"message": "blob not found"})
				return
			}
			files[e.Path] = fakeFlat{e.Mode, *e.SHA}
		}
	}
	reply(w, http.StatusCreated, map[string]string{"sha": s.buildTree(files)})
}

// tagRelease creates the tag a draft is published under, at its target
// commit, or the default branch's head, unless the tag is there.
func (s *fakeGitStore) tagRelease(tag, target string) {
	if _, ok := s.refs["tags/"+tag]; ok {
		return
	}
	if _, ok := s.commits[target]; !ok {
		target = s.refs["heads/"+s.branch]
	}
	s.refs["tags/"+tag] = target
}

// ref returns the commit ref names in repository, "" when there is none.
func (f *fakeGitHub) ref(repository, ref string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.git[repository].refs[ref]
}

// folder returns the files under path, "." for all, at ref of repository, by
// their path in the folder.
func (f *fakeGitHub) folder(repository, ref, path string) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.git[repository]
	files := map[string]fakeFlat{}
	s.flatten(s.commits[s.refs[ref]].tree, "", files)
	out := map[string]string{}
	for p, file := range files {
		if path != "." {
			var ok bool
			if p, ok = strings.CutPrefix(p, path+"/"); !ok {
				continue
			}
		}
		out[p] = string(s.blobs[file.sha])
	}
	return out
}

// commitOf returns the message and parents of commit sha in repository.
func (f *fakeGitHub) commitOf(repository, sha string) fakeGitCommit {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.git[repository].commits[sha]
}

// putFile commits path with data on repository's default branch, as a person
// would.
func (f *fakeGitHub) putFile(repository, path, data string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.git[repository].putFile(path, data)
}

// landFirst makes another app commit path with data to repository's default
// branch just before the operator's next update of it.
func (f *fakeGitHub) landFirst(repository, path, data string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.git[repository].landFirst = &fake.SourceFile{Path: path, Data: data}
}

// tagHead tags repository's default branch's head, as a person would.
func (f *fakeGitHub) tagHead(repository, tag string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.git[repository]
	s.refs["tags/"+tag] = s.refs["heads/"+s.branch]
}
