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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/adapters/git"
	"github.com/JairJosafath/theos/internal/adapters/github/githubtest"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// agentsKey is the private key of the tests' GitHub Apps, made once for the
// run: no key is written down anywhere.
var agentsKey = sync.OnceValue(func() *rsa.PrivateKey {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return key
})

// agentsSecret registers a GitHub App with permissions on the fake,
// installed on account, and returns the provider's Secret that names it and
// the App's id.
func agentsSecret(gh *githubtest.Server, account string, permissions map[string]string) (map[string][]byte, int64) {
	id, _ := gh.AddApp("theos-agents", account, &agentsKey().PublicKey, permissions)
	return map[string][]byte{
		model.GitHubSecretAppID: []byte(strconv.FormatInt(id, 10)),
		model.GitHubSecretPrivateKey: pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(agentsKey())}),
	}, id
}

// pushAs pushes files onto base as branch of repo the way a sandbox does:
// git over HTTPS with the agent's credential.
func pushAs(as ports.Credentials, repo ports.Repository, base, branch string, files scaffold.Tree) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	author := ports.Signature{Name: as.Username, Email: model.AgentEmail(as.Username), When: time.Now()}
	var err error
	if branch == ports.DefaultBranch {
		_, err = git.PushDefaultBranch(ctx, repo.CloneURL, &as, base, files, nil, "Push as the agent", author)
	} else {
		_, err = git.PushBranch(ctx, repo.CloneURL, &as, base, branch, files, nil, "Push as the agent", author)
	}
	return err
}

// TestAgentAccessContract covers #77: the GitHub adapter passes the
// AgentAccess contract against the fake GitHub. The operator is the
// account that administers each organization, and a GitHub App installed on
// it mints the agents' credentials.
func TestAgentAccessContract(t *testing.T) {
	gh := githubtest.New(t, "ada", hostToken)
	orgs := 0
	contract.AgentAccess(t, contract.AgentAccessEnv{
		New: func(t *testing.T) (ports.GitHost, ports.AgentAccess) {
			t.Helper()
			orgs++
			org := fmt.Sprintf("agents-%d", orgs)
			gh.AddOrg(org)
			secret, _ := agentsSecret(gh, org, agentPermissions())
			h, err := (Client{}).Host(ports.Config{URL: gh.URL(), Owner: org,
				Credentials: ports.Credentials{Token: hostToken}, Secret: secret})
			if err != nil {
				t.Fatal(err)
			}
			return h, h
		},
		Push: func(_ *testing.T, as ports.Credentials, repo ports.Repository, base, branch string,
			files scaffold.Tree) error {
			return pushAs(as, repo, base, branch, files)
		},
		// GitHub can do all of it: nothing is skipped.
		Cannot: map[string]string{},
	})
	if calls := gh.Unserved(); len(calls) > 0 {
		t.Errorf("the adapter made calls the fake GitHub doesn't serve: %q", calls)
	}
}

// TestAgentTokenRequest covers #77: a run's token is asked for one
// repository and three permissions, and nothing else.
func TestAgentTokenRequest(t *testing.T) {
	for _, tt := range []struct {
		repo ports.Repository
		want string
	}{
		{ports.Repository{Owner: "acme", Name: "shop"},
			`{"repositories":["shop"],"permissions":{"contents":"write","issues":"read","pull_requests":"write"}}`},
		{ports.Repository{Owner: "acme", Name: "shop.api"},
			`{"repositories":["shop.api"],"permissions":{"contents":"write","issues":"read","pull_requests":"write"}}`},
	} {
		got, err := json.Marshal(tokenRequest(tt.repo))
		if err != nil || string(got) != tt.want {
			t.Errorf("the token of %s/%s is asked for with %s, %v; want %s", tt.repo.Owner, tt.repo.Name, got, err, tt.want)
		}
	}
	// What would let an agent out of the fence is never asked for.
	asked := agentPermissions()
	for _, never := range []string{"workflows", "administration", "actions_variables", "actions", "statuses", "checks",
		"secrets", "environments", "members", "organization_administration", "packages", "deployments"} {
		if level, ok := asked[never]; ok {
			t.Errorf("an agent's token asks for %s: %s", never, level)
		}
	}
	if len(asked) != 3 {
		t.Errorf("an agent's token asks for %v, want three permissions", asked)
	}
}

