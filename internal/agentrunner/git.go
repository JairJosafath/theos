package agentrunner

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
)

// repo is a clone the runner works in, driven through the git CLI.
type repo struct {
	dir string
	// secret is removed from error messages.
	secret string
}

// clone checks out the job's branch, or starts it from the base when it doesn't
// exist yet. The token is part of the remote URL; it can only reach this one
// repository.
func clone(ctx context.Context, job Job, dir string) (*repo, error) {
	remote, err := url.Parse(job.RepoURL)
	if err != nil {
		return nil, fmt.Errorf("repository URL %q: %w", job.RepoURL, err)
	}
	if remote.Scheme == "http" || remote.Scheme == "https" {
		remote.User = url.UserPassword(job.Username, job.Token)
	}
	r := &repo{dir: dir, secret: job.Token}
	if err := retryTransient(ctx, networkRetryDelays, func() error {
		_, err := r.git(ctx, "", "clone", "--quiet", "--no-tags", remote.String(), dir)
		return err
	}); err != nil {
		return nil, err
	}
	for _, kv := range [][2]string{
		{"user.name", job.Username},
		{"user.email", job.Username + "@agents.c4.factory.io"},
	} {
		if _, err := r.git(ctx, dir, "config", kv[0], kv[1]); err != nil {
			return nil, err
		}
	}
	err = retryTransient(ctx, networkRetryDelays, func() error {
		_, err := r.git(ctx, dir, "ls-remote", "--exit-code", "--heads", "origin", job.Branch)
		return err
	})
	switch {
	case err == nil:
		_, err = r.git(ctx, dir, "checkout", "--quiet", "-b", job.Branch, "origin/"+job.Branch)
		return r, err
	case transientGitError(err):
		// Not knowing whether the branch exists is not the same as its absence:
		// starting over from the base would lose the earlier rounds.
		return nil, err
	}
	_, err = r.git(ctx, dir, "checkout", "--quiet", "-b", job.Branch, "origin/"+job.Base)
	return r, err
}

func (r *repo) head(ctx context.Context) (string, error) {
	out, err := r.git(ctx, r.dir, "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

// commitAll commits whatever the agent left uncommitted.
func (r *repo) commitAll(ctx context.Context, message string) error {
	status, err := r.git(ctx, r.dir, "status", "--porcelain")
	if err != nil || strings.TrimSpace(status) == "" {
		return err
	}
	if _, err := r.git(ctx, r.dir, "add", "--all"); err != nil {
		return err
	}
	_, err = r.git(ctx, r.dir, "commit", "--quiet", "--no-verify", "-m", message)
	return err
}

// changedPaths lists the paths that differ between two commits.
func (r *repo) changedPaths(ctx context.Context, from, to string) ([]string, error) {
	out, err := r.git(ctx, r.dir, "diff", "--name-only", from, to)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

func (r *repo) push(ctx context.Context, branch string) error {
	return retryTransient(ctx, networkRetryDelays, func() error {
		_, err := r.git(ctx, r.dir, "push", "--quiet", "origin", "HEAD:refs/heads/"+branch)
		return err
	})
}

func (r *repo) git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if r.secret != "" {
			msg = strings.ReplaceAll(msg, r.secret, "***")
		}
		return "", fmt.Errorf("git %s: %v: %s", args[0], err, msg)
	}
	return stdout.String(), nil
}

// showBase returns a file as it is on the base branch, or "" when it isn't there.
func (r *repo) showBase(ctx context.Context, base, path string) string {
	if path == "" {
		return ""
	}
	out, err := r.git(ctx, r.dir, "show", "origin/"+base+":"+path)
	if err != nil {
		return ""
	}
	return out
}

// cloneRelated clones the related Containers' repositories into root, one folder
// each, shallow and without a remote: they are for reading. A repository that
// can't be cloned is reported, not fatal.
func cloneRelated(ctx context.Context, job Job, root string) []RelatedRepo {
	names := make([]string, 0, len(job.Related))
	for name := range job.Related {
		names = append(names, name)
	}
	related := make([]RelatedRepo, 0, len(names))
	for _, name := range sortedCopy(names) {
		dir := filepath.Join(root, name)
		r := &repo{dir: dir, secret: job.Token}
		err := retryTransient(ctx, networkRetryDelays, func() error {
			_, err := r.git(ctx, "", "clone", "--quiet", "--depth", "1", "--no-tags", job.Related[name], dir)
			return err
		})
		if err == nil {
			_, err = r.git(ctx, dir, "remote", "remove", "origin")
		}
		if err != nil {
			related = append(related, RelatedRepo{Name: name, Error: shorten(err.Error(), 200)})
			continue
		}
		related = append(related, RelatedRepo{Name: name, Dir: dir})
	}
	return related
}
