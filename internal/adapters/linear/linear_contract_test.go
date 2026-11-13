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

package linear

import (
	"crypto/rand"
	"testing"

	"github.com/JairJosafath/theos/internal/adapters/linear/lineartest"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
)

const (
	testTeam    = "ENG"
	testLabel   = "factory"
	testAccount = "theos-bot"
)

// tracker is an IssueTracker of team with label on s, with token.
func tracker(t *testing.T, s *lineartest.Server, team, token string) ports.IssueTracker {
	t.Helper()
	tr, err := Client{}.Issues(Config{Team: team, Label: testLabel, Token: token, APIURL: s.URL()})
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// TestIssueTrackerContract covers #156: the Linear adapter passes the
// IssueTracker contract on the fake Linear, each subtest on a Linear of its
// own. People file, edit, close and comment on the fake itself, not through
// the adapter; the items they file carry the label.
func TestIssueTrackerContract(t *testing.T) {
	contract.IssueTracker(t, contract.IssueTrackerEnv{
		New: func(t *testing.T) contract.TrackerHost {
			t.Helper()
			s := lineartest.New(t)
			s.Team(testTeam)
			return contract.TrackerHost{
				Tracker:     tracker(t, s, testTeam, s.Account(testAccount)),
				Account:     testAccount,
				Refused:     tracker(t, s, testTeam, rand.Text()),
				LacksIssues: tracker(t, s, testTeam, s.Restricted("limited")),
				Elsewhere:   tracker(t, s, "NOPE", s.Account("elsewhere")),
				// A person's display name is their name.
				Person: func(_ *testing.T, name string) string { return name },
				File: func(_ *testing.T, author, title, body string) int {
					return s.File(lineartest.Item{Team: testTeam, Title: title, Description: body,
						Labels: []string{testLabel}, By: lineartest.Maker{User: author}})
				},
				PullRequest: func(t *testing.T, _, _ string) int {
					t.Fatal("Linear has no pull request among its items: the subtest is skipped")
					return 0
				},
				Edit:  func(_ *testing.T, number int, body string) { s.Edit(testTeam, number, body) },
				Close: func(_ *testing.T, number int) { s.Close(testTeam, number) },
				Say: func(_ *testing.T, number int, author, body string) {
					s.Say(testTeam, number, author, body)
				},
				Disable: func(t *testing.T) {
					t.Fatal("nothing turns Linear's issues off: the subtest is skipped")
				},
				Down: func(*testing.T) { s.Down(true) },
				Issue: func(_ *testing.T, number int) contract.TrackedIssue {
					shown := s.Show(testTeam, number)
					got := contract.TrackedIssue{Title: shown.Title, Body: shown.Description, Author: shown.CreatedBy.User,
						Created: shown.Created, Closed: shown.StateType == lineartest.Completed ||
							shown.StateType == lineartest.Canceled || shown.StateType == lineartest.Duplicate}
					switch shown.StateType {
					case lineartest.Completed:
						got.Reason = ports.CloseCompleted
					case lineartest.Canceled, lineartest.Duplicate:
						got.Reason = ports.CloseNotPlanned
					}
					for _, c := range shown.Comments {
						got.Comments = append(got.Comments, contract.TrackedComment{Body: c.Body, Own: c.By == testAccount})
					}
					return got
				},
			}
		},
		Cannot: map[string]string{
			"never lists a pull request": "a pull request is not among Linear's items, so there is none to leave out",
			"says issues that are turned off are disabled": "nothing turns off the issues of a Linear team: " +
				"an item always exists, and a team that doesn't is TrackerNotFound",
		},
	})
}

// TestHasNoTeams: Linear's teams are where its items are, with no members
// the factory asks after (the plan's decision 13), so the adapter is no
// ports.TeamMembers and no TeamMembers suite runs on it.
func TestHasNoTeams(t *testing.T) {
	s := lineartest.New(t)
	s.Team(testTeam)
	if _, ok := tracker(t, s, testTeam, s.Account(testAccount)).(ports.TeamMembers); ok {
		t.Error("the Linear tracker is a ports.TeamMembers, want a tracker with no teams to ask about")
	}
}
