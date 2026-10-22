package controller

import (
	"context"
	"maps"

	"github.com/JairJosafath/c4factory/internal/gitprovider"
)

// The fake host's Actions variables and secrets, per owner organization.

var _ gitprovider.OwnerActions = (*fakeProvider)(nil)

type fakeActionsState struct {
	variables map[string]map[string]string // owner -> name -> value
	secrets   map[string]map[string]string
}

func (g *fakeGit) actionsState() *fakeActionsState {
	if g.actions == nil {
		g.actions = &fakeActionsState{variables: map[string]map[string]string{}, secrets: map[string]map[string]string{}}
	}
	return g.actions
}

func setOwnerValue(all map[string]map[string]string, owner, name, value string) {
	if all[owner] == nil {
		all[owner] = map[string]string{}
	}
	all[owner][name] = value
}

func (p *fakeProvider) SetActionsVariable(_ context.Context, name, value string) error {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	setOwnerValue(p.git.actionsState().variables, p.cfg.Owner, name, value)
	return nil
}

func (p *fakeProvider) DeleteActionsVariable(_ context.Context, name string) error {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	delete(p.git.actionsState().variables[p.cfg.Owner], name)
	return nil
}

func (p *fakeProvider) SetActionsSecret(_ context.Context, name, value string) error {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	setOwnerValue(p.git.actionsState().secrets, p.cfg.Owner, name, value)
	return nil
}

func (p *fakeProvider) DeleteActionsSecret(_ context.Context, name string) error {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	delete(p.git.actionsState().secrets[p.cfg.Owner], name)
	return nil
}

// actionsVariables returns a copy of owner's Actions variables, empty for none.
func (g *fakeGit) actionsVariables(owner string) map[string]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return maps.Clone(nonNil(g.actionsState().variables[owner]))
}

// actionsSecrets returns a copy of owner's Actions secrets, empty for none.
func (g *fakeGit) actionsSecrets(owner string) map[string]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return maps.Clone(nonNil(g.actionsState().secrets[owner]))
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
