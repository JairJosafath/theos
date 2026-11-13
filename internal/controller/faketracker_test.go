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

package controller

import (
	"context"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/fake"
)

// The Brief's request specs run on the port's fake tracker (#154): each
// spec's releases repository is a project of a tracker of its own, so a
// tracker one spec takes down is down for no other.

// trackerLogin is the factory's account on every fake tracker, which says
// what the Brief says.
const trackerLogin = "theos-bot"

// fakeTrackers is the suite's adapter of every tracker (model.TrackerGitHub,
// model.TrackerLinear and model.TrackerJira): it builds a source's tracker
// with the fake tracker made for its repository, team or project, keeps the
// source it was built for, and counts the lists that came back unchanged. A
// project no spec made a tracker for is ports.ReasonTrackerNotFound.
type fakeTrackers struct {
	mu       sync.Mutex
	projects map[string]*specTracker
}

var _ ports.TrackerAdapter = (*fakeTrackers)(nil)

func newFakeTrackers() *fakeTrackers {
	return &fakeTrackers{projects: map[string]*specTracker{}}
}

func (f *fakeTrackers) Tracker(ctx context.Context, source ports.TrackerSource,
	secret map[string][]byte) (ports.IssueTracker, error) {
	f.mu.Lock()
	tr := f.projects[source.Project]
	f.mu.Unlock()
	if tr == nil {
		return nil, &ports.IssuesError{Reason: ports.ReasonTrackerNotFound, Message: "no tracker of " + source.Project}
	}
	port, err := tr.Tracker(ctx, source, secret)
	if err != nil {
		return nil, err
	}
	tr.mu.Lock()
	tr.source = source
	tr.mu.Unlock()
	counted := countedTracker{IssueTracker: port, notModified: &tr.notModified}
	if members, ok := port.(ports.TeamMembers); ok {
		return countedTeams{counted, members}, nil
	}
	return counted, nil
}

// specTracker is the fake tracker of one spec's releases repository,
// whose project it is, with the factory's account.
type specTracker struct {
	*fake.IssueTracker
	repository string
	// token is the factory's account's credential, which may use the issues
	// and read a team's members.
	token string
	// notModified counts the lists that came back unchanged.
	notModified atomic.Int64
	// source is the last source a tracker was built for.
	mu     sync.Mutex
	source ports.TrackerSource
}

// builtFor is the last source the Brief built this tracker for.
func (tr *specTracker) builtFor() ports.TrackerSource {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.source
}

// issueURL is the page of issue n.
func (tr *specTracker) issueURL(n int) string {
	GinkgoHelper()
	issue, _ := tr.issue(n)
	return issue.URL
}

// unlabel takes the request's label off issue n, as a person does.
func (tr *specTracker) unlabel(n int) {
	GinkgoHelper()
	Expect(tr.Unlabel(tr.repository, n)).To(Succeed())
}

// relabel puts the request's label back on issue n.
func (tr *specTracker) relabel(n int) {
	GinkgoHelper()
	Expect(tr.Label(tr.repository, n)).To(Succeed())
}

// newRequestsTracker makes the fake tracker of repository, and keeps it for
// the Brief's source of that repository.
func (f *fakeTrackers) newRequestsTracker(repository string) *specTracker {
	tr := &specTracker{IssueTracker: fake.NewIssueTracker(), repository: repository}
	tr.Project(repository)
	tr.token = tr.Account(trackerLogin, fake.TrackerAccess{Issues: true, Members: true})
	f.mu.Lock()
	defer f.mu.Unlock()
	f.projects[repository] = tr
	return tr
}

// file files an issue by author and returns its number.
func (tr *specTracker) file(author, title, body string) int {
	GinkgoHelper()
	n, err := tr.File(tr.repository, author, title, body)
	Expect(err).NotTo(HaveOccurred())
	return n
}

// pullRequest opens a pull request by author, among the issues.
func (tr *specTracker) pullRequest(author, title string) int {
	GinkgoHelper()
	n, err := tr.PullRequest(tr.repository, author, title)
	Expect(err).NotTo(HaveOccurred())
	return n
}

// closeByHand closes issue n as a person does.
func (tr *specTracker) closeByHand(n int) {
	GinkgoHelper()
	Expect(tr.Close(tr.repository, n)).To(Succeed())
}

// edit changes the body of issue n as its author does.
func (tr *specTracker) edit(n int, body string) {
	GinkgoHelper()
	Expect(tr.Edit(tr.repository, n, body)).To(Succeed())
}

// say comments body on issue n as author.
func (tr *specTracker) say(n int, author, body string) {
	GinkgoHelper()
	Expect(tr.Say(tr.repository, n, author, body)).To(Succeed())
}

// issue returns issue n and the bodies of its comments.
func (tr *specTracker) issue(n int) (fake.TrackedIssue, []string) {
	GinkgoHelper()
	issue, ok := tr.Issue(tr.repository, n)
	Expect(ok).To(BeTrue(), "issue %d of %s", n, tr.repository)
	comments := make([]string, 0, len(issue.Comments))
	for _, c := range issue.Comments {
		comments = append(comments, c.Body)
	}
	return issue, comments
}

// lists returns how many issue lists were asked, and how many of them came
// back unchanged.
func (tr *specTracker) lists() (asked, notModified int) {
	return tr.Calls("ListOpenIssues"), int(tr.notModified.Load())
}

// countedTracker counts the lists of its tracker that came back unchanged.
type countedTracker struct {
	ports.IssueTracker
	notModified *atomic.Int64
}

func (c countedTracker) ListOpenIssues(ctx context.Context, etag string) (ports.IssueList, error) {
	list, err := c.IssueTracker.ListOpenIssues(ctx, etag)
	if err == nil && list.NotModified {
		c.notModified.Add(1)
	}
	return list, err
}

// countedTeams is a countedTracker of a tracker that has teams.
type countedTeams struct {
	countedTracker
	ports.TeamMembers
}
