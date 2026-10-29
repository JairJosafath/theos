package fake

import (
	"context"
	"crypto/rand"
	"maps"

	"github.com/JairJosafath/c4factory/internal/ports"
)

// The fake host's agent access: a bot user per repository, with a fresh
// token each time.

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
	g.s.agentState().agents[repo.Owner+"/"+repo.Name] = user
	return ports.Credentials{Username: user, Token: rand.Text()}, nil
}

func (g *Git) RemoveAgentAccess(_ context.Context, _ ports.Repository, user string) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	if err := g.s.agentState().removeErrs[user]; err != nil {
		return err
	}
	// Deleting the user takes it off every repository.
	maps.DeleteFunc(g.s.agentState().agents, func(_, u string) bool { return u == user })
	return nil
}

func (g *Git) RevokeOldAgentTokens(_ context.Context, user string) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.agentState().revoked[user]++
	return nil
}

// Revocations is how often the operator revoked user's old tokens.
func (g *Git) Revocations(user string) int {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return g.s.agentState().revoked[user]
}
