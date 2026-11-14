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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// Branch and tag rules on GitHub, step 2 of docs/plans/github-host.md (#75).
//
// A rule is two things there. The fence is a repository ruleset: nobody
// updates or deletes the branch, or rewrites it, but the repository's
// administrators, which the operator is, and they only by merging a pull
// request unless the rule lets the operator push. What a pull request must
// pass, its checks and the paths it may not change, no ruleset of a branch
// can hold in full: required checks are exact names where the port's are
// patterns, and a path restriction is a push ruleset's, on some plans. And a
// ruleset binds nobody it lets bypass it, the operator first. So
// MergePullRequest checks the rule itself (github_host_pulls.go), and
// ProtectBranch keeps it for that in an Actions variable of the repository.
//
// A variable is no safe place alone: GitHub lets anyone with write access
// to the repository set one through the API. A ruleset is an
// administrator's, so the ruleset's name carries a digest of the rule, and a
// rule in the variable that is not the one the ruleset names refuses every
// merge it would have checked until ProtectBranch writes it again.

// rulesVariable is the Actions variable that holds a repository's branch
// rules, a JSON object by branch.
const rulesVariable = "THEOS_BRANCH_RULES"

// storedRule is what MergePullRequest checks of a branch's rule.
type storedRule struct {
	StatusContexts []string `json:"statusContexts,omitempty"`
	ProtectedPaths []string `json:"protectedPaths,omitempty"`
}

func (h *host) variablePath(repo ports.Repository) string {
	return h.repoPath(repo) + "/actions/variables"
}

// rulesValue reads the variable's value, and whether the repository has the
// variable at all.
func (h *host) rulesValue(ctx context.Context, repo ports.Repository) (string, bool, error) {
	status, data, err := h.do(ctx, http.MethodGet, h.variablePath(repo)+"/"+rulesVariable, nil)
	if err != nil {
		return "", false, err
	}
	what := "reading the branch rules of " + repo.Owner + "/" + repo.Name + " (variable " + rulesVariable + ")"
	switch status {
	case http.StatusOK:
	case http.StatusNotFound:
		return "", false, nil
	default:
		return "", false, failed(what, status, data)
	}
	var variable struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(data, &variable); err != nil {
		return "", false, fmt.Errorf("github: %s: %w", what, err)
	}
	return variable.Value, true, nil
}

// parseRules reads the rules by branch out of the variable's value; a value
// that is no such object holds none.
func parseRules(value string) map[string]storedRule {
	rules := map[string]storedRule{}
	if json.Unmarshal([]byte(value), &rules) != nil || rules == nil {
		return map[string]storedRule{}
	}
	return rules
}

// digest names a rule in its ruleset: the start of the SHA-256 of the rule
// as the variable holds it.
func (r storedRule) digest() string {
	data, _ := json.Marshal(r)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:digestLength]
}

// digestLength is how many hex digits of a rule's digest its ruleset's name
// carries: 128 bits, so that nobody finds a weaker rule of the same digest
// by trying.
const digestLength = 32

// branchRulesetName is the name of the ruleset that fences branch for the
// rule of digest.
func branchRulesetName(branch, digest string) string {
	return "theos: " + branch + " (rule " + digest + ")"
}

// ruleDigest returns the digest in name when name is the ruleset of branch.
func ruleDigest(name, branch string) (string, bool) {
	digest, found := strings.CutPrefix(name, "theos: "+branch+" (rule ")
	digest, closed := strings.CutSuffix(digest, ")")
	if !found || !closed || len(digest) != digestLength || strings.Trim(digest, "0123456789abcdef") != "" {
		return "", false
	}
	return digest, true
}

