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
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/JairJosafath/theos/internal/adapters/github/githubtest"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
	"github.com/JairJosafath/theos/internal/scaffold"
)

const hostToken = "ghs_contract"

// step2 is why a GitHost subtest can't run yet: what it needs is step 2 of
// docs/plans/github-host.md (#75).
const step2 = "step 2 of docs/plans/github-host.md (#75)"

// TestGitHostContract covers #74: the GitHub adapter passes the GitHost
// contract's repository, file, branch and tag subtests against the fake
// GitHub, each in an organization of its own; the rest wait for step 2.
func TestGitHostContract(t *testing.T) {
	gh := githubtest.New(t, "ada", hostToken)
	orgs := 0
	contract.GitHost(t, contract.GitHostEnv{
		New: func(t *testing.T) ports.GitHost {
			t.Helper()
			orgs++
			org := fmt.Sprintf("contract-%d", orgs)
			gh.AddOrg(org)
			h, err := (Client{}).GitHost(ports.Config{URL: gh.URL(), Owner: org,
				Credentials: ports.Credentials{Username: "ignored", Token: hostToken}})
			if err != nil {
				t.Fatal(err)
			}
			return h
		},
		Cannot: map[string]string{
			// Its pushes are the same Git calls as a branch's; it ends by
			// pushing past a branch rule.
			"pushes the default branch":                             step2 + ": it ends with a branch rule",
			"protects branches and tags again":                      step2,
			"opens and merges a pull request":                       step2,
			"refuses a merge past the rule and forces it":           step2,
			"updates a pull request, or reports a conflict":         step2,
			"lists a pull request's files, a rename's old path too": step2,
			"combines the statuses of a commit":                     step2,
			"reads a release and its asset":                         step2,
			"reads the source at a tag":                             step2,
		},
	})
	if calls := gh.Unserved(); len(calls) > 0 {
		t.Errorf("the adapter made calls the fake GitHub doesn't serve: %q", calls)
	}
}

// TestGitHostOnGitHub covers what the contract leaves to each host (#74):
// where the API is, a user's own repositories, an owner that is neither, and
// a wrong token.
func TestGitHostOnGitHub(t *testing.T) {
	ctx := context.Background()
	gh := githubtest.New(t, "ada", hostToken)
	gh.AddOrg("acme")
	creds := ports.Credentials{Token: hostToken}

	t.Run("github.com's API is api.github.com, an Enterprise Server's is under /api/v3", func(t *testing.T) {
		for url, want := range map[string]string{
			"":                           "https://api.github.com",
			"https://github.com":         "https://api.github.com",
			"https://github.com/":        "https://api.github.com",
			"https://github.example.com": "https://github.example.com/api/v3",
		} {
			h, err := (Client{}).GitHost(ports.Config{URL: url, Owner: "acme", Credentials: creds})
			if err != nil {
				t.Fatal(err)
			}
			if got := h.(*host).api.String(); got != want {
				t.Errorf("the API of %q is %s, want %s", url, got, want)
			}
		}
		h, _ := (Client{}).GitHost(ports.Config{Owner: "acme", Credentials: creds})
		want := ports.Repository{Owner: "acme", Name: "shop", CloneURL: "https://github.com/acme/shop.git",
			WebURL: "https://github.com/acme/shop"}
		if got := h.Repository("shop"); got != want {
			t.Errorf("Repository() = %+v, want %+v", got, want)
		}
	})

	t.Run("an owner and a token are required", func(t *testing.T) {
		if _, err := (Client{}).GitHost(ports.Config{Credentials: creds}); err == nil {
			t.Error("no owner went through")
		}
		if _, err := (Client{}).GitHost(ports.Config{Owner: "acme"}); err == nil {
			t.Error("no token went through")
		}
	})

	t.Run("the token's user gets a repository of its own", func(t *testing.T) {
		h, err := (Client{}).GitHost(ports.Config{URL: gh.URL(), Owner: "ada", Credentials: creds})
		if err != nil {
			t.Fatal(err)
		}
		repo, err := h.EnsureRepository(ctx, "notes", "Ada's notes")
		if err != nil {
			t.Fatal(err)
		}
		if repo.Owner != "ada" || !strings.HasSuffix(repo.CloneURL, "/ada/notes.git") {
			t.Errorf("EnsureRepository() = %+v", repo)
		}
	})

	t.Run("an owner that is neither an organization nor the token's user is refused, saying so", func(t *testing.T) {
		h, _ := (Client{}).GitHost(ports.Config{URL: gh.URL(), Owner: "someone-else", Credentials: creds})
		_, err := h.EnsureRepository(ctx, "notes", "")
		if err == nil || !strings.Contains(err.Error(), "someone-else is neither an organization the token may "+
			"create repositories in nor the token's own account, which is ada") {
			t.Errorf("EnsureRepository() = %v, want the reason", err)
		}
		other, _ := (Client{}).GitHost(ports.Config{URL: gh.URL(), Owner: "ada", Credentials: creds})
		if has, _ := other.HasBranch(ctx, other.Repository("notes"), ports.DefaultBranch); has {
			t.Error("the refused call touched the token's own repository")
		}
	})

	t.Run("a wrong token fails with GitHub's words", func(t *testing.T) {
		h, _ := (Client{}).GitHost(ports.Config{URL: gh.URL(), Owner: "acme",
			Credentials: ports.Credentials{Token: "wrong"}})
		if _, err := h.EnsureRepository(ctx, "shop", ""); err == nil || !strings.Contains(err.Error(), "Bad credentials") {
			t.Errorf("EnsureRepository() = %v, want Bad credentials", err)
		}
	})

	if calls := gh.Unserved(); len(calls) > 0 {
		t.Errorf("the adapter made calls the fake GitHub doesn't serve: %q", calls)
	}
}

