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

package github

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

	"github.com/JairJosafath/theos/internal/ports"
)

// gitPath is /repos/{owner}/{repo}/git/ and its rest.
func (g *githubReleases) gitPath(rest string) string {
	return g.repo + "/git/" + rest
}

// refPath escapes each segment of ref, which holds slashes of its own.
func refPath(ref string) (string, error) {
	ref = strings.TrimPrefix(ref, "refs/")
	segments := strings.Split(ref, "/")
	if len(segments) < 2 || segments[0] != "heads" && segments[0] != "tags" {
		return "", &ports.ReleaseError{Reason: ports.ReasonGitHubError, Message: fmt.Sprintf("github: %q is not heads/... or tags/...", ref)}
	}
	for i, s := range segments {
		if s == "" || s == "." || s == ".." {
			return "", &ports.ReleaseError{Reason: ports.ReasonGitHubError, Message: fmt.Sprintf("github: invalid ref %q", ref)}
		}
		segments[i] = url.PathEscape(s)
	}
	return strings.Join(segments, "/"), nil
}

// objectPath checks sha is a git object name before it goes in a path.
func objectPath(sha string) (string, error) {
	if len(sha) != 40 || strings.Trim(sha, "0123456789abcdef") != "" {
		return "", &ports.ReleaseError{Reason: ports.ReasonGitHubError, Message: fmt.Sprintf("github: %q is not a SHA", sha)}
	}
	return sha, nil
}

func (g *githubReleases) Repository(ctx context.Context) (ports.TargetRepository, error) {
	var r struct {
		ID            int64  `json:"id"`
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
		Private       bool   `json:"private"`
	}
	if err := g.call(ctx, http.MethodGet, g.api, g.repo, nil, http.StatusOK, "reading the repository", &r); err != nil {
		return ports.TargetRepository{}, err
	}
	return ports.TargetRepository{ID: r.ID, FullName: r.FullName, DefaultBranch: r.DefaultBranch, Private: r.Private}, nil
}

func (g *githubReleases) DefaultBranch(ctx context.Context) (string, error) {
	r, err := g.Repository(ctx)
	if err != nil {
		return "", err
	}
	if r.DefaultBranch == "" {
		return "", &ports.ReleaseError{Reason: ports.ReasonEmptyRepository, Message: "github: the repository has no default branch"}
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
	if re, ok := errors.AsType[*ports.ReleaseError](err); ok && re.Status == http.StatusNotFound {
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
		return "", &ports.ReleaseError{Reason: ports.ReasonGitHubError,
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

func (g *githubReleases) Commit(ctx context.Context, sha string) (ports.GitCommit, error) {
	path, err := objectPath(sha)
	if err != nil {
		return ports.GitCommit{}, err
	}
	var c githubCommit
	if err := g.call(ctx, http.MethodGet, g.api, g.gitPath("commits/"+path), nil, http.StatusOK,
		"reading commit "+sha, &c); err != nil {
		return ports.GitCommit{}, err
	}
	out := ports.GitCommit{SHA: c.SHA, Tree: c.Tree.SHA, Message: c.Message}
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

func (g *githubReleases) Tree(ctx context.Context, sha string, recursive bool) (ports.GitTree, error) {
	path, err := objectPath(sha)
	if err != nil {
		return ports.GitTree{}, err
	}
	path = g.gitPath("trees/" + path)
	if recursive {
		path += "?recursive=1"
	}
	var t githubTree
	if err := g.call(ctx, http.MethodGet, g.api, path, nil, http.StatusOK, "reading tree "+sha, &t); err != nil {
		return ports.GitTree{}, err
	}
	out := ports.GitTree{SHA: t.SHA, Truncated: t.Truncated, Entries: make([]ports.GitTreeEntry, 0, len(t.Tree))}
	for _, e := range t.Tree {
		entry := ports.GitTreeEntry{Path: e.Path, Mode: e.Mode, Type: e.Type}
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
		return "", &ports.ReleaseError{Reason: ports.ReasonGitHubError, Message: "github: creating a blob: its size is unknown"}
	case size > ports.MaxBlobSize:
		return "", &ports.ReleaseError{Reason: ports.ReasonSourceTooLarge,
			Message: fmt.Sprintf("github: a file of %d bytes is over GitHub's %d MiB limit for one", size, ports.MaxBlobSize>>20)}
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

func (g *githubReleases) CreateTree(ctx context.Context, base string, entries []ports.GitTreeEntry) (string, error) {
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
	if re, ok := errors.AsType[*ports.ReleaseError](err); ok && re.Status == http.StatusUnprocessableEntity &&
		strings.Contains(strings.ToLower(re.Message), "already exists") {
		return ports.ErrRefExists
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
	if re, ok := errors.AsType[*ports.ReleaseError](err); ok && re.Reason == ports.ReasonGitHubError &&
		re.Status == http.StatusUnprocessableEntity && strings.Contains(strings.ToLower(re.Message), "fast forward") {
		return ports.ErrNotFastForward
	}
	return err
}
