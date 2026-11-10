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
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// Pull requests on GitHub, step 2 of docs/plans/github-host.md (#75).

// ghPull is a pull request as GitHub gives it.
type ghPull struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	State   string `json:"state"`
	Merged  bool   `json:"merged"`
	// Mergeable is null while GitHub works it out in the background.
	Mergeable *bool `json:"mergeable"`
	// MergeCommitSHA is the merge's commit once merged; before, it is the
	// commit of GitHub's test merge and means nothing to the port.
	MergeCommitSHA *string `json:"merge_commit_sha"`
	Head           struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Additions    int `json:"additions"`
	Deletions    int `json:"deletions"`
	ChangedFiles int `json:"changed_files"`
	Commits      int `json:"commits"`
}

// newBody is a call's body that is a text alone: a comment, a release's notes.
type newBody struct {
	Body string `json:"body"`
}

func (h *host) pullPath(repo ports.Repository, number int) string {
	return h.repoPath(repo) + "/pulls/" + strconv.Itoa(number)
}

func pullName(repo ports.Repository, number int) string {
	return fmt.Sprintf("pull request %d of %s/%s", number, repo.Owner, repo.Name)
}

// getPull reads one pull request as GitHub gives it; ErrPullRequestNotFound
// without it.
func (h *host) getPull(ctx context.Context, repo ports.Repository, number int) (ghPull, error) {
	status, data, err := h.do(ctx, http.MethodGet, h.pullPath(repo, number), nil)
	if err != nil {
		return ghPull{}, err
	}
	switch status {
	case http.StatusOK:
	case http.StatusNotFound:
		return ghPull{}, fmt.Errorf("github: %s: %w", pullName(repo, number), ports.ErrPullRequestNotFound)
	default:
		return ghPull{}, failed("reading "+pullName(repo, number), status, data)
	}
	var p ghPull
	if err := json.Unmarshal(data, &p); err != nil || p.Number == 0 {
		return ghPull{}, fmt.Errorf("github: reading %s: no pull request in the answer", pullName(repo, number))
	}
	return p, nil
}

// pull is p as the port has it. Behind is asked of GitHub's comparison of the
// head with its base: mergeable_state says "behind" only on a branch whose
// rule wants it up to date.
func (h *host) pull(ctx context.Context, repo ports.Repository, p ghPull) (ports.PullRequest, error) {
	out := ports.PullRequest{Number: p.Number, URL: p.HTMLURL, Head: p.Head.Ref, HeadSHA: p.Head.SHA, State: p.State,
		Merged: p.Merged, Mergeable: p.Mergeable != nil && *p.Mergeable, Additions: p.Additions,
		Deletions: p.Deletions, ChangedFiles: p.ChangedFiles}
	if p.Merged && p.MergeCommitSHA != nil {
		out.MergeCommit = *p.MergeCommitSHA
	}
	if p.State != "open" {
		return out, nil
	}
	status, data, err := h.do(ctx, http.MethodGet, h.repoPath(repo)+"/compare/"+pathEscapeRef(p.Base.Ref)+"..."+
		pathEscapeRef(p.Head.SHA), nil)
	if err != nil {
		return ports.PullRequest{}, err
	}
	switch status {
	case http.StatusOK:
		var cmp struct {
			BehindBy int `json:"behind_by"`
		}
		if err := json.Unmarshal(data, &cmp); err != nil {
			return ports.PullRequest{}, fmt.Errorf("github: comparing %s with its base: %w", pullName(repo, p.Number), err)
		}
		out.Behind = cmp.BehindBy > 0
	case http.StatusNotFound:
		// The base branch is gone: nothing to be behind of.
	default:
		return ports.PullRequest{}, failed("comparing "+pullName(repo, p.Number)+" with its base", status, data)
	}
	return out, nil
}

