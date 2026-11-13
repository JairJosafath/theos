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
	"testing"

	"github.com/JairJosafath/theos/internal/ports"
)

// noTeamsTracker is an IssueTracker without the TeamMembers capability, as a
// tracker that has no teams.
type noTeamsTracker struct{ ports.IssueTracker }

// teamsTracker is one with it: login is in the teams of in.
type teamsTracker struct {
	ports.IssueTracker
	in    map[string]string
	asked []string
}

func (f *teamsTracker) TeamMember(_ context.Context, team, login string) (bool, error) {
	f.asked = append(f.asked, team)
	return f.in[team] == login, nil
}

// TestInTeams covers #151: a team's members are an optional capability of a
// tracker (ports.TeamMembers). A tracker that has it is asked team by team
// until one has the login; one that doesn't fails with TrackerError when an
// entry names teams, so nobody is let in or turned away on a guess, and is
// not asked at all when it names none.
func TestInTeams(t *testing.T) {
	ctx := context.Background()
	teams := []string{"acme/maintainers", "acme/support"}

	with := &teamsTracker{in: map[string]string{"acme/support": "ada"}}
	if member, err := inTeams(ctx, with, teams, "ada"); err != nil || !member || len(with.asked) != 2 {
		t.Errorf("inTeams() of a member of the second team = %v, %v after asking %v; want true after both", member, err,
			with.asked)
	}
	if member, err := inTeams(ctx, with, teams, "grace"); err != nil || member {
		t.Errorf("inTeams() of a stranger = %v, %v; want false", member, err)
	}

	member, err := inTeams(ctx, noTeamsTracker{}, teams, "ada")
	if member || err == nil || ports.IssuesReason(err) != ports.ReasonTrackerError {
		t.Errorf("inTeams() on a tracker without teams = %v, %v; want false and a failure with the reason %s", member,
			err, ports.ReasonTrackerError)
	}
	if member, err := inTeams(ctx, noTeamsTracker{}, nil, "ada"); err != nil || member {
		t.Errorf("inTeams() with no team to ask = %v, %v; want false and no failure", member, err)
	}
}
