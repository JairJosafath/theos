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
	"testing"

	"github.com/JairJosafath/theos/internal/ports"
)

// TeamMembersEnv builds the TeamMembers under test.
type TeamMembersEnv struct {
	// New returns a fresh tracker with a team nobody is in, and the
	// TeamMembers that speaks as the factory's account.
	New func(t *testing.T) TeamsHost
	// Cannot names the subtests the tracker can't run, with why; they are
	// skipped.
	Cannot map[string]string
}

// TeamsHost is a tracker's teams as the suite reaches them: through the
// capability as the factory's account, and beside it as the tracker's
// people and as the tracker itself.
type TeamsHost struct {
	// Members speaks as the factory's account, which may read the teams'
	// members.
	Members ports.TeamMembers
	// Refused is a TeamMembers of the same teams with a credential the
	// tracker doesn't know, and LacksMembers one with a credential that may
	// not read a team's members.
	Refused, LacksMembers ports.TeamMembers
	// Team is a team of the tracker without members, as TeamMember takes
	// it, and NoTeam the name of one the tracker doesn't have.
	Team, NoTeam string
	// Person makes a person's account and returns their login, as
	// TeamMember takes it.
	Person func(t *testing.T, name string) string
	// Join makes login an active member of team; Invite leaves login with
	// an invitation to team that isn't accepted.
	Join, Invite func(t *testing.T, team, login string)
	// Down makes the tracker answer nothing from now on, as an outage or a
	// rate limit does.
	Down func(t *testing.T)
}

// TeamMembers states what a Brief relies on of an IssueTracker that has
// teams (ports.TeamMembers, the port's optional capability): TeamMember is
// true for a team's active member alone, not for a stranger, not for a team
// the tracker doesn't have and not for an invitation that isn't accepted;
// and three failures have their reason, as ports.IssuesReason reads it, each
// with a TeamMember that is false: a credential the tracker doesn't know,
// one that may not read the members, and a tracker that doesn't answer.
//
// A tracker without teams doesn't implement the capability and doesn't run
// this suite. The names a team and a login may have stay in the adapter's
// tests.
func TeamMembers(t *testing.T, env TeamMembersEnv) {
	t.Helper()
	ctx, cancel := suiteContext()
	defer cancel()
	runSubtests(t, teamsSuite{ctx: ctx, env: env}, env.Cannot, []subtest[teamsSuite]{
		{"tells a team's active member from a stranger, and from a team it can't see", teamsSuite.members},
		{"does not count an invitation that isn't accepted", teamsSuite.invited},
		{"says a credential that may not read a team's members lacks them", teamsSuite.lacksMembers},
		{"says a credential the tracker doesn't know is unauthorized", teamsSuite.refused},
		{"says a tracker that doesn't answer is unavailable", teamsSuite.unavailable},
	})
}

// teamsSuite holds what every subtest of TeamMembers shares.
type teamsSuite struct {
	ctx context.Context
	env TeamMembersEnv
}

// joined makes a fresh tracker with ada in its team, and returns her login.
func (s teamsSuite) joined(t *testing.T) (TeamsHost, string) {
	t.Helper()
	h := s.env.New(t)
	ada := h.Person(t, authorAda)
	h.Join(t, h.Team, ada)
	return h, ada
}

// wantNoMember fails t unless a TeamMember of members for h's team and login
// failed with reason and said false.
func (s teamsSuite) wantNoMember(t *testing.T, h TeamsHost, members ports.TeamMembers, login, how, reason string) {
	t.Helper()
	member, err := members.TeamMember(s.ctx, h.Team, login)
	wantReason(t, "TeamMember()"+how, err, reason)
	if member {
		t.Error("TeamMember() that failed said the login is a member")
	}
}

func (s teamsSuite) members(t *testing.T) {
	h, ada := s.joined(t)
	grace := h.Person(t, authorGrace)
	for _, tt := range []struct {
		who, team, login string
		want             bool
	}{
		{"the team's member", h.Team, ada, true},
		{"a stranger to the team", h.Team, grace, false},
		{"a team the tracker doesn't have", h.NoTeam, ada, false},
	} {
		if got, err := h.Members.TeamMember(s.ctx, tt.team, tt.login); err != nil || got != tt.want {
			t.Errorf("TeamMember(%s, %s), %s = %v, %v; want %v", tt.team, tt.login, tt.who, got, err, tt.want)
		}
	}
}

func (s teamsSuite) invited(t *testing.T) {
	h := s.env.New(t)
	grace := h.Person(t, authorGrace)
	h.Invite(t, h.Team, grace)
	if got, err := h.Members.TeamMember(s.ctx, h.Team, grace); err != nil || got {
		t.Errorf("TeamMember() of someone invited = %v, %v; want false", got, err)
	}
	h.Join(t, h.Team, grace)
	if got, err := h.Members.TeamMember(s.ctx, h.Team, grace); err != nil || !got {
		t.Errorf("TeamMember() once the invitation is accepted = %v, %v; want true", got, err)
	}
}

func (s teamsSuite) lacksMembers(t *testing.T) {
	h, ada := s.joined(t)
	s.wantNoMember(t, h, h.LacksMembers, ada, " with a credential that may not read the members",
		ports.ReasonTokenLacksMembers)
}

func (s teamsSuite) refused(t *testing.T) {
	h, ada := s.joined(t)
	s.wantNoMember(t, h, h.Refused, ada, whenRefused, ports.ReasonUnauthorized)
}

func (s teamsSuite) unavailable(t *testing.T) {
	h, ada := s.joined(t)
	h.Down(t)
	s.wantNoMember(t, h, h.Members, ada, whenDown, ports.ReasonTrackerUnavailable)
}
