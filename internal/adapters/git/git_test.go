package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/JairJosafath/c4factory/internal/ports"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

var testAuthor = ports.Signature{Name: "Test", Email: "test@example.com", When: time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)}

// bareRepo creates an empty bare repository and returns its path, usable as a URL.
func bareRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo.git")
	if _, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{
		Bare:        true,
		InitOptions: git.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName(ports.DefaultBranch)},
	}); err != nil {
		t.Fatal(err)
	}
	return dir
}

// templateRepo makes a template repository with v1.0.0 tagged on the first commit
// and one more commit on main after it. It returns the path and the first commit.
func templateRepo(t *testing.T) (string, string) {
	t.Helper()
	ctx := context.Background()
	dir := bareRepo(t)
	first, err := PushInitialCommit(ctx, dir, nil, scaffold.Tree{
		"README.md.tmpl":    {Data: []byte("# {{ .Container }}\n")},
		"scripts/setup.sh":  {Data: []byte("#!/bin/sh\necho hi\n"), Executable: true},
		".c4factory/x.json": {Data: []byte("{}")},
	}, "Template v1", testAuthor)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := git.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateTag("v1.0.0", plumbing.NewHash(first), nil); err != nil {
		t.Fatal(err)
	}

	// A later commit on main, which a pinned fetch must not see.
	clone := t.TempDir()
	local, err := git.PlainClone(clone, false, &git.CloneOptions{URL: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, "README.md.tmpl"), []byte("# changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt, _ := local.Worktree()
	if _, err := wt.Add("README.md.tmpl"); err != nil {
		t.Fatal(err)
	}
	sig := &object.Signature{Name: "Test", Email: "test@example.com", When: testAuthor.When.Add(time.Hour)}
	if _, err := wt.Commit("After v1", &git.CommitOptions{Author: sig}); err != nil {
		t.Fatal(err)
	}
	if err := local.Push(&git.PushOptions{}); err != nil {
		t.Fatal(err)
	}
	return dir, first
}

func TestFetchTemplate(t *testing.T) {
	ctx := context.Background()
	dir, first := templateRepo(t)
	client := Client{}

	for _, ref := range []string{"v1.0.0", first} {
		t.Run("pinned to "+ref[:6], func(t *testing.T) {
			tree, err := client.FetchTemplate(ctx, dir, ref, nil)
			if err != nil {
				t.Fatalf("FetchTemplate() error = %v", err)
			}
			if got := string(tree["README.md.tmpl"].Data); got != "# {{ .Container }}\n" {
				t.Errorf("README.md.tmpl = %q, want the file at the pinned ref", got)
			}
			if !tree["scripts/setup.sh"].Executable {
				t.Error("scripts/setup.sh lost its executable bit")
			}
			if _, ok := tree[".c4factory/x.json"]; !ok {
				t.Error(".c4factory/ should be fetched; the renderer decides what to copy")
			}
			for p := range tree {
				if strings.HasPrefix(p, ".git/") {
					t.Errorf("tree contains %s", p)
				}
			}
		})
	}

	for _, tt := range []struct{ ref, want string }{
		{"main", "is a branch"},
		{"v9.9.9", "not found"},
		{strings.Repeat("a", 40), "not found"},
	} {
		t.Run("rejects "+tt.ref[:4], func(t *testing.T) {
			_, err := client.FetchTemplate(ctx, dir, tt.ref, nil)
			if !errors.Is(err, ports.ErrUnpinnedRef) || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("FetchTemplate(%q) error = %v, want ErrUnpinnedRef containing %q", tt.ref, err, tt.want)
			}
		})
	}
}

// A commit reachable only from a tag, such as a release cut from a branch that
// has since been deleted, is still a valid pin.
func TestFetchTemplateTaggedCommit(t *testing.T) {
	ctx := context.Background()
	dir, _ := templateRepo(t)
	clone := t.TempDir()
	local, err := git.PlainClone(clone, false, &git.CloneOptions{URL: dir})
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := local.Worktree()
	if err := wt.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName("release"), Create: true}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, "README.md.tmpl"), []byte("# release\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("README.md.tmpl"); err != nil {
		t.Fatal(err)
	}
	sig := &object.Signature{Name: "Test", Email: "test@example.com", When: testAuthor.When.Add(2 * time.Hour)}
	release, err := wt.Commit("Release", &git.CommitOptions{Author: sig})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := local.CreateTag("v2.0.0", release, nil); err != nil {
		t.Fatal(err)
	}
	// Only the tag is pushed; no branch on the remote holds the commit.
	if err := local.Push(&git.PushOptions{RefSpecs: []config.RefSpec{"refs/tags/v2.0.0:refs/tags/v2.0.0"}}); err != nil {
		t.Fatal(err)
	}

	tree, err := Client{}.FetchTemplate(ctx, dir, release.String(), nil)
	if err != nil {
		t.Fatalf("FetchTemplate() error = %v", err)
	}
	if got := string(tree["README.md.tmpl"].Data); got != "# release\n" {
		t.Errorf("README.md.tmpl = %q, want the file at the tagged commit", got)
	}
}

