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

package fake_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
	"github.com/JairJosafath/theos/internal/ports/fake"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// newOwner returns a view of g for a fresh owner.
func newOwner(g *fake.Git, owners *int) *fake.Git {
	*owners++
	return g.For(ports.Config{URL: "http://host.test", Owner: fmt.Sprintf("contract-%d", *owners),
		Credentials: ports.Credentials{Username: "operator", Token: "operator-token"}})
}

func TestIssuesContract(t *testing.T) {
	g, owners := fake.New(), 0
	contract.Issues(t, contract.IssuesEnv{
		New: func(*testing.T) (ports.GitHost, ports.Issues) {
			h := newOwner(g, &owners)
			return h, h
		},
		Assignee: "operator",
	})
}

func TestCIContract(t *testing.T) {
	g, owners := fake.New(), 0
	contract.CI(t, contract.CIEnv{
		New: func(*testing.T) (ports.GitHost, ports.CI) {
			h := newOwner(g, &owners)
			return h, h
		},
		NoLog: func(_ *testing.T, _ ports.GitHost, repo ports.Repository) string {
			checkURL := repo.WebURL + "/actions/runs/1/jobs/1"
			g.SetNoCheckLog(checkURL)
			return checkURL
		},
		Queue: func(_ *testing.T, _ ports.GitHost, repo ports.Repository, label string) {
			g.SetWaitingJobs(repo.Owner, ports.WaitingJob{ID: 1, RunID: 1, Name: "check", Labels: []string{label},
				URL: repo.WebURL + "/actions/runs/1/jobs/1"})
		},
	})
}

func TestAgentAccessContract(t *testing.T) {
	g, owners := fake.New(), 0
	contract.AgentAccess(t, contract.AgentAccessEnv{
		New: func(*testing.T) (ports.GitHost, ports.AgentAccess) {
			h := newOwner(g, &owners)
			return h, h
		},
		Push: func(_ *testing.T, as ports.Credentials, repo ports.Repository, base, branch string,
			files scaffold.Tree) error {
			_, err := g.PushAs(context.Background(), as, repo, base, branch, files)
			return err
		},
	})
}

func TestRegistryContract(t *testing.T) {
	g, owners := fake.New(), 0
	contract.Registry(t, contract.RegistryEnv{
		New: func(*testing.T) (ports.GitHost, ports.Registry) {
			h := newOwner(g, &owners)
			return h, h
		},
		// The fake keeps a go.mod apart from the files pushed: the
		// controllers' tests set it with SetGoMod.
		GoMod: func(_ *testing.T, repo ports.Repository, path string) {
			g.SetGoMod(repo.Owner, repo.Name, path)
		},
	})
}

// must is v, failing t on err.
func must[T any](v T, err error) func(*testing.T) T {
	return func(t *testing.T) T {
		t.Helper()
		noErr(t, err)
		return v
	}
}

// noErr fails t on err.
func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// chatServer is a fresh h as the ChatHost suite reaches it, with its bot.
func chatServer(t *testing.T, h *fake.ChatHost) contract.ChatServer {
	t.Helper()
	bot, token := h.NewUser("bot")
	host := must(h.Hosts()(ports.ChatConfig{Address: h.Address(), Token: token}))(t)
	return contract.ChatServer{
		Host:    host,
		Refused: h.For(ports.ChatConfig{Address: h.Address(), Token: rand.Text()}),
		Person: func(_ *testing.T, name string) string {
			user, _ := h.NewUser(name)
			return user
		},
		Join: func(t *testing.T, room, user string) { noErr(t, h.Join(room, user)) },
		Post: func(t *testing.T, room, user, text, threadRoot string) string {
			return must(h.Post(room, user, text, threadRoot))(t)
		},
		Amend: func(t *testing.T, room, user, message, text string) string {
			return must(h.Amend(room, user, message, text))(t)
		},
		Squat:  func(t *testing.T, alias string) { must(h.Squat(alias))(t) },
		Remove: func(t *testing.T, room string) { noErr(t, h.Remove(room, bot)) },
		Room: func(t *testing.T, room string) contract.ChatRoomView {
			t.Helper()
			r, ok := h.Room(room)
			if !ok {
				t.Fatalf("the chat host has no room %s", room)
			}
			view := contract.ChatRoomView{Name: r.Name, Topic: r.Topic, Private: r.Private, Members: r.Members}
			for _, p := range r.Posts {
				view.Posts = append(view.Posts, contract.ChatPosted{ID: p.ID, Sender: p.Sender, Text: p.Text,
					ThreadRoot: p.ThreadRoot, Reactions: p.Reactions})
			}
			return view
		},
	}
}

// TestChatHostContract covers #153: the fake chat host passes the ChatHost
// suite shaped as a host with Spaces and a display name per room, as
// Matrix is, and doesn't need to skip anything.
func TestChatHostContract(t *testing.T) {
	contract.ChatHost(t, contract.ChatHostEnv{
		New:    func(t *testing.T) contract.ChatServer { return chatServer(t, fake.NewChatHost()) },
		Cannot: map[string]string{},
	})
}

