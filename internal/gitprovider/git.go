package gitprovider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/JairJosafath/c4factory/internal/scaffold"
)

const (
	defaultBranch = "main"
	remoteName    = "origin"
)

var commitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

func auth(creds *Credentials) transport.AuthMethod {
	if creds == nil || creds.Token == "" {
		return nil
	}
	username := creds.Username
	if username == "" {
		username = "git"
	}
	return &githttp.BasicAuth{Username: username, Password: creds.Token}
}

// FetchTemplate returns the files of a template repository at a pinned ref: a
// tag, or a full commit SHA. A branch name fails with ErrUnpinnedRef.
func (c Client) FetchTemplate(ctx context.Context, repoURL, ref string, creds *Credentials) (scaffold.Tree, error) {
	remote := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{Name: remoteName, URLs: []string{repoURL}})
	refs, err := remote.ListContext(ctx, &git.ListOptions{Auth: auth(creds)})
	if err != nil {
		return nil, fmt.Errorf("list refs of %s: %w", repoURL, err)
	}
	has := func(name plumbing.ReferenceName) bool {
		return slices.ContainsFunc(refs, func(r *plumbing.Reference) bool { return r.Name() == name })
	}

	opts := &git.CloneOptions{URL: repoURL, Auth: auth(creds), Tags: git.NoTags}
	var checkout plumbing.Hash
	switch {
	case has(plumbing.NewTagReferenceName(ref)):
		opts.ReferenceName = plumbing.NewTagReferenceName(ref)
		opts.SingleBranch = true
		opts.Depth = 1
	case commitSHA.MatchString(ref):
		// The commit may be reachable only from a tag.
		opts.Tags = git.AllTags
		opts.NoCheckout = true
		checkout = plumbing.NewHash(ref)
	case has(plumbing.NewBranchReferenceName(ref)):
		return nil, fmt.Errorf("%w: %s is a branch in %s; pin a tag or a full commit SHA", ErrUnpinnedRef, ref, repoURL)
	default:
		return nil, fmt.Errorf("%w: %s not found in %s", ErrUnpinnedRef, ref, repoURL)
	}

	worktree := memfs.New()
	repo, err := git.CloneContext(ctx, memory.NewStorage(), worktree, opts)
	if err != nil {
		return nil, fmt.Errorf("clone %s at %s: %w", repoURL, ref, err)
	}
	if !checkout.IsZero() {
		wt, err := repo.Worktree()
		if err != nil {
			return nil, err
		}
		if err := wt.Checkout(&git.CheckoutOptions{Hash: checkout}); err != nil {
			return nil, checkoutError(err, ref, repoURL)
		}
	}
	return readTree(worktree)
}

// checkoutError reports a missing commit as ErrUnpinnedRef, which is permanent,
// and wraps any other checkout error as it is, so the caller retries it.
func checkoutError(err error, ref, repoURL string) error {
	if errors.Is(err, plumbing.ErrObjectNotFound) {
		return fmt.Errorf("%w: commit %s not found in %s", ErrUnpinnedRef, ref, repoURL)
	}
	return fmt.Errorf("check out %s in %s: %w", ref, repoURL, err)
}

// readTree reads every file of a checked-out worktree, except .git.
func readTree(root billy.Filesystem) (scaffold.Tree, error) {
	tree := scaffold.Tree{}
	var walk func(dir string) error
	walk = func(dir string) error {
		entries, err := root.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			p := path.Join(dir, entry.Name())
			if p == ".git" {
				continue
			}
			if entry.IsDir() {
				if err := walk(p); err != nil {
					return err
				}
				continue
			}
			if !entry.Mode().IsRegular() {
				continue
			}
			f, err := root.Open(p)
			if err != nil {
				return err
			}
			data, err := io.ReadAll(f)
			_ = f.Close()
			if err != nil {
				return err
			}
			tree[p] = scaffold.File{Data: data, Executable: entry.Mode()&0o111 != 0}
		}
		return nil
	}
	if err := walk(""); err != nil {
		return nil, err
	}
	return tree, nil
}

