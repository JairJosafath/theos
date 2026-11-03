package fake

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/JairJosafath/c4factory/internal/ports"
)

// The fake host's commit statuses and trees: what the operator copies a
// merged head's checks with, and what a test sets or reads of them.

// CommitStatuses returns the checks on sha: those a test set with
// SetCheckContexts, and those SetCommitStatus wrote.
func (g *Git) CommitStatuses(_ context.Context, repo ports.Repository, sha string) ([]ports.CommitStatus, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	checks := g.s.agentState().checks[repo.Owner+"/"+repo.Name+"@"+sha]
	out := make([]ports.CommitStatus, 0, len(checks.Contexts))
	for name, state := range checks.Contexts {
		out = append(out, ports.CommitStatus{Context: name, State: state, TargetURL: checks.URL})
	}
	slices.SortFunc(out, func(a, b ports.CommitStatus) int { return strings.Compare(a.Context, b.Context) })
	return out, nil
}

// SetCommitStatus writes a check's status onto sha, and moves its combined state.
func (g *Git) SetCommitStatus(_ context.Context, repo ports.Repository, sha string, status ports.CommitStatus) error {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.agentState()
	key := repo.Owner + "/" + repo.Name + "@" + sha
	s.written[key] = append(s.written[key], status)
	checks := s.checks[key]
	contexts := maps.Clone(checks.Contexts)
	if contexts == nil {
		contexts = map[string]string{}
	}
	contexts[status.Context] = status.State
	checks.Commit, checks.Contexts, checks.URL = sha, contexts, status.TargetURL
	checks.State = "success"
	for _, state := range contexts {
		if state == "failure" || state == "error" || state == "pending" && checks.State == "success" {
			checks.State = state
		}
	}
	s.checks[key] = checks
	return nil
}

// CommitStatusesWritten are the statuses SetCommitStatus wrote onto sha of
// owner/name, in order.
func (g *Git) CommitStatusesWritten(owner, name, sha string) []ports.CommitStatus {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	return slices.Clone(g.s.agentState().written[owner+"/"+name+"@"+sha])
}

// SetCommitTree sets the tree sha holds, as main moving during a merge would
// make the merge commit's differ from the head's.
func (g *Git) SetCommitTree(owner, name, sha, tree string) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	g.s.agentState().trees[owner+"/"+name+"@"+sha] = tree
}

// CommitTree returns the tree of sha: one set, the one a commit on main got,
// or what the head of a branch PushBranch made would give main.
func (g *Git) CommitTree(_ context.Context, repo ports.Repository, sha string) (string, error) {
	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	s := g.s.agentState()
	key := repo.Owner + "/" + repo.Name
	if tree, ok := s.trees[key+"@"+sha]; ok {
		return tree, nil
	}
	for ref, head := range s.branches {
		if edits, ok := s.edits[ref]; ok && head == sha && strings.HasPrefix(ref, key+"@") {
			return treeID(withEdits(g.s.repos[key].files, edits)), nil
		}
	}
	return "", fmt.Errorf("commit %s of %s not found", sha, key)
}