// OpenPullRequest opens a pull request from head into base, or returns the
// open one of head: GitHub refuses a second with 422.
func (h *host) OpenPullRequest(ctx context.Context, repo ports.Repository, head, base, title, body string) (
	ports.PullRequest, error) {
	status, data, err := h.do(ctx, http.MethodPost, h.repoPath(repo)+"/pulls", struct {
		Title string `json:"title"`
		Head  string `json:"head"`
		Base  string `json:"base"`
		Body  string `json:"body"`
	}{title, head, base, body})
	if err != nil {
		return ports.PullRequest{}, err
	}
	what := "opening a pull request from " + head + " in " + repo.Owner + "/" + repo.Name
	switch status {
	case http.StatusCreated:
		var p ghPull
		if err := json.Unmarshal(data, &p); err != nil || p.Number == 0 {
			return ports.PullRequest{}, fmt.Errorf("github: %s: no pull request in the answer", what)
		}
		return h.pull(ctx, repo, p)
	case http.StatusUnprocessableEntity:
		if !strings.Contains(string(data), "already exists") {
			return ports.PullRequest{}, failed(what, status, data)
		}
	default:
		return ports.PullRequest{}, failed(what, status, data)
	}
	open, err := h.list(ctx, h.repoPath(repo)+"/pulls",
		url.Values{"head": {repo.Owner + ":" + head}, "base": {base}, "state": {"open"}})
	if err != nil {
		return ports.PullRequest{}, err
	}
	if len(open) == 0 {
		return ports.PullRequest{}, failed(what, status, data)
	}
	var listed ghPull
	if err := json.Unmarshal(open[0], &listed); err != nil || listed.Number == 0 {
		return ports.PullRequest{}, fmt.Errorf("github: %s: no pull request in the list of its open ones", what)
	}
	// The list leaves out what one pull request's own answer has: its size
	// and whether it can merge.
	return h.GetPullRequest(ctx, repo, listed.Number)
}

// GetPullRequest reads a pull request; ErrPullRequestNotFound without it.
func (h *host) GetPullRequest(ctx context.Context, repo ports.Repository, number int) (ports.PullRequest, error) {
	p, err := h.getPull(ctx, repo, number)
	if err != nil {
		return ports.PullRequest{}, err
	}
	return h.pull(ctx, repo, p)
}

// CommentPullRequest adds a comment to a pull request, which on GitHub is a
// comment on the issue of its number.
func (h *host) CommentPullRequest(ctx context.Context, repo ports.Repository, number int, body string) error {
	status, data, err := h.do(ctx, http.MethodPost, h.issuePath(repo, number)+"/comments", newBody{body})
	if err != nil {
		return err
	}
	if status != http.StatusCreated {
		return failed("commenting on "+pullName(repo, number), status, data)
	}
	return nil
}

// MergePullRequest squashes a pull request into its base and deletes its
// branch, provided its head is still headSHA and it passes the base branch's
// rule. On GitHub the operator bypasses the ruleset that fences the branch,
// and no host setting holds a branch's protected paths, so the rule
// ProtectBranch kept is checked here, before the merge: no file of the pull
// request is under a protected path, and every status pattern has a check on
// headSHA that passed (docs/plans/github-host.md, "What step 2 found").
func (h *host) MergePullRequest(ctx context.Context, repo ports.Repository, number int, headSHA, title,
	message string) error {
	return h.merge(ctx, repo, number, headSHA, title, message, false)
}

// ForceMergePullRequest is MergePullRequest without the rule's check: the
// operator's own Standard, operations and release merges, whose files it
// read itself.
func (h *host) ForceMergePullRequest(ctx context.Context, repo ports.Repository, number int, headSHA, title,
	message string) error {
	return h.merge(ctx, repo, number, headSHA, title, message, true)
}

func (h *host) merge(ctx context.Context, repo ports.Repository, number int, headSHA, title, message string,
	force bool) error {
	p, err := h.getPull(ctx, repo, number)
	if err != nil {
		return err
	}
	if p.Head.SHA != headSHA {
		return fmt.Errorf("github: %s is at %s, not %s: it moved since it was checked", pullName(repo, number),
			p.Head.SHA, headSHA)
	}
	if p.Merged {
		// A merge whose answer was lost: the head asked for is the one merged.
		_ = h.DeleteBranch(ctx, repo, p.Head.Ref)
		return nil
	}
	if !force {
		rule, err := h.branchRule(ctx, repo, p.Base.Ref)
		if err != nil {
			return err
		}
		why, err := h.refusal(ctx, repo, number, headSHA, rule)
		if err != nil {
			return err
		}
		if why != "" {
			return fmt.Errorf("github: merging %s is refused by the rule of %s: %s", pullName(repo, number),
				p.Base.Ref, why)
		}
	}
	// sha makes GitHub refuse the merge (409) when the head moved meanwhile.
	status, data, err := h.do(ctx, http.MethodPut, h.pullPath(repo, number)+"/merge", struct {
		Title   string `json:"commit_title"`
		Message string `json:"commit_message"`
		SHA     string `json:"sha"`
		Method  string `json:"merge_method"`
	}{title, message, headSHA, "squash"})
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return failed("merging "+pullName(repo, number), status, data)
	}
	// The repository deletes a merged branch itself when it was made with
	// delete_branch_on_merge; one that wasn't is deleted here. The merge is
	// done either way, so a branch that stays is no failure of it.
	_ = h.DeleteBranch(ctx, repo, p.Head.Ref)
	return nil
}

