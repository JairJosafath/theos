package gitprovider

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
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
	"testing"
	"unicode/utf8"
)

// fakeGit is a repository's git objects and refs, hashed as git hashes them,
// served as GitHub's Git Data API does.
type fakeGit struct {
	defaultBranch string
	private       bool
	blobs         map[string][]byte
	// trees holds each tree's direct entries, by name.
	trees   map[string]map[string]githubTreeEntry
	commits map[string]githubCommit
	// tags are annotated tags: what each points at.
	tags map[string]githubObject
	// refs are by their full name, refs/heads/main.
	refs map[string]githubObject
	// protected refs refuse every update.
	protected map[string]bool
	// blobRequests records each blob request's Content-Length and
	// Transfer-Encoding.
	blobRequests []string
	// forced records each ref update's force.
	forced []bool
}

func newFakeGit() *fakeGit {
	return &fakeGit{defaultBranch: "main", blobs: map[string][]byte{}, trees: map[string]map[string]githubTreeEntry{},
		commits: map[string]githubCommit{}, tags: map[string]githubObject{}, refs: map[string]githubObject{},
		protected: map[string]bool{}}
}

func gitHash(kind string, data []byte) string {
	sum := sha1.Sum(append(fmt.Appendf(nil, "%s %d\x00", kind, len(data)), data...))
	return hex.EncodeToString(sum[:])
}

func (g *fakeGit) writeBlob(data []byte) string {
	sha := gitHash("blob", data)
	g.blobs[sha] = data
	return sha
}

// writeTree stores entries as git would, sorted with a tree's name as name/.
func (g *fakeGit) writeTree(entries map[string]githubTreeEntry) string {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sortName := func(n string) string {
		if entries[n].Type == "tree" {
			return n + "/"
		}
		return n
	}
	slices.SortFunc(names, func(a, b string) int { return strings.Compare(sortName(a), sortName(b)) })
	var buf bytes.Buffer
	for _, name := range names {
		e := entries[name]
		raw, _ := hex.DecodeString(*e.SHA)
		fmt.Fprintf(&buf, "%s %s\x00", strings.TrimPrefix(e.Mode, "0"), name)
		buf.Write(raw)
	}
	sha := gitHash("tree", buf.Bytes())
	g.trees[sha] = entries
	return sha
}

func (g *fakeGit) writeCommit(message, tree string, parents ...string) string {
	text := "tree " + tree + "\n"
	c := githubCommit{Message: message}
	c.Tree.SHA = tree
	for _, p := range parents {
		text += "parent " + p + "\n"
		c.Parents = append(c.Parents, struct {
			SHA string `json:"sha"`
		}{p})
	}
	c.SHA = gitHash("commit", []byte(text+"\n"+message))
	g.commits[c.SHA] = c
	return c.SHA
}

// insert sets, or with no SHA removes, the entry at path below entries,
// writing the trees on the way.
func (g *fakeGit) insert(entries map[string]githubTreeEntry, path []string, e githubTreeEntry) error {
	name := path[0]
	if len(path) == 1 {
		if e.SHA == nil {
			delete(entries, name)
			return nil
		}
		if _, ok := g.object(e.Type, *e.SHA); !ok {
			return fmt.Errorf("no %s %s", e.Type, *e.SHA)
		}
		e.Path = name
		entries[name] = e
		return nil
	}
	sub := map[string]githubTreeEntry{}
	if old, ok := entries[name]; ok && old.Type == "tree" {
		maps.Copy(sub, g.trees[*old.SHA])
	}
	if err := g.insert(sub, path[1:], e); err != nil {
		return err
	}
	if len(sub) == 0 {
		delete(entries, name)
		return nil
	}
	sha := g.writeTree(sub)
	entries[name] = githubTreeEntry{Path: name, Mode: "040000", Type: "tree", SHA: &sha}
	return nil
}