// history returns up to limit commits on the default branch, newest first. When
// the default branch is missing it reads another branch, so history anywhere
// counts.
func history(ctx context.Context, repoURL string, creds *Credentials, limit int) ([]Commit, error) {
	remote := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{Name: remoteName, URLs: []string{repoURL}})
	refs, err := remote.ListContext(ctx, &git.ListOptions{Auth: auth(creds)})
	if errors.Is(err, transport.ErrEmptyRemoteRepository) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list refs of %s: %w", repoURL, err)
	}
	branch, ok := historyBranch(refs)
	if !ok {
		return nil, nil
	}

	repo, err := git.CloneContext(ctx, memory.NewStorage(), nil, &git.CloneOptions{
		URL:           repoURL,
		Auth:          auth(creds),
		ReferenceName: branch,
		SingleBranch:  true,
		Depth:         limit,
		NoCheckout:    true,
		Tags:          git.NoTags,
	})
	if err != nil {
		return nil, fmt.Errorf("clone %s: %w", repoURL, err)
	}
	head, err := repo.Head()
	if err != nil {
		return nil, err
	}
	iter, err := repo.Log(&git.LogOptions{From: head.Hash()})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	var commits []Commit
	for len(commits) < limit {
		c, err := iter.Next()
		if errors.Is(err, io.EOF) || errors.Is(err, plumbing.ErrObjectNotFound) {
			break
		}
		if err != nil {
			return nil, err
		}
		commits = append(commits, Commit{Hash: c.Hash.String(), Message: c.Message})
	}
	return commits, nil
}

// historyBranch picks the branch history reads: the default branch, or else the
// first other branch by name. It reports false when there are no branches.
func historyBranch(refs []*plumbing.Reference) (plumbing.ReferenceName, bool) {
	var branches []plumbing.ReferenceName
	for _, r := range refs {
		if r.Name().IsBranch() {
			branches = append(branches, r.Name())
		}
	}
	if len(branches) == 0 {
		return "", false
	}
	if main := plumbing.NewBranchReferenceName(defaultBranch); slices.Contains(branches, main) {
		return main, true
	}
	slices.Sort(branches)
	return branches[0], true
}

// pushInitialCommit commits files in a fresh repository and pushes the default
// branch without force, so it fails if the branch already exists.
func pushInitialCommit(ctx context.Context, repoURL string, creds *Credentials, files scaffold.Tree,
	message string, author Signature) (string, error) {
	worktree := memfs.New()
	repo, err := git.InitWithOptions(memory.NewStorage(), worktree, git.InitOptions{
		DefaultBranch: plumbing.NewBranchReferenceName(defaultBranch),
	})
	if err != nil {
		return "", err
	}
	for _, p := range files.Paths() {
		f := files[p]
		mode := fs.FileMode(0o644)
		if f.Executable {
			mode = 0o755
		}
		if err := worktree.MkdirAll(path.Dir(p), 0o755); err != nil {
			return "", err
		}
		out, err := worktree.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
		if err != nil {
			return "", err
		}
		if _, err := out.Write(f.Data); err != nil {
			_ = out.Close()
			return "", err
		}
		if err := out.Close(); err != nil {
			return "", err
		}
	}
	wt, err := repo.Worktree()
	if err != nil {
		return "", err
	}
	if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		return "", err
	}
	sig := &object.Signature{Name: author.Name, Email: author.Email, When: author.When}
	hash, err := wt.Commit(message, &git.CommitOptions{Author: sig, Committer: sig})
	if err != nil {
		return "", err
	}
	if _, err := repo.CreateRemote(&config.RemoteConfig{Name: remoteName, URLs: []string{repoURL}}); err != nil {
		return "", err
	}
	branch := plumbing.NewBranchReferenceName(defaultBranch)
	err = repo.PushContext(ctx, &git.PushOptions{
		RemoteName: remoteName,
		Auth:       auth(creds),
		RefSpecs:   []config.RefSpec{config.RefSpec(branch + ":" + branch)},
	})
	if err != nil {
		return "", fmt.Errorf("push %s: %w", repoURL, err)
	}
	return hash.String(), nil
}