// listedRuleset is a ruleset as the list of a repository's has it.
type listedRuleset struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// rulesets lists the repository's own rulesets, its organization's left out.
func (h *host) rulesets(ctx context.Context, repo ports.Repository) ([]listedRuleset, error) {
	raw, err := h.list(ctx, h.repoPath(repo)+"/rulesets", url.Values{"includes_parents": {strconv.FormatBool(false)}})
	if err != nil {
		return nil, err
	}
	listed := make([]listedRuleset, 0, len(raw))
	for _, r := range raw {
		var rs listedRuleset
		if err := json.Unmarshal(r, &rs); err != nil {
			return nil, fmt.Errorf("github: reading the rulesets of %s/%s: %w", repo.Owner, repo.Name, err)
		}
		listed = append(listed, rs)
	}
	return listed, nil
}

// branchRule is the rule ProtectBranch kept for branch; the zero rule for a
// branch it never fenced. The ruleset says which rule that is: one in the
// variable that is another, or none there, is an error, since anyone with
// write access to the repository can set the variable and only its
// administrators the ruleset.
func (h *host) branchRule(ctx context.Context, repo ports.Repository, branch string) (ports.BranchRule, error) {
	listed, err := h.rulesets(ctx, repo)
	if err != nil {
		return ports.BranchRule{}, err
	}
	var want string
	fenced := false
	for _, rs := range listed {
		if digest, ok := ruleDigest(rs.Name, branch); ok {
			want, fenced = digest, true
			break
		}
	}
	if !fenced {
		return ports.BranchRule{}, nil
	}
	value, _, err := h.rulesValue(ctx, repo)
	if err != nil {
		return ports.BranchRule{}, err
	}
	kept, ok := parseRules(value)[branch]
	if !ok || kept.digest() != want {
		return ports.BranchRule{}, fmt.Errorf("github: the rule of %s in %s/%s (variable %s) is not the one its ruleset "+
			"names, %s: it was changed since the branch was protected", branch, repo.Owner, repo.Name, rulesVariable,
			want)
	}
	return ports.BranchRule{StatusContexts: kept.StatusContexts, ProtectedPaths: kept.ProtectedPaths}, nil
}

// keepBranchRule writes branch's rule into the variable, beside the other
// branches' rules. A value someone broke is written over: the branches it
// loses are refused their merges until they are protected again.
func (h *host) keepBranchRule(ctx context.Context, repo ports.Repository, branch string, rule ports.BranchRule) error {
	value, exists, err := h.rulesValue(ctx, repo)
	if err != nil {
		return err
	}
	rules := parseRules(value)
	rules[branch] = storedRule{StatusContexts: rule.StatusContexts, ProtectedPaths: rule.ProtectedPaths}
	kept, err := json.Marshal(rules)
	if err != nil {
		return err
	}
	body := map[string]string{"name": rulesVariable, "value": string(kept)}
	method, path, want := http.MethodPost, h.variablePath(repo), http.StatusCreated
	if exists {
		method, path, want = http.MethodPatch, h.variablePath(repo)+"/"+rulesVariable, http.StatusNoContent
	}
	status, data, err := h.do(ctx, method, path, body)
	if err != nil {
		return err
	}
	if status != want {
		return failed("keeping the rule of "+branch+" in "+repo.Owner+"/"+repo.Name+" (variable "+rulesVariable+")",
			status, data)
	}
	return nil
}

// ruleset is a repository ruleset as GitHub takes it.
type ruleset struct {
	Name         string            `json:"name"`
	Target       string            `json:"target"`
	Enforcement  string            `json:"enforcement"`
	Conditions   rulesetConditions `json:"conditions"`
	BypassActors []bypassActor     `json:"bypass_actors"`
	Rules        []rulesetRule     `json:"rules"`
}

type rulesetConditions struct {
	RefName refNames `json:"ref_name"`
}

type refNames struct {
	Include []string `json:"include"`
	Exclude []string `json:"exclude"`
}

type bypassActor struct {
	ActorID    int64  `json:"actor_id"`
	ActorType  string `json:"actor_type"`
	BypassMode string `json:"bypass_mode"`
}

type rulesetRule struct {
	Type string `json:"type"`
}

// adminRole is the id GitHub gives the repository role "admin" in a
// ruleset's bypass actors.
const adminRole = 5