// TestGitHostRefsOnGitHub covers the default branch's pushes before any
// rule, a commit's tree, annotated tags, and what isn't built yet (#74).
func TestGitHostRefsOnGitHub(t *testing.T) {
	ctx := context.Background()
	gh := githubtest.New(t, "ada", hostToken)
	gh.AddOrg("acme")
	creds := ports.Credentials{Token: hostToken}
	author := ports.Signature{Name: "Ada", Email: "ada@example.com"}
	h, err := (Client{}).GitHost(ports.Config{URL: gh.URL(), Owner: "acme", Credentials: creds})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := h.EnsureRepository(ctx, "shop", "The shop")
	if err != nil {
		t.Fatal(err)
	}
	head, err := h.PushInitialCommit(ctx, repo, scaffold.Tree{"README.md": {Data: []byte("hello\n")}}, "Scaffold\n", author)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("pushes the default branch: on its head, not on a moved base, not an empty change", func(t *testing.T) {
		sha, err := h.PushDefaultBranch(ctx, repo, head, scaffold.Tree{"a.txt": {Data: []byte("a\n")}}, nil, "Add a.txt", author)
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := h.BranchCommit(ctx, repo, ports.DefaultBranch); got != sha {
			t.Errorf("BranchCommit(main) = %q, want %s", got, sha)
		}
		if _, err := h.PushDefaultBranch(ctx, repo, head, scaffold.Tree{"b.txt": {Data: []byte("b\n")}}, nil,
			"On a moved base", author); err == nil {
			t.Error("a push on a moved base went through")
		}
		if _, err := h.PushDefaultBranch(ctx, repo, sha, scaffold.Tree{"a.txt": {Data: []byte("a\n")}}, nil, "Nothing",
			author); !errors.Is(err, ports.ErrEmptyChange) {
			t.Errorf("PushDefaultBranch() of no change = %v, want ErrEmptyChange", err)
		}
	})

	t.Run("a commit's tree, and none for a commit it doesn't have", func(t *testing.T) {
		tree, err := h.CommitTree(ctx, repo, head)
		if err != nil || len(tree) != 40 || tree == head {
			t.Errorf("CommitTree() = %q, %v; want a tree's id", tree, err)
		}
		if _, err := h.CommitTree(ctx, repo, strings.Repeat("0", 40)); err == nil {
			t.Error("CommitTree() of a commit the repository doesn't have went through")
		}
	})

	t.Run("a tag is annotated, names its commit and is never moved", func(t *testing.T) {
		if err := h.CreateTag(ctx, repo, "v1.0.0", head, "Release v1.0.0"); err != nil {
			t.Fatal(err)
		}
		if got, _ := h.GetTag(ctx, repo, "v1.0.0"); got != head {
			t.Errorf("GetTag() = %q, want the commit %s, not the tag object", got, head)
		}
		main, _ := h.BranchCommit(ctx, repo, ports.DefaultBranch)
		err := h.CreateTag(ctx, repo, "v1.0.0", main, "Again")
		if err == nil || !strings.Contains(err.Error(), "already has tag v1.0.0") {
			t.Errorf("CreateTag() of a tag that exists = %v", err)
		}
		if got, _ := h.GetTag(ctx, repo, "v1.0.0"); got != head {
			t.Errorf("the tag moved to %s", got)
		}
	})

	t.Run("what step 2 brings says so", func(t *testing.T) {
		_, err := h.OpenPullRequest(ctx, repo, "task/a", ports.DefaultBranch, "t", "b")
		if !errors.Is(err, ErrNotYet) || !strings.Contains(err.Error(), "OpenPullRequest comes with step 2") {
			t.Errorf("OpenPullRequest() = %v, want ErrNotYet naming the step", err)
		}
	})

	if calls := gh.Unserved(); len(calls) > 0 {
		t.Errorf("the adapter made calls the fake GitHub doesn't serve: %q", calls)
	}
}