// readFiles returns branch's copy of those of paths it has, and the commit it
// read them at. A path that is no regular file there, missing or a directory,
// is left out. A path ending in "/" reads every regular file below that
// directory, at any depth.
func readFiles(ctx context.Context, repoURL string, creds *Credentials, branch string,
	paths []string) (scaffold.Tree, string, error) {
	repo, err := git.CloneContext(ctx, memory.NewStorage(), nil, &git.CloneOptions{
		URL:           repoURL,
		Auth:          auth(creds),
		ReferenceName: plumbing.NewBranchReferenceName(branch),
		SingleBranch:  true,
		Depth:         1,
		NoCheckout:    true,
		Tags:          git.NoTags,
	})
	if err != nil {
		return nil, "", fmt.Errorf("clone %s at %s: %w", repoURL, branch, err)
	}
	head, err := repo.Head()
	if err != nil {
		return nil, "", err
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return nil, "", err
	}
	root, err := commit.Tree()
	if err != nil {
		return nil, "", err
	}
	files := scaffold.Tree{}
	for _, p := range paths {
		if dir, ok := strings.CutSuffix(p, "/"); ok {
			if err := treeDir(repo, root, dir, files); err != nil {
				return nil, "", fmt.Errorf("read %s at %s in %s: %w", p, branch, repoURL, err)
			}
			continue
		}
		f, ok, err := treeFile(repo, root, p)
		if err != nil {
			return nil, "", fmt.Errorf("read %s at %s in %s: %w", p, branch, repoURL, err)
		}
		if ok {
			files[p] = f
		}
	}
	return files, head.Hash().String(), nil
}

// treeDir adds every regular file below directory dir of tree to files, by its
// full path. A dir that is missing, or no directory, adds nothing.
func treeDir(repo *git.Repository, tree *object.Tree, dir string, files scaffold.Tree) error {
	for name := range strings.SplitSeq(dir, "/") {
		e, ok := treeEntry(tree, name)
		if !ok || e.Mode != filemode.Dir {
			return nil
		}
		var err error
		if tree, err = repo.TreeObject(e.Hash); err != nil {
			return err
		}
	}
	return tree.Files().ForEach(func(f *object.File) error {
		if !f.Mode.IsFile() || f.Mode == filemode.Symlink {
			return nil
		}
		data, err := f.Contents()
		if err != nil {
			return err
		}
		files[dir+"/"+f.Name] = scaffold.File{Data: []byte(data), Executable: f.Mode == filemode.Executable}
		return nil
	})
}

// treeFile reads the regular file at p in tree, executable bit included. It
// reports false when tree has none there: nothing, a directory, a symlink or a
// submodule.
func treeFile(repo *git.Repository, tree *object.Tree, p string) (scaffold.File, bool, error) {
	names := strings.Split(p, "/")
	for _, dir := range names[:len(names)-1] {
		e, ok := treeEntry(tree, dir)
		if !ok || e.Mode != filemode.Dir {
			return scaffold.File{}, false, nil
		}
		var err error
		if tree, err = repo.TreeObject(e.Hash); err != nil {
			return scaffold.File{}, false, err
		}
	}
	e, ok := treeEntry(tree, names[len(names)-1])
	if !ok || !e.Mode.IsFile() || e.Mode == filemode.Symlink {
		return scaffold.File{}, false, nil
	}
	blob, err := repo.BlobObject(e.Hash)
	if err != nil {
		return scaffold.File{}, false, err
	}
	r, err := blob.Reader()
	if err != nil {
		return scaffold.File{}, false, err
	}
	data, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		return scaffold.File{}, false, err
	}
	return scaffold.File{Data: data, Executable: e.Mode == filemode.Executable}, true, nil
}

