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
	"strconv"

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
// ruleset binds nobody it lets bypass it, the operator first. So the rule is
// kept beside the ruleset, in an Actions variable of the repository, and
// MergePullRequest checks it (github_host_pulls.go). The variable is the
// repository administrators' to write, as the ruleset is: an agent's token
// must hold neither the administration nor the variables permission.

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

// branchRules reads the rules ProtectBranch kept, and whether the repository
// has the variable at all. A value that is no such object is an error: a
// rule that can't be read refuses the merge it would have checked.
func (h *host) branchRules(ctx context.Context, repo ports.Repository) (map[string]storedRule, bool, error) {
	status, data, err := h.do(ctx, http.MethodGet, h.variablePath(repo)+"/"+rulesVariable, nil)
	if err != nil {
		return nil, false, err
	}
	what := "reading the branch rules of " + repo.Owner + "/" + repo.Name + " (variable " + rulesVariable + ")"
	switch status {
	case http.StatusOK:
	case http.StatusNotFound:
		return map[string]storedRule{}, false, nil
	default:
		return nil, false, failed(what, status, data)
	}
	var variable struct {
		Value string `json:"value"`
	}
	rules := map[string]storedRule{}
	if err := json.Unmarshal(data, &variable); err != nil {
		return nil, false, fmt.Errorf("github: %s: %w", what, err)
	}
	if err := json.Unmarshal([]byte(variable.Value), &rules); err != nil {
		return nil, false, fmt.Errorf("github: %s: its value is no JSON object of rules: %w", what, err)
	}
	return rules, true, nil
}

// branchRule is the rule ProtectBranch kept for branch; the zero rule when it
// kept none.
func (h *host) branchRule(ctx context.Context, repo ports.Repository, branch string) (ports.BranchRule, error) {
	rules, _, err := h.branchRules(ctx, repo)
	if err != nil {
		return ports.BranchRule{}, err
	}
	kept := rules[branch]
	return ports.BranchRule{StatusContexts: kept.StatusContexts, ProtectedPaths: kept.ProtectedPaths}, nil
}

// keepBranchRule writes branch's rule into the variable, beside the other
// branches' rules.
func (h *host) keepBranchRule(ctx context.Context, repo ports.Repository, branch string, rule ports.BranchRule) error {
	rules, exists, err := h.branchRules(ctx, repo)
	if err != nil {
		return err
	}
	rules[branch] = storedRule{StatusContexts: rule.StatusContexts, ProtectedPaths: rule.ProtectedPaths}
	value, err := json.Marshal(rules)
	if err != nil {
		return err
	}
	body := map[string]string{"name": rulesVariable, "value": string(value)}
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

// The bypass modes of a ruleset: always, or only by merging a pull request.
const (
	bypassAlways      = "always"
	bypassPullRequest = "pull_request"
)

// branchRuleset is the ruleset that fences branch for rule: only the
// repository's administrators update it, by a pull request's merge, and by a
// push of their own when rule.OperatorPushes; nobody deletes or rewrites it.
// The checks and the paths of rule are no part of it: MergePullRequest holds
// a pull request to them.
func branchRuleset(branch string, rule ports.BranchRule) ruleset {
	mode := bypassPullRequest
	if rule.OperatorPushes {
		mode = bypassAlways
	}
	return ruleset{
		Name: "theos: " + branch, Target: "branch", Enforcement: "active",
		Conditions:   rulesetConditions{RefName: refNames{Include: []string{"refs/heads/" + branch}, Exclude: []string{}}},
		BypassActors: []bypassActor{{ActorID: adminRole, ActorType: "RepositoryRole", BypassMode: mode}},
		Rules:        []rulesetRule{{Type: "update"}, {Type: "deletion"}, {Type: "non_fast_forward"}},
	}
}

// tagRuleset is the ruleset that lets only the repository's administrators
// make, move and delete the tags matching pattern.
func tagRuleset(pattern string) ruleset {
	return ruleset{
		Name: "theos: tags " + pattern, Target: "tag", Enforcement: "active",
		Conditions:   rulesetConditions{RefName: refNames{Include: []string{"refs/tags/" + pattern}, Exclude: []string{}}},
		BypassActors: []bypassActor{{ActorID: adminRole, ActorType: "RepositoryRole", BypassMode: bypassAlways}},
		Rules:        []rulesetRule{{Type: "creation"}, {Type: "update"}, {Type: "deletion"}},
	}
}

// putRuleset makes the repository's ruleset of rs's name what rs says,
// creating it when there is none.
func (h *host) putRuleset(ctx context.Context, repo ports.Repository, rs ruleset) error {
	raw, err := h.list(ctx, h.repoPath(repo)+"/rulesets", nil)
	if err != nil {
		return err
	}
	method, path, want := http.MethodPost, h.repoPath(repo)+"/rulesets", http.StatusCreated
	for _, r := range raw {
		var listed struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(r, &listed); err != nil {
			return fmt.Errorf("github: reading the rulesets of %s/%s: %w", repo.Owner, repo.Name, err)
		}
		if listed.Name == rs.Name {
			method, path, want = http.MethodPut, path+"/"+strconv.FormatInt(listed.ID, 10), http.StatusOK
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
// ruleset. The rule is kept first: a branch fenced with no rule kept would
// merge unchecked.
func (h *host) ProtectBranch(ctx context.Context, repo ports.Repository, branch string, rule ports.BranchRule) error {
	if err := h.keepBranchRule(ctx, repo, branch, rule); err != nil {
		return err
	}
	return h.putRuleset(ctx, repo, branchRuleset(branch, rule))
}

// ProtectTags lets only the repository's administrators push tags matching
// pattern.
func (h *host) ProtectTags(ctx context.Context, repo ports.Repository, pattern string) error {
	return h.putRuleset(ctx, repo, tagRuleset(pattern))
}
