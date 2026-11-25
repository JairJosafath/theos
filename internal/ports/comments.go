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

package ports

import (
	"context"
	"time"
)

// IssueComment is a comment on an issue or a pull request as the Git host
// shows it: its ID, its author's login, the host's stamp of its creation,
// and its text.
type IssueComment struct {
	ID      int64
	Author  string
	Created time.Time
	Body    string
}

// IssueComments is an optional part of Issues: an issue's comments read
// with the host's IDs, authors and stamps, and a comment posted and read
// back as the host stamped it. The lead runtime's markers are such
// comments (docs/plans/lead-runtime.md, decision 4): a claim's lease is read
// by the host's stamps, never a session's clock. Asked for by interface.
type IssueComments interface {
	// ListIssueComments returns the comments of an issue or a pull request,
	// oldest first, every one of them however many there are;
	// ErrIssueNotFound when there is no such issue.
	ListIssueComments(ctx context.Context, repo Repository, number int) ([]IssueComment, error)
	// PostIssueComment adds a comment to an issue or a pull request and
	// returns it as the host stamped it.
	PostIssueComment(ctx context.Context, repo Repository, number int, body string) (IssueComment, error)
}