// refusal says why the rule refuses the merge of a pull request at headSHA,
// or "" when it passes.
func (h *host) refusal(ctx context.Context, repo ports.Repository, number int, headSHA string,
	rule ports.BranchRule) (string, error) {
	if len(rule.ProtectedPaths) > 0 {
		files, err := h.PullRequestFiles(ctx, repo, number)
		if err != nil {
			return "", err
		}
		var protected []string
		for _, f := range files {
			if underPaths(f, rule.ProtectedPaths) {
				protected = append(protected, f)
			}
		}
		if len(protected) > 0 {
			return "it changes " + strings.Join(protected, ", ") + ", which no pull request may", nil
		}
	}
	if len(rule.StatusContexts) > 0 {
		checks, err := h.CombinedStatus(ctx, repo, headSHA)
		if err != nil {
			return "", err
		}
		if unpassed := model.UnpassedChecks(rule.StatusContexts, checks.Contexts); len(unpassed) > 0 {
			return "no check matching " + strings.Join(unpassed, ", ") + " has passed on " + headSHA, nil
		}
	}
	return "", nil
}

// underPaths reports whether file is one of paths or below one that is a
// directory, ending in "/".
func underPaths(file string, paths []string) bool {
	for _, p := range paths {
		if file == p || strings.HasSuffix(p, "/") && strings.HasPrefix(file, p) {
			return true
		}
	}
	return false
}

// UpdatePullRequest merges the base into the pull request's branch. GitHub
// does it in the background (202), so the head moves a moment later; a branch
// that conflicts answers 422, and so does one that has all of its base, which
// is no error.
func (h *host) UpdatePullRequest(ctx context.Context, repo ports.Repository, number int) error {
	status, data, err := h.do(ctx, http.MethodPut, h.pullPath(repo, number)+"/update-branch", map[string]string{})
	if err != nil {
		return err
	}
	switch {
	case status == http.StatusAccepted:
		return nil
	case status == http.StatusUnprocessableEntity && strings.Contains(string(data), "merge conflict"):
		return fmt.Errorf("github: updating %s: %w", pullName(repo, number), ports.ErrConflict)
	case status == http.StatusUnprocessableEntity && strings.Contains(string(data), "no new commits"):
		return nil
	}
	return failed("updating "+pullName(repo, number), status, data)
}

// maxPullFiles and maxPullCommits are how many files and commits of a pull
// request GitHub lists; past them the list is cut short without a word.
const (
	maxPullFiles   = 3000
	maxPullCommits = 250
)

// PullRequestFiles returns every path a pull request changes, a renamed
// file's old path too; ErrTooManyFiles when GitHub lists fewer than the pull
// request changes.
func (h *host) PullRequestFiles(ctx context.Context, repo ports.Repository, number int) ([]string, error) {
	p, err := h.getPull(ctx, repo, number)
	if err != nil {
		return nil, err
	}
	raw, err := h.list(ctx, h.pullPath(repo, number)+"/files", nil)
	if err != nil {
		if p.ChangedFiles > maxPullFiles {
			return nil, fmt.Errorf("github: %s changes %d files: %w", pullName(repo, number), p.ChangedFiles,
				ports.ErrTooManyFiles)
		}
		return nil, err
	}
	if len(raw) < p.ChangedFiles {
		return nil, fmt.Errorf("github: %s changes %d files and GitHub lists %d: %w", pullName(repo, number),
			p.ChangedFiles, len(raw), ports.ErrTooManyFiles)
	}
	var paths []string
	for _, r := range raw {
		var f struct {
			Filename string `json:"filename"`
			Previous string `json:"previous_filename"`
		}
		if err := json.Unmarshal(r, &f); err != nil || f.Filename == "" {
			return nil, fmt.Errorf("github: reading the files of %s: no filename in the answer", pullName(repo, number))
		}
		paths = append(paths, f.Filename)
		if f.Previous != "" {
			paths = append(paths, f.Previous)
		}
	}
	return paths, nil
}

