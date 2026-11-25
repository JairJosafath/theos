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

package fake

import (
	"context"

	"github.com/JairJosafath/theos/internal/ports"
)

// The fake host's stamped comments (ports.IssueComments): each comment an
// issue gets is kept with an ID that counts up across the host, its
// author's login and the issues' clock (SetClock).

var _ ports.IssueComments = (*Git)(nil)

// stampComment is body as the host makes a comment of author's, stamped by
// the issues' clock (SetClock). The caller holds the lock.
func (s *state) stampComment(author, body string) ports.IssueComment {
	s.commentIDs++
	return ports.IssueComment{ID: s.commentIDs, Author: author, Created: s.issueState().clock(), Body: body}
}

// loginLocked is Login's account for this view, "" when it names none. The
// caller holds the lock.
func (g *Git) loginLocked() string {
	if g.cfg.Credentials.Username != "" {
		return g.cfg.Credentials.Username
	}
	return g.s.logins[g.cfg.Credentials.Token]
}

// ListIssueComments returns the issue's comments, oldest first.
func (g *Git) ListIssueComments(ctx context.Context, repo ports.Repository, number int) ([]ports.IssueComment,
	error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	i, err := g.issueOf(repo, number)
	if err != nil {
		return nil, err
	}
	return append([]ports.IssueComment(nil), i.Stamped...), nil
}

// PostIssueComment comments on the issue and returns the comment.
func (g *Git) PostIssueComment(ctx context.Context, repo ports.Repository, number int,
	body string) (ports.IssueComment, error) {
	if err := ctx.Err(); err != nil {
		return ports.IssueComment{}, err
	}
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	i, err := g.issueOf(repo, number)
	if err != nil {
		return ports.IssueComment{}, err
	}
	c := g.s.stampComment(g.loginLocked(), body)
	i.Comments, i.Stamped = append(i.Comments, body), append(i.Stamped, c)
	g.s.deliver(repo, EventIssue, number)
	return c, nil
}

// CommentAs comments on an issue as login, as another account would.
func (g *Git) CommentAs(owner, name string, number int, login, body string) ports.IssueComment {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	i := g.s.issueState().find(owner+"/"+name, number)
	if i == nil {
		return ports.IssueComment{}
	}
	c := g.s.stampComment(login, body)
	i.Comments, i.Stamped = append(i.Comments, body), append(i.Stamped, c)
	g.s.deliver(ports.Repository{Owner: owner, Name: name}, EventIssue, number)
	return c
}
