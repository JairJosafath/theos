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
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/adapters/github/githubtest"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
	"github.com/JairJosafath/theos/internal/scaffold"
)

const hostToken = "ghs_contract"

// TestGitHostContract covers #74 and #75: the GitHub adapter passes the whole
// GitHost contract against the fake GitHub, each subtest in an organization
// of its own. A check is reported as Actions reports a job, a check run, and
// a release is made as a release run makes it.
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
		SetStatus: func(t *testing.T, repo ports.Repository, commit, context, state string) {
			t.Helper()
			status, conclusion := "completed", state
			if state == ports.CheckPending {
				status, conclusion = "in_progress", ""
			}
			gh.AddCheckRun(repo.Owner, repo.Name, commit, context, status, conclusion)
		},
		Release: func(t *testing.T, repo ports.Repository, tag, commit, body, asset, content string) {
			t.Helper()
			if err := gh.AddRelease(repo.Owner, repo.Name, tag, commit, body, asset, content, false); err != nil {
				t.Fatal(err)
			}
		},
		// GitHub can do all of it: nothing is skipped.
		Cannot: map[string]string{},
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
// rule, a commit's tree and annotated tags (#74).
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

	if calls := gh.Unserved(); len(calls) > 0 {
		t.Errorf("the adapter made calls the fake GitHub doesn't serve: %q", calls)
	}
}

// TestIssuesContract covers #76: the GitHub adapter passes the Issues
// contract against the fake GitHub, exclusive labels and refused closes
// emulated.
func TestIssuesContract(t *testing.T) {
	gh := githubtest.New(t, "ada", hostToken)
	orgs := 0
	contract.Issues(t, contract.IssuesEnv{
		New: func(t *testing.T) (ports.GitHost, ports.Issues) {
			t.Helper()
			orgs++
			org := fmt.Sprintf("issues-%d", orgs)
			gh.AddOrg(org)
			h, err := (Client{}).Host(ports.Config{URL: gh.URL(), Owner: org, Credentials: ports.Credentials{Token: hostToken}})
			if err != nil {
				t.Fatal(err)
			}
			return h, h
		},
		Assignee: "ada",
	})
	if calls := gh.Unserved(); len(calls) > 0 {
		t.Errorf("the adapter made calls the fake GitHub doesn't serve: %q", calls)
	}
}