// TestAgentAccessStateOnGitHub covers #77: what a provider's condition says
// of agent access by what its Secret holds. A token alone is
// NeedsGitHubApp: the provider scaffolds and reads issues, and no agent
// gets a credential.
func TestAgentAccessStateOnGitHub(t *testing.T) {
	gh := githubtest.New(t, "ada", hostToken)
	gh.AddOrg("acme")
	whole, _ := agentsSecret(gh, "acme", agentPermissions())
	with := func(change func(map[string][]byte)) map[string][]byte {
		secret := map[string][]byte{}
		for k, v := range whole {
			secret[k] = v
		}
		change(secret)
		return secret
	}
	for _, tt := range []struct {
		name      string
		secret    map[string][]byte
		available bool
		reason    string
		says      string
	}{
		{"a token alone", nil, false, ReasonNeedsGitHubApp,
			"holds a token and no GitHub App, so agents get no credential: a person's token never reaches a sandbox. " +
				"Add appId and privateKey"},
		{"an App's id without its key", with(func(s map[string][]byte) { delete(s, model.GitHubSecretPrivateKey) }),
			false, ReasonNeedsGitHubApp, "it lacks privateKey"},
		{"an App's key without its id", with(func(s map[string][]byte) { delete(s, model.GitHubSecretAppID) }),
			false, ReasonNeedsGitHubApp, "it lacks appId"},
		{"an id that is no number", with(func(s map[string][]byte) { s[model.GitHubSecretAppID] = []byte("theos") }),
			false, ReasonGitHubAppInvalid, `appId "theos" is not a number`},
		{"a key that is no key", with(func(s map[string][]byte) { s[model.GitHubSecretPrivateKey] = []byte("no key") }),
			false, ReasonGitHubAppInvalid, "privateKey: no PEM block"},
		{"an installation that is no number", with(func(s map[string][]byte) {
			s[model.GitHubSecretInstallationID] = []byte("acme")
		}), false, ReasonGitHubAppInvalid, `installationId "acme" is not a number`},
		{"an App whole", whole, true, ports.ReasonAgentAccessAvailable, "Agents get a credential"},
		{"an App with its installation", with(func(s map[string][]byte) {
			s[model.GitHubSecretInstallationID] = []byte(" 42\n")
		}), true, ports.ReasonAgentAccessAvailable, "Agents get a credential"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, err := (Client{}).Host(ports.Config{URL: gh.URL(), Owner: "acme",
				Credentials: ports.Credentials{Token: hostToken}, Secret: tt.secret})
			if err != nil {
				t.Fatalf("Host() = %v: a provider without agent access still scaffolds", err)
			}
			available, reason, message := ports.AgentAccessState(h)
			if available != tt.available || reason != tt.reason || !strings.Contains(message, tt.says) {
				t.Errorf("AgentAccessState() = %v, %s, %q; want %v, %s, saying %q", available, reason, message,
					tt.available, tt.reason, tt.says)
			}
			if tt.available {
				return
			}
			_, err = h.EnsureAgentAccess(context.Background(), h.Repository("shop"), "acme-shop-agent")
			unavailable, ok := errors.AsType[*ports.AgentAccessUnavailable](err)
			if !ok || unavailable.Reason != tt.reason {
				t.Errorf("EnsureAgentAccess() = %v, want the same reason", err)
			}
		})
	}
	if calls := gh.Unserved(); len(calls) > 0 {
		t.Errorf("the adapter made calls the fake GitHub doesn't serve: %q", calls)
	}
}

// fenced is the shop with its main protected, its release tags the
// operator's, a second repository beside it, and a GitHub App that mints
// the agents' credentials.
type fenced struct {
	shop
	other ports.Repository
	app   int64
	agent string
}

