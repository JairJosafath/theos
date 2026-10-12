/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/mod/modfile"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// Events of publishing a release's source.
const (
	reasonSourcePublished        = "SourcePublished"
	reasonSourceNotGoInstallable = "SourceNotGoInstallable"
)

// maxGoMod bounds the go.mod read for its module path.
const maxGoMod = 1 << 20

// publishSource is the folder the Crew publishes its releases' source to, or
// nil when it publishes none.
func publishSource(crew *c4v1alpha1.Crew) *model.PublishSource {
	spec := crew.Spec.PublishReleases
	if spec == nil || spec.Source == nil {
		return nil
	}
	return &model.PublishSource{Path: spec.Source.Path, TagPrefix: spec.Source.TagPrefix}
}

// sourceOverlaps names the other Crews, in any namespace, publishing source to
// the Crew's repository in a folder overlapping its own.
func (r *CrewReconciler) sourceOverlaps(ctx context.Context, crew *c4v1alpha1.Crew) ([]string, error) {
	var crews c4v1alpha1.CrewList
	if err := r.List(ctx, &crews); err != nil {
		return nil, err
	}
	var places []model.SourcePlace
	for _, c := range crews.Items {
		if p := c.Spec.PublishReleases; p != nil && p.Source != nil {
			places = append(places, model.SourcePlace{Crew: c.Namespace + "/" + c.Name,
				Repository: p.GitHub.Repository, Path: p.Source.Path})
		}
	}
	spec := crew.Spec.PublishReleases
	return model.SourceOverlaps(model.SourcePlace{Crew: crew.Namespace + "/" + crew.Name,
		Repository: spec.GitHub.Repository, Path: spec.Source.Path}, places), nil
}

