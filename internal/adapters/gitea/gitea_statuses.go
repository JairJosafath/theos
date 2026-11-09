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

package gitea

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/JairJosafath/theos/internal/ports"
)

// CommitStatuses returns the latest status of each check on commit sha.
func (g *gitea) CommitStatuses(ctx context.Context, repo ports.Repository, sha string) ([]ports.CommitStatus, error) {
	status, data, err := g.do(ctx, http.MethodGet,
		"/api/v1/repos/"+repoPath(repo)+"/commits/"+url.PathEscape(sha)+"/status", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("gitea: reading checks of %s at %s failed (HTTP %d): %s",
			repoPath(repo), sha, status, apiMessage(data))
	}
	var combined struct {
		Statuses []struct {
			Status      string `json:"status"`
			TargetURL   string `json:"target_url"`
			Context     string `json:"context"`
			Description string `json:"description"`
		} `json:"statuses"`
	}
	if err := json.Unmarshal(data, &combined); err != nil {
		return nil, fmt.Errorf("gitea: reading checks of %s: %w", repoPath(repo), err)
	}
	out := make([]ports.CommitStatus, 0, len(combined.Statuses))
	for _, s := range combined.Statuses {
		out = append(out, ports.CommitStatus{Context: s.Context, State: s.Status, TargetURL: s.TargetURL,
			Description: s.Description})
	}
	return out, nil
}

// SetCommitStatus writes a check's status onto commit sha.
func (g *gitea) SetCommitStatus(ctx context.Context, repo ports.Repository, sha string, s ports.CommitStatus) error {
	body, err := json.Marshal(struct {
		Context     string `json:"context"`
		State       string `json:"state"`
		TargetURL   string `json:"target_url"`
		Description string `json:"description"`
	}{s.Context, s.State, s.TargetURL, s.Description})
	if err != nil {
		return err
	}
	status, data, err := g.do(ctx, http.MethodPost,
		"/api/v1/repos/"+repoPath(repo)+"/statuses/"+url.PathEscape(sha), body)
	if err != nil {
		return err
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return fmt.Errorf("gitea: writing check %q of %s at %s failed (HTTP %d): %s",
			s.Context, repoPath(repo), sha, status, apiMessage(data))
	}
	return nil
}

// CommitTree returns the id of the tree commit sha holds.
func (g *gitea) CommitTree(ctx context.Context, repo ports.Repository, sha string) (string, error) {
	status, data, err := g.do(ctx, http.MethodGet,
		"/api/v1/repos/"+repoPath(repo)+"/git/commits/"+url.PathEscape(sha), nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("gitea: reading commit %s of %s failed (HTTP %d): %s",
			sha, repoPath(repo), status, apiMessage(data))
	}
	var commit struct {
		Commit struct {
			Tree struct {
				SHA string `json:"sha"`
			} `json:"tree"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(data, &commit); err != nil {
		return "", fmt.Errorf("gitea: reading commit %s of %s: %w", sha, repoPath(repo), err)
	}
	if commit.Commit.Tree.SHA == "" {
		return "", fmt.Errorf("gitea: commit %s of %s names no tree", sha, repoPath(repo))
	}
	return commit.Commit.Tree.SHA, nil
}
