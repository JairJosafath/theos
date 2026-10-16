package gitprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

var _ OwnerRunners = (*gitea)(nil)

// giteaRunner is a runner as Gitea's runners API lists it.
type giteaRunner struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Busy      bool   `json:"busy"`
	Ephemeral bool   `json:"ephemeral"`
	Labels    []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func (r giteaRunner) runner() Runner {
	out := Runner{ID: r.ID, Name: r.Name, Ephemeral: r.Ephemeral, Busy: r.Busy}
	switch r.Status {
	case "online", "idle", "active":
		out.Online = true
	}
	for _, l := range r.Labels {
		out.Labels = append(out.Labels, l.Name)
	}
	return out
}

// runnersPath is the owner organization's runners API.
func (g *gitea) runnersPath() string {
	return "/api/v1/orgs/" + url.PathEscape(g.owner) + "/actions/runners"
}

// RunnerRegistrationToken returns the owner organization's runner registration
// token. Gitea 1.27 serves it on POST only, and hands out the token it has
// until it is reset, making one only when there is none. It takes the
// organization's owner or an admin.
func (g *gitea) RunnerRegistrationToken(ctx context.Context) (string, error) {
	status, data, err := g.do(ctx, http.MethodPost, g.runnersPath()+"/registration-token", nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("gitea: reading the runner registration token of %q failed (HTTP %d): %s",
			g.owner, status, apiMessage(data))
	}
	var token struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &token); err != nil || token.Token == "" {
		return "", fmt.Errorf("gitea: reading the runner registration token of %q: no token in the answer", g.owner)
	}
	return token.Token, nil
}

// Runners returns the runners registered to the owner organization, reading
// at most runnerPages pages: a pool has far fewer, and the first pages are
// enough to clear dead registrations a few at a time.
func (g *gitea) Runners(ctx context.Context) ([]Runner, error) {
	var out []Runner
	for page := 1; page <= runnerPages; page++ {
		query := url.Values{"limit": {strconv.Itoa(issuePageSize)}, "page": {strconv.Itoa(page)}}
		status, data, err := g.do(ctx, http.MethodGet, g.runnersPath()+"?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("gitea: listing the runners of %q failed (HTTP %d): %s", g.owner, status, apiMessage(data))
		}
		var runners struct {
			Runners []giteaRunner `json:"runners"`
		}
		if err := json.Unmarshal(data, &runners); err != nil {
			return nil, fmt.Errorf("gitea: listing the runners of %q: %w", g.owner, err)
		}
		for _, r := range runners.Runners {
			out = append(out, r.runner())
		}
		if len(runners.Runners) < issuePageSize {
			break
		}
	}
	return out, nil
}

// DeleteRunner removes the owner organization's runner id; one Gitea doesn't
// have, gone already or never the organization's, is no error.
func (g *gitea) DeleteRunner(ctx context.Context, id int64) error {
	status, data, err := g.do(ctx, http.MethodDelete, g.runnersPath()+"/"+strconv.FormatInt(id, 10), nil)
	if err != nil {
		return err
	}
	switch status {
	case http.StatusNoContent, http.StatusOK, http.StatusNotFound:
		return nil
	default:
		return fmt.Errorf("gitea: deleting runner %d of %q failed (HTTP %d): %s", id, g.owner, status, apiMessage(data))
	}
}