// readSource reads the Container's files at tag from Gitea and hashes them
// into the release's source, the factory's own left out; it reads the module
// path from the root go.mod.
func readSource(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository,
	tag string) (*model.ReleaseSource, error) {
	var files []model.SourceFile
	module := ""
	err := provider.ReadSource(ctx, repo, tag, func(f gitprovider.SourceFile, content io.Reader) error {
		file := model.SourceFile{Path: f.Path, Mode: model.FileMode(f.Mode), Size: f.Size}
		var err error
		if f.Path == "go.mod" && f.Mode != gitprovider.SourceSymlink && f.Size <= maxGoMod {
			var data []byte
			if data, err = io.ReadAll(content); err != nil {
				return err
			}
			module = modfile.ModulePath(data)
			file.Blob = model.BlobSHA(data)
		} else if file.Blob, err = model.HashBlob(content, f.Size); err != nil {
			return fmt.Errorf("reading %s: %w", f.Path, err)
		}
		files = append(files, file)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return model.NewSource(files, module)
}

// sourceRead is a release's source as read from Gitea at its tag.
type sourceRead struct {
	tag    string
	source *model.ReleaseSource
}

// githubSource is what the operator read of the repository for a release's
// source: the facts, and the branch and head tree a source commit builds on.
type githubSource struct {
	facts    *model.GitHubSource
	branch   string
	headRoot string
}

// readGitHubSource reads the default branch's head and the folder's tree at
// it, with the blobs in it, and the commit the source tag names with the
// folder's tree there.
func readGitHubSource(ctx context.Context, target gitprovider.SourceTarget, path,
	sourceTag string) (*githubSource, error) {
	branch, err := target.DefaultBranch(ctx)
	if err != nil {
		return nil, err
	}
	out := &githubSource{facts: &model.GitHubSource{}, branch: branch}
	if out.facts.Head, err = target.Ref(ctx, "heads/"+branch); err != nil {
		return nil, err
	}
	if out.facts.Head != "" {
		head, err := target.Commit(ctx, out.facts.Head)
		if err != nil {
			return nil, err
		}
		out.headRoot = head.Tree
		if out.facts.HeadTree, err = folderTree(ctx, target, head.Tree, path); err != nil {
			return nil, err
		}
		if out.facts.HeadTree != "" {
			// Only to skip uploads: a listing cut short uploads a few blobs again.
			tree, err := target.Tree(ctx, out.facts.HeadTree, true)
			if err != nil {
				return nil, err
			}
			for _, e := range tree.Entries {
				if e.Type == "blob" {
					out.facts.HeadBlobs = append(out.facts.HeadBlobs, e.SHA)
				}
			}
		}
	}
	if out.facts.TagCommit, err = target.Ref(ctx, "tags/"+sourceTag); err != nil || out.facts.TagCommit == "" {
		return out, err
	}
	tagged, err := target.Commit(ctx, out.facts.TagCommit)
	if err != nil {
		return nil, err
	}
	out.facts.TagTree, err = folderTree(ctx, target, tagged.Tree, path)
	return out, err
}

// folderTree is the tree of path under root, one folder at a time, or "" when
// there is no folder there.
func folderTree(ctx context.Context, target gitprovider.SourceTarget, root, path string) (string, error) {
	if path == "." {
		return root, nil
	}
	sha := root
	for name := range strings.SplitSeq(path, "/") {
		tree, err := target.Tree(ctx, sha, false)
		if err != nil {
			return "", err
		}
		sha = ""
		for _, e := range tree.Entries {
			if e.Path == name && e.Type == "tree" {
				sha = e.SHA
			}
		}
		if sha == "" {
			return "", nil
		}
	}
	return sha, nil
}

// commitSource uploads the blobs GitHub lacks, streamed from Gitea's archive
// again, builds the folder's tree and the root tree, the head's with only the
// folder replaced, commits it on the head and fast-forwards the default
// branch. It returns Gitea's failure apart from GitHub's.
func commitSource(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository, tag string,
	target gitprovider.SourceTarget, gh *githubSource, c *model.SourceCommit) (commit string, giteaErr, err error) {
	if giteaErr, err = uploadBlobs(ctx, provider, repo, tag, target, c.Upload); giteaErr != nil || err != nil {
		return "", giteaErr, err
	}
	entries := make([]gitprovider.GitTreeEntry, 0, len(c.Files))
	for _, f := range c.Files {
		entries = append(entries, gitprovider.GitTreeEntry{Path: f.Path, Mode: string(f.Mode), Type: "blob", SHA: f.Blob})
	}
	folder, err := target.CreateTree(ctx, "", entries)
	if err != nil {
		return "", nil, err
	}
	if folder != c.Tree {
		return "", nil, fmt.Errorf("github made the source's tree %s, not %s", folder, c.Tree)
	}
	root := folder
	if c.Path != "." {
		if root, err = target.CreateTree(ctx, gh.headRoot, []gitprovider.GitTreeEntry{
			{Path: c.Path, Mode: "040000", Type: "tree", SHA: folder}}); err != nil {
			return "", nil, err
		}
	}
	if commit, err = target.CreateCommit(ctx, c.Message, root, []string{c.Parent}); err != nil {
		return "", nil, err
	}
	return commit, nil, target.UpdateRef(ctx, "heads/"+gh.branch, commit)
}

// uploadBlobs streams each file of upload from Gitea's archive into a blob.
func uploadBlobs(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository, tag string,
	target gitprovider.SourceTarget, upload []model.SourceFile) (giteaErr, err error) {
	if len(upload) == 0 {
		return nil, nil
	}
	want := map[string]model.SourceFile{}
	for _, f := range upload {
		want[f.Path] = f
	}
	var githubErr error
	giteaErr = provider.ReadSource(ctx, repo, tag, func(f gitprovider.SourceFile, content io.Reader) error {
		file, ok := want[f.Path]
		if !ok {
			return nil
		}
		delete(want, f.Path)
		sha, err := target.CreateBlob(ctx, f.Size, content)
		switch {
		case err != nil:
			githubErr = err
		case sha != file.Blob:
			githubErr = fmt.Errorf("github stored %s as blob %s, not %s", f.Path, sha, file.Blob)
		}
		return githubErr
	})
	switch {
	case githubErr != nil:
		return nil, githubErr
	case giteaErr != nil:
		return giteaErr, nil
	case len(want) > 0:
		return fmt.Errorf("the source of %s changed while it was published: %d files are gone", tag, len(want)), nil
	}
	return nil, nil
}

// publishFailure is a failed GitHub call as the rule reads it: a branch that
// moved on is no failure, the next poll builds on it.
func publishFailure(err error) *model.PublishFailure {
	if errors.Is(err, gitprovider.ErrNotFastForward) {
		return &model.PublishFailure{Reason: model.PublishBranchMoved, Detail: err.Error()}
	}
	return &model.PublishFailure{Reason: gitprovider.ReleaseReason(err), Detail: err.Error()}
}
