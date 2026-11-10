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

package github

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// Agent credentials on GitHub, step 4 of docs/plans/github-host.md (#77):
// the fence's side of the adapter, with the branch rules of
// github_host_rules.go.
//
// An agent's credential is an installation token of a GitHub App, minted
// for one repository with the permissions a run needs and no other. The App
// is the agents' alone. The operator acts with the provider's own token, of
// an account that administers the repositories, because GitHub tells
// accounts apart, not tokens: a token of an App the operator also acted as
// would bypass the rulesets the operator bypasses, and nobody reviews a pull
// request they opened themselves. What holds an agent's token:
//
//   - another repository: the token is minted for one;
//   - main and the release tags: the rulesets, which the App is no bypass
//     actor of;
//   - a workflow: the workflows permission is never asked for, so GitHub
//     refuses a push that changes one, on any branch;
//   - the Standard's protected paths: MergePullRequest's own check;
//   - the rulesets, the kept rule and the repository's settings:
//     administration and the variables of Actions are never asked for.
//
// GitHub has no bot user to make: the user of a credential is the agent's
// name for the operator's own books, and its commits carry the agent's
// address as their author.

// The reasons a provider on GitHub has no agent access.
const (
	// ReasonNeedsGitHubApp: the Secret holds no App to mint with. A person's
	// token never reaches a sandbox.
	ReasonNeedsGitHubApp = "NeedsGitHubApp"
	// ReasonGitHubAppInvalid: the Secret names an App that can't be used as
	// it is written.
	ReasonGitHubAppInvalid = "GitHubAppInvalid"
)

// agentApp is the GitHub App that mints the agents' credentials.
type agentApp struct {
	id string
	// installation is the id of its installation on the owner; "" asks
	// GitHub for it.
	installation string
	key          *rsa.PrivateKey
}

// agentsOf reads the agents' App from a provider's Secret: appId and
// privateKey, and installationId where the Secret names the installation.
// Without an App it says why agents get no credential.
func agentsOf(secret map[string][]byte) (*agentApp, *ports.AgentAccessUnavailable) {
	value := func(key string) string { return strings.TrimSpace(string(secret[key])) }
	id, pem := value(model.GitHubSecretAppID), value(model.GitHubSecretPrivateKey)
	const permissions = "the repository permissions Contents (read and write), Pull requests (read and write) and " +
		"Issues (read-only)"
	switch {
	case id == "" && pem == "":
		return nil, &ports.AgentAccessUnavailable{Reason: ReasonNeedsGitHubApp, Message: "The provider's Secret holds " +
			"a token and no GitHub App, so agents get no credential: a person's token never reaches a sandbox. " +
			"Add " + model.GitHubSecretAppID + " and " + model.GitHubSecretPrivateKey + " of a GitHub App installed " +
			"on the owner with " + permissions + "."}
	case id == "" || pem == "":
		missing := model.GitHubSecretAppID
		if pem == "" {
			missing = model.GitHubSecretPrivateKey
		}
		return nil, &ports.AgentAccessUnavailable{Reason: ReasonNeedsGitHubApp, Message: "The provider's Secret " +
			"names a GitHub App in part: it lacks " + missing + ", so agents get no credential."}
	}
	invalid := func(format string, a ...any) (*agentApp, *ports.AgentAccessUnavailable) {
		return nil, &ports.AgentAccessUnavailable{Reason: ReasonGitHubAppInvalid, Message: "The provider's Secret " +
			"names a GitHub App that can't mint the agents' credentials: " + fmt.Sprintf(format, a...) + "."}
	}
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		return invalid("%s %q is not a number", model.GitHubSecretAppID, id)
	}
	installation := value(model.GitHubSecretInstallationID)
	if _, err := strconv.ParseInt(installation, 10, 64); installation != "" && err != nil {
		return invalid("%s %q is not a number", model.GitHubSecretInstallationID, installation)
	}
	key, err := appPrivateKey(secret[model.GitHubSecretPrivateKey])
	if err != nil {
		return invalid("%s: %v", model.GitHubSecretPrivateKey, err)
	}
	return &agentApp{id: id, installation: installation, key: key}, nil
}

// CheckAgentAccess says whether the provider's Secret names an App that can
// mint, without calling GitHub: nil, or why agents get no credential.
func (h *host) CheckAgentAccess() error {
	if h.noAgents != nil {
		return h.noAgents
	}
	return nil
}

