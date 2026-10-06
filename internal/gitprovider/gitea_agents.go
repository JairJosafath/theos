package gitprovider

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Check states, as Gitea's combined commit status reports them. An empty state
// means no check has reported on the commit yet.
const (
	CheckPending = "pending"
	CheckSuccess = "success"
	CheckFailure = "failure"
	CheckError   = "error"
)

// CheckStatus is the combined state of the checks on one commit.
type CheckStatus struct {
	Commit string
	State  string
	URL    string
	// Contexts is the state of each check by its context, such as
	// "check / check (pull_request)".
	Contexts map[string]string
}

// PullRequest is a pull request on a repository.
type PullRequest struct {
	Number  int
	URL     string
	Head    string
	HeadSHA string
	State   string
	Merged  bool
	// Mergeable is false while the pull request conflicts with its base.
	Mergeable bool
	// MergeCommit is the commit the merge made on the base; empty until merged.
	MergeCommit string
}

// agentTokenName is the name of the access token made for an agent bot user.
const agentTokenName = "c4factory-agent"

// ProtectBranch makes branch accept changes only through pull requests whose
// status checks matching statusContexts (glob patterns) pass.
func (g *gitea) ProtectBranch(ctx context.Context, repo Repository, branch string, rule BranchRule) error {
	patterns := make([]string, 0, len(rule.ProtectedPaths))
	for _, p := range rule.ProtectedPaths {
		if strings.HasSuffix(p, "/") {
			p += "**"
		}
		patterns = append(patterns, p)
	}
	body, _ := json.Marshal(map[string]any{
		"rule_name":                 branch,
		"enable_push":               rule.OperatorPushes,
		"enable_push_whitelist":     rule.OperatorPushes,
		"push_whitelist_usernames":  pushers(rule, g.creds.Username),
		"enable_status_check":       len(rule.StatusContexts) > 0,
		"status_check_contexts":     rule.StatusContexts,
		"block_on_outdated_branch":  false,
		"enable_merge_whitelist":    true,
		"merge_whitelist_usernames": []string{g.creds.Username},
		"protected_file_patterns":   strings.Join(patterns, ";"),
	})
	base := "/api/v1/repos/" + repoPath(repo) + "/branch_protections"
	status, data, err := g.do(ctx, http.MethodPost, base, body)
	if err != nil {
		return err
	}
	switch status {
	case http.StatusCreated:
		return nil
	case http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity:
		// The rule exists already: bring it up to date.
		status, data, err = g.do(ctx, http.MethodPatch, base+"/"+url.PathEscape(branch), body)
		if err != nil {
			return err
		}
		if status == http.StatusOK {
			return nil
		}
	}
	return fmt.Errorf("gitea: protecting %s of %s failed (HTTP %d): %s", branch, repoPath(repo), status, apiMessage(data))
}

// pushers are the users that may push to a branch under rule.
func pushers(rule BranchRule, operator string) []string {
	if rule.OperatorPushes {
		return []string{operator}
	}
	return []string{}
}

// ProtectTags lets only the provider's user push tags matching pattern.
func (g *gitea) ProtectTags(ctx context.Context, repo Repository, pattern string) error {
	base := "/api/v1/repos/" + repoPath(repo) + "/tag_protections"
	status, data, err := g.do(ctx, http.MethodGet, base, nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("gitea: listing tag protections of %s failed (HTTP %d): %s", repoPath(repo), status, apiMessage(data))
	}
	var rules []struct {
		ID          int64  `json:"id"`
		NamePattern string `json:"name_pattern"`
	}
	if err := json.Unmarshal(data, &rules); err != nil {
		return fmt.Errorf("gitea: reading tag protections of %s: %w", repoPath(repo), err)
	}
	body, _ := json.Marshal(map[string]any{"name_pattern": pattern, "whitelist_usernames": []string{g.creds.Username}})
	method, path, want := http.MethodPost, base, http.StatusCreated
	for _, r := range rules {
		if r.NamePattern == pattern {
			method, path, want = http.MethodPatch, base+"/"+strconv.FormatInt(r.ID, 10), http.StatusOK
		}
	}
	status, data, err = g.do(ctx, method, path, body)
	if err != nil {
		return err
	}
	if status != want {
		return fmt.Errorf("gitea: protecting tags %s of %s failed (HTTP %d): %s", pattern, repoPath(repo), status, apiMessage(data))
	}
	return nil
}

