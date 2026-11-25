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

package contract

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/JairJosafath/theos/internal/ports"
)

// IssueCommentsEnv builds the IssueComments under test.
type IssueCommentsEnv struct {
	// New returns the GitHost of a fresh owner that holds no repository,
	// where the suite makes the repository, and the owner's Issues, which
	// are IssueComments.
	New func(t *testing.T) (ports.GitHost, ports.Issues)
	// Cannot names the subtests the host can't run, with why; they are
	// skipped.
	Cannot map[string]string
}

// pageComments is more comments than one page of any host's listing holds.
const pageComments = 105

// IssueComments states what the lead runtime relies on of an
// IssueComments (docs/plans/lead-runtime.md, decision 4): a comment posted
// comes back with an ID, the credential's login as its author and the
// host's stamp; the issue's comments are listed oldest first, those
// CommentIssue made among them, every one past a page; and an issue the
// repository lacks is ErrIssueNotFound.
func IssueComments(t *testing.T, env IssueCommentsEnv) {
	t.Helper()
	ctx, cancel := suiteContext()
	defer cancel()
	runSubtests(t, issueCommentsSuite{ctx: ctx, env: env}, env.Cannot, []subtest[issueCommentsSuite]{
		{"posts a comment and reads it back as the host stamped it", issueCommentsSuite.posts},
		{"lists every comment, past a page", issueCommentsSuite.pages},
	})
}

// issueCommentsSuite holds what every subtest of IssueComments shares.
type issueCommentsSuite struct {
	ctx context.Context
	env IssueCommentsEnv
}

// issue is a fresh issue on a fresh repository, and the host's
// IssueComments and the credential's login.
func (s issueCommentsSuite) issue(t *testing.T) (ports.IssueComments, ports.Issues, ports.Repository, int, string) {
	t.Helper()
	h, issues := s.env.New(t)
	comments, ok := issues.(ports.IssueComments)
	if !ok {
		t.Fatalf("%T is no ports.IssueComments", issues)
	}
	repo := must(h.EnsureRepository(s.ctx, "contract-comments", "the IssueComments contract suite"))(t)
	issue := must(issues.CreateIssue(s.ctx, repo, "Comments", "The comments' issue.", nil, 0))(t)
	login := must(h.Login(s.ctx))(t)
	return comments, issues, repo, issue.Number, login
}

func (s issueCommentsSuite) posts(t *testing.T) {
	comments, issues, repo, n, login := s.issue(t)
	first := must(comments.PostIssueComment(s.ctx, repo, n, "<!-- theos:offer -->\n\nThe first."))(t)
	if first.ID == 0 || first.Author != login || first.Created.IsZero() || first.Body != "<!-- theos:offer -->\n\nThe first." {
		t.Errorf("posted comment = %+v, want an ID, author %s, a stamp and the body", first, login)
	}
	if err := issues.CommentIssue(s.ctx, repo, n, "The second."); err != nil {
		t.Fatal(err)
	}
	third := must(comments.PostIssueComment(s.ctx, repo, n, "The third."))(t)
	got := must(comments.ListIssueComments(s.ctx, repo, n))(t)
	if len(got) != 3 {
		t.Fatalf("listed %d comments, want 3: %+v", len(got), got)
	}
	for i, body := range []string{first.Body, "The second.", "The third."} {
		if got[i].Body != body || got[i].Author != login || got[i].Created.IsZero() {
			t.Errorf("comment %d = %+v, want %q by %s, stamped", i, got[i], body, login)
		}
	}
	if got[0].ID != first.ID || got[2].ID != third.ID || !got[0].Created.Equal(first.Created) {
		t.Errorf("listed %+v, %+v; want the posted comments' IDs and stamps", got[0], got[2])
	}
	if got[0].ID == got[1].ID || got[1].ID == got[2].ID || got[2].Created.Before(got[0].Created) {
		t.Errorf("comments %+v: want distinct IDs, oldest first", got)
	}
	if _, err := comments.ListIssueComments(s.ctx, repo, n+1000); !errors.Is(err, ports.ErrIssueNotFound) {
		t.Errorf("the comments of an issue the repository lacks: %v, want ErrIssueNotFound", err)
	}
}

func (s issueCommentsSuite) pages(t *testing.T) {
	comments, _, repo, n, _ := s.issue(t)
	for i := range pageComments {
		if _, err := comments.PostIssueComment(s.ctx, repo, n, fmt.Sprintf("Comment %d.", i)); err != nil {
			t.Fatal(err)
		}
	}
	got := must(comments.ListIssueComments(s.ctx, repo, n))(t)
	if len(got) != pageComments {
		t.Fatalf("listed %d comments, want all %d", len(got), pageComments)
	}
	for i, c := range got {
		if want := fmt.Sprintf("Comment %d.", i); c.Body != want {
			t.Fatalf("comment %d is %q, want %q: oldest first", i, c.Body, want)
		}
	}
}
