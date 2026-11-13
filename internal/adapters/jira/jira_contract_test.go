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

package jira

import (
	"crypto/rand"
	"testing"

	"github.com/JairJosafath/theos/internal/adapters/jira/jiratest"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
)

const (
	testProject = "SHOP"
	testLabel   = "factory"
	testAccount = "theos-bot"
)

// tracker is an IssueTracker of project with testLabel on s, as the account
// of creds. change edits the config before the adapter is built.
func tracker(t *testing.T, s *jiratest.Server, project string, creds jiratest.Credentials,
	change ...func(*Config)) ports.IssueTracker {
	t.Helper()
	cfg := Config{Site: jiratest.SiteURL, CloudID: s.CloudID(), Project: project, Label: testLabel,
		Email: creds.Email, Token: creds.Token, Address: s.URL()}
	for _, c := range change {
		c(&cfg)
	}
	tr, err := Client{}.Issues(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// reasonOf is how an item shows closed to the suite: a resolution of Won't
// Do is not planned, any other completed.
func reasonOf(shown jiratest.Shown) ports.CloseReason {
	if shown.Resolution == jiratest.ResolutionWontDo {
		return ports.CloseNotPlanned
	}
	return ports.CloseCompleted
}

// TestIssueTrackerContract covers #157: the Jira adapter passes the
// IssueTracker contract on the fake Jira Cloud, each subtest on a site of
// its own. People file, edit, close and comment on the fake itself, not
// through the adapter; the items they file carry the label.
func TestIssueTrackerContract(t *testing.T) {
	contract.IssueTracker(t, contract.IssueTrackerEnv{
		New: func(t *testing.T) contract.TrackerHost {
			t.Helper()
			s := jiratest.New(t)
			s.Project(testProject)
			creds := s.Account(testAccount)
			return contract.TrackerHost{
				Tracker: tracker(t, s, testProject, creds),
				Account: testAccount,
				Refused: tracker(t, s, testProject, jiratest.Credentials{Email: creds.Email, Token: rand.Text()}),
				// A token that has none of the scopes: the gateway says so.
				LacksIssues: tracker(t, s, testProject, s.Restricted("limited")),
				Elsewhere:   tracker(t, s, "NOPE", creds),
				// A person's display name is their name.
				Person: func(_ *testing.T, name string) string { return name },
				File: func(_ *testing.T, author, title, body string) int {
					who := jiratest.Maker{Name: author}
					return s.File(jiratest.Item{Project: testProject, Title: title, Description: body,
						Labels: []string{testLabel}, Reporter: who, Creator: who})
				},
				PullRequest: func(t *testing.T, _, _ string) int {
					t.Fatal("Jira has no pull request among its items: the subtest is skipped")
					return 0
				},
				Edit:  func(_ *testing.T, number int, body string) { s.Edit(testProject, number, body) },
				Close: func(_ *testing.T, number int) { s.Finish(testProject, number) },
				Say:   func(_ *testing.T, number int, author, body string) { s.Say(testProject, number, author, body) },
				Disable: func(t *testing.T) {
					t.Fatal("nothing turns off a Jira project's issues: the subtest is skipped")
				},
				Down: func(*testing.T) { s.Down(true) },
				Issue: func(_ *testing.T, number int) contract.TrackedIssue {
					shown := s.Show(testProject, number)
					got := contract.TrackedIssue{Title: shown.Title, Body: shown.Description, Author: shown.Reporter,
						Created: shown.Created, Closed: shown.Category == jiratest.CategoryDone}
					if got.Closed {
						got.Reason = reasonOf(shown)
					}
					for _, c := range shown.Comments {
						got.Comments = append(got.Comments, contract.TrackedComment{Body: c.Body, Own: c.By == testAccount})
					}
					return got
				},
			}
		},
		Cannot: map[string]string{
			"never lists a pull request": "a pull request is not among Jira's items, so there is none to leave out",
			"says issues that are turned off are disabled": "nothing turns off the issues of a Jira project: " +
				"an item always exists, and a project that doesn't is TrackerNotFound",
			"closes a closed issue again, with the new reason, even one a person closed": "a closed item stays as it is " +
				"(decision 17): a Done item usually offers no transition into another Done status, its resolution can be " +
				"edited only where an admin put the field on a screen, and reopening it to close it again would run the " +
				"team's automations twice; TestClosedItemStaysClosed holds it",
		},
	})
}

// TestHasNoTeams: Jira Cloud's projects have no members the factory asks
// after (the plan's decision 13), so the adapter is no ports.TeamMembers and
// no TeamMembers suite runs on it.
func TestHasNoTeams(t *testing.T) {
	s := jiratest.New(t)
	s.Project(testProject)
	if _, ok := tracker(t, s, testProject, s.Account(testAccount)).(ports.TeamMembers); ok {
		t.Error("the Jira tracker is a ports.TeamMembers, want a tracker with no teams to ask about")
	}
}