// treeEntry returns tree's entry called name, if it has one.
func treeEntry(tree *object.Tree, name string) (object.TreeEntry, bool) {
	i := slices.IndexFunc(tree.Entries, func(e object.TreeEntry) bool { return e.Name == name })
	if i < 0 {
		return object.TreeEntry{}, false
	}
	return tree.Entries[i], true
}

// pushBranch commits files, and the removal of removes, on top of commit base of
// the default branch, and force-pushes the commit as branch. The commit's tree is
// base's with only those paths changed. A change that leaves base as it is fails
// with ErrEmptyChange and pushes nothing.
func pushBranch(ctx context.Context, repoURL string, creds *Credentials, base, branch string, files scaffold.Tree,
	removes []string, message string, author Signature) (string, error) {
	if branch == defaultBranch {
		return "", fmt.Errorf("won't force-push the default branch %s of %s", defaultBranch, repoURL)
	}
	return commitAndPush(ctx, repoURL, creds, base, branch, files, removes, message, author, true)
}

// pushDefaultBranch commits files, and the removal of removes, on top of commit
// base of the default branch, and pushes it to the default branch without
// force: it fails when the branch moved on past base. A change that leaves base
// as it is fails with ErrEmptyChange and pushes nothing.
func pushDefaultBranch(ctx context.Context, repoURL string, creds *Credentials, base string, files scaffold.Tree,
	removes []string, message string, author Signature) (string, error) {
	return commitAndPush(ctx, repoURL, creds, base, defaultBranch, files, removes, message, author, false)
}

// commitAndPush is pushBranch and pushDefaultBranch: one commit on base,
// pushed as branch, with force or without.
func commitAndPush(ctx context.Context, repoURL string, creds *Credentials, base, branch string, files scaffold.Tree,
	removes []string, message string, author Signature, force bool) (string, error) {
	if !commitSHA.MatchString(base) {
		return "", fmt.Errorf("base %q is not a full commit SHA", base)
	}
	// A path mapped to nil is removed; files win over removes.
	edits := map[string]*scaffold.File{}
	for _, p := range removes {
		edits[p] = nil
	}
	for p, f := range files {
		edits[p] = &f
	}
	for p := range edits {
		if !fs.ValidPath(p) || p == "." {
			return "", fmt.Errorf("invalid path %q", p)
		}
	}

	// Not shallow, so base is there even when the default branch moved on since.
	repo, err := git.CloneContext(ctx, memory.NewStorage(), nil, &git.CloneOptions{
		URL:           repoURL,
		Auth:          auth(creds),
		ReferenceName: plumbing.NewBranchReferenceName(defaultBranch),
		SingleBranch:  true,
		NoCheckout:    true,
		Tags:          git.NoTags,
	})
	if err != nil {
		return "", fmt.Errorf("clone %s: %w", repoURL, err)
	}
	parent, err := repo.CommitObject(plumbing.NewHash(base))
	if err != nil {
		return "", fmt.Errorf("find base %s on %s of %s: %w", base, defaultBranch, repoURL, err)
	}
	baseTree, err := parent.Tree()
	if err != nil {
		return "", err
	}
	tree, _, err := editTree(repo, baseTree, edits)
	if err != nil {
		return "", err
	}
	if tree == baseTree.Hash {
		return "", fmt.Errorf("%w: %s on %s of %s", ErrEmptyChange, branch, base, repoURL)
	}
	sig := object.Signature{Name: author.Name, Email: author.Email, When: author.When}
	hash, err := storeObject(repo, &object.Commit{
		Author:       sig,
		Committer:    sig,
		Message:      message,
		TreeHash:     tree,
		ParentHashes: []plumbing.Hash{parent.Hash},
	})
	if err != nil {
		return "", err
	}
	ref := plumbing.NewBranchReferenceName(branch)
	if err := repo.Storer.SetReference(plumbing.NewHashReference(ref, hash)); err != nil {
		return "", err
	}
	spec := string(ref) + ":" + string(ref)
	if force {
		spec = "+" + spec
	}
	err = repo.PushContext(ctx, &git.PushOptions{
		RemoteName: remoteName,
		Auth:       auth(creds),
		RefSpecs:   []config.RefSpec{config.RefSpec(spec)},
	})
	if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return "", fmt.Errorf("push %s to %s: %w", branch, repoURL, err)
	}
	return hash.String(), nil
}