// CombinedStatus returns the combined state of the checks on ref, and the latest
// state of each check by its context.
func (g *gitea) CombinedStatus(ctx context.Context, repo Repository, ref string) (CheckStatus, error) {
	status, data, err := g.do(ctx, http.MethodGet,
		"/api/v1/repos/"+repoPath(repo)+"/commits/"+url.PathEscape(ref)+"/status", nil)
	if err != nil {
		return CheckStatus{}, err
	}
	if status != http.StatusOK {
		return CheckStatus{}, fmt.Errorf("gitea: reading checks of %s at %s failed (HTTP %d): %s",
			repoPath(repo), ref, status, apiMessage(data))
	}
	var combined struct {
		State      string `json:"state"`
		SHA        string `json:"sha"`
		TotalCount int    `json:"total_count"`
		Statuses   []struct {
			Status    string `json:"status"`
			TargetURL string `json:"target_url"`
			Context   string `json:"context"`
		} `json:"statuses"`
	}
	if err := json.Unmarshal(data, &combined); err != nil {
		return CheckStatus{}, fmt.Errorf("gitea: reading checks of %s: %w", repoPath(repo), err)
	}
	out := CheckStatus{Commit: combined.SHA}
	if combined.TotalCount == 0 || len(combined.Statuses) == 0 {
		return out, nil
	}
	out.State = combined.State
	out.Contexts = make(map[string]string, len(combined.Statuses))
	for _, s := range combined.Statuses {
		out.Contexts[s.Context] = s.Status
	}
	// Point at the check that decided the state, else the first one.
	out.URL = combined.Statuses[0].TargetURL
	for _, s := range combined.Statuses {
		if s.Status == combined.State && s.TargetURL != "" {
			out.URL = s.TargetURL
			break
		}
	}
	return out, nil
}

// EnsureAgentAccess makes user a restricted bot account that can write to repo
// and nothing else, and returns a fresh access token for it. It needs an admin
// token. Calling it again rotates the token.
func (g *gitea) EnsureAgentAccess(ctx context.Context, repo Repository, user string) (Credentials, error) {
	password, err := randomSecret()
	if err != nil {
		return Credentials{}, err
	}
	account := map[string]any{
		"login_name":           user,
		"source_id":            0,
		"email":                user + "@agents.c4.factory.io",
		"password":             password,
		"must_change_password": false,
		"restricted":           true,
		"visibility":           "private",
	}
	status, data, err := g.do(ctx, http.MethodGet, "/api/v1/users/"+url.PathEscape(user), nil)
	if err != nil {
		return Credentials{}, err
	}
	switch status {
	case http.StatusNotFound:
		account["username"] = user
		body, _ := json.Marshal(account)
		status, data, err = g.do(ctx, http.MethodPost, "/api/v1/admin/users", body)
		if err != nil {
			return Credentials{}, err
		}
		if status != http.StatusCreated {
			return Credentials{}, fmt.Errorf("gitea: creating agent user %s failed (HTTP %d): %s", user, status, apiMessage(data))
		}
	case http.StatusOK:
		// Existing user: set a password we know, so we can make its token.
		body, _ := json.Marshal(account)
		status, data, err = g.do(ctx, http.MethodPatch, "/api/v1/admin/users/"+url.PathEscape(user), body)
		if err != nil {
			return Credentials{}, err
		}
		if status != http.StatusOK {
			return Credentials{}, fmt.Errorf("gitea: updating agent user %s failed (HTTP %d): %s", user, status, apiMessage(data))
		}
	default:
		return Credentials{}, fmt.Errorf("gitea: reading user %s failed (HTTP %d): %s", user, status, apiMessage(data))
	}

	body, _ := json.Marshal(map[string]string{"permission": "write"})
	status, data, err = g.do(ctx, http.MethodPut,
		"/api/v1/repos/"+repoPath(repo)+"/collaborators/"+url.PathEscape(user), body)
	if err != nil {
		return Credentials{}, err
	}
	if status != http.StatusNoContent {
		return Credentials{}, fmt.Errorf("gitea: adding %s to %s failed (HTTP %d): %s", user, repoPath(repo), status, apiMessage(data))
	}

	// Tokens are made by the user itself, with basic auth.
	as := &gitea{base: g.base, owner: g.owner, http: g.http, basic: &Credentials{Username: user, Token: password}}
	tokens := "/api/v1/users/" + url.PathEscape(user) + "/tokens"
	if _, _, err := as.do(ctx, http.MethodDelete, tokens+"/"+agentTokenName, nil); err != nil {
		return Credentials{}, err
	}
	body, _ = json.Marshal(map[string]any{"name": agentTokenName, "scopes": []string{"write:repository"}})
	status, data, err = as.do(ctx, http.MethodPost, tokens, body)
	if err != nil {
		return Credentials{}, err
	}
	if status != http.StatusCreated {
		return Credentials{}, fmt.Errorf("gitea: making a token for %s failed (HTTP %d): %s", user, status, apiMessage(data))
	}
	var token struct {
		SHA1 string `json:"sha1"`
	}
	if err := json.Unmarshal(data, &token); err != nil || token.SHA1 == "" {
		return Credentials{}, fmt.Errorf("gitea: reading the token for %s: %v", user, err)
	}
	return Credentials{Username: user, Token: token.SHA1}, nil
}

