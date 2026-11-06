package fake

import (
	"context"
	"crypto/rand"
	"fmt"
	"maps"
	"slices"

	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// The fake host's agent access: a bot user per repository, with a fresh
// token each time that replaces the last, as the host rotates it.

// AgentUser returns the bot user given write access to owner/name.
func (g *Git) AgentUser(owner, name string) string {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.agentState().agents[owner+"/"+name]
}

// FailAgentRemoval makes deleting bot user fail with err from now on.
func (g *Git) FailAgentRemoval(user string, err error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.agentState().removeErrs[user] = err
}

func (g *Git) EnsureAgentAccess(_ context.Context, repo ports.Repository, user string) (ports.Credentials, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	token := rand.Text()
	g.s.agentState().agents[repo.Owner+"/"+repo.Name] = user
	g.s.agentState().tokens[user] = token
	return ports.Credentials{Username: user, Token: token}, nil
}

func (g *Git) RemoveAgentAccess(_ context.Context, _ ports.Repository, user string) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	if err := g.s.agentState().removeErrs[user]; err != nil {
		return err
	}
	// Deleting the user takes it off every repository.
	maps.DeleteFunc(g.s.agentState().agents, func(_, u string) bool { return u == user })
	delete(g.s.agentState().tokens, user)
	return nil
}

func (g *Git) RevokeOldAgentTokens(_ context.Context, user string) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.agentState().revoked[user]++
	return nil
}

// PushAs pushes files onto base as branch of repo with the credential as, as
// an agent's sandbox pushes, and returns the new head. The host refuses a
// token that isn't a bot's current one, a bot pushing to a repository it
// wasn't given, and a bot pushing to a protected branch, which only the
// operator may push to.
func (g *Git) PushAs(ctx context.Context, as ports.Credentials, repo ports.Repository, base, branch string,
	files scaffold.Tree) (string, error) {
	g.s.mu.Lock()
	s := g.s.agentState()
	key := repo.Owner + "/" + repo.Name
	var err error
	switch {
	case as.Token == "" || s.tokens[as.Username] != as.Token:
		err = fmt.Errorf("push to %s as %s: authentication failed", key, as.Username)
	case s.agents[key] != as.Username:
		err = fmt.Errorf("push to %s as %s: repository not found", key, as.Username)
	case slices.Contains(s.protected[key], branch):
		err = fmt.Errorf("push to %s of %s as %s: the branch is protected", branch, key, as.Username)
	}
	g.s.mu.Unlock()
	if err != nil {
		return "", err
	}
	author := ports.Signature{Name: as.Username}
	if branch == ports.DefaultBranch {
		return g.PushDefaultBranch(ctx, repo, base, files, nil, "Push as "+as.Username, author)
	}
	return g.PushBranch(ctx, repo, base, branch, files, nil, "Push as "+as.Username, author)
}

// Revocations is how often the operator revoked user's old tokens.
func (g *Git) Revocations(user string) int {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.agentState().revoked[user]
}