// TestChatHostContractWithoutSpaces covers #153: shaped as Slack is, with
// no Space and a channel's one name, the fake skips the two subtests of
// decision 22 in docs/plans/requests-and-chat.md and runs the rest.
func TestChatHostContractWithoutSpaces(t *testing.T) {
	contract.ChatHost(t, contract.ChatHostEnv{
		New: func(t *testing.T) contract.ChatServer {
			return chatServer(t, fake.NewChatHost(fake.WithoutSpaces(), fake.OneNamePerRoom()))
		},
		Cannot: map[string]string{
			"makes a Space and the rooms in it, each a room of its own": "the host has no Spaces: the " +
				"workspace is the Space (decision 5)",
			"brings a room's name to the one asked for": "a room's one name is its alias, as a channel's " +
				"(decision 4)",
		},
	})
}

// trackerProject is the project the IssueTracker suites read.
const trackerProject = "acme/shop"

// TestIssueTrackerContract covers #153: the fake tracker passes the
// IssueTracker suite, each subtest on a tracker of its own, and skips
// nothing.
func TestIssueTrackerContract(t *testing.T) {
	contract.IssueTracker(t, contract.IssueTrackerEnv{
		New: func(*testing.T) contract.TrackerHost {
			tr := fake.NewIssueTracker()
			tr.Project(trackerProject)
			const account = "theos-bot"
			token := tr.Account(account, fake.TrackerAccess{Issues: true})
			return contract.TrackerHost{
				Tracker:     tr.For(trackerProject, token),
				Account:     account,
				Refused:     tr.For(trackerProject, rand.Text()),
				LacksIssues: tr.For(trackerProject, tr.Account("reader", fake.TrackerAccess{Members: true})),
				Elsewhere:   tr.For("acme/nowhere", token),
				// A person's login is their name.
				Person: func(_ *testing.T, name string) string { return name },
				File: func(t *testing.T, author, title, body string) int {
					return must(tr.File(trackerProject, author, title, body))(t)
				},
				PullRequest: func(t *testing.T, author, title string) int {
					return must(tr.PullRequest(trackerProject, author, title))(t)
				},
				Edit: func(t *testing.T, number int, body string) {
					noErr(t, tr.Edit(trackerProject, number, body))
				},
				Close: func(t *testing.T, number int) { noErr(t, tr.Close(trackerProject, number)) },
				Say: func(t *testing.T, number int, author, body string) {
					noErr(t, tr.Say(trackerProject, number, author, body))
				},
				Disable: func(*testing.T) { tr.Disable(trackerProject) },
				Down:    func(*testing.T) { tr.Down(true) },
				Issue: func(t *testing.T, number int) contract.TrackedIssue {
					t.Helper()
					i, ok := tr.Issue(trackerProject, number)
					if !ok {
						t.Fatalf("the tracker has no issue %d", number)
					}
					got := contract.TrackedIssue{Title: i.Title, Body: i.Body, Author: i.Author, Created: i.Created,
						Closed: i.Closed, Reason: i.Reason}
					for _, c := range i.Comments {
						got.Comments = append(got.Comments, contract.TrackedComment{Body: c.Body, Own: c.Author == account})
					}
					return got
				},
			}
		},
		Cannot: map[string]string{},
	})
}

// TestTeamMembersContract covers #153: the fake tracker has teams, so its
// port is a ports.TeamMembers and passes that capability's suite.
func TestTeamMembersContract(t *testing.T) {
	contract.TeamMembers(t, contract.TeamMembersEnv{
		New: func(t *testing.T) contract.TeamsHost {
			t.Helper()
			tr := fake.NewIssueTracker()
			tr.Project(trackerProject)
			tr.Team("acme/maintainers")
			members := func(token string) ports.TeamMembers {
				m, ok := tr.For(trackerProject, token).(ports.TeamMembers)
				if !ok {
					t.Fatal("the port of a tracker with teams is no ports.TeamMembers")
				}
				return m
			}
			return contract.TeamsHost{
				Members:      members(tr.Account("theos-bot", fake.TrackerAccess{Issues: true, Members: true})),
				Refused:      members(rand.Text()),
				LacksMembers: members(tr.Account("issues-only", fake.TrackerAccess{Issues: true})),
				Team:         "acme/maintainers",
				NoTeam:       "acme/nobody",
				// A person's login is their name.
				Person: func(_ *testing.T, name string) string { return name },
				Join:   func(t *testing.T, team, login string) { noErr(t, tr.Join(team, login)) },
				Invite: func(t *testing.T, team, login string) { noErr(t, tr.Invite(team, login)) },
				Down:   func(*testing.T) { tr.Down(true) },
			}
		},
		Cannot: map[string]string{},
	})
}

