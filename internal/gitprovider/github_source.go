package gitprovider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// MaxBlobSize is GitHub's limit for one blob: a file over it can't be published.
const MaxBlobSize = 100 << 20

// ErrNotFastForward is a branch update GitHub refused because the branch moved
// on: another commit landed first. It is no failure; the next try builds on
// the new head.
var ErrNotFastForward = errors.New("github: the update is not a fast-forward")

// ErrRefExists is a ref created after someone else created it first.
var ErrRefExists = errors.New("github: the ref already exists")

// GitTreeEntry is one entry of a git tree on GitHub.
type GitTreeEntry struct {
	// Path is the entry's name; in a recursive listing, or when creating a
	// tree, its path from the tree's root.
	Path string
	// Mode is git's: 100644, 100755, 120000 (a symlink), 040000 (a tree) or
	// 160000 (a submodule).
	Mode string
	// Type is blob, tree or commit.
	Type string
	// SHA is the object's; "" when creating a tree removes the path.
	SHA string
}

// GitTree is a git tree on GitHub.
type GitTree struct {
	SHA     string
	Entries []GitTreeEntry
	// Truncated is set when a recursive listing was too big for GitHub to send
	// whole.
	Truncated bool
}

// GitCommit is a git commit on GitHub.
type GitCommit struct {
	SHA     string
	Tree    string
	Parents []string
	Message string
}

// SourceTarget is a repository a release's source is written to, through
// GitHub's Git Data API: it reads and creates git objects and refs, and only
// ever moves a branch by a fast-forward.
type SourceTarget interface {
	// DefaultBranch returns the repository's default branch's name.
	DefaultBranch(ctx context.Context) (string, error)
	// Ref returns the commit ref names (heads/main, tags/engine/v0.6.1), an
	// annotated tag peeled; "" when there is no such ref. A repository that
	// is gone reads as no ref: DefaultBranch tells the two apart.
	Ref(ctx context.Context, ref string) (string, error)
	// Commit reads commit sha.
	Commit(ctx context.Context, sha string) (GitCommit, error)
	// Tree reads tree sha, with every path below it when recursive.
	Tree(ctx context.Context, sha string, recursive bool) (GitTree, error)
	// CreateBlob streams size bytes of content into a blob and returns its
	// SHA. A size over MaxBlobSize fails with ReasonSourceTooLarge before any
	// request. The caller closes content.
	CreateBlob(ctx context.Context, size int64, content io.Reader) (string, error)
	// CreateTree creates a tree of entries on base (none when ""): base's
	// paths stay unless an entry replaces or, with no SHA, removes them.
	CreateTree(ctx context.Context, base string, entries []GitTreeEntry) (string, error)
	// CreateCommit creates a commit of tree on parents. It moves no branch.
	CreateCommit(ctx context.Context, message, tree string, parents []string) (string, error)
	// CreateRef creates ref (heads/..., tags/...) at sha; ErrRefExists when it
	// is there already.
	CreateRef(ctx context.Context, ref, sha string) error
	// UpdateRef moves ref to sha, never by force: ErrNotFastForward when sha
	// doesn't descend from where ref is, ReasonBranchProtected when GitHub's
	// protection or rules refuse it.
	UpdateRef(ctx context.Context, ref, sha string) error
}

// gitPath is /repos/{owner}/{repo}/git/ and its rest.
func (g *githubReleases) gitPath(rest string) string {
	return g.repo + "/git/" + rest
}

// refPath escapes each segment of ref, which holds slashes of its own.
func refPath(ref string) (string, error) {
	ref = strings.TrimPrefix(ref, "refs/")
	segments := strings.Split(ref, "/")
	if len(segments) < 2 || segments[0] != "heads" && segments[0] != "tags" {
		return "", &ReleaseError{Reason: ReasonGitHubError, Message: fmt.Sprintf("github: %q is not heads/... or tags/...", ref)}
	}
	for i, s := range segments {
		if s == "" || s == "." || s == ".." {
			return "", &ReleaseError{Reason: ReasonGitHubError, Message: fmt.Sprintf("github: invalid ref %q", ref)}
		}
		segments[i] = url.PathEscape(s)
	}
	return strings.Join(segments, "/"), nil
}

