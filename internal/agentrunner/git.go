package agentrunner

import (
	"bytes"
	"context"
	"errors"
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
	// start is the work branch's head before this run: the base for a new branch.
	start string
	// conflicts are the files merging the base left in conflict, for the agent.
	conflicts []string
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
	if job.Role == RolePlan {
		// Each run plans afresh from the brief on the base branch; a rerun of the
		// round replaces the branch.
		if _, err := r.git(ctx, dir, "checkout", "--quiet", "-b", job.Branch, "origin/"+job.Base); err != nil {
			return nil, err
		}
		r.start, err = r.head(ctx)
		return r, err
	}
	err = retryTransient(ctx, networkRetryDelays, func() error {
		_, err := r.git(ctx, dir, "ls-remote", "--exit-code", "--heads", "origin", job.Branch)
		return err
	})
	switch {
	case err == nil:
		if _, err := r.git(ctx, dir, "checkout", "--quiet", "-b", job.Branch, "origin/"+job.Branch); err != nil {
			return nil, err
		}
		if r.start, err = r.head(ctx); err != nil {
			return nil, err
		}
		if job.Role == RoleImplement {
			// Other changes may have merged since the last round: start from them.
			return r, r.mergeBase(ctx, job.Base)
		}
		return r, nil
	case transientGitError(err):
		// Not knowing whether the branch exists is not the same as its absence:
		// starting over from the base would lose the earlier rounds.
		return nil, err
	}
	if _, err := r.git(ctx, dir, "checkout", "--quiet", "-b", job.Branch, "origin/"+job.Base); err != nil {
		return nil, err
	}
	r.start, err = r.head(ctx)
	return r, err
}

// mergeBase merges the base branch into the work branch. Conflicts stay in the
// working tree, listed in r.conflicts, for the agent to resolve.
func (r *repo) mergeBase(ctx context.Context, base string) error {
	_, mergeErr := r.git(ctx, r.dir, "merge", "--quiet", "--no-edit", "origin/"+base)
	if mergeErr == nil {
		return nil
	}
	out, err := r.git(ctx, r.dir, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return err
	}
	if r.conflicts = strings.Fields(out); len(r.conflicts) == 0 {
		return mergeErr
	}
	return nil
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

// changedPaths lists the paths the work branch changes, as its pull request
// shows them: since it forked from the base, without what it merged from there.
func (r *repo) changedPaths(ctx context.Context, base string) ([]string, error) {
	out, err := r.git(ctx, r.dir, "diff", "--name-only", "--no-renames", "origin/"+base+"...HEAD")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// sameTreeAsBase reports whether HEAD's files equal the base branch's, which
// says the work branch adds nothing even when its commits differ.
func (r *repo) sameTreeAsBase(ctx context.Context, base string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--quiet", "origin/"+base, "HEAD") //nolint:gosec // origin/ keeps base from being an option
	cmd.Dir = r.dir
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return false, nil
	}
	return false, fmt.Errorf("git diff origin/%s HEAD: %w", base, err)
}

// proposeBranch pushes a branch from origin/<base> that holds paths as the work
// head has them (removed where the head deleted them), in one commit, then goes
// back to the work branch.
func (r *repo) proposeBranch(ctx context.Context, base, branch string, paths []string, message string) error {
	head, err := r.head(ctx)
	if err != nil {
		return err
	}
	work, err := r.git(ctx, r.dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return err
	}
	work = strings.TrimSpace(work)
	if _, err := r.git(ctx, r.dir, "checkout", "--quiet", "-B", branch, "origin/"+base); err != nil {
		return err
	}
	for _, p := range paths {
		if _, err := r.git(ctx, r.dir, "cat-file", "-e", head+":"+p); err == nil {
			_, err = r.git(ctx, r.dir, "checkout", head, "--", p)
			if err != nil {
				return err
			}
		} else if _, err := r.git(ctx, r.dir, "rm", "--quiet", "--force", "--ignore-unmatch", "--", p); err != nil {
			return err
		}
	}
	if err := r.commitStaged(ctx, message); err != nil {
		return err
	}
	if err := retryTransient(ctx, networkRetryDelays, func() error {
		_, err := r.git(ctx, r.dir, "push", "--quiet", "--force", "origin", "HEAD:refs/heads/"+branch)
		return err
	}); err != nil {
		return err
	}
	_, err = r.git(ctx, r.dir, "checkout", "--quiet", work)
	return err
}

// restorePaths puts each path back as origin/<base> has it (removed when the base
// lacks it), in one commit.
func (r *repo) restorePaths(ctx context.Context, base string, paths []string, message string) error {
	for _, p := range paths {
		if _, err := r.git(ctx, r.dir, "cat-file", "-e", "origin/"+base+":"+p); err == nil {
			if _, err := r.git(ctx, r.dir, "checkout", "origin/"+base, "--", p); err != nil {
				return err
			}
		} else if _, err := r.git(ctx, r.dir, "rm", "--quiet", "--force", "--ignore-unmatch", "--", p); err != nil {
			return err
		}
	}
	return r.commitStaged(ctx, message)
}

// commitStaged commits the index, if it differs from HEAD.
func (r *repo) commitStaged(ctx context.Context, message string) error {
	if _, err := r.git(ctx, r.dir, "diff", "--cached", "--quiet"); err == nil {
		return nil
	}
	_, err := r.git(ctx, r.dir, "commit", "--quiet", "--no-verify", "-m", message)
	return err
}

// keepOnly makes the work branch one commit on origin/<base> that holds dir as
// the work head has it, and nothing else of the work head.
func (r *repo) keepOnly(ctx context.Context, base, branch, dir, message string) error {
	head, err := r.head(ctx)
	if err != nil {
		return err
	}
	if _, err := r.git(ctx, r.dir, "checkout", "--quiet", "-B", branch, "origin/"+base); err != nil {
		return err
	}
	if _, err := r.git(ctx, r.dir, "rm", "-r", "--quiet", "--ignore-unmatch", "--", dir); err != nil {
		return err
	}
	if _, err := r.git(ctx, r.dir, "cat-file", "-e", head+":"+dir); err == nil {
		if _, err := r.git(ctx, r.dir, "checkout", head, "--", dir); err != nil {
			return err
		}
	}
	return r.commitStaged(ctx, message)
}

// forcePush replaces branch on the remote with HEAD.
func (r *repo) forcePush(ctx context.Context, branch string) error {
	return retryTransient(ctx, networkRetryDelays, func() error {
		_, err := r.git(ctx, r.dir, "push", "--quiet", "--force", "origin", "HEAD:refs/heads/"+branch)
		return err
	})
}

func (r *repo) push(ctx context.Context, branch string) error {
	return retryTransient(ctx, networkRetryDelays, func() error {
		_, err := r.git(ctx, r.dir, "push", "--quiet", "origin", "HEAD:refs/heads/"+branch)
		return err
	})
}

func (r *repo) git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // git, with arguments the runner builds
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