func (g *fakeGit) object(kind, sha string) (any, bool) {
	switch kind {
	case "blob":
		v, ok := g.blobs[sha]
		return v, ok
	case "tree":
		v, ok := g.trees[sha]
		return v, ok
	case "commit":
		v, ok := g.commits[sha]
		return v, ok
	}
	return nil, false
}

// list returns tree's entries, sorted, with every path below it when recursive.
func (g *fakeGit) list(tree, prefix string, recursive bool) []githubTreeEntry {
	var out []githubTreeEntry
	for _, e := range g.trees[tree] {
		e.Path = prefix + e.Path
		out = append(out, e)
		if recursive && e.Type == "tree" {
			out = append(out, g.list(*e.SHA, e.Path+"/", true)...)
		}
	}
	slices.SortFunc(out, func(a, b githubTreeEntry) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// descends reports whether commit has ancestor among its ancestors, or is it.
func (g *fakeGit) descends(commit, ancestor string) bool {
	if commit == ancestor {
		return true
	}
	for _, p := range g.commits[commit].Parents {
		if g.descends(p.SHA, ancestor) {
			return true
		}
	}
	return false
}

// serve answers a Git Data or default branch request, under f's lock.
func (g *fakeGit) serve(f *fakeGitHub, w http.ResponseWriter, r *http.Request) {
	repo := "/repos/" + fakeGitHubRepo
	if r.URL.Path == repo {
		f.json(w, http.StatusOK, map[string]any{"id": fakeGitHubRepoID, "full_name": fakeGitHubRepo,
			"default_branch": g.defaultBranch, "private": g.private})
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, repo+"/git/")
	kind, arg, _ := strings.Cut(rest, "/")
	if len(g.refs) == 0 {
		f.reply(w, http.StatusConflict, `{"message":"Git Repository is empty."}`)
		return
	}
	notFound := func() { f.reply(w, http.StatusNotFound, `{"message":"Not Found"}`) }
	switch {
	case r.Method == http.MethodGet:
		g.read(f, w, r, kind, arg)
	case r.Method == http.MethodPost && rest == "blobs":
		g.blobRequests = append(g.blobRequests, fmt.Sprintf("length=%d encoding=%v type=%s",
			r.ContentLength, r.TransferEncoding, r.Header.Get("Content-Type")))
		var in struct{ Content, Encoding string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Encoding != "base64" {
			f.reply(w, http.StatusBadRequest, `{"message":"Problems parsing JSON"}`)
			return
		}
		data, err := base64.StdEncoding.DecodeString(in.Content)
		if err != nil {
			g.invalid(f, w, "content is not base64")
			return
		}
		f.json(w, http.StatusCreated, githubObject{SHA: g.writeBlob(data)})
	case r.Method == http.MethodPost && rest == "trees":
		g.createTree(f, w, r)
	case r.Method == http.MethodPost && rest == "commits":
		var in struct {
			Message, Tree string
			Parents       []string
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if _, ok := g.trees[in.Tree]; !ok {
			g.invalid(f, w, "Tree SHA does not exist")
			return
		}
		f.json(w, http.StatusCreated, githubObject{SHA: g.writeCommit(in.Message, in.Tree, in.Parents...)})
	case r.Method == http.MethodPost && rest == "refs":
		var in struct{ Ref, SHA string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		if _, ok := g.refs[in.Ref]; ok {
			g.invalid(f, w, "Reference already exists")
			return
		}
		if _, ok := g.commits[in.SHA]; !ok {
			g.invalid(f, w, "Object does not exist")
			return
		}
		g.refs[in.Ref] = githubObject{SHA: in.SHA, Type: "commit"}
		f.json(w, http.StatusCreated, map[string]any{"ref": in.Ref, "object": g.refs[in.Ref]})
	case r.Method == http.MethodPatch && kind == "refs":
		g.updateRef(f, w, r, "refs/"+arg)
	default:
		f.errorf("unexpected request %s %s", r.Method, r.URL.Path)
		notFound()
	}
}

// read answers a GET of a ref, an annotated tag, a commit or a tree.
func (g *fakeGit) read(f *fakeGitHub, w http.ResponseWriter, r *http.Request, kind, arg string) {
	switch kind {
	case "ref":
		obj, ok := g.refs["refs/"+arg]
		if !ok {
			f.reply(w, http.StatusNotFound, `{"message":"Not Found"}`)
			return
		}
		f.json(w, http.StatusOK, map[string]any{"ref": "refs/" + arg, "object": obj})
	case "tags":
		obj, ok := g.tags[arg]
		if !ok {
			f.reply(w, http.StatusNotFound, `{"message":"Not Found"}`)
			return
		}
		f.json(w, http.StatusOK, map[string]any{"sha": arg, "object": obj})
	case "commits":
		c, ok := g.commits[arg]
		if !ok {
			f.reply(w, http.StatusNotFound, `{"message":"Not Found"}`)
			return
		}
		f.json(w, http.StatusOK, c)
	case "trees":
		if _, ok := g.trees[arg]; !ok {
			f.reply(w, http.StatusNotFound, `{"message":"Not Found"}`)
			return
		}
		f.json(w, http.StatusOK, githubTree{SHA: arg, Tree: g.list(arg, "", r.URL.Query().Get("recursive") != "")})
	default:
		f.errorf("unexpected request %s %s", r.Method, r.URL.Path)
		f.reply(w, http.StatusNotFound, `{"message":"Not Found"}`)
	}
}

func (g *fakeGit) createTree(f *fakeGitHub, w http.ResponseWriter, r *http.Request) {
	var in struct {
		BaseTree string            `json:"base_tree"`
		Tree     []githubTreeEntry `json:"tree"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	entries := map[string]githubTreeEntry{}
	if in.BaseTree != "" {
		base, ok := g.trees[in.BaseTree]
		if !ok {
			g.invalid(f, w, "base_tree is not a valid tree oid")
			return
		}
		maps.Copy(entries, base)
	}
	for _, e := range in.Tree {
		if err := g.insert(entries, strings.Split(e.Path, "/"), e); err != nil {
			g.invalid(f, w, "tree.sha "+err.Error())
			return
		}
	}
	f.json(w, http.StatusCreated, githubObject{SHA: g.writeTree(entries)})
}

func (g *fakeGit) updateRef(f *fakeGitHub, w http.ResponseWriter, r *http.Request, ref string) {
	var in struct {
		SHA   string
		Force bool
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	g.forced = append(g.forced, in.Force)
	old, ok := g.refs[ref]
	switch {
	case !ok:
		g.invalid(f, w, "Reference does not exist")
	case g.protected[ref]:
		g.invalid(f, w, "Protected branch update failed for "+ref+".")
	case !g.descends(in.SHA, old.SHA) && !in.Force:
		g.invalid(f, w, "Update is not a fast forward")
	default:
		g.refs[ref] = githubObject{SHA: in.SHA, Type: "commit"}
		f.json(w, http.StatusOK, map[string]any{"ref": ref, "object": g.refs[ref]})
	}
}

func (g *fakeGit) invalid(f *fakeGitHub, w http.ResponseWriter, why string) {
	f.reply(w, http.StatusUnprocessableEntity, fmt.Sprintf(`{"message":%q}`, why))
}

// seed gives the repository a main branch whose head holds files.
func (g *fakeGit) seed(files map[string]string) (commit, tree string) {
	root := map[string]githubTreeEntry{}
	for _, path := range slices.Sorted(maps.Keys(files)) {
		sha := g.writeBlob([]byte(files[path]))
		_ = g.insert(root, strings.Split(path, "/"), githubTreeEntry{Mode: "100644", Type: "blob", SHA: &sha})
	}
	tree = g.writeTree(root)
	commit = g.writeCommit("initial", tree)
	g.refs["refs/heads/main"] = githubObject{SHA: commit, Type: "commit"}
	return commit, tree
}

// TestGitHubSource publishes a folder the way the plan's decision 4 does: a
// blob each, the folder's tree whole, the root on the head's with the folder
// replaced, a commit on the head, a fast-forward and a tag. Every
// SourceTarget method is walked, and each request is a Git Data call or the
// default branch's.
func TestGitHubSource(t *testing.T) {
	f, g := newFakeGitHub(t)
	ctx := context.Background()
	head, headTree := f.git.seed(map[string]string{
		"README.md": "the repo", "vscode/main.ts": "other app", "engine/old.go": "hand-made copy"})

	commit := readHead(t, g, head, headTree)

	source := createSource(t, g)
	// 32 bytes of JSON before the base64 and 2 after: go.mod, link, main.go,
	// run.sh.
	wantBlobs := []string{"length=34 encoding=[] type=application/json", "length=46 encoding=[] type=application/json",
		"length=54 encoding=[] type=application/json", "length=50 encoding=[] type=application/json"}
	if !reflect.DeepEqual(f.git.blobRequests, wantBlobs) {
		t.Errorf("blob requests = %q, want %q", f.git.blobRequests, wantBlobs)
	}
	folder, err := g.CreateTree(ctx, "", source)
	if err != nil {
		t.Fatalf("CreateTree() error = %v", err)
	}
	newRoot, err := g.CreateTree(ctx, commit.Tree, []GitTreeEntry{{Path: "engine", Mode: "040000", Type: "tree", SHA: folder}})
	if err != nil {
		t.Fatalf("CreateTree() on the head's error = %v", err)
	}
	tree, err := g.Tree(ctx, newRoot, true)
	wantTree := []string{"README.md 100644 blob", "engine 040000 tree", "engine/go.mod 100644 blob",
		"engine/link 120000 blob", "engine/main.go 100644 blob", "engine/run.sh 100755 blob",
		"vscode 040000 tree", "vscode/main.ts 100644 blob"}
	if err != nil || !reflect.DeepEqual(entryNames(tree), wantTree) {
		t.Errorf("Tree(recursive) = %v, %v; want %v: the folder replaced whole, the rest the head's", entryNames(tree), err, wantTree)
	}
	// Removing a path, with no SHA.
	if trimmed, err := g.CreateTree(ctx, newRoot, []GitTreeEntry{{Path: "README.md", Mode: "100644", Type: "blob"}}); err != nil {
		t.Errorf("CreateTree() removing a path error = %v", err)
	} else if tree, _ := g.Tree(ctx, trimmed, false); slices.Contains(entryNames(tree), "README.md 100644 blob") {
		t.Errorf("CreateTree() removing README.md left %v", entryNames(tree))
	}

	message := "CEL Lens engine/v0.6.1: source\n\nC4factory-Source: shop/engine v0.6.1"
	sourceCommit, err := g.CreateCommit(ctx, message, newRoot, []string{head})
	if err != nil {
		t.Fatalf("CreateCommit() error = %v", err)
	}
	if c, err := g.Commit(ctx, sourceCommit); err != nil || c.Tree != newRoot || !reflect.DeepEqual(c.Parents, []string{head}) || c.Message != message {
		t.Errorf("Commit() of the new commit = %+v, %v", c, err)
	}
	if err := g.UpdateRef(ctx, "heads/main", sourceCommit); err != nil {
		t.Fatalf("UpdateRef() error = %v", err)
	}
	if err := g.CreateRef(ctx, "tags/engine/v0.6.1", sourceCommit); err != nil {
		t.Fatalf("CreateRef() error = %v", err)
	}
	if got, err := g.Ref(ctx, "tags/engine/v0.6.1"); err != nil || got != sourceCommit {
		t.Errorf("Ref() of the new tag = %q, %v; want %s", got, err, sourceCommit)
	}
	if err := g.CreateRef(ctx, "refs/tags/engine/v0.6.1", sourceCommit); !errors.Is(err, ErrRefExists) {
		t.Errorf("CreateRef() again error = %v; want ErrRefExists", err)
	}
	// The head moved on before this update: not a fast-forward, no failure.
	if err := g.UpdateRef(ctx, "heads/main", head); !errors.Is(err, ErrNotFastForward) {
		t.Errorf("UpdateRef() backwards error = %v; want ErrNotFastForward", err)
	}
	if !reflect.DeepEqual(f.git.forced, []bool{false, false}) {
		t.Errorf("ref updates forced = %v, want never", f.git.forced)
	}

	checkSourceCalls(t, f, head, headTree)

	f.git.protected["refs/heads/main"] = true
	if err := g.UpdateRef(ctx, "heads/main", sourceCommit); ReleaseReason(err) != ReasonBranchProtected {
		t.Errorf("UpdateRef() of a protected branch error = %v; want reason %s", err, ReasonBranchProtected)
	}
}

// readHead reads the default branch's head the way a publish starts: the
// branch, its ref, its commit and its root tree.
func readHead(t *testing.T, g ReleaseTarget, head, headTree string) GitCommit {
	t.Helper()
	ctx := context.Background()
	branch, err := g.DefaultBranch(ctx)
	if err != nil || branch != "main" {
		t.Fatalf("DefaultBranch() = %q, %v", branch, err)
	}
	got, err := g.Ref(ctx, "heads/"+branch)
	if err != nil || got != head {
		t.Fatalf("Ref(heads/main) = %q, %v; want %s", got, err, head)
	}
	if got, err := g.Ref(ctx, "tags/engine/v0.6.1"); err != nil || got != "" {
		t.Errorf("Ref() of no tag = %q, %v; want none", got, err)
	}
	commit, err := g.Commit(ctx, head)
	if err != nil || commit.Tree != headTree || len(commit.Parents) != 0 || commit.Message != "initial" {
		t.Fatalf("Commit() = %+v, %v; want tree %s", commit, err, headTree)
	}
	root, err := g.Tree(ctx, commit.Tree, false)
	wantRoot := []string{"README.md 100644 blob", "engine 040000 tree", "vscode 040000 tree"}
	if err != nil || !reflect.DeepEqual(entryNames(root), wantRoot) || root.Truncated {
		t.Errorf("Tree() = %v, %v; want %v", entryNames(root), err, wantRoot)
	}
	return commit
}

// createSource streams the source's blobs, each from a reader that isn't a
// strings.Reader, so the Content-Length must come from size, and returns
// their entries.
func createSource(t *testing.T, g ReleaseTarget) []GitTreeEntry {
	t.Helper()
	files := map[string]string{"main.go": "package main\n", "run.sh": "#!/bin/sh\n", "go.mod": "", "link": "main.go"}
	modes := map[string]string{"main.go": "100644", "run.sh": "100755", "go.mod": "100644", "link": "120000"}
	source := make([]GitTreeEntry, 0, len(files))
	for _, name := range slices.Sorted(maps.Keys(files)) {
		sha, err := g.CreateBlob(context.Background(), int64(len(files[name])), io.MultiReader(strings.NewReader(files[name])))
		if want := gitHash("blob", []byte(files[name])); err != nil || sha != want {
			t.Fatalf("CreateBlob(%s) = %s, %v; want %s, git's hash", name, sha, err, want)
		}
		source = append(source, GitTreeEntry{Path: name, Mode: modes[name], Type: "blob", SHA: sha})
	}
	return source
}

// checkSourceCalls checks each SourceTarget method made its request, and
// that a method added to SourceTarget is walked too.
func checkSourceCalls(t *testing.T, f *fakeGitHub, head, headTree string) {
	t.Helper()
	calls := map[string]bool{}
	for _, p := range f.paths {
		calls[p] = true
	}
	repo := "/repos/" + fakeGitHubRepo
	git := repo + "/git/"
	wantCalls := map[string]string{
		"Repository":    "GET " + repo,
		"DefaultBranch": "GET " + repo,
		"Ref":           "GET " + git + "ref/heads/main",
		"Commit":        "GET " + git + "commits/" + head,
		"Tree":          "GET " + git + "trees/" + headTree,
		"CreateBlob":    "POST " + git + "blobs",
		"CreateTree":    "POST " + git + "trees",
		"CreateCommit":  "POST " + git + "commits",
		"CreateRef":     "POST " + git + "refs",
		"UpdateRef":     "PATCH " + git + "refs/heads/main",
	}
	for method, call := range wantCalls {
		if !calls[call] {
			t.Errorf("%s made no %s; requests = %v", method, call, calls)
		}
	}
	if n := reflect.TypeFor[SourceTarget]().NumMethod(); n != len(wantCalls) {
		t.Errorf("SourceTarget has %d methods; walk the new ones in this test and add their requests above", n)
	}
}

func entryNames(tree GitTree) []string {
	out := make([]string, 0, len(tree.Entries))
	for _, e := range tree.Entries {
		out = append(out, e.Path+" "+e.Mode+" "+e.Type)
	}
	return out
}

// TestGitHubSourceAnnotatedTag peels a tag object to its commit.
// fakeGitHubRepoID is GitHub's id of fakeGitHubRepo.
const fakeGitHubRepoID = 4242

func TestGitHubRepository(t *testing.T) {
	f, g := newFakeGitHub(t)
	f.git.seed(map[string]string{"README.md": "the repo"})
	got, err := g.Repository(context.Background())
	want := TargetRepository{ID: fakeGitHubRepoID, FullName: fakeGitHubRepo, DefaultBranch: "main"}
	if err != nil || got != want {
		t.Errorf("Repository() = %+v, %v; want %+v", got, err, want)
	}
}

// A renamed repository's old name redirects to it by id, as GitHub's API
// does: the client follows, and reads the new name and the same id.
func TestGitHubRepositoryRenamed(t *testing.T) {
	token := rand.Text()
	var paths []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("request %s %s has Authorization %q", r.Method, r.URL.Path, got)
		}
		switch r.URL.Path {
		case "/repos/" + fakeGitHubRepo:
			http.Redirect(w, r, "/repositories/4242", http.StatusMovedPermanently)
		case "/repositories/4242":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":4242,"full_name":"acme/private-cel-lens","default_branch":"main"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(api.Close)
	g, err := Client{}.GitHubReleases(GitHubConfig{Repository: fakeGitHubRepo, Token: token, APIURL: api.URL})
	if err != nil {
		t.Fatalf("GitHubReleases() error = %v", err)
	}
	got, err := g.Repository(context.Background())
	want := TargetRepository{ID: 4242, FullName: "acme/private-cel-lens", DefaultBranch: "main"}
	if err != nil || got != want {
		t.Errorf("Repository() = %+v, %v; want %+v", got, err, want)
	}
	if wantPaths := []string{"GET /repos/" + fakeGitHubRepo, "GET /repositories/4242"}; !reflect.DeepEqual(paths, wantPaths) {
		t.Errorf("requests = %q, want %q", paths, wantPaths)
	}
}

// TestGitHubSourcePrivate reads a private repository as private.
func TestGitHubSourcePrivate(t *testing.T) {
	f, g := newFakeGitHub(t)
	if read, err := g.Repository(context.Background()); err != nil || read.Private {
		t.Errorf("Repository() of a public repository = %+v, %v; want it public", read, err)
	}
	f.git.private = true
	if read, err := g.Repository(context.Background()); err != nil || !read.Private {
		t.Errorf("Repository() of a private repository = %+v, %v; want it private", read, err)
	}
}

func TestGitHubSourceAnnotatedTag(t *testing.T) {
	f, g := newFakeGitHub(t)
	head, tree := f.git.seed(map[string]string{"a": "a"})
	tagSHA := gitHash("tag", []byte("object "+head))
	f.git.tags[tagSHA] = githubObject{SHA: head, Type: "commit"}
	f.git.refs["refs/tags/v1"] = githubObject{SHA: tagSHA, Type: "tag"}
	treeTag := gitHash("tag", []byte("object "+tree))
	f.git.tags[treeTag] = githubObject{SHA: tree, Type: "tree"}
	f.git.refs["refs/tags/tree"] = githubObject{SHA: treeTag, Type: "tag"}

	if got, err := g.Ref(context.Background(), "tags/v1"); err != nil || got != head {
		t.Errorf("Ref() of an annotated tag = %q, %v; want its commit %s", got, err, head)
	}
	if got, err := g.Ref(context.Background(), "tags/tree"); err == nil {
		t.Errorf("Ref() of a tag of a tree = %q; want an error", got)
	}
}

// TestGitHubSourceInputs refuses what can't be a ref or an object before any
// request is made.
func TestGitHubSourceInputs(t *testing.T) {
	f, g := newFakeGitHub(t)
	f.git.seed(map[string]string{"a": "a"})
	ctx := context.Background()
	for _, ref := range []string{"main", "heads/", "heads/../main", "notes/x", "heads//main", ""} {
		if _, err := g.Ref(ctx, ref); err == nil {
			t.Errorf("Ref(%q) error = nil, want one", ref)
		}
		if err := g.UpdateRef(ctx, ref, strings.Repeat("a", 40)); err == nil {
			t.Errorf("UpdateRef(%q) error = nil, want one", ref)
		}
		if err := g.CreateRef(ctx, ref, strings.Repeat("a", 40)); err == nil {
			t.Errorf("CreateRef(%q) error = nil, want one", ref)
		}
	}
	for _, sha := range []string{"", "HEAD", "../contents", strings.Repeat("A", 40), strings.Repeat("a", 39)} {
		if _, err := g.Commit(ctx, sha); err == nil {
			t.Errorf("Commit(%q) error = nil, want one", sha)
		}
		if _, err := g.Tree(ctx, sha, true); err == nil {
			t.Errorf("Tree(%q) error = nil, want one", sha)
		}
	}
	if len(f.paths) != 0 {
		t.Errorf("requests = %v, want none", f.paths)
	}
}

// failingReader fails the test if anything reads it.
type failingReader struct{ t *testing.T }

func (r failingReader) Read([]byte) (int, error) {
	r.t.Error("a blob over the limit was read")
	return 0, io.EOF
}

// TestGitHubSourceBlobs covers a blob's size: over the limit fails with
// SourceTooLarge before any request, an unknown one is refused, a file
// shorter than its size fails, and an empty one is a blob.
func TestGitHubSourceBlobs(t *testing.T) {
	f, g := newFakeGitHub(t)
	f.git.seed(map[string]string{"a": "a"})
	ctx := context.Background()
	if _, err := g.CreateBlob(ctx, MaxBlobSize+1, failingReader{t}); ReleaseReason(err) != ReasonSourceTooLarge {
		t.Errorf("CreateBlob() over the limit error = %v; want reason %s", err, ReasonSourceTooLarge)
	}
	if _, err := g.CreateBlob(ctx, -1, strings.NewReader("abc")); err == nil {
		t.Error("CreateBlob() of an unknown size error = nil, want one")
	}
	if len(f.paths) != 0 {
		t.Errorf("requests = %v, want none", f.paths)
	}
	if _, err := g.CreateBlob(ctx, 10, io.MultiReader(strings.NewReader("abc"))); ReleaseReason(err) != ReasonGitHubError || err == nil {
		t.Errorf("CreateBlob() of fewer bytes than its size error = %v; want reason %s", err, ReasonGitHubError)
	}
	// More bytes than the size: only size of them are sent.
	if sha, err := g.CreateBlob(ctx, 3, strings.NewReader("abcdef")); err != nil || sha != gitHash("blob", []byte("abc")) {
		t.Errorf("CreateBlob() of more bytes than its size = %s, %v; want abc's blob", sha, err)
	}
	if sha, err := g.CreateBlob(ctx, 0, strings.NewReader("")); err != nil || sha != gitHash("blob", nil) {
		t.Errorf("CreateBlob() of an empty file = %s, %v; want git's empty blob", sha, err)
	}
	// A body big enough to stream past the pipe's buffers, every byte value.
	data := bytes.Repeat([]byte{0, 1, 2, 254, 255, '"', '\\'}, 200_000)
	if sha, err := g.CreateBlob(ctx, int64(len(data)), bytes.NewReader(data)); err != nil || sha != gitHash("blob", data) {
		t.Errorf("CreateBlob() of %d bytes = %s, %v; want git's hash", len(data), sha, err)
	}
}

// TestGitHubSourceFailures maps GitHub's refusals of the Git Data calls to the
// plan's reasons: a protected branch, a blob GitHub finds too large, an empty
// repository, and those every call shares.
func TestGitHubSourceFailures(t *testing.T) {
	sha := strings.Repeat("a", 40)
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"bad token", 401, `{"message":"Bad credentials"}`, ReasonUnauthorized},
		{"no permission", 403, `{"message":"Resource not accessible by personal access token"}`, ReasonUnauthorized},
		{"no repository", 404, `{"message":"Not Found"}`, ReasonRepositoryNotFound},
		{"empty repository", 409, `{"message":"Git Repository is empty."}`, ReasonEmptyRepository},
		{"empty repository 422", 422, `{"message":"Repository is empty."}`, ReasonEmptyRepository},
		{"protected branch", 422, `{"message":"Protected branch update failed for refs/heads/main."}`, ReasonBranchProtected},
		{"ruleset", 409, `{"message":"Repository rule violations found\n\nCannot update this protected ref."}`, ReasonBranchProtected},
		{"too large", 413, `{"message":"Payload Too Large"}`, ReasonSourceTooLarge},
		{"server error", 502, "<html>Bad Gateway</html>", ReasonGitHubError},
		{"other validation", 422, `{"message":"Validation Failed"}`, ReasonGitHubError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, g := newFakeGitHub(t)
			f.fail = func(w http.ResponseWriter) { f.reply(w, tt.status, tt.body) }
			ctx := context.Background()
			calls := map[string]func() error{
				"Repository":    func() error { _, err := g.Repository(ctx); return err },
				"DefaultBranch": func() error { _, err := g.DefaultBranch(ctx); return err },
				"Ref":           func() error { _, err := g.Ref(ctx, "heads/main"); return err },
				"Commit":        func() error { _, err := g.Commit(ctx, sha); return err },
				"Tree":          func() error { _, err := g.Tree(ctx, sha, true); return err },
				"CreateBlob":    func() error { _, err := g.CreateBlob(ctx, 2, strings.NewReader("ab")); return err },
				"CreateTree": func() error {
					_, err := g.CreateTree(ctx, sha, []GitTreeEntry{{Path: "a", Mode: "100644", Type: "blob", SHA: sha}})
					return err
				},
				"CreateCommit": func() error { _, err := g.CreateCommit(ctx, "m", sha, nil); return err },
				"CreateRef":    func() error { return g.CreateRef(ctx, "tags/v1", sha) },
				"UpdateRef":    func() error { return g.UpdateRef(ctx, "heads/main", sha) },
			}
			for name, call := range calls {
				err := call()
				if name == "Ref" && tt.status == http.StatusNotFound {
					// No such ref; DefaultBranch tells a gone repository apart.
					if err != nil {
						t.Errorf("Ref() error = %v; want none", err)
					}
					continue
				}
				if ReleaseReason(err) != tt.want || err == nil {
					t.Errorf("%s() error = %v; want reason %s", name, err, tt.want)
				}
				if errors.Is(err, ErrNotFastForward) || errors.Is(err, ErrRefExists) {
					t.Errorf("%s() error = %v; a refusal is not a race", name, err)
				}
				if err != nil && (strings.Contains(err.Error(), f.token) || utf8.RuneCountInString(err.Error()) > 400) {
					t.Errorf("%s() error %q holds the token or is too long", name, err)
				}
			}
		})
	}
}
