package gitea

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/JairJosafath/theos/internal/ports"
)

var _ ports.OwnerRunners = (*gitea)(nil)

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

// online is whether Gitea has heard from the runner lately.
func (r giteaRunner) online() bool {
	switch r.Status {
	case "online", "idle", "active":
		return true
	}
	return false
}

func (r giteaRunner) runner() ports.Runner {
	out := ports.Runner{ID: r.ID, Name: r.Name, Ephemeral: r.Ephemeral, Busy: r.Busy, Online: r.online()}
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

// Runners returns the runners registered to the owner organization.
func (g *gitea) Runners(ctx context.Context) ([]ports.Runner, error) {
	runners, err := g.listRunners(ctx, g.runnersPath(), fmt.Sprintf("%q", g.owner))
	if err != nil {
		return nil, err
	}
	out := make([]ports.Runner, 0, len(runners))
	for _, r := range runners {
		out = append(out, r.runner())
	}
	return out, nil
}

// listRunners reads the runners Gitea lists at path, of whose, at most
// runnerPages pages; a pool has far fewer. Gitea lists the runners it heard
// from last first, so one that calls it while the pages are read can move to
// an earlier page: a runner listed twice is returned once.
func (g *gitea) listRunners(ctx context.Context, path, whose string) ([]giteaRunner, error) {
	var out []giteaRunner
	seen := map[int64]bool{}
	for page := 1; page <= runnerPages; page++ {
		status, data, err := g.do(ctx, http.MethodGet, path+"?"+pageQuery(page).Encode(), nil)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("gitea: listing the runners of %s failed (HTTP %d): %s", whose, status, apiMessage(data))
		}
		var runners struct {
			Runners []giteaRunner `json:"runners"`
		}
		if err := json.Unmarshal(data, &runners); err != nil {
			return nil, fmt.Errorf("gitea: listing the runners of %s: %w", whose, err)
		}
		for _, r := range runners.Runners {
			if !seen[r.ID] {
				seen[r.ID] = true
				out = append(out, r)
			}
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