// objectPath checks sha is a git object name before it goes in a path.
func objectPath(sha string) (string, error) {
	if len(sha) != 40 || strings.Trim(sha, "0123456789abcdef") != "" {
		return "", &ReleaseError{Reason: ReasonGitHubError, Message: fmt.Sprintf("github: %q is not a SHA", sha)}
	}
	return sha, nil
}

func (g *githubReleases) DefaultBranch(ctx context.Context) (string, error) {
	var r struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := g.call(ctx, http.MethodGet, g.api, g.repo, nil, http.StatusOK, "reading the repository", &r); err != nil {
		return "", err
	}
	if r.DefaultBranch == "" {
		return "", &ReleaseError{Reason: ReasonEmptyRepository, Message: "github: the repository has no default branch"}
	}
	return r.DefaultBranch, nil
}

type githubObject struct {
	SHA  string `json:"sha"`
	Type string `json:"type"`
}

// maxTagDepth bounds the annotated tags peeled to reach a commit.
const maxTagDepth = 5

func (g *githubReleases) Ref(ctx context.Context, ref string) (string, error) {
	path, err := refPath(ref)
	if err != nil {
		return "", err
	}
	var r struct {
		Object githubObject `json:"object"`
	}
	err = g.call(ctx, http.MethodGet, g.api, g.gitPath("ref/"+path), nil, http.StatusOK, "reading ref "+ref, &r)
	if re, ok := errors.AsType[*ReleaseError](err); ok && re.Status == http.StatusNotFound {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	obj := r.Object
	for range maxTagDepth {
		if obj.Type != "tag" {
			break
		}
		sha, err := objectPath(obj.SHA)
		if err != nil {
			return "", err
		}
		var tag struct {
			Object githubObject `json:"object"`
		}
		if err := g.call(ctx, http.MethodGet, g.api, g.gitPath("tags/"+sha), nil, http.StatusOK,
			"reading tag "+ref, &tag); err != nil {
			return "", err
		}
		obj = tag.Object
	}
	if obj.Type != "commit" {
		return "", &ReleaseError{Reason: ReasonGitHubError,
			Message: fmt.Sprintf("github: ref %s names a %s, not a commit", ref, obj.Type)}
	}
	return obj.SHA, nil
}

type githubCommit struct {
	SHA     string `json:"sha"`
	Message string `json:"message"`
	Tree    struct {
		SHA string `json:"sha"`
	} `json:"tree"`
	Parents []struct {
		SHA string `json:"sha"`
	} `json:"parents"`
}

func (g *githubReleases) Commit(ctx context.Context, sha string) (GitCommit, error) {
	path, err := objectPath(sha)
	if err != nil {
		return GitCommit{}, err
	}
	var c githubCommit
	if err := g.call(ctx, http.MethodGet, g.api, g.gitPath("commits/"+path), nil, http.StatusOK,
		"reading commit "+sha, &c); err != nil {
		return GitCommit{}, err
	}
	out := GitCommit{SHA: c.SHA, Tree: c.Tree.SHA, Message: c.Message}
	for _, p := range c.Parents {
		out.Parents = append(out.Parents, p.SHA)
	}
	return out, nil
}

type githubTreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	// SHA is null to remove the path.
	SHA *string `json:"sha"`
}

type githubTree struct {
	SHA       string            `json:"sha"`
	Tree      []githubTreeEntry `json:"tree"`
	Truncated bool              `json:"truncated"`
}

func (g *githubReleases) Tree(ctx context.Context, sha string, recursive bool) (GitTree, error) {
	path, err := objectPath(sha)
	if err != nil {
		return GitTree{}, err
	}
	path = g.gitPath("trees/" + path)
	if recursive {
		path += "?recursive=1"
	}
	var t githubTree
	if err := g.call(ctx, http.MethodGet, g.api, path, nil, http.StatusOK, "reading tree "+sha, &t); err != nil {
		return GitTree{}, err
	}
	out := GitTree{SHA: t.SHA, Truncated: t.Truncated, Entries: make([]GitTreeEntry, 0, len(t.Tree))}
	for _, e := range t.Tree {
		entry := GitTreeEntry{Path: e.Path, Mode: e.Mode, Type: e.Type}
		if e.SHA != nil {
			entry.SHA = *e.SHA
		}
		out.Entries = append(out.Entries, entry)
	}
	return out, nil
}

// The JSON a blob is sent as, around its base64 content.
const (
	blobHead = `{"encoding":"base64","content":"`
	blobTail = `"}`
)