// RemoveAgentAccess deletes the bot user EnsureAgentAccess made, which drops its
// collaborator rights and tokens. It does not purge: what the bot wrote stays,
// shown as Gitea's ghost user. It needs an admin token.
func (g *gitea) RemoveAgentAccess(ctx context.Context, repo Repository, user string) error {
	status, data, err := g.do(ctx, http.MethodDelete, "/api/v1/admin/users/"+url.PathEscape(user), nil)
	if err != nil {
		return err
	}
	if status != http.StatusNoContent && status != http.StatusNotFound {
		return fmt.Errorf("gitea: deleting agent user %s of %s failed (HTTP %d): %s", user, repoPath(repo), status, apiMessage(data))
	}
	return nil
}

type giteaPull struct {
	Number    int    `json:"number"`
	HTMLURL   string `json:"html_url"`
	State     string `json:"state"`
	Merged    bool   `json:"merged"`
	Mergeable bool   `json:"mergeable"`
	// MergeCommit is merge_commit_sha, set once the pull request is merged.
	MergeCommit *string `json:"merge_commit_sha"`
	Head        struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
}

func (p giteaPull) pullRequest() PullRequest {
	out := PullRequest{Number: p.Number, URL: p.HTMLURL, Head: p.Head.Ref, HeadSHA: p.Head.SHA, State: p.State,
		Merged: p.Merged, Mergeable: p.Mergeable}
	if p.MergeCommit != nil {
		out.MergeCommit = *p.MergeCommit
	}
	return out
}

// OpenPullRequest opens a pull request from head into base, or returns the open
// one that already exists for head.
func (g *gitea) OpenPullRequest(ctx context.Context, repo Repository, head, base, title, body string) (PullRequest, error) {
	payload, _ := json.Marshal(map[string]string{"head": head, "base": base, "title": title, "body": body})
	status, data, err := g.do(ctx, http.MethodPost, "/api/v1/repos/"+repoPath(repo)+"/pulls", payload)
	if err != nil {
		return PullRequest{}, err
	}
	if status == http.StatusCreated {
		var pull giteaPull
		if err := json.Unmarshal(data, &pull); err != nil {
			return PullRequest{}, fmt.Errorf("gitea: reading the new pull request: %w", err)
		}
		return pull.pullRequest(), nil
	}
	if status != http.StatusConflict && status != http.StatusUnprocessableEntity {
		return PullRequest{}, fmt.Errorf("gitea: opening a pull request from %s failed (HTTP %d): %s", head, status, apiMessage(data))
	}
	// One is open already.
	status, data, err = g.do(ctx, http.MethodGet, "/api/v1/repos/"+repoPath(repo)+"/pulls?state=open&limit=50", nil)
	if err != nil {
		return PullRequest{}, err
	}
	var pulls []giteaPull
	if status != http.StatusOK || json.Unmarshal(data, &pulls) != nil {
		return PullRequest{}, fmt.Errorf("gitea: listing pull requests of %s failed (HTTP %d): %s", repoPath(repo), status, apiMessage(data))
	}
	for _, p := range pulls {
		if p.Head.Ref == head {
			return p.pullRequest(), nil
		}
	}
	return PullRequest{}, fmt.Errorf("gitea: opening a pull request from %s was refused and none is open", head)
}

// GetPullRequest reads a pull request.
func (g *gitea) GetPullRequest(ctx context.Context, repo Repository, number int) (PullRequest, error) {
	status, data, err := g.do(ctx, http.MethodGet, "/api/v1/repos/"+repoPath(repo)+"/pulls/"+strconv.Itoa(number), nil)
	if err != nil {
		return PullRequest{}, err
	}
	var pull giteaPull
	if status != http.StatusOK || json.Unmarshal(data, &pull) != nil {
		return PullRequest{}, fmt.Errorf("gitea: reading pull request %d of %s failed (HTTP %d): %s", number, repoPath(repo), status, apiMessage(data))
	}
	return pull.pullRequest(), nil
}

// CommentPullRequest adds a comment to a pull request.
func (g *gitea) CommentPullRequest(ctx context.Context, repo Repository, number int, body string) error {
	return g.CommentIssue(ctx, repo, number, body)
}

// MergePullRequest squashes a pull request into its base and deletes its branch.
func (g *gitea) MergePullRequest(ctx context.Context, repo Repository, number int, headSHA, title, message string) error {
	return g.merge(ctx, repo, number, headSHA, title, message, false)
}

