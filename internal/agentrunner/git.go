package agentrunner

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os/exec"
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
	if _, err := r.git(ctx, "", "clone", "--quiet", "--no-tags", remote.String(), dir); err != nil {
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
	if _, err := r.git(ctx, dir, "ls-remote", "--exit-code", "--heads", "origin", job.Branch); err == nil {
		_, err = r.git(ctx, dir, "checkout", "--quiet", "-b", job.Branch, "origin/"+job.Branch)
		return r, err
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
	_, err := r.git(ctx, r.dir, "push", "--quiet", "origin", "HEAD:refs/heads/"+branch)
	return err
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