// CreateBlob writes the JSON body as it reads content, base64 on the way, with
// its Content-Length known ahead: the file is never held whole.
func (g *githubReleases) CreateBlob(ctx context.Context, size int64, content io.Reader) (string, error) {
	switch {
	case size < 0:
		return "", &ReleaseError{Reason: ReasonGitHubError, Message: "github: creating a blob: its size is unknown"}
	case size > MaxBlobSize:
		return "", &ReleaseError{Reason: ReasonSourceTooLarge,
			Message: fmt.Sprintf("github: a file of %d bytes is over GitHub's %d MiB limit for one", size, MaxBlobSize>>20)}
	}
	pr, pw := io.Pipe()
	go func() {
		_, _ = io.WriteString(pw, blobHead)
		enc := base64.NewEncoder(base64.StdEncoding, pw)
		n, err := io.Copy(enc, io.LimitReader(content, size))
		if err == nil && n < size {
			err = fmt.Errorf("the file ended after %d of %d bytes", n, size)
		}
		if err == nil {
			err = enc.Close()
		}
		if err == nil {
			_, err = io.WriteString(pw, blobTail)
		}
		pw.CloseWithError(err)
	}()
	req, err := g.request(ctx, http.MethodPost, g.api, g.gitPath("blobs"), pr)
	if err != nil {
		_ = pr.Close()
		return "", err
	}
	req.ContentLength = int64(len(blobHead)) + int64(base64.StdEncoding.EncodedLen(int(size))) + int64(len(blobTail))
	req.Header.Set("Content-Type", "application/json")
	var b githubObject
	err = g.send(req, http.StatusCreated, fmt.Sprintf("creating a blob of %d bytes", size), &b)
	// The request may end before the body was read, a refusal say; the
	// goroutine then stops at its next write.
	_ = pr.Close()
	return b.SHA, err
}

func (g *githubReleases) CreateTree(ctx context.Context, base string, entries []GitTreeEntry) (string, error) {
	tree := make([]githubTreeEntry, 0, len(entries))
	for _, e := range entries {
		entry := githubTreeEntry{Path: e.Path, Mode: e.Mode, Type: e.Type}
		if e.SHA != "" {
			entry.SHA = &e.SHA
		}
		tree = append(tree, entry)
	}
	in := struct {
		BaseTree string            `json:"base_tree,omitempty"`
		Tree     []githubTreeEntry `json:"tree"`
	}{base, tree}
	payload, _ := json.Marshal(in)
	var t githubObject
	err := g.call(ctx, http.MethodPost, g.api, g.gitPath("trees"), payload, http.StatusCreated,
		fmt.Sprintf("creating a tree of %d entries", len(entries)), &t)
	return t.SHA, err
}

func (g *githubReleases) CreateCommit(ctx context.Context, message, tree string, parents []string) (string, error) {
	if parents == nil {
		parents = []string{}
	}
	payload, _ := json.Marshal(map[string]any{"message": message, "tree": tree, "parents": parents})
	var c githubObject
	err := g.call(ctx, http.MethodPost, g.api, g.gitPath("commits"), payload, http.StatusCreated, "creating a commit", &c)
	return c.SHA, err
}

func (g *githubReleases) CreateRef(ctx context.Context, ref, sha string) error {
	if _, err := refPath(ref); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"ref": "refs/" + strings.TrimPrefix(ref, "refs/"), "sha": sha})
	err := g.call(ctx, http.MethodPost, g.api, g.gitPath("refs"), payload, http.StatusCreated, "creating ref "+ref, nil)
	if re, ok := errors.AsType[*ReleaseError](err); ok && re.Status == http.StatusUnprocessableEntity &&
		strings.Contains(strings.ToLower(re.Message), "already exists") {
		return ErrRefExists
	}
	return err
}

func (g *githubReleases) UpdateRef(ctx context.Context, ref, sha string) error {
	path, err := refPath(ref)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"sha": sha, "force": false})
	err = g.call(ctx, http.MethodPatch, g.api, g.gitPath("refs/"+path), payload, http.StatusOK, "moving ref "+ref, nil)
	if re, ok := errors.AsType[*ReleaseError](err); ok && re.Reason == ReasonGitHubError &&
		re.Status == http.StatusUnprocessableEntity && strings.Contains(strings.ToLower(re.Message), "fast forward") {
		return ErrNotFastForward
	}
	return err
}