// TestIssuesOnGitHub covers what GitHub does differently and the contract
// can't see (#76): pull requests among the issues, lists longer than a page,
// and the note that marks an exclusive label.
func TestIssuesOnGitHub(t *testing.T) {
	ctx := context.Background()
	gh := githubtest.New(t, "ada", hostToken)
	gh.AddOrg("acme")
	h, err := (Client{}).Host(ports.Config{URL: gh.URL(), Owner: "acme", Credentials: ports.Credentials{Token: hostToken}})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := h.EnsureRepository(ctx, "shop", "")
	if err != nil {
		t.Fatal(err)
	}
	labels := []ports.Label{{Name: "status/ready", Color: "#0e8a16", Description: "Ready", Exclusive: true},
		{Name: "status/doing", Color: "#fbca04", Exclusive: true}, {Name: "kind/bug", Color: "#d73a4a", Description: "A bug"}}
	if err := h.EnsureLabels(ctx, repo, labels); err != nil {
		t.Fatal(err)
	}

	t.Run("lists leave pull requests out and read every page", func(t *testing.T) {
		gh.AddPullRequest("acme", "shop", "A pull request")
		for n := range 130 {
			if _, err := h.CreateIssue(ctx, repo, fmt.Sprintf("Issue %d", n), "", []string{"kind/bug"}, 0); err != nil {
				t.Fatal(err)
			}
		}
		for _, label := range []string{"", "kind/bug"} {
			listed, err := h.ListIssues(ctx, repo, label)
			if err != nil || len(listed) != 130 {
				t.Errorf("ListIssues(%q) lists %d issues, %v; want the 130 that are no pull request", label, len(listed), err)
			}
		}
	})

	t.Run("an exclusive label replaces its scope's others and keeps the rest; a plain one adds", func(t *testing.T) {
		issue, err := h.CreateIssue(ctx, repo, "Labelled", "", []string{"status/ready", "kind/bug"}, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, add := range []string{"status/doing", "status/ready", "status/doing"} {
			if err := h.AddIssueLabel(ctx, repo, issue.Number, add); err != nil {
				t.Fatal(err)
			}
		}
		got, _ := h.GetIssue(ctx, repo, issue.Number)
		if want := []string{"kind/bug", "status/doing"}; !slices.Equal(sortedCopy(got.Labels), want) {
			t.Errorf("labels %q, want %q: one status, the kind kept", got.Labels, want)
		}
	})

	t.Run("a label a person described keeps their words when its scope turns exclusive", func(t *testing.T) {
		labels[2].Exclusive = true
		if err := h.EnsureLabels(ctx, repo, labels); err != nil {
			t.Fatal(err)
		}
		exclusive, err := h.(*host).exclusive(ctx, repo, "kind/bug")
		if err != nil || !exclusive {
			t.Errorf("kind/bug exclusive = %v, %v; want true", exclusive, err)
		}
		labels[2].Exclusive = false
		if err := h.EnsureLabels(ctx, repo, labels); err != nil {
			t.Fatal(err)
		}
		if exclusive, _ := h.(*host).exclusive(ctx, repo, "kind/bug"); exclusive {
			t.Error("kind/bug stayed exclusive")
		}
		if exclusive, _ := h.(*host).exclusive(ctx, repo, "unscoped"); exclusive {
			t.Error("a label without a scope is exclusive")
		}
	})

	if calls := gh.Unserved(); len(calls) > 0 {
		t.Errorf("the adapter made calls the fake GitHub doesn't serve: %q", calls)
	}
}

// TestIssueNotesOnGitHub covers what a comment, an assignment and a
// milestone's edits leave on the host (#76).
func TestIssueNotesOnGitHub(t *testing.T) {
	ctx := context.Background()
	gh := githubtest.New(t, "ada", hostToken)
	gh.AddOrg("acme")
	h, err := (Client{}).Host(ports.Config{URL: gh.URL(), Owner: "acme", Credentials: ports.Credentials{Token: hostToken}})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := h.EnsureRepository(ctx, "shop", "")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("a comment and an assignment reach the issue", func(t *testing.T) {
		issue, err := h.CreateIssue(ctx, repo, "Talked about", "", nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.CommentIssue(ctx, repo, issue.Number, "Picked up"); err != nil {
			t.Fatal(err)
		}
		if err := h.AssignIssue(ctx, repo, issue.Number, []string{"ada"}); err != nil {
			t.Fatal(err)
		}
		if got := gh.Comments("acme", "shop", issue.Number); !slices.Equal(got, []string{"Picked up"}) {
			t.Errorf("comments %q", got)
		}
		if got := gh.Assignees("acme", "shop", issue.Number); !slices.Equal(got, []string{"ada"}) {
			t.Errorf("assignees %q", got)
		}
		if err := h.AssignIssue(ctx, repo, issue.Number, nil); err != nil {
			t.Fatal(err)
		}
		if got := gh.Assignees("acme", "shop", issue.Number); len(got) != 0 {
			t.Errorf("assignees after clearing them: %q", got)
		}
	})

	t.Run("a milestone's description and title change, its due date stays", func(t *testing.T) {
		due := time.Date(2030, 1, 7, 12, 0, 0, 0, time.UTC)
		made, err := h.CreateMilestone(ctx, repo, "v0.1.0", "First", due)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.DescribeMilestone(ctx, repo, made.ID, "The first release"); err != nil {
			t.Fatal(err)
		}
		all, err := h.ListAllMilestones(ctx, repo)
		if err != nil || len(all) != 1 || all[0].Description != "The first release" || !all[0].Due.Equal(due) {
			t.Errorf("ListAllMilestones() = %+v, %v", all, err)
		}
	})

	if calls := gh.Unserved(); len(calls) > 0 {
		t.Errorf("the adapter made calls the fake GitHub doesn't serve: %q", calls)
	}
}

func sortedCopy(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return out
}
