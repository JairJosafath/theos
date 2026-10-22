package gitprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

var _ OwnerActions = (*gitea)(nil)

// actionsPath is the owner organization's Actions variable or secret name,
// under kind ("variables" or "secrets").
func (g *gitea) actionsPath(kind, name string) string {
	return "/api/v1/orgs/" + url.PathEscape(g.owner) + "/actions/" + kind + "/" + url.PathEscape(name)
}

// SetActionsVariable creates or updates the owner organization's variable.
// Gitea updates on PUT and answers 404 for a variable it doesn't have, which
// POST then creates.
func (g *gitea) SetActionsVariable(ctx context.Context, name, value string) error {
	body, err := json.Marshal(map[string]string{"value": value})
	if err != nil {
		return err
	}
	status, data, err := g.do(ctx, http.MethodPut, g.actionsPath("variables", name), body)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		status, data, err = g.do(ctx, http.MethodPost, g.actionsPath("variables", name), body)
		if err != nil {
			return err
		}
	}
	switch status {
	case http.StatusCreated, http.StatusNoContent, http.StatusOK:
		return nil
	default:
		return fmt.Errorf("gitea: setting the Actions variable %s of %q failed (HTTP %d): %s",
			name, g.owner, status, apiMessage(data))
	}
}

// DeleteActionsVariable removes the owner organization's variable; one Gitea
// doesn't have is no error.
func (g *gitea) DeleteActionsVariable(ctx context.Context, name string) error {
	return g.deleteActions(ctx, "variables", name)
}

// SetActionsSecret creates or updates the owner organization's secret, which
// Gitea does on one PUT.
func (g *gitea) SetActionsSecret(ctx context.Context, name, value string) error {
	body, err := json.Marshal(map[string]string{"data": value})
	if err != nil {
		return err
	}
	status, data, err := g.do(ctx, http.MethodPut, g.actionsPath("secrets", name), body)
	if err != nil {
		return err
	}
	switch status {
	case http.StatusCreated, http.StatusNoContent, http.StatusOK:
		return nil
	default:
		return fmt.Errorf("gitea: setting the Actions secret %s of %q failed (HTTP %d): %s",
			name, g.owner, status, apiMessage(data))
	}
}

// DeleteActionsSecret removes the owner organization's secret; one Gitea
// doesn't have is no error.
func (g *gitea) DeleteActionsSecret(ctx context.Context, name string) error {
	return g.deleteActions(ctx, "secrets", name)
}

func (g *gitea) deleteActions(ctx context.Context, kind, name string) error {
	status, data, err := g.do(ctx, http.MethodDelete, g.actionsPath(kind, name), nil)
	if err != nil {
		return err
	}
	switch status {
	case http.StatusNoContent, http.StatusOK, http.StatusNotFound:
		return nil
	default:
		return fmt.Errorf("gitea: deleting the Actions %s %s of %q failed (HTTP %d): %s",
			kind[:len(kind)-1], name, g.owner, status, apiMessage(data))
	}
}