func TestHistoryAndInitialCommit(t *testing.T) {
	ctx := context.Background()
	dir := bareRepo(t)

	commits, err := History(ctx, dir, nil, 2)
	if err != nil || len(commits) != 0 {
		t.Fatalf("history(empty) = %v, %v; want no commits", commits, err)
	}

	files := scaffold.Tree{"a.txt": {Data: []byte("a")}, "dir/run.sh": {Data: []byte("#!/bin/sh"), Executable: true}}
	hash, err := PushInitialCommit(ctx, dir, nil, files, "Scaffold x\n\nC4-Factory-Container: p/x\n", testAuthor)
	if err != nil {
		t.Fatalf("pushInitialCommit() error = %v", err)
	}

	commits, err = History(ctx, dir, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 || commits[0].Hash != hash || !strings.Contains(commits[0].Message, "C4-Factory-Container: p/x") {
		t.Fatalf("history() = %+v, want the one initial commit %s", commits, hash)
	}

	if _, err := PushInitialCommit(ctx, dir, nil, files, "again", testAuthor); err == nil {
		t.Error("a second initial commit was pushed over an existing branch")
	}

	// The pushed files, modes included, come back as they went in.
	tree, err := Client{}.FetchTemplate(ctx, dir, hash, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(tree["a.txt"].Data) != "a" || !tree["dir/run.sh"].Executable {
		t.Errorf("round trip lost content or modes: %+v", tree)
	}
}

// A repository whose history is on another branch isn't empty, so the operator
// mustn't scaffold into it.
func TestHistoryOnAnotherBranch(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "repo.git")
	if _, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{
		Bare:        true,
		InitOptions: git.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName("master")},
	}); err != nil {
		t.Fatal(err)
	}
	clone := t.TempDir()
	local, err := git.PlainInitWithOptions(clone, &git.PlainInitOptions{
		InitOptions: git.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName("master")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, "README.md"), []byte("# theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt, _ := local.Worktree()
	if _, err := wt.Add("README.md"); err != nil {
		t.Fatal(err)
	}
	sig := &object.Signature{Name: "Someone", Email: "someone@example.com", When: testAuthor.When}
	hash, err := wt.Commit("Their commit", &git.CommitOptions{Author: sig})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := local.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{dir}}); err != nil {
		t.Fatal(err)
	}
	if err := local.Push(&git.PushOptions{}); err != nil {
		t.Fatal(err)
	}

	commits, err := History(ctx, dir, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 || commits[0].Hash != hash.String() {
		t.Fatalf("history() = %+v, want their commit %s on master", commits, hash)
	}

	// #1908: main itself is missing, which readFiles reports as such, so a
	// caller fails instead of retrying.
	if _, _, err := ReadFiles(ctx, dir, nil, ports.DefaultBranch, []string{"README.md"}); !errors.Is(err, ports.ErrBranchNotFound) {
		t.Fatalf("readFiles(main) error = %v, want ErrBranchNotFound", err)
	}
	files, _, err := ReadFiles(ctx, dir, nil, "master", []string{"README.md"})
	if err != nil {
		t.Fatal(err)
	}
	if string(files["README.md"].Data) != "# theirs\n" {
		t.Fatalf("readFiles(master) = %q, want their README", files["README.md"].Data)
	}
}

func TestCheckoutError(t *testing.T) {
	other := errors.New("disk full")
	for _, tt := range []struct {
		name     string
		err      error
		unpinned bool
	}{
		{"missing commit", plumbing.ErrObjectNotFound, true},
		{"wrapped missing commit", fmt.Errorf("get commit: %w", plumbing.ErrObjectNotFound), true},
		{"any other error", other, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := checkoutError(tt.err, "abc", "repo")
			if got := errors.Is(err, ports.ErrUnpinnedRef); got != tt.unpinned {
				t.Errorf("errors.Is(%v, ErrUnpinnedRef) = %v, want %v", err, got, tt.unpinned)
			}
			if !tt.unpinned && !errors.Is(err, tt.err) {
				t.Errorf("checkoutError() = %v, want it to wrap %v", err, tt.err)
			}
		})
	}
}