// TestTrackerWithoutTeams covers #153: a tracker without teams gives a port
// that is no ports.TeamMembers, as the Brief finds the capability by a type
// assertion and fails an entry that names teams there.
func TestTrackerWithoutTeams(t *testing.T) {
	tr := fake.NewIssueTracker(fake.WithoutTeams())
	tr.Project(trackerProject)
	port := tr.For(trackerProject, tr.Account("theos-bot", fake.TrackerAccess{Issues: true, Members: true}))
	if _, ok := port.(ports.TeamMembers); ok {
		t.Error("the port of a tracker without teams is a ports.TeamMembers")
	}
	if _, err := port.ListOpenIssues(context.Background(), ""); err != nil {
		t.Errorf("ListOpenIssues() of a tracker without teams = %v", err)
	}
}

// TestIssueTrackersBuildForASource covers #154: ports.IssueTrackers builds
// the tracker of a source with its tracker's adapter, here the fake, which
// reads the credential out of the Secret's data; a source of a tracker with
// no adapter, and a Secret without the credential, fail with the port's
// errors.
func TestIssueTrackersBuildForASource(t *testing.T) {
	ctx := context.Background()
	tr := fake.NewIssueTracker()
	tr.Project(trackerProject)
	if _, err := tr.File(trackerProject, "ada", "Dark theme", "Please."); err != nil {
		t.Fatal(err)
	}
	token := tr.Account("theos-bot", fake.TrackerAccess{Issues: true})
	trackers := ports.IssueTrackers{"fake": tr}
	source := ports.TrackerSource{Tracker: "fake", Project: trackerProject}

	port, err := trackers.Tracker(ctx, source, map[string][]byte{fake.CredentialKey: []byte(token)})
	if err != nil {
		t.Fatalf("Tracker() error = %v", err)
	}
	if list, err := port.ListOpenIssues(ctx, ""); err != nil || len(list.Issues) != 1 {
		t.Errorf("ListOpenIssues() = %+v, %v; want the one issue", list, err)
	}

	for name, secret := range map[string]map[string][]byte{"no Secret": nil, "no key": {"other": []byte(token)}} {
		_, err := trackers.Tracker(ctx, source, secret)
		if missing, ok := errors.AsType[*ports.CredentialMissingError](err); !ok ||
			missing.Keys != "the key "+fake.CredentialKey {
			t.Errorf("%s: Tracker() error = %v, want a CredentialMissingError naming the key", name, err)
		}
	}

	_, err = trackers.Tracker(ctx, ports.TrackerSource{Tracker: "other", Project: trackerProject},
		map[string][]byte{fake.CredentialKey: []byte(token)})
	if ports.IssuesReason(err) != ports.ReasonTrackerError || !strings.Contains(err.Error(), `"other"`) {
		t.Errorf("Tracker() of a tracker with no adapter = %v, want %s naming it", err, ports.ReasonTrackerError)
	}
}

// TestLaggingListAndIssueOpen covers #161: a tracker whose list lags goes on
// listing an issue closed, as Jira's search index can for minutes; IssueOpen
// reads the issue on its own and says it is closed, unlabelled or not there.
func TestLaggingListAndIssueOpen(t *testing.T) {
	ctx := context.Background()
	tr := fake.NewIssueTracker()
	tr.Project(trackerProject)
	port := tr.For(trackerProject, tr.Account("theos-bot", fake.TrackerAccess{Issues: true}))
	reader, ok := port.(ports.IssueReader)
	if !ok {
		t.Fatal("the fake tracker's port is no ports.IssueReader")
	}
	closed, _ := tr.File(trackerProject, "ada", "Closed", "")
	unlabelled, _ := tr.File(trackerProject, "ada", "Unlabelled", "")
	open, _ := tr.File(trackerProject, "ada", "Open", "")
	pull, _ := tr.PullRequest(trackerProject, "ada", "A pull request")
	tr.Lag(trackerProject, true)
	if err := port.CloseIssue(ctx, closed, ports.CloseNotPlanned); err != nil {
		t.Fatal(err)
	}
	if err := tr.Unlabel(trackerProject, unlabelled); err != nil {
		t.Fatal(err)
	}
	listed := func() []int {
		list, err := port.ListOpenIssues(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		out := make([]int, 0, len(list.Issues))
		for _, i := range list.Issues {
			out = append(out, i.Number)
		}
		return out
	}
	if got := listed(); fmt.Sprint(got) != fmt.Sprint([]int{closed, open}) {
		t.Errorf("a lagging list = %v, want the closed issue still listed: %v", got, []int{closed, open})
	}
	for n, want := range map[int]bool{closed: false, unlabelled: false, open: true, pull: false, 99: false} {
		if got, err := reader.IssueOpen(ctx, n); err != nil || got != want {
			t.Errorf("IssueOpen(%d) = %v, %v; want %v", n, got, err, want)
		}
	}
	tr.Lag(trackerProject, false)
	if got := listed(); fmt.Sprint(got) != fmt.Sprint([]int{open}) {
		t.Errorf("the list caught up = %v, want %v", got, []int{open})
	}
	tr.Down(true)
	if _, err := reader.IssueOpen(ctx, open); ports.IssuesReason(err) != ports.ReasonTrackerUnavailable {
		t.Errorf("IssueOpen() of a tracker that is down = %v, want %s", err, ports.ReasonTrackerUnavailable)
	}
}