// PullRequestCommits returns a pull request's commits, oldest first, also
// once it is merged and its branch deleted. It fails for more commits than
// GitHub lists.
func (h *host) PullRequestCommits(ctx context.Context, repo ports.Repository, number int) ([]ports.PullCommit, error) {
	p, err := h.getPull(ctx, repo, number)
	if err != nil {
		return nil, err
	}
	if p.Commits > maxPullCommits {
		return nil, fmt.Errorf("github: %s has %d commits, and GitHub lists %d", pullName(repo, number), p.Commits,
			maxPullCommits)
	}
	raw, err := h.list(ctx, h.pullPath(repo, number)+"/commits", nil)
	if err != nil {
		return nil, err
	}
	commits := make([]ports.PullCommit, 0, len(raw))
	for _, r := range raw {
		var c struct {
			SHA    string `json:"sha"`
			Commit struct {
				Author struct {
					Email string `json:"email"`
				} `json:"author"`
			} `json:"commit"`
			Parents []struct {
				SHA string `json:"sha"`
			} `json:"parents"`
		}
		if err := json.Unmarshal(r, &c); err != nil || c.SHA == "" {
			return nil, fmt.Errorf("github: reading the commits of %s: no sha in the answer", pullName(repo, number))
		}
		commits = append(commits, ports.PullCommit{SHA: c.SHA, AuthorEmail: c.Commit.Author.Email,
			Parents: len(c.Parents)})
	}
	return commits, nil
}

// ClosePullRequest closes a pull request without merging it.
func (h *host) ClosePullRequest(ctx context.Context, repo ports.Repository, number int) error {
	status, data, err := h.do(ctx, http.MethodPatch, h.pullPath(repo, number), map[string]string{"state": "closed"})
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return failed("closing "+pullName(repo, number), status, data)
	}
	return nil
}

// The review events GitHub takes, by the port's, and the port's by the
// states GitHub answers.
var (
	reviewEvents = map[string]string{ports.ReviewApprove: "APPROVE", ports.ReviewRequestChanges: "REQUEST_CHANGES",
		ports.ReviewComment: "COMMENT"}
	reviewStates = map[string]string{"APPROVED": ports.ReviewApprove, "CHANGES_REQUESTED": ports.ReviewRequestChanges,
		"COMMENTED": ports.ReviewComment}
)

// ReviewPullRequest posts a review of commit. GitHub lets no account approve,
// or ask for changes on, a pull request it opened itself.
func (h *host) ReviewPullRequest(ctx context.Context, repo ports.Repository, number int, commit, event,
	body string) error {
	gh, ok := reviewEvents[event]
	if !ok {
		return fmt.Errorf("github: reviewing %s: no such review event %q", pullName(repo, number), event)
	}
	status, data, err := h.do(ctx, http.MethodPost, h.pullPath(repo, number)+"/reviews", struct {
		Commit string `json:"commit_id"`
		Event  string `json:"event"`
		Body   string `json:"body"`
	}{commit, gh, body})
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return failed("reviewing "+pullName(repo, number), status, data)
	}
	return nil
}

// PullRequestReviews lists the reviews submitted on a pull request, oldest
// first; a draft not yet submitted is left out. A review of a commit the
// head has moved past is stale. GitHub keeps no word of what a dismissed
// review said, so one reads as a comment, dismissed.
func (h *host) PullRequestReviews(ctx context.Context, repo ports.Repository, number int) ([]ports.PullReview, error) {
	p, err := h.getPull(ctx, repo, number)
	if err != nil {
		return nil, err
	}
	raw, err := h.list(ctx, h.pullPath(repo, number)+"/reviews", nil)
	if err != nil {
		return nil, err
	}
	var reviews []ports.PullReview
	for _, r := range raw {
		var rv struct {
			State  string `json:"state"`
			Commit string `json:"commit_id"`
			Body   string `json:"body"`
			User   struct {
				Login string `json:"login"`
			} `json:"user"`
		}
		if err := json.Unmarshal(r, &rv); err != nil {
			return nil, fmt.Errorf("github: reading the reviews of %s: %w", pullName(repo, number), err)
		}
		review := ports.PullReview{User: rv.User.Login, Commit: rv.Commit, Body: rv.Body,
			Stale: rv.Commit != p.Head.SHA}
		switch state, known := reviewStates[rv.State]; {
		case known:
			review.State = state
		case rv.State == "DISMISSED":
			review.State, review.Dismissed = ports.ReviewComment, true
		default:
			// PENDING: a draft only its author sees.
			continue
		}
		reviews = append(reviews, review)
	}
	return reviews, nil
}