// agentPermissions are what an agent's token may do in its repository: push
// its branch and open its pull request, and read the issue it works. Nothing
// else is ever asked for: not workflows, not administration, not the
// variables of Actions.
func agentPermissions() map[string]string {
	return map[string]string{"contents": mayWrite, "pull_requests": mayWrite, "issues": mayRead}
}

// The levels of a permission of an installation token.
const (
	mayRead  = "read"
	mayWrite = "write"
)

// agentTokenRequest is what a run's token is asked of GitHub with.
type agentTokenRequest struct {
	Repositories []string          `json:"repositories"`
	Permissions  map[string]string `json:"permissions"`
}

// tokenRequest asks for a token of repo alone, with agentPermissions.
func tokenRequest(repo ports.Repository) agentTokenRequest {
	return agentTokenRequest{Repositories: []string{repo.Name}, Permissions: agentPermissions()}
}

// agentTokens keeps the tokens minted for each agent of each repository
// until they expire, the newest last: an installation token is revoked with
// itself alone, so one the operator forgot can only run out, within the
// hour GitHub gives it. The operator builds a host anew for each call, so
// they are kept here.
var agentTokens = struct {
	sync.Mutex
	byKey map[string][]ports.Credentials
}{byKey: map[string][]ports.Credentials{}}

// agentsOfOwner is what the keys of the owner's agents' tokens start with;
// agentKey names those of user in repo.
func (h *host) agentsOfOwner() string { return h.api.String() + "\x00" + h.owner + "/" }

func (h *host) agentKey(repo ports.Repository, user string) string {
	return h.api.String() + "\x00" + repo.Owner + "/" + repo.Name + "\x00" + user
}

// installation returns the id of the App's installation that reaches repo.
func (h *host) installation(ctx context.Context, jwt string, repo ports.Repository) (string, error) {
	if h.agents.installation != "" {
		return h.agents.installation, nil
	}
	status, data, err := h.doAs(ctx, jwt, http.MethodGet, h.repoPath(repo)+"/installation", nil)
	if err != nil {
		return "", err
	}
	what := "finding the installation of GitHub App " + h.agents.id + " on " + repo.Owner + "/" + repo.Name
	switch status {
	case http.StatusOK:
	case http.StatusNotFound:
		return "", fmt.Errorf("github: GitHub App %s is not installed on %s/%s: install it on %s, for all of its "+
			"repositories or with this one among them", h.agents.id, repo.Owner, repo.Name, repo.Owner)
	default:
		return "", failed(what, status, data)
	}
	var found struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(data, &found); err != nil || found.ID == 0 {
		return "", fmt.Errorf("github: %s: no installation in the answer", what)
	}
	return strconv.FormatInt(found.ID, 10), nil
}

// EnsureAgentAccess mints a token for user that writes repo and no other
// repository, with agentPermissions, good for an hour. The tokens minted
// for user before stay as they are until RevokeOldAgentTokens or
// RemoveAgentAccess: a sandbox started with one may still be running.
func (h *host) EnsureAgentAccess(ctx context.Context, repo ports.Repository, user string) (ports.Credentials, error) {
	if h.noAgents != nil {
		return ports.Credentials{}, h.noAgents
	}
	jwt, err := AppJWT(h.agents.id, h.agents.key, h.now())
	if err != nil {
		return ports.Credentials{}, fmt.Errorf("github: signing as GitHub App %s: %w", h.agents.id, err)
	}
	installation, err := h.installation(ctx, jwt, repo)
	if err != nil {
		return ports.Credentials{}, err
	}
	asked := tokenRequest(repo)
	status, data, err := h.doAs(ctx, jwt, http.MethodPost, "/app/installations/"+installation+"/access_tokens", asked)
	if err != nil {
		return ports.Credentials{}, err
	}
	what := "minting the credential of " + user + " for " + repo.Owner + "/" + repo.Name
	if status != http.StatusCreated {
		return ports.Credentials{}, fmt.Errorf("%w; GitHub App %s must be installed on %s with the permissions "+
			"contents: write, pull_requests: write and issues: read, and reach this repository",
			failed(what, status, data), h.agents.id, repo.Owner)
	}
	var minted struct {
		Token       string            `json:"token"`
		ExpiresAt   time.Time         `json:"expires_at"`
		Permissions map[string]string `json:"permissions"`
		Selection   string            `json:"repository_selection"`
		Repos       []struct {
			Name string `json:"name"`
		} `json:"repositories"`
	}
	if err := json.Unmarshal(data, &minted); err != nil || minted.Token == "" {
		return ports.Credentials{}, fmt.Errorf("github: %s: no token in the answer", what)
	}
	creds := ports.Credentials{Username: user, Token: minted.Token, Expires: minted.ExpiresAt}
	if creds.Expires.IsZero() {
		creds.Expires = h.now().Add(time.Hour)
	}
	// A token that may do more than was asked is no agent's token, whatever
	// made GitHub give it: it is revoked, never handed on.
	if why := wider(asked, minted.Permissions, minted.Selection, len(minted.Repos)); why != "" {
		_ = h.revoke(ctx, creds.Token)
		return ports.Credentials{}, fmt.Errorf("github: %s: GitHub answered with a token that %s; it was revoked",
			what, why)
	}
	key := h.agentKey(repo, user)
	now := h.now()
	agentTokens.Lock()
	for k, kept := range agentTokens.byKey {
		live := kept[:0]
		for _, c := range kept {
			if now.Before(c.Expires) {
				live = append(live, c)
			}
		}
		if len(live) == 0 {
			delete(agentTokens.byKey, k)
		} else {
			agentTokens.byKey[k] = live
		}
	}
	agentTokens.byKey[key] = append(agentTokens.byKey[key], creds)
	agentTokens.Unlock()
	return creds, nil
}

