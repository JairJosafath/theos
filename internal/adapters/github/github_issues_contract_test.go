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
	"crypto/rand"
	"net/http"
	"strings"
	"testing"

	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
)

// TestIssueTrackerContract covers #61: the GitHub issues client passes the
// IssueTracker contract against the fake GitHub of this package's tests,
// each subtest on a repository of its own. People file, edit, close and
// comment on the fake itself, not through the client.
func TestIssueTrackerContract(t *testing.T) {
	contract.IssueTracker(t, contract.IssueTrackerEnv{
		New: func(t *testing.T) contract.TrackerHost {
			t.Helper()
			f, tracker := newFakeGitHubIssues(t)
			return contract.TrackerHost{
				Tracker: tracker,
				// The token's account, as the fake names who wrote a comment.
				Account:     "theos-bot",
				Refused:     f.with(t, fakeGitHubRepo, rand.Text(), ""),
				LacksIssues: f.with(t, fakeGitHubRepo, rand.Text(), lacksIssues),
				Elsewhere:   f.with(t, "acme/nowhere", f.token, ""),
				// A person's login is their name.
				Person: func(_ *testing.T, name string) string { return name },
				File: func(_ *testing.T, author, title, body string) int {
					return f.file(title, body, author, false)
				},
				PullRequest: func(_ *testing.T, author, title string) int { return f.file(title, "", author, true) },
				Edit: func(_ *testing.T, number int, body string) {
					f.person(number, func(i *githubIssueState) { i.Body = body })
				},
				Close: func(_ *testing.T, number int) {
					f.person(number, func(i *githubIssueState) { i.State, i.StateReason = stateClosed, string(ports.CloseCompleted) })
				},
				Say: func(_ *testing.T, number int, author, body string) {
					f.person(number, func(*githubIssueState) {
						f.nextID++
						f.comments[number] = append(f.comments[number],
							githubComment{ID: f.nextID, Body: body, User: githubUser{Login: author}})
					})
				},
				Disable: func(*testing.T) { f.answer(http.StatusGone, "Issues are disabled for this repo") },
				Down:    func(*testing.T) { f.answer(http.StatusServiceUnavailable, "No server is currently available") },
				Issue:   f.shown,
			}
		},
		// GitHub can do all of it: nothing is skipped.
		Cannot: map[string]string{},
	})
}

// TestTeamMembersContract covers #151: GitHub has teams, so its issues
// client is a ports.TeamMembers and passes that capability's contract on the
// same fake GitHub. People join teams and are invited on the fake itself.
func TestTeamMembersContract(t *testing.T) {
	contract.TeamMembers(t, contract.TeamMembersEnv{
		New: func(t *testing.T) contract.TeamsHost {
			t.Helper()
			f, tracker := newFakeGitHubIssues(t)
			return contract.TeamsHost{
				Members:      teamMembers(t, tracker),
				Refused:      teamMembers(t, f.with(t, fakeGitHubRepo, rand.Text(), "")),
				LacksMembers: teamMembers(t, f.with(t, fakeGitHubRepo, rand.Text(), lacksMembers)),
				Team:         "acme/maintainers",
				NoTeam:       "acme/nobody",
				// A person's login is their name.
				Person: func(_ *testing.T, name string) string { return name },
				Join:   func(_ *testing.T, team, login string) { f.membership(team, login, "active") },
				Invite: func(_ *testing.T, team, login string) { f.membership(team, login, "pending") },
				Down:   func(*testing.T) { f.answer(http.StatusServiceUnavailable, "No server is currently available") },
			}
		},
		// GitHub can do all of it: nothing is skipped.
		Cannot: map[string]string{},
	})
}

// with is a client of repository on the fake with token, which may not do
// what lacks names (lacksIssues, lacksMembers) when it is set.
func (f *fakeGitHubIssues) with(t *testing.T, repository, token, lacks string) ports.IssueTracker {
	t.Helper()
	if lacks != "" {
		f.lacks[token] = lacks
	}
	other, err := Client{}.GitHubIssues(ports.GitHubConfig{Repository: repository, Token: token, APIURL: f.url})
	if err != nil {
		t.Fatal(err)
	}
	return other
}

// person does what a person does to issue number on GitHub's own pages.
func (f *fakeGitHubIssues) person(number int, do func(*githubIssueState)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	do(f.issue(number))
}

// membership gives login a membership of team in state.
func (f *fakeGitHubIssues) membership(team, login, state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.members[team] == nil {
		f.members[team] = map[string]string{}
	}
	f.members[team][login] = state
}

// answer makes the fake answer every call of a token it knows with status
// and message.
func (f *fakeGitHubIssues) answer(status int, message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, status, map[string]string{"message": message})
	}
}

// shown is issue number as GitHub's page shows it.
func (f *fakeGitHubIssues) shown(t *testing.T, number int) contract.TrackedIssue {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.issue(number)
	if i == nil {
		t.Fatalf("the fake has no issue %d", number)
	}
	out := contract.TrackedIssue{Title: i.Title, Body: i.Body, Author: i.User.Login, Created: i.Created,
		Closed: i.State == stateClosed}
	if out.Closed {
		out.Reason = ports.CloseReason(i.StateReason)
	}
	for _, c := range f.comments[number] {
		// The fake's token is theos-bot's, whatever the letters' case.
		out.Comments = append(out.Comments,
			contract.TrackedComment{Body: c.Body, Own: strings.EqualFold(c.User.Login, "theos-bot")})
	}
	return out
}