// ForceMergePullRequest is MergePullRequest overriding the branch protection,
// which the admin user may do.
func (g *gitea) ForceMergePullRequest(ctx context.Context, repo Repository, number int, headSHA, title, message string) error {
	return g.merge(ctx, repo, number, headSHA, title, message, true)
}

func (g *gitea) merge(ctx context.Context, repo Repository, number int, headSHA, title, message string, force bool) error {
	body := map[string]any{
		"Do":                        "squash",
		"head_commit_id":            headSHA,
		"merge_title_field":         title,
		"merge_message_field":       message,
		"delete_branch_after_merge": true,
	}
	if force {
		body["force_merge"] = true
	}
	payload, _ := json.Marshal(body)
	status, data, err := g.do(ctx, http.MethodPost,
		"/api/v1/repos/"+repoPath(repo)+"/pulls/"+strconv.Itoa(number)+"/merge", payload)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("gitea: merging pull request %d failed (HTTP %d): %s", number, status, apiMessage(data))
	}
	return nil
}

// PullRequestFiles returns the paths a pull request changes, both the old and the
// new path of a renamed file. A list that fills all maxIssuePages pages is
// ErrTooManyFiles, not cut short: the caller may be checking every path.
func (g *gitea) PullRequestFiles(ctx context.Context, repo Repository, number int) ([]string, error) {
	var out []string
	for page := 1; ; page++ {
		if page > maxIssuePages {
			return nil, fmt.Errorf("gitea: pull request %d of %s changes %d files or more: %w",
				number, repoPath(repo), maxIssuePages*issuePageSize, ErrTooManyFiles)
		}
		status, data, err := g.do(ctx, http.MethodGet, fmt.Sprintf("/api/v1/repos/%s/pulls/%d/files?page=%d&limit=%d",
			repoPath(repo), number, page, issuePageSize), nil)
		if err != nil {
			return nil, err
		}
		var files []struct {
			Filename         string `json:"filename"`
			PreviousFilename string `json:"previous_filename"`
		}
		if status != http.StatusOK || json.Unmarshal(data, &files) != nil {
			return nil, fmt.Errorf("gitea: listing the files of pull request %d of %s failed (HTTP %d): %s",
				number, repoPath(repo), status, apiMessage(data))
		}
		for _, f := range files {
			out = append(out, f.Filename)
			if f.PreviousFilename != "" && f.PreviousFilename != f.Filename {
				out = append(out, f.PreviousFilename) // a rename deletes its old path
			}
		}
		if len(files) < issuePageSize {
			return out, nil
		}
	}
}

// ClosePullRequest closes a pull request without merging it.
func (g *gitea) ClosePullRequest(ctx context.Context, repo Repository, number int) error {
	payload, _ := json.Marshal(struct {
		State string `json:"state"`
	}{stateClosed})
	status, data, err := g.do(ctx, http.MethodPatch, "/api/v1/repos/"+repoPath(repo)+"/pulls/"+strconv.Itoa(number), payload)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return fmt.Errorf("gitea: closing pull request %d of %s failed (HTTP %d): %s", number, repoPath(repo), status, apiMessage(data))
	}
	return nil
}

func (g *gitea) DeleteBranch(ctx context.Context, repo Repository, branch string) error {
	// Gitea takes the branch's slashes as they are; each segment is escaped.
	segments := strings.Split(branch, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	status, data, err := g.do(ctx, http.MethodDelete,
		"/api/v1/repos/"+repoPath(repo)+"/branches/"+strings.Join(segments, "/"), nil)
	if err != nil {
		return err
	}
	if status != http.StatusNoContent && status != http.StatusNotFound {
		return fmt.Errorf("gitea: deleting branch %s of %s failed (HTTP %d): %s", branch, repoPath(repo), status, apiMessage(data))
	}
	return nil
}

func repoPath(repo Repository) string {
	return url.PathEscape(repo.Owner) + "/" + url.PathEscape(repo.Name)
}

func randomSecret() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// CheckLog returns the log of the CI job a check status links to, such as
// /apps/app/actions/runs/27/jobs/27.
func (g *gitea) CheckLog(ctx context.Context, repo Repository, checkURL string) (string, error) {
	_, job, ok := strings.Cut(checkURL, "/jobs/")
	job = strings.Trim(job, "/")
	if !ok || job == "" || strings.Contains(job, "/") {
		return "", fmt.Errorf("gitea: %q does not link to a CI job", checkURL)
	}
	status, data, err := g.do(ctx, http.MethodGet,
		"/api/v1/repos/"+repoPath(repo)+"/actions/jobs/"+url.PathEscape(job)+"/logs", nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("gitea: reading the log of job %s failed (HTTP %d): %s", job, status, apiMessage(data))
	}
	return string(data), nil
}