func newFenced(t *testing.T) fenced {
	t.Helper()
	f := fenced{shop: newShop(t), agent: "acme-shop-agent"}
	secret, app := agentsSecret(f.gh, "acme", agentPermissions())
	h, err := (Client{}).Host(ports.Config{URL: f.gh.URL(), Owner: "acme", Credentials: ports.Credentials{Token: hostToken},
		Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	f.h, f.app = h, app
	if f.other, err = h.EnsureRepository(f.ctx, "other", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := h.PushInitialCommit(f.ctx, f.other, scaffold.Tree{shopReadme: {Data: []byte("other\n")}}, "Scaffold\n",
		shopAuthor); err != nil {
		t.Fatal(err)
	}
	if err := h.ProtectBranch(f.ctx, f.repo, ports.DefaultBranch, mergeRule); err != nil {
		t.Fatal(err)
	}
	if err := h.ProtectTags(f.ctx, f.repo, "v*"); err != nil {
		t.Fatal(err)
	}
	return f
}

// mint is the agent's credential for the shop, and the host as that
// credential sees it.
func (f fenced) mint(t *testing.T) (ports.Credentials, *host) {
	t.Helper()
	creds, err := f.h.EnsureAgentAccess(f.ctx, f.repo, f.agent)
	if err != nil {
		t.Fatal(err)
	}
	as, err := (Client{}).Host(ports.Config{URL: f.gh.URL(), Owner: "acme", Credentials: ports.Credentials{Token: creds.Token}})
	if err != nil {
		t.Fatal(err)
	}
	return creds, as.(*host)
}

// TestAgentFenceOnGitHub covers #77: what an agent's token does in its
// repository, and what holds it on GitHub.
func TestAgentFenceOnGitHub(t *testing.T) {
	f := newFenced(t)
	t.Run("the credential is the App's, for the agent, for an hour", f.credential)
	t.Run("it pushes its branch, opens its pull request and reads its issue, and writes no issue", f.works)
	t.Run("it can't merge its pull request, which the operator reviews and merges", f.merges)
	t.Run("it can't write main or a release tag over the API either", f.refs)
	t.Run("it can't change a workflow, on any branch", f.workflows)
	t.Run("it reaches no other repository", f.otherRepository)
	t.Run("it can't touch the rulesets, the kept rule or a check's status", f.settings)
}

func (f fenced) credential(t *testing.T) {
	before := time.Now()
	creds, _ := f.mint(t)
	if creds.Username != f.agent || creds.Token == "" || creds.Token == hostToken {
		t.Errorf("EnsureAgentAccess() = user %q, want %q with a token of its own", creds.Username, f.agent)
	}
	if left := creds.Expires.Sub(before); left <= 0 || left > time.Hour+time.Minute {
		t.Errorf("the credential expires in %s, want within GitHub's hour", left)
	}
	if n := f.gh.Tokens(f.app); n != 1 {
		t.Errorf("the App has %d live tokens, want the one", n)
	}
	if err := f.h.RemoveAgentAccess(f.ctx, f.repo, f.agent); err != nil {
		t.Fatal(err)
	}
}

func (f fenced) works(t *testing.T) {
	creds, as := f.mint(t)
	issue, err := f.h.CreateIssue(f.ctx, f.repo, "Add a.txt", "", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := pushAs(creds, f.repo, f.head, "task/works", scaffold.Tree{"src/a.txt": {Data: []byte("a\n")}}); err != nil {
		t.Fatalf("the agent's push of its branch = %v", err)
	}
	if _, err := as.OpenPullRequest(f.ctx, f.repo, "task/works", ports.DefaultBranch, "Add a.txt", ""); err != nil {
		t.Errorf("the agent opening its pull request = %v", err)
	}
	if got, err := as.GetIssue(f.ctx, f.repo, issue.Number); err != nil || got.Title != "Add a.txt" {
		t.Errorf("the agent reading its issue = %+v, %v", got, err)
	}
	if _, err := as.CreateIssue(f.ctx, f.repo, "By the agent", "", nil, 0); err == nil ||
		!strings.Contains(err.Error(), "Resource not accessible by integration") {
		t.Errorf("the agent filing an issue = %v, want GitHub's 403", err)
	}
}

func (f fenced) merges(t *testing.T) {
	creds, as := f.mint(t)
	if err := pushAs(creds, f.repo, f.head, "task/merge", scaffold.Tree{"src/m.txt": {Data: []byte("m\n")}}); err != nil {
		t.Fatal(err)
	}
	pr, err := as.OpenPullRequest(f.ctx, f.repo, "task/merge", ports.DefaultBranch, "Add m.txt", "")
	if err != nil {
		t.Fatal(err)
	}
	f.passed(pr.HeadSHA)
	// The check passed, and the agent's token writes contents: the ruleset
	// alone stands between it and main.
	err = as.ForceMergePullRequest(f.ctx, f.repo, pr.Number, pr.HeadSHA, "Merge", "")
	if err == nil || !strings.Contains(err.Error(), "Repository rule violations found") {
		t.Errorf("the agent merging its own pull request = %v, want the ruleset's refusal", err)
	}
	if f.merged(t, pr) {
		t.Fatal("the agent merged its own pull request")
	}
	// The pull request is the App's, so the operator's account may review it.
	if err := f.h.ReviewPullRequest(f.ctx, f.repo, pr.Number, pr.HeadSHA, ports.ReviewApprove, "Good"); err != nil {
		t.Errorf("the operator approving the agent's pull request = %v", err)
	}
	if err := as.ReviewPullRequest(f.ctx, f.repo, pr.Number, pr.HeadSHA, ports.ReviewApprove, "Mine"); err == nil {
		t.Error("the agent approved its own pull request")
	}
	if err := f.merge(pr); err != nil {
		t.Errorf("the operator merging the agent's pull request = %v", err)
	}
}

func (f fenced) refs(t *testing.T) {
	_, as := f.mint(t)
	main, _ := f.h.BranchCommit(f.ctx, f.repo, ports.DefaultBranch)
	for what, err := range map[string]error{
		"making a release tag": as.CreateTag(f.ctx, f.repo, "v9.0.0", main, "Release"),
		"deleting main":        as.DeleteBranch(f.ctx, f.repo, ports.DefaultBranch),
	} {
		if err == nil || !strings.Contains(err.Error(), "Repository rule violations found") {
			t.Errorf("the agent %s = %v, want the ruleset's refusal", what, err)
		}
	}
	if tag, _ := f.h.GetTag(f.ctx, f.repo, "v9.0.0"); tag != "" {
		t.Errorf("the agent's tag exists, at %s", tag)
	}
	if now, _ := f.h.BranchCommit(f.ctx, f.repo, ports.DefaultBranch); now != main {
		t.Errorf("main moved from %s to %q", main, now)
	}
	// A tag that is no release's, and a branch of its own, are the agent's to make.
	if err := as.CreateTag(f.ctx, f.repo, "try-1", main, "A try"); err != nil {
		t.Errorf("the agent making a tag no rule names = %v", err)
	}
	// The operator still makes the release's tag.
	if err := f.h.CreateTag(f.ctx, f.repo, "v9.0.0", main, "Release"); err != nil {
		t.Errorf("the operator making a release tag = %v", err)
	}
}

func (f fenced) workflows(t *testing.T) {
	creds, _ := f.mint(t)
	workflow := scaffold.Tree{".github/workflows/check.yml": {Data: []byte("on: push\n")}}
	if err := pushAs(creds, f.repo, f.head, "task/workflow", workflow); err == nil {
		t.Error("the agent pushed a branch that changes a workflow")
	}
	if said := f.gh.Refusals(); !strings.Contains(said, "refusing to allow a GitHub App to create or update workflow "+
		"`.github/workflows/check.yml` without `workflows` permission") {
		t.Errorf("GitHub refused the push saying %q, want the workflows permission named", said)
	}
	if has, _ := f.h.HasBranch(f.ctx, f.repo, "task/workflow"); has {
		t.Error("the branch with the workflow exists")
	}
	// The rest of .github/ it may push; the merge's own check holds that.
	other := scaffold.Tree{".github/CODEOWNERS": {Data: []byte("* @ada\n")}}
	if err := pushAs(creds, f.repo, f.head, "task/owners", other); err != nil {
		t.Errorf("the agent pushing a file beside the workflows = %v", err)
	}
	// The operator's own token changes workflows: a Standard's update does.
	main, _ := f.h.BranchCommit(f.ctx, f.repo, ports.DefaultBranch)
	if _, err := f.h.PushBranch(f.ctx, f.repo, main, "standard/update", workflow, nil, "Update the workflow",
		shopAuthor); err != nil {
		t.Errorf("the operator pushing a workflow = %v", err)
	}
}

func (f fenced) otherRepository(t *testing.T) {
	creds, as := f.mint(t)
	head, _ := f.h.BranchCommit(f.ctx, f.other, ports.DefaultBranch)
	if err := pushAs(creds, f.other, head, "task/other", scaffold.Tree{"a.txt": {Data: []byte("a\n")}}); err == nil {
		t.Error("the agent pushed to a repository its token wasn't minted for")
	}
	// A repository the token wasn't minted for is no repository to it: 404.
	if commit, _ := as.BranchCommit(f.ctx, f.other, ports.DefaultBranch); commit != "" {
		t.Errorf("the agent read main of a repository its token wasn't minted for: %s", commit)
	}
	if files, _, err := as.ReadFiles(f.ctx, f.other, ports.DefaultBranch, []string{shopReadme}); err == nil {
		t.Errorf("the agent read the files of a repository its token wasn't minted for: %v", files)
	}
	if _, err := as.EnsureRepository(f.ctx, "mine", ""); err == nil {
		t.Error("the agent made a repository")
	}
	if _, err := as.tokenUser(f.ctx); err == nil || !strings.Contains(err.Error(), "Resource not accessible by integration") {
		t.Errorf("the agent's token as a person = %v, want GitHub's 403", err)
	}
}

func (f fenced) settings(t *testing.T) {
	_, as := f.mint(t)
	main, _ := f.h.BranchCommit(f.ctx, f.repo, ports.DefaultBranch)
	variable := as.variablePath(f.repo) + "/" + rulesVariable
	status, _, err := as.do(f.ctx, http.MethodPatch, variable, map[string]string{"name": rulesVariable, "value": "{}"})
	if err != nil || status != http.StatusForbidden {
		t.Errorf("the agent writing the kept rule: HTTP %d, %v; want 403", status, err)
	}
	for what, err := range map[string]error{
		"protecting a branch": as.ProtectBranch(f.ctx, f.repo, "task/a", ports.BranchRule{}),
		"freeing the tags":    as.ProtectTags(f.ctx, f.repo, "x*"),
		"passing a check": as.SetCommitStatus(f.ctx, f.repo, main, ports.CommitStatus{Context: checkUnit,
			State: ports.CheckSuccess}),
	} {
		if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
			t.Errorf("the agent %s = %v, want GitHub's 403", what, err)
		}
	}
	if rule, err := f.h.(*host).branchRule(f.ctx, f.repo, ports.DefaultBranch); err != nil ||
		len(rule.ProtectedPaths) != len(mergeRule.ProtectedPaths) {
		t.Errorf("the rule of main after the agent's tries = %+v, %v", rule, err)
	}
}

// TestAgentTokensOnGitHub covers #77: an agent's tokens from the first to
// the last, and what the App must be to mint one.
func TestAgentTokensOnGitHub(t *testing.T) {
	f := newFenced(t)
	t.Run("a second token leaves the first to a run that holds it, until the old ones are revoked", f.rotates)
	t.Run("removing the access revokes every token of the agent, and no other agent's", f.removes)
	t.Run("an App that is not installed, or was given too little, mints nothing", f.cannotMint)
	t.Run("a token that may do more than was asked is no agent's token", widerTokens)
}

// usable says whether GitHub still takes creds for a push of a branch of
// its own.
func (f fenced) usable(creds ports.Credentials) bool {
	return pushAs(creds, f.repo, f.head, "task/"+rand.Text(), scaffold.Tree{"src/t.txt": {Data: []byte(rand.Text())}}) == nil
}

func (f fenced) rotates(t *testing.T) {
	first, _ := f.mint(t)
	second, _ := f.mint(t)
	if first.Token == second.Token || !f.usable(first) || !f.usable(second) {
		t.Fatalf("after a second mint the first token works: %v, the second: %v; want two tokens that do",
			f.usable(first), f.usable(second))
	}
	for range 2 {
		if err := f.h.RevokeOldAgentTokens(f.ctx, f.agent); err != nil {
			t.Fatalf("RevokeOldAgentTokens() = %v", err)
		}
	}
	if f.usable(first) || !f.usable(second) || f.gh.Tokens(f.app) != 1 {
		t.Errorf("after revoking the old ones the first token works: %v, the second: %v, and the App has %d; "+
			"want the second alone", f.usable(first), f.usable(second), f.gh.Tokens(f.app))
	}
	if err := f.h.RevokeOldAgentTokens(f.ctx, "nobody"); err != nil {
		t.Errorf("RevokeOldAgentTokens() of an agent with none = %v", err)
	}
	if err := f.h.RemoveAgentAccess(f.ctx, f.repo, f.agent); err != nil {
		t.Fatal(err)
	}
}

func (f fenced) removes(t *testing.T) {
	mine, _ := f.mint(t)
	again, _ := f.mint(t)
	theirs, err := f.h.EnsureAgentAccess(f.ctx, f.other, "acme-other-agent")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := f.h.RemoveAgentAccess(f.ctx, f.repo, f.agent); err != nil {
			t.Fatalf("RemoveAgentAccess() = %v", err)
		}
	}
	if f.usable(mine) || f.usable(again) {
		t.Error("a token of the removed agent still pushes")
	}
	head, _ := f.h.BranchCommit(f.ctx, f.other, ports.DefaultBranch)
	if err := pushAs(theirs, f.other, head, "task/theirs", scaffold.Tree{"a.txt": {Data: []byte("a\n")}}); err != nil {
		t.Errorf("the other agent's push after the removal = %v", err)
	}
	if n := f.gh.Tokens(f.app); n != 1 {
		t.Errorf("the App has %d live tokens, want the other agent's one", n)
	}
	if err := f.h.RemoveAgentAccess(f.ctx, f.other, "acme-other-agent"); err != nil {
		t.Fatal(err)
	}
}

func (f fenced) cannotMint(t *testing.T) {
	f.gh.AddOrg("elsewhere")
	notInstalled, _ := agentsSecret(f.gh, "elsewhere", agentPermissions())
	h, _ := (Client{}).Host(ports.Config{URL: f.gh.URL(), Owner: "acme", Credentials: ports.Credentials{Token: hostToken},
		Secret: notInstalled})
	_, err := h.EnsureAgentAccess(f.ctx, f.repo, f.agent)
	if err == nil || !strings.Contains(err.Error(), "is not installed on acme/shop: install it on acme") {
		t.Errorf("EnsureAgentAccess() with an App installed elsewhere = %v, want what to do", err)
	}

	tooLittle, _ := agentsSecret(f.gh, "acme", map[string]string{"contents": "read", "pull_requests": "write",
		"issues": "read"})
	h, _ = (Client{}).Host(ports.Config{URL: f.gh.URL(), Owner: "acme", Credentials: ports.Credentials{Token: hostToken},
		Secret: tooLittle})
	_, err = h.EnsureAgentAccess(f.ctx, f.repo, f.agent)
	if err == nil || !strings.Contains(err.Error(), "The permissions requested are not granted to this installation") ||
		!strings.Contains(err.Error(), "contents: write, pull_requests: write and issues: read") {
		t.Errorf("EnsureAgentAccess() with an App given too little = %v, want GitHub's words and the permissions", err)
	}

	wrong, _ := agentsSecret(f.gh, "acme", agentPermissions())
	wrong[model.GitHubSecretInstallationID] = []byte("999999")
	h, _ = (Client{}).Host(ports.Config{URL: f.gh.URL(), Owner: "acme", Credentials: ports.Credentials{Token: hostToken},
		Secret: wrong})
	if _, err := h.EnsureAgentAccess(f.ctx, f.repo, f.agent); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("EnsureAgentAccess() with an installation that is not the App's = %v, want the 404", err)
	}
}

func widerTokens(t *testing.T) {
	asked := tokenRequest(ports.Repository{Owner: "acme", Name: "shop"})
	for _, tt := range []struct {
		name         string
		permissions  map[string]string
		selection    string
		repositories int
		want         string
	}{
		{"what was asked", agentPermissions(), "selected", 1, ""},
		{"with the metadata every token reads", map[string]string{"contents": "write", "metadata": "read"}, "selected", 1, ""},
		{"less than asked", map[string]string{"contents": "read", "issues": "read"}, "selected", 1, ""},
		{"a permission not asked for", map[string]string{"contents": "write", "workflows": "write"}, "selected", 1,
			"may write workflows, which was not asked for"},
		{"more of a permission than asked", map[string]string{"issues": "write"}, "selected", 1,
			"may write issues, which was not asked for"},
		{"administration", map[string]string{"administration": "read"}, "selected", 1,
			"may read administration, which was not asked for"},
		{"every repository of the installation", agentPermissions(), "all", 0, "reaches more than the one repository"},
		{"two repositories", agentPermissions(), "selected", 2, "reaches more than the one repository"},
	} {
		if got := wider(asked, tt.permissions, tt.selection, tt.repositories); got != tt.want &&
			(tt.want == "" || !strings.Contains(got, tt.want)) {
			t.Errorf("%s: wider() = %q, want %q", tt.name, got, tt.want)
		}
	}
}
