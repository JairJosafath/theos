package fake

import (
	"context"
	"maps"
)

// The fake host's Actions variables and secrets, per owner organization.

type actionsStore struct {
	variables map[string]map[string]string // owner -> name -> value
	secrets   map[string]map[string]string
}

func (s *state) actionsState() *actionsStore {
	if s.actions == nil {
		s.actions = &actionsStore{variables: map[string]map[string]string{}, secrets: map[string]map[string]string{}}
	}
	return s.actions
}

func setOwnerValue(all map[string]map[string]string, owner, name, value string) {
	if all[owner] == nil {
		all[owner] = map[string]string{}
	}
	all[owner][name] = value
}

func (g *Git) SetActionsVariable(_ context.Context, name, value string) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	setOwnerValue(g.s.actionsState().variables, g.cfg.Owner, name, value)
	return nil
}

func (g *Git) DeleteActionsVariable(_ context.Context, name string) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	delete(g.s.actionsState().variables[g.cfg.Owner], name)
	return nil
}

func (g *Git) SetActionsSecret(_ context.Context, name, value string) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	setOwnerValue(g.s.actionsState().secrets, g.cfg.Owner, name, value)
	return nil
}

func (g *Git) DeleteActionsSecret(_ context.Context, name string) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	delete(g.s.actionsState().secrets[g.cfg.Owner], name)
	return nil
}

// ActionsVariables returns a copy of owner's Actions variables, empty for none.
func (g *Git) ActionsVariables(owner string) map[string]string {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return maps.Clone(nonNil(g.s.actionsState().variables[owner]))
}

// ActionsSecrets returns a copy of owner's Actions secrets, empty for none.
func (g *Git) ActionsSecrets(owner string) map[string]string {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return maps.Clone(nonNil(g.s.actionsState().secrets[owner]))
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
