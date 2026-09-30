package gitprovider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"slices"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
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
			return nil, fmt.Errorf("%w: commit %s not found in %s", ErrUnpinnedRef, ref, repoURL)
		}
	}
	return readTree(worktree)
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