// What a ruleset fences, that it is enforced, and its bypass modes: always,
// or only by merging a pull request.
const (
	targetBranch      = "branch"
	targetTag         = "tag"
	enforced          = "active"
	bypassAlways      = "always"
	bypassPullRequest = "pull_request"
)

// branchRuleset is the ruleset that fences branch for rule: only the
// repository's administrators update it, by a pull request's merge, and by a
// push of their own when rule.OperatorPushes; nobody deletes or rewrites it.
// The checks and the paths of rule are no part of it but by their digest, in
// its name: MergePullRequest holds a pull request to them.
func branchRuleset(branch string, rule ports.BranchRule) ruleset {
	mode := bypassPullRequest
	if rule.OperatorPushes {
		mode = bypassAlways
	}
	return ruleset{
		Name:   branchRulesetName(branch, storedRule{rule.StatusContexts, rule.ProtectedPaths}.digest()),
		Target: targetBranch, Enforcement: enforced,
		Conditions:   rulesetConditions{RefName: refNames{Include: []string{"refs/heads/" + branch}, Exclude: []string{}}},
		BypassActors: []bypassActor{{ActorID: adminRole, ActorType: "RepositoryRole", BypassMode: mode}},
		Rules:        []rulesetRule{{Type: "update"}, {Type: "deletion"}, {Type: "non_fast_forward"}},
	}
}

// tagRuleset is the ruleset that lets only the repository's administrators
// make, move and delete the tags matching pattern.
func tagRuleset(pattern string) ruleset {
	return ruleset{
		Name: "theos: tags " + pattern, Target: targetTag, Enforcement: enforced,
		Conditions:   rulesetConditions{RefName: refNames{Include: []string{"refs/tags/" + pattern}, Exclude: []string{}}},
		BypassActors: []bypassActor{{ActorID: adminRole, ActorType: "RepositoryRole", BypassMode: bypassAlways}},
		Rules:        []rulesetRule{{Type: "creation"}, {Type: "update"}, {Type: "deletion"}},
	}
}

// putRuleset makes the repository's ruleset that same finds by its name
// what rs says, creating it when there is none.
func (h *host) putRuleset(ctx context.Context, repo ports.Repository, rs ruleset, same func(name string) bool) error {
	listed, err := h.rulesets(ctx, repo)
	if err != nil {
		return err
	}
	method, path, want := http.MethodPost, h.repoPath(repo)+"/rulesets", http.StatusCreated
	for _, l := range listed {
		if same(l.Name) {
			method, path, want = http.MethodPut, path+"/"+strconv.FormatInt(l.ID, 10), http.StatusOK
			break
		}
	}
	status, data, err := h.do(ctx, method, path, rs)
	if err != nil {
		return err
	}
	if status != want {
		return failed("writing the ruleset "+strconv.Quote(rs.Name)+" of "+repo.Owner+"/"+repo.Name, status, data)
	}
	return nil
}

// ProtectBranch keeps rule for MergePullRequest and fences branch with a
// ruleset that names it. The rule is kept first: until the ruleset names
// the new rule, merges into a branch fenced before are refused, never
// checked against less.
func (h *host) ProtectBranch(ctx context.Context, repo ports.Repository, branch string, rule ports.BranchRule) error {
	if err := h.keepBranchRule(ctx, repo, branch, rule); err != nil {
		return err
	}
	return h.putRuleset(ctx, repo, branchRuleset(branch, rule), func(name string) bool {
		_, ok := ruleDigest(name, branch)
		return ok
	})
}

// Login is the login of the account the token is of: GitHub takes no
// username beside a token, so it is always the host's answer (#268).
func (h *host) Login(ctx context.Context) (string, error) {
	return h.tokenUser(ctx)
}

// ProtectTags lets only the repository's administrators push tags matching
// pattern.
func (h *host) ProtectTags(ctx context.Context, repo ports.Repository, pattern string) error {
	rs := tagRuleset(pattern)
	return h.putRuleset(ctx, repo, rs, func(name string) bool { return name == rs.Name })
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
