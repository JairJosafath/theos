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
	"github.com/JairJosafath/theos/internal/scaffold"
)

// AgentAccessEnv builds the AgentAccesses under test and pushes as an agent
// does on their host.
type AgentAccessEnv struct {
	// New returns the GitHost of a fresh owner that holds no repository,
	// where the suite makes the repositories, and the owner's AgentAccess.
	New func(t *testing.T) (ports.GitHost, ports.AgentAccess)
	// Push pushes a commit of files onto base as branch of repo with the
	// credential as, the way an agent's sandbox pushes, and returns the
	// host's refusal, if any.
	Push func(t *testing.T, as ports.Credentials, repo ports.Repository, base, branch string,
		files scaffold.Tree) error
	// Cannot names the subtests the host can't run, with why; they are
	// skipped.
	Cannot map[string]string
}

// AgentAccess states what the controllers rely on of every AgentAccess: the
// credential it mints writes to its repository and no other, can't push to
// a protected main, and is refused once the access is removed;
// RevokeOldAgentTokens keeps the current credential; removing and revoking
// are safe to repeat, and to call for a user that isn't there.
func AgentAccess(t *testing.T, env AgentAccessEnv) {
	t.Helper()
	ctx, cancel := suiteContext()
	defer cancel()
	runSubtests(t, agentSuite{ctx: ctx, env: env}, env.Cannot, []subtest[agentSuite]{
		{"writes its repository and no other", agentSuite.writesItsRepository},
		{"can't push to a protected main", agentSuite.protectedMain},
		{"is refused once removed", agentSuite.removed},
		{"keeps the current credential when revoking old ones", agentSuite.revokesOld},
	})
}

// agentSuite holds what every subtest of AgentAccess shares.
type agentSuite struct {
	ctx context.Context
	env AgentAccessEnv
}

// agentFile is what the agent pushes.
var agentFile = scaffold.Tree{"agent.txt": {Data: []byte("by the agent\n")}}

// seeded is a fresh owner's GitHost and AgentAccess with the repositories
// names, each holding the initial commit, and their heads. The agent's user is
// named after the owner, so it is fresh too, and it is removed when t ends.
func (s agentSuite) seeded(t *testing.T, names ...string) (ports.GitHost, ports.AgentAccess,
	[]ports.Repository, []string, string) {
	t.Helper()
	h, access := s.env.New(t)
	repos := make([]ports.Repository, 0, len(names))
	heads := make([]string, 0, len(names))
	for _, name := range names {
		repo := must(h.EnsureRepository(s.ctx, name, "the AgentAccess contract suite"))(t)
		repos = append(repos, repo)
		heads = append(heads, must(h.PushInitialCommit(s.ctx, repo, seedFiles, "Scaffold\n", contractAuthor))(t))
	}
	user := repos[0].Owner + "-agent"
	t.Cleanup(func() { _ = access.RemoveAgentAccess(context.Background(), repos[0], user) })
	return h, access, repos, heads, user
}

// mint is EnsureAgentAccess of user on repo, checked.
func (s agentSuite) mint(t *testing.T, access ports.AgentAccess, repo ports.Repository,
	user string) ports.Credentials {
	t.Helper()
	creds := must(access.EnsureAgentAccess(s.ctx, repo, user))(t)
	if creds.Username != user || creds.Token == "" {
		t.Fatalf("EnsureAgentAccess() = user %q with a token of %d bytes, want %q with one", creds.Username,
			len(creds.Token), user)
	}
	return creds
}

// agentBranch is the branch the agent pushes.
const agentBranch = "task/a"

// pushes fails t unless as may push agentBranch of repo, which then holds a
// commit.
func (s agentSuite) pushes(t *testing.T, h ports.GitHost, as ports.Credentials, repo ports.Repository, base string) {
	t.Helper()
	branch := agentBranch
	if err := s.env.Push(t, as, repo, base, branch, agentFile); err != nil {
		t.Fatalf("pushing %s of %s with the agent's credential = %v", branch, repo.Name, err)
	}
	if commit := must(h.BranchCommit(s.ctx, repo, branch))(t); commit == "" || commit == base {
		t.Errorf("after the agent's push %s of %s is at %q, want a new commit", branch, repo.Name, commit)
	}
}

// refused fails t unless the host refuses as pushing branch of repo, which
// stays at head ("" for none).
func (s agentSuite) refused(t *testing.T, h ports.GitHost, as ports.Credentials, repo ports.Repository, base,
	branch, head, why string) {
	t.Helper()
	if err := s.env.Push(t, as, repo, base, branch, agentFile); err == nil {
		t.Errorf("the agent pushed %s of %s, %s", branch, repo.Name, why)
	}
	if commit := must(h.BranchCommit(s.ctx, repo, branch))(t); commit != head {
		t.Errorf("after a refused push %s of %s is at %q, want %q", branch, repo.Name, commit, head)
	}
}

func (s agentSuite) writesItsRepository(t *testing.T) {
	h, access, repos, heads, user := s.seeded(t, contractRepo, otherRepo)
	creds := s.mint(t, access, repos[0], user)
	s.pushes(t, h, creds, repos[0], heads[0])
	s.refused(t, h, creds, repos[1], heads[1], agentBranch, "", "a repository it wasn't given")
}

func (s agentSuite) protectedMain(t *testing.T) {
	h, access, repos, heads, user := s.seeded(t, contractRepo)
	repo, head := repos[0], heads[0]
	if err := h.ProtectBranch(s.ctx, repo, ports.DefaultBranch, ports.BranchRule{StatusContexts: []string{checkGlob},
		OperatorPushes: true}); err != nil {
		t.Fatal(err)
	}
	creds := s.mint(t, access, repo, user)
	s.refused(t, h, creds, repo, head, ports.DefaultBranch, head, "a protected main")
	s.pushes(t, h, creds, repo, head)
}

func (s agentSuite) removed(t *testing.T) {
	h, access, repos, heads, user := s.seeded(t, contractRepo)
	repo, head := repos[0], heads[0]
	creds := s.mint(t, access, repo, user)
	s.pushes(t, h, creds, repo, head)
	for i := range 2 { // the second finds the access gone
		if err := access.RemoveAgentAccess(s.ctx, repo, user); err != nil {
			t.Fatalf("RemoveAgentAccess() #%d = %v", i+1, err)
		}
	}
	s.refused(t, h, creds, repo, head, "task/b", "", "once its access was removed")
}

func (s agentSuite) revokesOld(t *testing.T) {
	h, access, repos, heads, user := s.seeded(t, contractRepo)
	repo, head := repos[0], heads[0]
	if err := access.RevokeOldAgentTokens(s.ctx, user); err != nil {
		t.Errorf("RevokeOldAgentTokens() of a user that isn't there = %v", err)
	}
	creds := s.mint(t, access, repo, user)
	for range 2 {
		if err := access.RevokeOldAgentTokens(s.ctx, user); err != nil {
			t.Fatalf("RevokeOldAgentTokens() = %v", err)
		}
	}
	s.pushes(t, h, creds, repo, head)
}