// wider says how a minted token goes past what was asked: a permission that
// wasn't, or more than the one repository; "" when it doesn't.
func wider(asked agentTokenRequest, permissions map[string]string, selection string, repositories int) string {
	for permission, level := range permissions {
		// metadata is every installation token's: the repository's name and visibility.
		if permission == "metadata" && level == mayRead {
			continue
		}
		if want, ok := asked.Permissions[permission]; !ok || level != want && level != mayRead {
			return "may " + level + " " + permission + ", which was not asked for"
		}
	}
	if selection == "all" || repositories > len(asked.Repositories) {
		return "reaches more than the one repository asked for"
	}
	return ""
}

// revoke makes an installation token no token any more, which GitHub lets
// the token itself do. One GitHub no longer knows is revoked already.
func (h *host) revoke(ctx context.Context, token string) error {
	status, data, err := h.doAs(ctx, token, http.MethodDelete, "/installation/token", nil)
	if err != nil {
		return err
	}
	if status != http.StatusNoContent && status != http.StatusUnauthorized {
		return failed("revoking an agent's credential", status, data)
	}
	return nil
}

// revokeKept revokes the tokens kept under key, all of them or all but the
// newest, and forgets those it revoked.
func (h *host) revokeKept(ctx context.Context, key string, keepNewest bool) error {
	agentTokens.Lock()
	kept := append([]ports.Credentials(nil), agentTokens.byKey[key]...)
	agentTokens.Unlock()
	revoke := kept
	if keepNewest && len(kept) > 0 {
		revoke = kept[:len(kept)-1]
	}
	for _, c := range revoke {
		if err := h.revoke(ctx, c.Token); err != nil {
			return err
		}
		agentTokens.Lock()
		left := agentTokens.byKey[key][:0]
		for _, k := range agentTokens.byKey[key] {
			if k.Token != c.Token {
				left = append(left, k)
			}
		}
		if len(left) == 0 {
			delete(agentTokens.byKey, key)
		} else {
			agentTokens.byKey[key] = left
		}
		agentTokens.Unlock()
	}
	return nil
}

// RemoveAgentAccess revokes every token minted for user on repo that the
// operator still holds. GitHub has no bot user to delete, and a token
// minted before the operator last started can't be revoked: it runs out
// within the hour GitHub gave it. A user with none is no error.
func (h *host) RemoveAgentAccess(ctx context.Context, repo ports.Repository, user string) error {
	return h.revokeKept(ctx, h.agentKey(repo, user), false)
}

// RevokeOldAgentTokens revokes the tokens minted for user in the owner's
// repositories before the current one of each, and keeps that one. A user
// with none is no error.
func (h *host) RevokeOldAgentTokens(ctx context.Context, user string) error {
	agentTokens.Lock()
	keys := make([]string, 0, len(agentTokens.byKey))
	for key := range maps.Keys(agentTokens.byKey) {
		if strings.HasPrefix(key, h.agentsOfOwner()) && strings.HasSuffix(key, "\x00"+user) {
			keys = append(keys, key)
		}
	}
	agentTokens.Unlock()
	for _, key := range keys {
		if err := h.revokeKept(ctx, key, true); err != nil {
			return err
		}
	}
	return nil
}
