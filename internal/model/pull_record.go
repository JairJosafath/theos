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

package model

import "strings"

// What a Task keeps of its merged pull request for the factory's metrics
// (docs/plans/done/measure-and-improve.md, decisions 3 and 4): how many of its
// commits a person wrote, and how large it is.

// AgentEmailDomain is the domain of every agent's commit email,
// <AgentUsername>@AgentEmailDomain, as theos-agent configures git and the Git
// host's bot account is made.
const AgentEmailDomain = "agents.theos.factory.io"

// AgentEmail is the commit email of the agent bot user.
func AgentEmail(user string) string {
	return user + "@" + AgentEmailDomain
}

// PullCommit is one commit of a pull request, as PersonCommits reads it.
type PullCommit struct {
	AuthorEmail string
	// Parents is how many parents the commit has: a merge has two or more.
	Parents int
}

// PersonCommits counts the commits of a pull request a person wrote: those
// not by an agent (their email is under AgentEmailDomain) and not a merge,
// which is how the operator takes main into a branch, and is no code of
// anyone's.
func PersonCommits(commits []PullCommit) int {
	n := 0
	for _, c := range commits {
		if c.Parents > 1 || strings.HasSuffix(strings.ToLower(strings.TrimSpace(c.AuthorEmail)), "@"+AgentEmailDomain) {
			continue
		}
		n++
	}
	return n
}

// The sizes of a change, by its changed lines (lines added and removed), as
// the metrics compare a merged Task's cost within its size: S under 100, M
// under 500, L under 1000, XL beyond.
const (
	SizeS  = "S"
	SizeM  = "M"
	SizeL  = "L"
	SizeXL = "XL"
)

// ChangeSize is the size of a change of additions and deletions.
func ChangeSize(additions, deletions int) string {
	switch lines := additions + deletions; {
	case lines < 100:
		return SizeS
	case lines < 500:
		return SizeM
	case lines < 1000:
		return SizeL
	}
	return SizeXL
}
