package model

import "testing"

// measure-and-improve, decision 3: a person's commit is any commit on the
// pull request that isn't an agent's or a merge.
func TestPersonCommits(t *testing.T) {
	agent := AgentEmail(AgentUsername("shop", "api"))
	tests := []struct {
		name    string
		commits []PullCommit
		want    int
	}{
		{"none", nil, 0},
		{"the agent's alone", []PullCommit{{AuthorEmail: agent, Parents: 1}, {AuthorEmail: agent, Parents: 1}}, 0},
		{"another agent's, in any case", []PullCommit{{AuthorEmail: "Other-Agent@AGENTS.theos.factory.io", Parents: 1}}, 0},
		{"main merged in by the operator", []PullCommit{{AuthorEmail: "theos@example.com", Parents: 2}}, 0},
		{"a person's", []PullCommit{{AuthorEmail: agent, Parents: 1}, {AuthorEmail: "ada@example.com", Parents: 1},
			{AuthorEmail: "ada@example.com", Parents: 1}}, 2},
		{"a look-alike domain is a person's", []PullCommit{{AuthorEmail: "x@agents.theos.factory.io.example.com",
			Parents: 1}}, 1},
		{"no email is a person's", []PullCommit{{Parents: 1}}, 1},
	}
	for _, tt := range tests {
		if got := PersonCommits(tt.commits); got != tt.want {
			t.Errorf("%s: PersonCommits = %d, want %d", tt.name, got, tt.want)
		}
	}
}

// measure-and-improve, decision 4: sizes split at 100, 500 and 1000 changed
// lines.
func TestChangeSize(t *testing.T) {
	for _, tt := range []struct {
		add, del int
		want     string
	}{
		{0, 0, SizeS}, {60, 39, SizeS}, {60, 40, SizeM}, {499, 0, SizeM}, {250, 250, SizeL},
		{999, 0, SizeL}, {1000, 0, SizeXL}, {5000, 3000, SizeXL},
	} {
		if got := ChangeSize(tt.add, tt.del); got != tt.want {
			t.Errorf("ChangeSize(%d, %d) = %s, want %s", tt.add, tt.del, got, tt.want)
		}
	}
}