// editTree stores tree with edits applied: a path mapped to a file is written,
// one mapped to nil removed. It returns the new tree's hash and whether it is
// empty. tree is nil for a directory that isn't there yet. Removing a path that
// is no file in tree changes nothing.
func editTree(repo *git.Repository, tree *object.Tree, edits map[string]*scaffold.File) (plumbing.Hash, bool, error) {
	entries := map[string]object.TreeEntry{}
	if tree != nil {
		for _, e := range tree.Entries {
			entries[e.Name] = e
		}
	}
	dirs := map[string]map[string]*scaffold.File{}
	for p, f := range edits {
		if dir, rest, nested := strings.Cut(p, "/"); nested {
			if dirs[dir] == nil {
				dirs[dir] = map[string]*scaffold.File{}
			}
			dirs[dir][rest] = f
			continue
		}
		if f == nil {
			if e, ok := entries[p]; ok && e.Mode != filemode.Dir {
				delete(entries, p)
			}
			continue
		}
		hash, err := storeBlob(repo, f.Data)
		if err != nil {
			return plumbing.ZeroHash, false, err
		}
		mode := filemode.Regular
		if f.Executable {
			mode = filemode.Executable
		}
		entries[p] = object.TreeEntry{Name: p, Mode: mode, Hash: hash}
	}
	for dir, inner := range dirs {
		var sub *object.Tree
		if e, ok := entries[dir]; ok && e.Mode == filemode.Dir {
			var err error
			if sub, err = repo.TreeObject(e.Hash); err != nil {
				return plumbing.ZeroHash, false, err
			}
		}
		hash, empty, err := editTree(repo, sub, inner)
		if err != nil {
			return plumbing.ZeroHash, false, err
		}
		switch {
		case !empty:
			entries[dir] = object.TreeEntry{Name: dir, Mode: filemode.Dir, Hash: hash}
		case sub != nil:
			delete(entries, dir) // Git keeps no empty directories.
		}
	}
	sorted := slices.SortedFunc(maps.Values(entries), func(a, b object.TreeEntry) int {
		return strings.Compare(treeSortName(a), treeSortName(b))
	})
	hash, err := storeObject(repo, &object.Tree{Entries: sorted})
	return hash, len(sorted) == 0, err
}

// treeSortName is what Git sorts a tree entry by: its name, with a trailing
// slash for a directory.
func treeSortName(e object.TreeEntry) string {
	if e.Mode == filemode.Dir {
		return e.Name + "/"
	}
	return e.Name
}

// storeBlob stores data as a blob in repo and returns its hash.
func storeBlob(repo *git.Repository, data []byte) (plumbing.Hash, error) {
	obj := repo.Storer.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	w, err := obj.Writer()
	if err != nil {
		return plumbing.ZeroHash, err
	}
	if _, err := w.Write(data); err != nil {
		_ = w.Close()
		return plumbing.ZeroHash, err
	}
	if err := w.Close(); err != nil {
		return plumbing.ZeroHash, err
	}
	return repo.Storer.SetEncodedObject(obj)
}

// storeObject stores a tree or commit in repo and returns its hash.
func storeObject(repo *git.Repository, o object.Object) (plumbing.Hash, error) {
	obj := repo.Storer.NewEncodedObject()
	if err := o.Encode(obj); err != nil {
		return plumbing.ZeroHash, err
	}
	return repo.Storer.SetEncodedObject(obj)
}
