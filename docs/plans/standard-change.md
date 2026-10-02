# Standard Changes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When an issue needs a protected file, the factory files and merges that change as its own gated Task, then finishes the issue, with no person acting.

**Architecture:** The runner splits an implement round's change into a proposal (eligible protected paths, pushed to `standard/<task>`), the Task's own change, and refused paths. The operator files the proposal as a `kind/standard-change` issue and parks the Task in a new phase `Blocked`; the Crew runs that issue as a Task whose `spec.allowedProtectedPaths` comes from the filing Task's status; the operator force-merges it after its own checks, and the Blocked Task resumes in the same round. Rules live in `internal/model`; the controllers, runner and Git adapter only carry them out.

**Tech Stack:** Go 1.x kubebuilder operator (controller-runtime, envtest, Ginkgo/Gomega), git CLI in the runner, Gitea 1.27.3 REST API, React/TypeScript UI (vitest).

**Spec:** [docs/superpowers/specs/2026-10-02-standard-change-design.md](../superpowers/specs/2026-10-02-standard-change-design.md)

## Global Constraints

- Proposal branch: `standard/<task>`, cut from the base; the patch stays in Git, the result carries branch, paths and reason only (result ≤ 4000 bytes).
- Ineligible ("never") paths: `AGENTS.md`, `docs/agents/`, `.c4factory.yaml`. Every other protected path is eligible (Makefile, `.gitea/`, lint config, `.editorconfig`, `docs/style/`, `.devcontainer/`, `tsconfig.json`, …).
- The issue: title `Standard change for #<n>: <paths>`, authored by the operator, labels `kind/standard-change` (created if missing) and `status/ready`, in the waiting issue's milestone, blocking the waiting issue in Gitea.
- Phase `Blocked`: runs no agent, spends no round, does not count against the Crew's `parallel`.
- `spec.allowedProtectedPaths` is set only by the Crew, only from a Task's `status.standardChange`; a label alone grants nothing. A gated Task may change exactly its allowed paths and nothing else.
- At most two Standard changes per Task; a third fails the Task with a message containing `more than two Standard changes`.
- A resumed Task whose branch equals `main` ends `Merged` with reason `DoneByStandardChange`; the operator closes its issue with a comment naming the change.
- Gated merge: the operator checks CI passed, the reviewer approved and every changed path is allowed, then merges with Gitea's `force_merge` as admin (or the Task 1 fallback).
- `internal/model` and `internal/scaffold` import no Kubernetes, controller-runtime, `net/http` or `api/`. Never hand-edit `config/crd/bases/*.yaml`, `config/rbac/role.yaml`, `zz_generated.*.go`, `PROJECT`; after `*_types.go` run `make manifests generate`.
- `make check` passes at the end of every code task; `make ui-test` for UI changes.
- Conventional Commits, one per task at least; every commit message ends with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Don't push.
- Templates are pinned: a guide change is a new version in `templates/versions.txt`, published with `make seed`; existing Containers keep theirs.
- Live steps run only against the Kind cluster `c4factory`. Never print a secret; the Gitea admin password contains `/`, so it never goes in a URL or a sed pattern (`curl -u`). Kill every port-forward in the step that opened it.

## Review Focus

1. **Old guides say "stop".** Containers on Standard v1.0.0 have `docs/agents/implementer.md` saying "If one blocks you, stop and say so" and `AGENTS.md` saying "Leave … alone": the implementer prompt must override them, or #4 fails exactly as before. Pinned in Task 13.
2. **The Crew polls between the filing and the status write.** The operator creates the issue in Gitea before the Task's status names it; a Crew poll in that gap must not start the issue without its allowance. Pinned in Task 4 (the issue waits).
3. **The gated merge closes its issue before its Task says Merged.** `closeIssue` runs inside the same reconcile that sets `Merged`; a Blocked Task reading in between must not see "closed without a merge". Pinned in Task 3.
4. **The status write after filing conflicts.** A repeated `Propose` step must reuse the open issue it filed (found by a marker naming the proposal branch), not file a second one. Pinned in Task 9.
5. **The check fails in a round that touched an eligible protected path.** No proposal is filed, and the Task branch never carries the protected edit. Pinned in Task 5.

## Where the code differs from the spec's wording

- `gitprovider` already creates labels (`EnsureLabels`); only the label itself is new. It has no write call for dependencies, PR files or closing a PR: Task 8 adds them.
- Gitea issues don't say what closed them. "Closed by a merge" is read from Kubernetes: a Task on the change's issue reached `Merged`. The issue's state only decides "closed without a merge" (Task 3).
- The Crew rejects an issue without an "Acceptance criteria" checklist, so the filed issue's body carries one: the three reviewer checks from the spec.
- The spec doesn't say how the gated implementer finds the proposal branch; the Task spec gets no branch field. The issue body names it, and the gated prompt tells the implementer to merge `origin/<branch>` first (Task 13).
- A Task with a written goal has no issue to block; its protected edits still fail the round as today. Only issue Tasks propose.
- The spec's "the agent's reason": the implementer ends its summary with a line `Standard change: <why>`; without it the reason is the summary.
- Existing runner tests for protected paths run without an issue, so they keep passing unchanged.
- Only `go-standard` and `ts-standard` ship agent guides; `flutter-standard` has none and gets no new version.
- `gitprovider` comes before the controller tasks: the controller can't compile against methods that don't exist yet.

## File map

| File | Responsibility |
| --- | --- |
| `internal/model/standard.go` (new) | Eligibility, the split, proposal branch, reason, the issue's text, the label, `LiftedPaths` (fallback only) |
| `internal/model/task.go` | Phase `Blocked`, actions, facts, outcome of a change, resume feedback, gated-merge rule |
| `internal/model/crew.go` | `Blocked` outside the count, allowance from a Task's status |
| `internal/agentrunner/{job,run,git}.go` | Env, split, proposal branch, allowance check, resume, result |
| `internal/agentrunner/prompt.go` | Both prompts |
| `api/v1alpha1/task_types.go` | `Blocked`, `allowedProtectedPaths`, `status.standardChange` |
| `internal/gitprovider/{gitprovider,gitea_issues,gitea_agents}.go` | Milestone, dependency, PR files, close PR, force merge |
| `internal/controller/task_standard.go` (new) | Propose, resume, done-by-change, gated merge, change facts |
| `internal/controller/{task_actions,task_controller,task_sandbox,crew_controller}.go` | Wiring |
| `internal/controller/task_standard_test.go` (new) | envtest for the whole feature |
| `templates/{go,ts}-standard/…`, `templates/versions.txt` | Guides, v1.1.0 |
| `ui/src/k8s/types.ts`, `ui/src/model/factory.ts`, `ui/src/components/Factory.tsx`, `ui/src/styles.css` | `Blocked` stage and its issue |
| `docs/design.md`, `docs/architecture.md`, `docs/superpowers/specs/2026-10-02-issue-driven-factory-design.md`, `docs/results-v4.md` | Docs and evidence |

---

### Task 1: Live spike: does `force_merge` get past a protected file?

**Files:**
- Create: `docs/screenshots/v4-force-merge-spike.txt` (the script's output; it holds no secret)

**Interfaces:**
- Produces: the outcome **A** (force merge works) or **B** (refused), and whether Gitea reports `mergeable: false` for a PR that touches a protected file. Tasks 8 and 11 branch on it.

- [ ] **Step 1: Write the spike script to the scratchpad** (`$SCRATCH/spike.sh`; one Bash call runs it, so the port-forward dies with it)

```bash
#!/usr/bin/env bash
# Does Gitea's force_merge, as the operator, merge a PR that touches a protected file?
set -uo pipefail
G=http://localhost:3917/api/v1 REPO=cel-lens/spike-force-merge
kubectl -n gitea port-forward svc/gitea 3917:3000 >/dev/null 2>&1 & pf=$!
trap 'kill $pf 2>/dev/null' EXIT
sleep 3
PW=$(kubectl -n gitea get secret gitea-admin -o jsonpath='{.data.password}' | base64 -d)
TOKEN=$(kubectl -n cel-lens get secret gitea-credentials -o jsonpath='{.data.token}' | base64 -d)
admin() { curl -sS -u "c4admin:$PW" -X "$1" -H 'Content-Type: application/json' ${3:+-d "$3"} -w '\nHTTP %{http_code}\n' "$G$2"; }
operator() { curl -sS -H "Authorization: token $TOKEN" -X "$1" -H 'Content-Type: application/json' ${3:+-d "$3"} -w '\nHTTP %{http_code}\n' "$G$2"; }
body() { head -n -1; }
b64() { printf "$1" | base64 -w0; }
echo "== version"; admin GET /version
echo "== repo"; admin POST /orgs/cel-lens/repos '{"name":"spike-force-merge","auto_init":true,"default_branch":"main","private":true}' | tail -1
echo "== Makefile on main"; admin POST /repos/$REPO/contents/Makefile "{\"content\":\"$(b64 'check:\n\ttrue\n')\",\"message\":\"Add Makefile\",\"branch\":\"main\"}" | tail -1
echo "== protect main"; admin POST /repos/$REPO/branch_protections '{"rule_name":"main","enable_push":false,"enable_merge_whitelist":true,"merge_whitelist_usernames":["c4admin"],"protected_file_patterns":"Makefile"}' | tail -1
admin GET /repos/$REPO/branch_protections/main | body | jq -c '{protected_file_patterns, block_admin_merge_override}'
echo "== branch and edit"; admin POST /repos/$REPO/branches '{"new_branch_name":"spike","old_branch_name":"main"}' | tail -1
SHA=$(admin GET "/repos/$REPO/contents/Makefile?ref=spike" | body | jq -r .sha)
admin PUT /repos/$REPO/contents/Makefile "{\"content\":\"$(b64 'check:\n\ttrue\ndist:\n\tnpm run dist --if-present\n')\",\"sha\":\"$SHA\",\"message\":\"Add dist\",\"branch\":\"spike\"}" | tail -1
echo "== PR"; admin POST /repos/$REPO/pulls '{"head":"spike","base":"main","title":"Touch Makefile"}' | tail -1
sleep 5
HEAD=$(admin GET /repos/$REPO/pulls/1 | body | jq -r .head.sha)
admin GET /repos/$REPO/pulls/1 | body | jq -c '{mergeable, state}'
echo "== normal merge as the operator (expect a refusal)"; operator POST /repos/$REPO/pulls/1/merge "{\"Do\":\"squash\",\"head_commit_id\":\"$HEAD\"}"
echo "== force merge as the operator"; operator POST /repos/$REPO/pulls/1/merge "{\"Do\":\"squash\",\"head_commit_id\":\"$HEAD\",\"force_merge\":true}"
admin GET /repos/$REPO/pulls/1 | body | jq -c '{merged, state}'
echo "== clean up"; admin DELETE /repos/$REPO | tail -1
```

- [ ] **Step 2: Run it and keep the output**

Run: `bash $SCRATCH/spike.sh 2>&1 | tee docs/screenshots/v4-force-merge-spike.txt`
Expected: version `1.27.3`; `block_admin_merge_override: false`; `mergeable: true`; the normal merge `HTTP 405` with a message about protected files; the force merge `HTTP 200`; `merged: true`; clean-up `HTTP 204`.

- [ ] **Step 3: Check the port-forward is gone**

Run: `ps -eo pid,args | grep '[p]ort-forward svc/gitea 3917'`
Expected: no output.

- [ ] **Step 4: Record the outcome** as the file's first line: `Outcome A: force_merge merges a protected-file PR (mergeable=<value>)` or `Outcome B: force_merge refused: <message>`.

- [ ] **Step 5: Commit**: `git add docs/screenshots/v4-force-merge-spike.txt`, message `docs: record the force_merge spike on a protected file`. Body: "Outcome <A|B>.".

---

### Task 2: model: what a Standard change is

**Files:**
- Create: `internal/model/standard.go`, `internal/model/standard_test.go`
- Modify: `internal/model/model.go:276-288` (`TouchedPaths` uses the shared `covers`)

**Interfaces:**
- Produces:
  - `var AgentNeverPaths = []string{"AGENTS.md", "docs/agents/", ".c4factory.yaml"}`
  - `const MaxStandardChanges = 2`, `const MaxProposalPaths = 20`
  - `const LabelStandardChange = "kind/standard-change"`; `func StandardChangeLabel() IssueLabel` (color `#5319e7`, description "Changes protected files for another issue; filed by the operator", not exclusive)
  - `type Proposal struct { Branch string \`json:"branch"\`; Paths []string \`json:"paths"\`; Reason string \`json:"reason,omitempty"\` }`
  - `type ChangeSplit struct { Task, Proposal, Refused []string }`
  - `func SplitChange(changed, protected, allowed []string, canPropose bool) ChangeSplit`
  - `func EligibleAllowance(paths []string) []string`
  - `func ProposalBranch(task string) string` → `"standard/" + task`
  - `func ProposalMarker(branch string) string` → `"<!-- c4factory:standard-change " + branch + " -->"`
  - `func ProposalReason(summary string) string`
  - `func StandardChangeIssue(issue int, task string, p Proposal) (title, body string)`

- [ ] **Step 1: Write the failing tests** in `standard_test.go`

```go
func TestSplitChange(t *testing.T) {
	std := AgentProtectedPaths([]string{"AGENTS.md", "Makefile", "docs/agents/"})
	for _, tt := range []struct {
		name                      string
		changed, protected, allow []string
		canPropose                bool
		want                      ChangeSplit
	}{
		{"unprotected", []string{"src/a.ts"}, std, nil, true, ChangeSplit{Task: []string{"src/a.ts"}}},
		{"eligible is proposed", []string{"Makefile", "src/a.ts"}, std, nil, true,
			ChangeSplit{Task: []string{"src/a.ts"}, Proposal: []string{"Makefile"}}},
		{"platform directory is eligible", []string{".gitea/workflows/release.yaml"}, std, nil, true,
			ChangeSplit{Proposal: []string{".gitea/workflows/release.yaml"}}},
		{"agents' rules and contract refused", []string{"AGENTS.md", "docs/agents/reviewer.md", ".c4factory.yaml"}, std, nil, true,
			ChangeSplit{Refused: []string{"AGENTS.md", "docs/agents/reviewer.md", ".c4factory.yaml"}}},
		{"mixed", []string{"Makefile", "AGENTS.md", "src/a.ts"}, std, nil, true,
			ChangeSplit{Task: []string{"src/a.ts"}, Proposal: []string{"Makefile"}, Refused: []string{"AGENTS.md"}}},
		{"no issue, no proposal", []string{"Makefile"}, std, nil, false, ChangeSplit{Refused: []string{"Makefile"}}},
		{"gated: allowed is the Task's", []string{"Makefile"}, std, []string{"Makefile"}, true, ChangeSplit{Task: []string{"Makefile"}}},
		{"gated: anything else refused", []string{"Makefile", "README.md", ".golangci.yml"}, std, []string{"Makefile"}, true,
			ChangeSplit{Task: []string{"Makefile"}, Refused: []string{"README.md", ".golangci.yml"}}},
		{"unprotected AGENTS.md is the Task's", []string{"AGENTS.md"}, AgentProtectedPaths(nil), nil, true,
			ChangeSplit{Task: []string{"AGENTS.md"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := SplitChange(tt.changed, tt.protected, tt.allow, tt.canPropose); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SplitChange = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestEligibleAllowance(t *testing.T) {
	got := EligibleAllowance([]string{"Makefile", "AGENTS.md", "docs/agents/x.md", ".c4factory.yaml", ".gitea/workflows/check.yaml"})
	want := []string{"Makefile", ".gitea/workflows/check.yaml"}
	// assert reflect.DeepEqual(got, want); and EligibleAllowance(nil) == nil
}

func TestProposalReason(t *testing.T) {
	// "Did X.\nStandard change: make dist must build the packages.\nIt calls npm run dist."
	//   -> "make dist must build the packages.\nIt calls npm run dist."
	// "Did X, no marker." -> "Did X, no marker."
}

func TestStandardChangeIssue(t *testing.T) {
	p := Proposal{Branch: "standard/vscode-extension-4-2", Paths: []string{"Makefile"}, Reason: "make dist must build the packages"}
	title, body := StandardChangeIssue(4, "vscode-extension-4-2", p)
	// title == "Standard change for #4: Makefile"
	// body contains: p.Reason, "`Makefile`", "origin/standard/vscode-extension-4-2", "#4", "`vscode-extension-4-2`",
	//   "weakens no check", "works with this change alone", ProposalMarker(p.Branch)
	// MissingAcceptanceCriteria(body) == ""
	// two paths: title == "Standard change for #4: Makefile and .gitea/workflows/release.yaml"
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/model/ -run 'SplitChange|EligibleAllowance|ProposalReason|StandardChangeIssue'`
Expected: FAIL, undefined names.

- [ ] **Step 3: Implement `standard.go`**

`covers(prot, path string) bool` is `TouchedPaths`'s match (equal, or under a directory ending in `/`); `TouchedPaths` calls it. `SplitChange`: with `allowed` set, a path covered by `allowed` goes to Task, every other path to Refused, never Proposal; without it, an unprotected path goes to Task, a protected one covered by `AgentNeverPaths` to Refused, any other protected one to Proposal when `canPropose`, else Refused. Order within each list follows `changed`. `ProposalReason` takes everything after the first line starting `Standard change:`, trimmed, else the trimmed summary. The issue body: a lead line naming the Task and the waiting issue, `## Why` with the reason, `## Change` listing the paths in backticks and "merge `origin/<branch>` first", `## Acceptance criteria` with three checklist items (the paths change as proposed and nothing else; "The change weakens no check: no target, test, lint rule or CI job is removed or skipped"; "`main` works with this change alone"), then the marker. Titles join paths with `joinAnd`.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/model/`
Expected: PASS (the existing `TouchedPaths` tests too).

- [ ] **Step 5: `make check`, then commit**: `git add internal/model/standard.go internal/model/standard_test.go internal/model/model.go`, message `feat(model): split a change into a Standard change proposal`.

---

### Task 3: model: Task steps for `Blocked`, the resume and the gated merge

**Files:**
- Modify: `internal/model/task.go:15-22` (phases), `:40-55` (`RunResult`), `:79-112` (`TaskFacts`), `:122-140` (actions), `:170-249` (`nextTaskStep`)
- Test: `internal/model/task_test.go`

**Interfaces:**
- Consumes: `Proposal`, `MaxStandardChanges`, `covers`, `joinAnd` (Task 2).
- Produces:
  - `const TaskBlocked = "Blocked"`
  - `ActionPropose TaskAction = "Propose"`, `ActionResume = "Resume"`, `ActionDoneByStandardChange = "DoneByStandardChange"`
  - `RunResult.Proposal *Proposal`, `RunResult.Empty bool` (the resumed branch equals the base)
  - `TaskFacts.StandardChanges int` (filed so far), `TaskFacts.Resumed bool` (this implement run is the first after a change merged), `TaskFacts.Change ChangeFacts`, `TaskFacts.Feedback string` (the round's feedback, `status.feedback`)
  - `type ChangeFacts struct { Issue int; Paths []string; Merged bool; Failed string }`
  - `type ChangeTask struct { Name string; Final, Failed bool; Message string }`
  - `func StandardChangeOutcome(issue int, tasks []ChangeTask, issueClosed bool) (merged bool, failed string)`
  - `func ResumeFeedback(issue int, paths []string, earlier string) string`
  - `func GatedMergeRefusal(checks, verdict string, changed, allowed []string) string` ("" means merge)

- [ ] **Step 1: Write the failing tests** (rows in the existing `NextTaskStep` table style, plus two new tables)

```go
done := func(r RunResult) RunState { return RunState{Started: true, Done: true, Result: &r} }
prop := &Proposal{Branch: "standard/t", Paths: []string{"Makefile"}}
// NextTaskStep rows: facts -> action, message/feedback substring
{Phase: TaskImplementing, Round: 1, MaxRounds: 3, Run: done(RunResult{Commit: "c", Proposal: prop})} -> ActionPropose, "Standard change to Makefile"
{… StandardChanges: 1 …same…} -> ActionPropose
{… StandardChanges: 2 …same…} -> ActionFail, "more than two Standard changes"
{… Run: done(RunResult{Error: "boom", Proposal: prop})} -> ActionRetry
{Phase: TaskImplementing, Resumed: true, Change: ChangeFacts{Issue: 31}, Run: done(RunResult{Empty: true})} -> ActionDoneByStandardChange, "#31"
{Phase: TaskImplementing, Run: done(RunResult{Commit: "c", Empty: true})} -> ActionOpenPR   // Empty counts only on a resume
{Phase: TaskBlocked, Change: ChangeFacts{Issue: 31, Paths: []string{"Makefile"}}} -> ActionWait, "Waiting for Standard change #31 (Makefile)"
{Phase: TaskBlocked, Round: 3, MaxRounds: 3, Feedback: "- fix x", Change: ChangeFacts{Issue: 31, Paths: []string{"Makefile"}, Merged: true}} -> ActionResume, feedback contains "#31", "Makefile" and "- fix x"
{Phase: TaskBlocked, Change: ChangeFacts{Issue: 31, Failed: "Task x-31 failed: Failed after 3 rounds"}} -> ActionFail, "Standard change #31 will not merge: Task x-31 failed"
{Phase: TaskBlocked, NoFreeSlot: true, AgentBackIn: time.Hour, Change: ChangeFacts{Issue: 31, Merged: true}} -> ActionResume, SlotWait false
{Phase: TaskBlocked, NoFreeSlot: true, Change: ChangeFacts{Issue: 31}} -> ActionWait, SlotWait false

func TestStandardChangeOutcome(t *testing.T) {
	// no tasks, open                               -> false, ""
	// [{x-31 active}], issueClosed=true            -> false, ""   (Review Focus 3)
	// [{x-31 Final}] merged                        -> true, ""
	// [{x-31 Final Failed "Failed after 3 rounds"}] -> false, "Task x-31 failed: Failed after 3 rounds"
	// [{x-31 failed}, {x-31-2 active}]             -> false, ""
	// [{x-31 failed}, {x-31-2 merged}]             -> true, ""
	// no tasks, issueClosed=true                   -> false, "issue #31 was closed without a merge"
}

func TestResumeFeedback(t *testing.T) {
	// ResumeFeedback(31, []string{"Makefile"}, "") contains "#31", "Makefile", "merged into main"
	// ResumeFeedback(31, …, "The reviewer asked for changes:\n- fix x") also contains "- fix x"
}

func TestGatedMergeRefusal(t *testing.T) {
	// ("success","approve",[Makefile],[Makefile])                     -> ""
	// ("success","approve",[.gitea/workflows/release.yaml],[.gitea/]) -> ""
	// ("failure", …)            -> contains "CI"
	// (…,"changes", …)          -> contains "approve"
	// changed [Makefile README.md] -> contains "README.md"
	// changed []                -> contains "changes nothing"
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/model/ -run 'NextTaskStep|StandardChangeOutcome|ResumeFeedback|GatedMergeRefusal'`
Expected: FAIL, undefined names.

- [ ] **Step 3: Implement**

In `nextTaskStep`'s Implementing case, after the Unavailable and Error checks: a Proposal fails the Task when `StandardChanges >= MaxStandardChanges` (message `The change needs a third Standard change (<paths>); more than two Standard changes need a person`), else `ActionPropose` (message `The change needs a Standard change to <paths>; filing it`); then `Resumed && Empty` gives `ActionDoneByStandardChange` (message `Standard change #<n> did the whole issue: the branch has nothing beyond main`). New case `TaskBlocked`: Merged → `ActionResume` with `Feedback: ResumeFeedback(issue, paths, f.Feedback)` and message `Standard change #<n> merged; round <r> goes on`; Failed → `ActionFail`; else `ActionWait`. `NextTaskStep`'s slot and agent gates apply only to Start actions, so `Blocked` needs no change there. `StandardChangeOutcome`: any merged wins; else any active means pending; else the last failed Task's message; else a closed issue fails.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/model/`
Expected: PASS.

- [ ] **Step 5: `make check`, then commit**: `git add internal/model/task.go internal/model/task_test.go`, message `feat(model): block a Task on its Standard change and resume it`.

---

### Task 4: model: the Crew leaves `Blocked` out and grants the allowance

**Files:**
- Modify: `internal/model/crew.go:53-72` (`Issue`, `CrewTask`), `:93-98` (`IssueStart`), `:122-205` (`NextCrewStep`)
- Test: `internal/model/crew_test.go`

**Interfaces:**
- Consumes: `EligibleAllowance` (Task 2).
- Produces: `CrewTask.Blocked bool`; `CrewTask.StandardChange *IssueChange`; `type IssueChange struct { Issue int; Paths []string }`; `Issue.StandardChange bool` (labelled `kind/standard-change`); `IssueStart.AllowedPaths []string`.

- [ ] **Step 1: Write the failing tests**

```go
filer := CrewTask{Name: "c-1", Issue: 1, Blocked: true, StandardChange: &IssueChange{Issue: 2, Paths: []string{"Makefile", "AGENTS.md"}}}
change := Issue{Number: 2, Body: clear, StandardChange: true}
// Parallel 1, Tasks [filer], Issues [change]
//   -> Start == []IssueStart{{Issue: 2, Task: "c-2", Attempt: 1, AllowedPaths: []string{"Makefile"}}}
// Parallel 1, Tasks [{c-1, Issue 1, not final, not blocked}], Issues [{3, clear}] -> no Start, Ready [3]
// Parallel 1, Tasks nil, Issues [change]  -> no Start, Waiting [2]          (Review Focus 2)
// filer Final and Failed, Issues [change] -> Start with AllowedPaths [Makefile]
// Tasks [filer], Issues [{2, clear, StandardChange: false}] -> Start with AllowedPaths nil (a status alone grants nothing)
```

(`clear` is a body with an "Acceptance criteria" checklist, as the existing tests use.)

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/model/ -run Crew`
Expected: FAIL.

- [ ] **Step 3: Implement**

`free := max(f.Parallel, 1) - (len(step.Active) - blocked)` where `blocked` counts non-final Blocked Tasks; they stay in `Active` and keep their issue in `working`. A candidate with `StandardChange` looks up the first Task (by name) whose `StandardChange.Issue` is its number; none, or an empty `EligibleAllowance`, sends it to `Waiting`; otherwise its `IssueStart.AllowedPaths` is that allowance. Messages are unchanged.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/model/`
Expected: PASS.

- [ ] **Step 5: `make check`, then commit**: `git add internal/model/crew.go internal/model/crew_test.go`, message `feat(model): run Standard changes beside blocked Tasks, with their allowance`.

---

### Task 5: runner: split the change, push the proposal, hold the allowance

**Files:**
- Modify: `internal/agentrunner/job.go:24-50` (env names), `:56-86` (`Job`), `:89-180` (`JobFromEnv`); `internal/agentrunner/run.go:21-71` (`Result`, `Encode`), `:105-155` (`implement`); `internal/agentrunner/git.go` (two helpers)
- Test: `internal/agentrunner/agentrunner_test.go`

**Interfaces:**
- Consumes: `SplitChange`, `Proposal`, `ProposalBranch`, `ProposalReason`, `MaxProposalPaths`, `AgentProtectedPaths` (Task 2).
- Produces:
  - `EnvAllowedPaths = "C4_ALLOWED_PATHS"` (comma-separated), `EnvResumed = "C4_RESUMED"` (`strconv.ParseBool`, empty is false)
  - `Job.AllowedPaths []string`, `Job.Resumed bool`
  - `Result.Proposal *model.Proposal \`json:"proposal,omitempty"\``, `Result.Empty bool \`json:"empty,omitempty"\`` (set in Task 6)
  - `func (r *repo) proposeBranch(ctx context.Context, base, branch string, paths []string, message string) error`: branch from `origin/<base>`, each path taken from the work head (or removed when the head deleted it), one commit, `push --force`, back on the work branch
  - `func (r *repo) restorePaths(ctx context.Context, base string, paths []string, message string) error`: each path back to `origin/<base>` (or removed when the base lacks it), one commit

- [ ] **Step 1: Write the failing tests**

A `filesAgent{files map[string]string; summary string}` writes each file and returns `summary`. Each test uses `bareRepo` with `Makefile` (`check:\n`) and `README.md`, a job with `Issue = 4` and `ProtectedPaths = []string{"Makefile"}` unless it says otherwise.

```go
TestRunImplementProposesAStandardChange: agent writes Makefile and src.txt, summary "Done.\nStandard change: make dist must build the packages"
	got.Error == ""; *got.Proposal == model.Proposal{Branch: "standard/add-crop", Paths: []string{"Makefile"}, Reason: "make dist must build the packages"}
	`git diff --name-only main standard/add-crop` in a fresh clone == "Makefile\n"
	task/add-crop has src.txt, and its Makefile == "check:\n"; got.Commit == branchHead(t, url, "task/add-crop")
	second case, agent writes Makefile only: Error == "", Proposal set, Commit == branchHead(task/add-crop)  (nothing else to do: it still waits)
TestRunImplementRefusesTheAgentsRules: ProtectedPaths [AGENTS.md], agent writes AGENTS.md
	got.Error contains "AGENTS.md"; branchHead(task/add-crop) == "" and branchHead(standard/add-crop) == ""
TestRunImplementGated: AllowedPaths [Makefile]
	agent writes Makefile only -> Error == "", Proposal == nil, task/add-crop's Makefile changed, no standard/add-crop
	agent writes Makefile and README.md -> Error contains "this Task may change only Makefile"
TestRunImplementKeepsNoProtectedEditWhenTheCheckFails: check "sh -c 'exit 1'", agent writes Makefile and src.txt   (Review Focus 5)
	Error contains "still fails" and "was not kept"; Proposal == nil; no standard/add-crop; task/add-crop's Makefile == "check:\n"
TestResultEncodeKeepsTheProposal: Summary 10 KB, Proposal{Branch, 20 paths, Reason 10 KB}
	len(Encode()) <= MaxResultBytes; decoded Proposal.Branch and Paths unchanged
TestJobFromEnv rows: C4_ALLOWED_PATHS "Makefile, .gitea/" -> [Makefile .gitea/]; C4_RESUMED "true" -> true; "" -> false; "maybe" -> error
```

The existing "protected path" and "path the Standard protects" subtests have no issue, so they still expect the refusal; leave them.

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/agentrunner/`
Expected: FAIL.

- [ ] **Step 3: Implement**

In `implement`, after `changedPaths`: `split := model.SplitChange(changed, model.AgentProtectedPaths(job.ProtectedPaths), job.AllowedPaths, job.Issue > 0)`. Refused paths fail the round before any push: gated Tasks with `the change touches <paths>; this Task may change only <allowed>`, others with today's message. More than `MaxProposalPaths` proposed paths fail with `the Standard change touches <n> protected files; at most 20 go in one`. With a proposal: when the check passed, `proposeBranch(… model.ProposalBranch(job.Task) …)` then `restorePaths`, and `Result.Proposal` carries branch, paths and `ProposalReason(summary)` cut to `maxProposalReason = 1000` bytes; when the check failed, only `restorePaths`, and the error gets `\nYour change to <paths> was not kept: a Standard change is filed only when the check passes with it. Make it again.` Then push the work branch as today. `Encode` shortens `Proposal.Reason` (never branch or paths) before giving up on the summary.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/agentrunner/`
Expected: PASS.

- [ ] **Step 5: `make check`, then commit**: `git add internal/agentrunner/`, message `feat(agentrunner): move protected edits to a Standard change branch`.

---

### Task 6: runner: the resumed round

**Files:**
- Modify: `internal/agentrunner/run.go` (`implement`), `internal/agentrunner/git.go`
- Test: `internal/agentrunner/agentrunner_test.go`

**Interfaces:**
- Consumes: `Job.Resumed`, `Result.Empty` (Task 5).
- Produces: `func (r *repo) sameTreeAsBase(ctx context.Context, base string) (bool, error)` (`git diff --quiet origin/<base> HEAD`: exit 0 true, exit 1 false).

- [ ] **Step 1: Write the failing tests**

```go
TestRunImplementResumed:
  "branch equals main": round 1 with FakeAgent{Role: implement, Round: 1} pushes STAND-IN.md;
	pushToMain(t, url, "STAND-IN.md", "Round 1 of the stand-in agent.\n"); job.Resumed = true;
	FakeAgent{NoChange: true} -> Error == "", Empty == true, Commit == ""
  "work left, nothing new": round 1 as above, no push to main; Resumed, NoChange
	-> Error == "", Empty == false, Commit == branchHead(t, url, "task/add-crop")
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/agentrunner/ -run Resumed`
Expected: FAIL ("the agent made no changes").

- [ ] **Step 3: Implement**: "no changes" applies only when `!job.Resumed`; on a resume, `sameTreeAsBase` true sets `Empty` and returns before splitting or pushing.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/agentrunner/`
Expected: PASS.

- [ ] **Step 5: `make check`, then commit**: `git add internal/agentrunner/`, message `feat(agentrunner): a resumed round may change nothing`.

---

### Task 7: API: `Blocked`, `allowedProtectedPaths`, `status.standardChange`

**Files:**
- Modify: `api/v1alpha1/task_types.go:23-74` (spec, phase enum), `:111-224` (status)
- Generated: `config/crd/bases/c4.factory.io_tasks.yaml`, `api/v1alpha1/zz_generated.deepcopy.go` (by `make manifests generate`)
- Create: `internal/controller/task_standard_test.go`

**Interfaces:**
- Produces:
  - `TaskBlocked TaskPhase = "Blocked"`; enum `Pending;Implementing;Checking;Reviewing;Blocked;Merged;Failed`
  - `TaskSpec.AllowedProtectedPaths []string \`json:"allowedProtectedPaths,omitempty"\``: `+listType=atomic`, `MaxItems=20`, `items:MaxLength=256`, and on the field `self.all(p, p != 'AGENTS.md' && p != '.c4factory.yaml' && p != 'docs/agents' && !p.startsWith('docs/agents/'))` with message `allowedProtectedPaths may not include AGENTS.md, docs/agents/ or .c4factory.yaml`
  - on `TaskSpec`: `!has(self.allowedProtectedPaths) || has(self.issue)` ("allowedProtectedPaths needs an issue") and `has(self.allowedProtectedPaths) == has(oldSelf.allowedProtectedPaths)` ("allowedProtectedPaths is immutable"); on the field `self == oldSelf` (same message)
  - `TaskStatus.StandardChange *TaskStandardChange \`json:"standardChange,omitempty"\``
  - `type TaskStandardChange struct { Issue int32 \`json:"issue"\`; IssueURL string \`json:"issueURL,omitempty"\`; Paths []string \`json:"paths"\` (+listType=atomic, MaxItems=20); Branch string \`json:"branch,omitempty"\`; Count int32 \`json:"count"\`; ResumedRound int32 \`json:"resumedRound,omitempty"\` }`, each field with a doc comment in the file's style

- [ ] **Step 1: Write the failing envtest** in `task_standard_test.go`

```go
var _ = Describe("Task allowedProtectedPaths", func() {
	It("takes eligible paths on an issue Task only, and never changes", func() {
		ns := newProjectNamespace()
		gated := func(name string, paths ...string) *c4v1alpha1.Task {
			t := newTask(ns, name, ""); t.Spec.Issue = 2; t.Spec.AllowedProtectedPaths = paths; return t
		}
		Expect(k8sClient.Create(ctx, gated("ok", "Makefile", ".gitea/workflows/release.yaml"))).To(Succeed())
		for _, never := range model.AgentNeverPaths {
			err := k8sClient.Create(ctx, gated("never", never))
			Expect(err).To(MatchError(ContainSubstring("may not include AGENTS.md")), never)
		}
		goal := newTask(ns, "goal", "Do it"); goal.Spec.AllowedProtectedPaths = []string{"Makefile"}
		Expect(k8sClient.Create(ctx, goal)).To(MatchError(ContainSubstring("needs an issue")))
		task := getTask(Default, ns, "ok")
		Expect(patch(task, func() { task.Spec.AllowedProtectedPaths = []string{"Makefile", ".golangci.yml"} })).
			To(MatchError(ContainSubstring("immutable")))
	})
})
```

- [ ] **Step 2: Run it to see it fail**

Run: `make test` (or `go test ./internal/controller/ -ginkgo.focus allowedProtectedPaths` with the envtest assets)
Expected: FAIL, unknown field.

- [ ] **Step 3: Add the types and markers, then `make manifests generate`.**

- [ ] **Step 4: Run the test**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: `make check`, then commit**: `git add api/ config/crd/ internal/controller/task_standard_test.go`, message `feat(api): Task phase Blocked, allowed protected paths and the Standard change status`.

---

### Task 8: gitprovider: milestone, dependency, PR files, closing and force-merging

**Files:**
- Modify: `internal/gitprovider/gitprovider.go:61-122` (interface), `internal/gitprovider/gitea_issues.go:13-21,81-99` (`Issue.Milestone`), `internal/gitprovider/gitea_agents.go:310-327` (merge)
- Test: `internal/gitprovider/gitea_issues_test.go`, `internal/gitprovider/gitea_agents_test.go`
- Modify (fakes, so the controller tests compile): `internal/controller/fakegit_test.go`, `internal/controller/fakeissues_test.go`

**Interfaces:**
- Produces:
  - `Issue.Milestone int64` (0 when none; from `milestone.id`)
  - `AddIssueDependency(ctx context.Context, repo Repository, number, blocker int) error`: issue `number` depends on `blocker`; reads `GET …/issues/{number}/dependencies?limit=50` and returns nil when `blocker` is there, else `POST` the same path with `{"owner","repo","index": blocker}` (201)
  - `PullRequestFiles(ctx context.Context, repo Repository, number int) ([]string, error)`: `GET …/pulls/{n}/files?page=P&limit=50` until a page has fewer than 50
  - `ClosePullRequest(ctx context.Context, repo Repository, number int) error`: `PATCH …/pulls/{n}` `{"state":"closed"}` (200 or 201)
  - **Branch A only:** `ForceMergePullRequest(ctx context.Context, repo Repository, number int, headSHA, title, message string) error`: `MergePullRequest`'s payload plus `"force_merge": true`, through one shared helper
  - Fakes: `fakeProvider` implements them; `(g *fakeGit) setPullFiles(owner, name string, number int, files ...string)`; `(g *fakeGit) setIssueMilestone(owner, name string, number int, title string)` (creates the milestone if needed); fake `GetIssue` returns its milestone ID; fake `MergePullRequest` refuses with `protected files` when the PR's files touch the repository rule's `ProtectedPaths` (as Gitea does); branch A: fake `ForceMergePullRequest` merges and records it, `(g *fakeGit) forcedMerges(owner, name string) []int`

- [ ] **Step 1: Write the failing httptest tests**

```go
TestAddIssueDependency: host has issue 4 depending on nothing -> POST /api/v1/repos/o/r/issues/4/dependencies body {"owner":"o","repo":"r","index":31};
	a second call, with #31 now listed, makes no POST
TestPullRequestFiles: host serves 50 then 3 files -> 53 names, pages 1 and 2 requested
TestClosePullRequest: PATCH /api/v1/repos/o/r/pulls/7 body {"state":"closed"}
TestGetIssueMilestone: issue JSON with "milestone":{"id":5} -> Issue.Milestone == 5
Branch A: TestForceMergePullRequest: POST …/pulls/7/merge body has "force_merge":true, "Do":"squash", "head_commit_id":"abc"
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/gitprovider/`
Expected: FAIL.

- [ ] **Step 3: Implement the methods and the fakes.**

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/gitprovider/ && make test`
Expected: PASS (the controller tests still pass with the stricter fake merge: their pull requests touch no protected file).

- [ ] **Step 5: `make check`, then commit**: `git add internal/gitprovider/ internal/controller/fakegit_test.go internal/controller/fakeissues_test.go`, message `feat(gitprovider): issue dependencies, pull request files, closing and force-merging`.

---

### Task 9: controller: a proposal files the issue and blocks the Task

**Files:**
- Create: `internal/controller/task_standard.go`
- Modify: `internal/controller/task_actions.go:51-151` (`apply`, `wait`), `internal/controller/task_sandbox.go:66-115` (`sandboxRun`, env), `:242-249` (result), `internal/controller/task_controller.go:58-80,177-192` (reconciler field, facts), `internal/controller/suite_test.go:144`
- Test: `internal/controller/task_sandbox_unit_test.go`, `internal/controller/task_standard_test.go`

**Interfaces:**
- Consumes: `ActionPropose`, `TaskBlocked`, `StandardChangeIssue`, `ProposalMarker`, `StandardChangeLabel`, `LabelStandardChange`, `LabelReady` (model); `AddIssueDependency`, `Issue.Milestone`, `ListIssues`, `CreateIssue`, `EnsureLabels` (gitprovider); `TaskStandardChange` (API); `EnvAllowedPaths`, `EnvResumed`, `Result.Proposal`, `Result.Empty` (runner).
- Produces:
  - `sandboxRun.allowedPaths []string` (from `task.Spec.AllowedProtectedPaths`, both roles) and `sandboxRun.resumed bool` (implement only; set in Task 12); env `C4_ALLOWED_PATHS` when non-empty, `C4_RESUMED=true` when resumed
  - `runState` copies `Proposal` and `Empty` into `model.RunResult`
  - facts: `StandardChanges = status.StandardChange.Count`, `Change.Issue`/`Change.Paths` from `status.StandardChange`
  - `func (r *TaskReconciler) propose(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus, container *c4v1alpha1.Container, step model.TaskStep) (ctrl.Result, error)`
  - `TaskReconciler.StandardChangeRecheck time.Duration` (zero means a minute, like `ChecksRecheck`; `suite_test.go` sets `time.Second`); `wait` gives a `Blocked` Task the reason `WaitingForStandardChange` (not `Waiting`, which would overwrite it on the next reconcile) and requeues it after that interval. A Blocked Task also wakes when a Task on its Container changes phase (existing watch)
  - Progressing reason `WaitingForStandardChange`; log line `Filed a Standard change` with `issue`, `paths`

- [ ] **Step 1: Write the failing tests**

Unit (`task_sandbox_unit_test.go`): a Task with `AllowedProtectedPaths: [Makefile, .gitea/]` gives env `C4_ALLOWED_PATHS=Makefile,.gitea/` for both roles; `resumed: true` on implement gives `C4_RESUMED=true`; neither set gives neither variable.

envtest (`task_standard_test.go`):

```go
It("files a Standard change for a proposal and blocks", func() {
	// readyContainer(ns); mustCreate(newAgent(ns, 3)); n := gitFake.addIssue(ns, "orders-api", "Package it", clearIssue, []string{model.LabelReady})
	// gitFake.setIssueMilestone(ns, "orders-api", n, "v0.1.0"); Task "orders-api-1" with Spec.Issue = n
	// wait for sandbox orders-api-1-r1-implement; gitFake.pushBranch(…, "task/orders-api-1", "sha-1")
	// finishSandbox(…, &agentrunner.Result{Role: "implement", Commit: "sha-1", Summary: "Wrote scripts/dist.mjs.",
	//	Proposal: &model.Proposal{Branch: "standard/orders-api-1", Paths: []string{"Makefile"}, Reason: "make dist must build the packages"}}, "")
	g.Expect(task.Status.Phase).To(Equal(c4v1alpha1.TaskBlocked))
	g.Expect(task.Status.Round).To(Equal(int32(1)))
	g.Expect(task.Status.Sandbox).To(BeEmpty())
	g.Expect(task.Status.StandardChange).To(PointTo(MatchFields(IgnoreExtras, Fields{
		"Issue": Equal(int32(m)), "Paths": Equal([]string{"Makefile"}), "Branch": Equal("standard/orders-api-1"), "Count": Equal(int32(1))})))
	g.Expect(condition(task.Status.Conditions, ConditionProgressing).Reason).To(Equal("WaitingForStandardChange"))
	filed := gitFake.issue(ns, "orders-api", m)
	// filed.Title == "Standard change for #1: Makefile"; filed.Labels contains model.LabelStandardChange and model.LabelReady
	// filed.milestone == "v0.1.0"; filed.Body contains "make dist must build the packages"
	// gitFake.issue(…, n).blockers contains fmt.Sprintf("#%d", m); its comments mention "#<m>"
	// gitFake.pullRequests(ns, "orders-api") is empty
})
It("reuses the open Standard change it filed when the step repeats", func() {   // Review Focus 4
	// as above, but before finishSandbox file an open issue labelled kind/standard-change whose body has
	// model.ProposalMarker("standard/orders-api-1"); after it: status.StandardChange.Issue == that issue,
	// and exactly one open issue carries model.LabelStandardChange
})
```

- [ ] **Step 2: Run them to see them fail**

Run: `make test`
Expected: FAIL.

- [ ] **Step 3: Implement `propose`**

1. Work through `r.gitRepo` (the operator's user, so the operator authors the issue). Read the result from `status.Sandbox` (as `openPR` does) and the waiting issue (`GetIssue(task.Spec.Issue)`) for its milestone; errors return (requeue).
2. `EnsureLabels(issueLabels() + StandardChangeLabel())`.
3. Find an open `kind/standard-change` issue whose body holds `ProposalMarker(branch)`; else `CreateIssue(title, body, [LabelStandardChange, LabelReady], milestone)` with `StandardChangeIssue`. Errors return.
4. Notes (logged, not fatal): `AddIssueDependency(task.Spec.Issue, filed)`, and a comment on the waiting issue: Task `<name>` needs a Standard change to `<paths>`, filed as #`<m>`, which blocks this issue.
5. `endRun`; `status.Commit`, `status.Summary` from the result; `status.StandardChange = {Issue, IssueURL, Paths, Branch, Count: previous Count + 1}`; phase `Blocked`; Progressing `WaitingForStandardChange` with the step's message.

- [ ] **Step 4: Run the tests**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: `make check`, then commit**: `git add internal/controller/`, message `feat(controller): file a Standard change and block the Task on it`.

---

### Task 10: controller: the Crew runs the change with its allowance

**Files:**
- Modify: `internal/controller/crew_controller.go:125-131` (Tasks), `:168-183` (create), `:208-225` (`readyIssues`)
- Test: `internal/controller/task_standard_test.go`

**Interfaces:**
- Consumes: `CrewTask.Blocked`, `CrewTask.StandardChange`, `Issue.StandardChange`, `IssueStart.AllowedPaths` (Task 4); `TaskSpec.AllowedProtectedPaths` (Task 7).
- Produces: Crew-created Tasks carry `Spec.AllowedProtectedPaths = s.AllowedPaths`; the log line `Started Task for issue` gains `allowedPaths`.

- [ ] **Step 1: Write the failing envtest**

```go
It("runs a Standard change beside the Task it blocks, with its allowance", func() {
	// readyContainer(ns); crewAgents(ns, 2); crew := newCrew(ns); crew.Spec.Parallel = 1
	// issue 1 ready; mustCreate(crew); finish orders-api-1-r1-implement with a Proposal for Makefile (as in Task 9)
	g.Expect(getTask(g, ns, "orders-api-1").Status.Phase).To(Equal(c4v1alpha1.TaskBlocked))
	gated := getTask(g, ns, "orders-api-2")
	g.Expect(gated.Spec.AllowedProtectedPaths).To(Equal([]string{"Makefile"}))
	g.Expect(sandboxEnv(g, ns, "orders-api-2-r1-implement")).To(ContainElement(map[string]any{"name": "C4_ALLOWED_PATHS", "value": "Makefile"}))
	g.Expect(getCrew(g, ns).Status.ActiveTasks).To(ConsistOf("orders-api-1", "orders-api-2"))
})
It("leaves a kind/standard-change issue no Task filed waiting", func() {
	// an issue labelled ready and kind/standard-change, no Task: Crew status Waiting contains it; no Task is created (Consistently, 3s)
})
```

- [ ] **Step 2: Run it to see it fail**

Run: `make test`
Expected: FAIL.

- [ ] **Step 3: Implement**: `Blocked` and `StandardChange` from each Task's status; `StandardChange` on an issue from `slices.Contains(i.Labels, model.LabelStandardChange)`; `AllowedProtectedPaths` on create.

- [ ] **Step 4: Run the tests**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: `make check`, then commit**: `git add internal/controller/`, message `feat(controller): Crews run Standard changes with the filing Task's allowance`.

---

### Task 11: controller: the gated merge

**Files:**
- Modify: `internal/controller/task_standard.go`, `internal/controller/task_actions.go:323-376` (`merge`)
- Branch B also: `internal/model/standard.go`, `internal/model/standard_test.go`
- Test: `internal/controller/task_standard_test.go`

**Interfaces:**
- Consumes: `GatedMergeRefusal` (Task 3); `PullRequestFiles`, `CombinedStatus`, and branch A `ForceMergePullRequest` / branch B `ProtectBranch` + `MergePullRequest` (Task 8); `protectedPaths` (`protection.go`).
- Produces: `func (r *TaskReconciler) gatedMerge(ctx context.Context, provider gitprovider.Provider, repo gitprovider.Repository, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus, container *c4v1alpha1.Container) (refusal string, err error)`; Branch B: `func LiftedPaths(protected, allowed []string) []string` in model (protected minus every entry that covers an allowed path). Reason `GatedMergeRefused`; log line `Merged a Standard change with the admin override` (A) or `Merged a Standard change with its paths lifted` (B), with `url`, `paths`.

- [ ] **Step 1: Write the failing tests**

```go
It("force-merges a Standard change after checking CI, the approval and every path", func() {
	// readyContainer(ns); crewAgents(ns, 2); no Crew. Task "orders-api-1" made directly: Spec.Issue = n,
	// ReviewerRef "reviewer", AllowedProtectedPaths [Makefile]
	// implement -> push sha-1 -> finish -> checks success -> review sandbox; gitFake.setPullFiles(ns, "orders-api", pr.Number, "Makefile")
	// review approve -> Task Merged; pullRequests[0].Merged; issue n closed
	// Branch A: gitFake.forcedMerges(ns, "orders-api") == []int{pr.Number}
	// Branch B: gitFake.branchRule(ns, "orders-api") ProtectedPaths again contains "Makefile"
})
It("refuses a Standard change that touches a path it may not", func() {
	// as above, setPullFiles(…, "Makefile", "README.md"): Task Failed, Succeeded reason "GatedMergeRefused",
	// message contains "README.md"; PR not merged; issue n labelled status/needs-human
})
// Branch B model test: LiftedPaths([.c4factory.yaml .devcontainer/ .gitea/ AGENTS.md Makefile], [Makefile])
//   == [.c4factory.yaml .devcontainer/ .gitea/ AGENTS.md]; with allowed [.gitea/workflows/release.yaml] .gitea/ goes
```

- [ ] **Step 2: Run them to see them fail**

Run: `make test`
Expected: FAIL (the fake refuses the normal merge with "protected files").

- [ ] **Step 3: Implement**

In `merge`, where it now calls `MergePullRequest`, a Task with `AllowedProtectedPaths` calls `gatedMerge`: read `CombinedStatus(status.Commit)` and `PullRequestFiles`, ask `GatedMergeRefusal(checks.State, status.Review.Verdict, files, allowed)`; a refusal ends the run, finishes the Task `Failed` with reason `GatedMergeRefused` and hands the issue back (the merge is never tried). Then:
- **Branch A:** `ForceMergePullRequest` with the reviewed commit, the same title and message.
- **Branch B:** `ProtectBranch` with `ProtectedPaths: model.LiftedPaths(protected, allowed)`, `MergePullRequest`, then `ProtectBranch` with the full rule whatever the merge returned; a failed restore is returned (the next `startRun` restores too).
- **If Task 1 recorded `mergeable: false`:** in `facts`, skip the `conflicted` read for a Task with `AllowedProtectedPaths`, so Gitea's flag doesn't send it to `ResolveConflicts`; a real conflict then fails the merge call and is retried with backoff.
Everything after the merge (end run, close the issue, `Merged`) is unchanged.

- [ ] **Step 4: Run the tests**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: `make check`, then commit**: `git add internal/`, message `feat(controller): merge a Standard change past the protected-file rule after checking it`.

---

### Task 12: controller: resume, done by the change, and failures

**Files:**
- Modify: `internal/controller/task_standard.go`, `internal/controller/task_actions.go` (`apply`), `internal/controller/task_controller.go` (`facts`), `internal/controller/task_actions.go:230-262` (`startRun` passes `resumed`)
- Test: `internal/controller/task_standard_test.go`

**Interfaces:**
- Consumes: `ActionResume`, `ActionDoneByStandardChange`, `StandardChangeOutcome`, `ChangeTask`, `ResumeFeedback` (Task 3); `ClosePullRequest`, `GetIssue` (Task 8); `sandboxRun.resumed` (Task 9).
- Produces:
  - `func (r *TaskReconciler) changeFacts(ctx context.Context, task *c4v1alpha1.Task, status *c4v1alpha1.TaskStatus, container *c4v1alpha1.Container) (model.ChangeFacts, error)`: in `Blocked`, lists the Container's Tasks (index `indexTaskContainer`) with `Spec.Issue == StandardChange.Issue` as `ChangeTask`s (`Message` = their Succeeded condition's message), reads the issue's state, and fills `Merged`/`Failed` from `StandardChangeOutcome`
  - `Resumed` fact and `sandboxRun.resumed`: `status.StandardChange != nil && status.StandardChange.ResumedRound == status.Round` in `Implementing`
  - facts: `Feedback = status.Feedback`
  - `resume`: phase `Implementing`, same round, `Feedback = step.Feedback`, `ResumedRound = Round`, `Checks = nil`, `ReviewAttempts = 0`, Progressing `Resumed`; note on the issue; log `Resumed Task after its Standard change merged` with `issue`, `round`
  - `doneByStandardChange`: end the run, close the open pull request if any (note), comment `Done by Standard change #<m> (<url>): with it merged, this issue needed nothing more.`, remove `status/in-progress`, close the issue, finish `Merged` with reason `DoneByStandardChange`; log `Merged Task done by its Standard change`

- [ ] **Step 1: Write the failing envtests**

```go
It("resumes when its Standard change merges, and ends when that did the whole issue", func() {
	// the Task 10 setup, then drive orders-api-2 to Merged (push, finish implement, checks success,
	// setPullFiles Makefile, review approve) -> gitFake.issue(…, 2).State == "closed"
	g.Expect(task1.Status.Phase).To(Equal(c4v1alpha1.TaskImplementing))
	g.Expect(task1.Status.Round).To(Equal(int32(1)))
	g.Expect(task1.Status.Feedback).To(ContainSubstring("#2"))
	g.Expect(task1.Status.History).To(ContainElement(HaveField("Reason", "Resumed")))
	// delete pod orders-api-1-r1-implement and wait for a fresh sandbox of that name (as the conflict test does)
	g.Expect(sandboxEnv(g, ns, "orders-api-1-r1-implement")).To(ContainElement(map[string]any{"name": "C4_RESUMED", "value": "true"}))
	// finishSandbox(… &agentrunner.Result{Role: "implement", Empty: true}, "")
	g.Expect(task1.Status.Phase).To(Equal(c4v1alpha1.TaskMerged))
	g.Expect(condition(task1.Status.Conditions, ConditionSucceeded).Reason).To(Equal("DoneByStandardChange"))
	// issue 1 closed, a comment contains "Done by Standard change #2"
})
It("fails when its Standard change fails", func() {
	// gated Task finished with an Error at its last round (crewAgents(ns, 1)) -> orders-api-1 Failed,
	// message contains "Standard change #2 will not merge"; issue 1 labelled status/needs-human
})
It("fails when its Standard change is closed without a merge", func() {
	// no Crew (it would start the change within its 1 s poll): the Task 9 setup, proposal -> Blocked on issue m;
	// close issue m in the fake as a person would -> orders-api-1 Failed with "closed without a merge"
})
```

- [ ] **Step 2: Run them to see them fail**

Run: `make test`
Expected: FAIL.

- [ ] **Step 3: Implement** the facts, the two actions in `apply` and their helpers in `task_standard.go`. `ActionFail` from `Blocked` uses the existing fail path (comment, hand back).

- [ ] **Step 4: Run the tests**

Run: `make test`
Expected: PASS.

- [ ] **Step 5: `make check`, then commit**: `git add internal/controller/`, message `feat(controller): resume a Task when its Standard change merges`.

---

### Task 13: prompts: Standard changes for implementers and reviewers

**Files:**
- Modify: `internal/agentrunner/prompt.go:46-89` (`ImplementPrompt`), `:91-143` (`ReviewPrompt`)
- Test: `internal/agentrunner/agentrunner_test.go`

**Interfaces:**
- Consumes: `Job.AllowedPaths`, `Job.Issue`, `Job.ProtectedPaths`, `model.AgentNeverPaths`.

- [ ] **Step 1: Write the failing test**

```go
func TestStandardChangePrompts(t *testing.T) {
	issue := Job{Container: "c", Project: "p", Issue: 4, Goal: "Issue #4", Branch: "task/x", Base: "main",
		ProtectedPaths: []string{"AGENTS.md", "Makefile"}, CheckCommand: []string{"make", "check"}}
	p := ImplementPrompt(issue, Workspace{})
	// p contains "Standard change:", "on main alone", "hook", "AGENTS.md, docs/agents/ and .c4factory.yaml",
	//   "even if your role guide or AGENTS.md says to stop"                    (Review Focus 1)
	goal := issue; goal.Issue = 0
	// ImplementPrompt(goal, …) contains "do not change" and not "Standard change:"
	gated := issue; gated.AllowedPaths = []string{"Makefile"}
	// ImplementPrompt(gated, …) contains "Change only Makefile", "git merge origin/standard/", "proposal branch"
	// ReviewPrompt(issue, …) contains "Makefile" and "a Standard change"
	// ReviewPrompt(gated, …) contains "weakens no check", "works with this change alone", "no more than its issue asks"
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/agentrunner/ -run StandardChangePrompts`
Expected: FAIL.

- [ ] **Step 3: Write the prompt text**

Issue Task: the protected paths are listed; a change to one of them that the issue truly needs goes in the same work, is moved by the runner to a Standard change that a separate Task merges, and must work on `main` alone (a hook such as `npm run dist --if-present`, not a call to a file only this branch has); end the summary with `Standard change: <why the issue needs it>`; never `AGENTS.md`, `docs/agents/` and `.c4factory.yaml`; this holds even if your role guide or AGENTS.md says to stop. Written goal: today's "do not change" line. Gated Task: this Task carries a Standard change; the issue names the proposal branch: start with `git merge origin/<branch>`; change only the allowed paths, nothing else. Reviewer, every Task: the protected list; such changes come through a Standard change, so don't require them in this pull request. Gated reviewer: the three checks.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/agentrunner/`
Expected: PASS.

- [ ] **Step 5: `make check`, then commit**: `git add internal/agentrunner/`, message `feat(agentrunner): tell implementers and reviewers about Standard changes`.

---

### Task 14: Standards' guides, new template versions

**Files:**
- Modify: `templates/go-standard/docs/agents/implementer.md:34-35`, `templates/ts-standard/docs/agents/implementer.md` (the "Never change the Standard's or the platform's files" bullet), both `docs/agents/reviewer.md`, both `AGENTS.md.tmpl` (the "Leave … alone" rule), `templates/versions.txt` (`go-standard v1.1.0`, `ts-standard v1.1.0`), `config/samples/cel-lens/factory.yaml:39,68` (`ref: v1.1.0`)

- [ ] **Step 1: Write the guides**: the implementer bullet becomes a short "Standard changes" section matching the prompt (hook that works on `main` alone, the `Standard change:` line, never the agents' rules or `.c4factory.yaml`); each reviewer guide gets the three gated checks and "don't require a protected file in a normal pull request"; the `AGENTS.md` rule says protected files change only through a Standard change.

- [ ] **Step 2: Bump the versions** in `templates/versions.txt` and the two sample `ref`s.

- [ ] **Step 3: Verify**

Run: `make test`
Expected: PASS (the fake Git host loads the templates at the versions in `versions.txt`, and the catalog tests render them).

- [ ] **Step 4: Commit**: `git add templates/ config/samples/cel-lens/factory.yaml`, message `feat(templates): go-standard and ts-standard v1.1.0 describe Standard changes`.

---

### Task 15: UI: the `Blocked` stage and the issue it waits on

**Files:**
- Modify: `ui/src/k8s/types.ts:197,220-260` (`TaskPhase`, `Task.status.standardChange`, `Task.spec.allowedProtectedPaths`), `ui/src/model/factory.ts:10-53,198-215` (stage, columns), `ui/src/components/Factory.tsx` (Task detail, next to the follow-ups at ~554/657), `ui/src/styles.css:1785` (`.stage-blocked`)
- Test: `ui/src/model/factory.test.ts`

**Interfaces:**
- Produces: `Stage` gains `'blocked'` (`STAGES` entry: label `Blocked`, doing `waiting on a Standard change`, explanation `Waits for the Standard change it filed to merge`); `stageOf` maps `Blocked`; `COLUMNS` gets `{ key: 'blocked', label: 'Blocked' }` before `done`; `export function standardChangeLink(task: Task): { label: string; url?: string; paths: string[] } | undefined` (only while the phase is `Blocked`).

- [ ] **Step 1: Write the failing test**

```ts
it('shows a Blocked Task in its own column, with the issue it waits on', () => {
  const change = { issue: 31, issueURL: 'http://gitea/cel-lens/vscode-extension/issues/31', paths: ['Makefile'], count: 1 }
  const blocked = /* a Task built as the neighbouring tests do, phase 'Blocked', status.standardChange = change */
  expect(stageOf({ phase: 'Blocked' })).toBe('blocked')
  expect(boardColumns([blocked]).blocked).toEqual([blocked])
  expect(standardChangeLink(blocked)).toEqual({ label: '#31', url: change.issueURL, paths: ['Makefile'] })
  expect(standardChangeLink({ ...blocked, status: { ...blocked.status, phase: 'Implementing' } })).toBeUndefined()
})
```

- [ ] **Step 2: Run it to see it fail**

Run: `make ui-test`
Expected: FAIL.

- [ ] **Step 3: Implement** the types, stage, column, link and its rendering ("Waits on #31 (Makefile)"), and a `.stage-blocked` color beside the other stages.

- [ ] **Step 4: Run the tests**

Run: `make ui-test`
Expected: PASS.

- [ ] **Step 5: Commit**: `git add ui/src/`, message `feat(ui): show Blocked Tasks and the Standard change they wait on`.

---

### Task 16: docs

**Files:**
- Modify: `docs/design.md` (v4 list: a bullet "A protected file without a person" linking the spec; the v2 fence bullet: the Git host refuses protected files, and only the operator's gated merge of a Standard change passes, after its own checks), `docs/architecture.md` ("Flow of one Task": proposal, `Blocked`, resume, gated merge; "Flow of one Crew": `Blocked` outside `parallel`, the allowance, and fix the stale "while a Task works on the Container nothing starts" to "up to `parallel` Tasks"), `docs/superpowers/specs/2026-10-02-issue-driven-factory-design.md` (the issue-states diagram: a `kind/standard-change` issue filed `status/ready` that blocks the waiting one, whose Task is `Blocked` until it merges)

- [ ] **Step 1: Write the three changes**, each a few lines in the file's own style.
- [ ] **Step 2: Check links:** `grep -n "standard-change-design" docs/design.md` finds the link.
- [ ] **Step 3: Commit**: `git add docs/design.md docs/architecture.md docs/superpowers/specs/2026-10-02-issue-driven-factory-design.md`, message `docs: Standard changes in the design, the flows and the issue states`.

---

### Task 17: deploy to Kind

**Files:** none (cluster only).

- [ ] **Step 1: Pause both Crews and let running Tasks finish**

```bash
for c in cel-language-server vscode-extension; do kubectl -n cel-lens patch crew $c --type merge -p '{"spec":{"paused":true}}'; done
kubectl -n cel-lens get tasks --no-headers | awk '$4 ~ /Implementing|Checking|Reviewing/'
```
Expected: the second command prints nothing (repeat until it does).

- [ ] **Step 2: Rebuild the agent image** (the CEL Lens Agents run `c4factory/agent-go:dev`; `make agent-image` builds the Flutter one, not needed here)

Run: `make agent-image-go`, then `kubectl -n gitea get pods` and, if Gitea restarted, `kubectl -n gitea rollout status deploy/gitea --timeout=300s`.
Expected: Gitea Running and Ready.

- [ ] **Step 3: Publish the templates**

Run: `make seed`
Expected: the log tags `go-standard` and `ts-standard` `v1.1.0`; "Seed Job completed".

- [ ] **Step 4: Deploy the operator and the UI**

Run: `make deploy && kubectl -n c4-factory-system rollout status deploy/c4-factory-controller-manager --timeout=300s && make ui`
Expected: rolled out; `kubectl get crd tasks.c4.factory.io -o jsonpath='{.spec.versions[0].schema.openAPIV3Schema.properties.status.properties.phase.enum}'` lists `Blocked`.

- [ ] **Step 5: Point the sample Standards at v1.1.0** (patched, not re-applied, so live Agent tweaks stay)

```bash
for s in go-standard ts-standard; do kubectl -n cel-lens patch standard $s --type merge -p '{"spec":{"template":{"ref":"v1.1.0"}}}'; done
kubectl -n cel-lens get containers
```
Expected: every Container still `Ready`.

- [ ] **Step 6: Check health and unpause**

Run: `make doctor`, then the Step 1 loop with `"paused":false`.
Expected: doctor reports no problem; both Crews `Idle` or `Working`.

---

### Task 18: Done when: #4 goes through the factory with no person acting

**Files:**
- Create: `docs/screenshots/v4-standard-change.txt`
- Modify: `docs/results-v4.md` (a paragraph "**Fifth part, a Standard change.**" after the fourth)

- [ ] **Step 1: Close PR #30 unmerged and ready #4** (one Bash call; scratchpad script, as in Task 1)

```bash
set -uo pipefail
G=http://localhost:3917/api/v1 R=cel-lens/vscode-extension
kubectl -n gitea port-forward svc/gitea 3917:3000 >/dev/null 2>&1 & pf=$!; trap 'kill $pf 2>/dev/null' EXIT; sleep 3
PW=$(kubectl -n gitea get secret gitea-admin -o jsonpath='{.data.password}' | base64 -d)
api() { curl -sS -u "c4admin:$PW" -X "$1" -H 'Content-Type: application/json' ${3:+-d "$3"} "$G$2"; }
api POST /repos/$R/issues/30/comments '{"body":"Closed unmerged: the factory now carries Makefile changes as Standard changes, and #4 is ready again."}' >/dev/null
api PATCH /repos/$R/pulls/30 '{"state":"closed"}' | jq -c '{number, state, merged}'
api POST /repos/$R/issues/4/labels '{"labels":[22]}' | jq -c '[.[].name]'
```
Expected: `{"number":30,"state":"closed","merged":false}`; #4's labels include `status/ready` and not `status/needs-human` (24; the scope is exclusive). Then `ps -eo pid,args | grep '[p]ort-forward svc/gitea 3917'` prints nothing.

- [ ] **Step 2: Follow the Tasks** without acting: `kubectl -n cel-lens get tasks` every 10 minutes (a Monitor loop is fine).
Expected, in order: `vscode-extension-4-2` Implementing → `Blocked` (`kubectl -n cel-lens get task vscode-extension-4-2 -o jsonpath='{.status.standardChange}'` names issue `<m>` and `Makefile`); `vscode-extension-<m>` runs with `spec.allowedProtectedPaths: [Makefile]` and merges; `vscode-extension-4-2` resumes in the same round and reaches `Merged`. If anything stalls or fails, record it as it is; don't fix it by hand.

- [ ] **Step 3: Check `make dist` on `main`** with a throwaway Job in `cel-lens` (image `c4factory/agent-go:dev`, `imagePullPolicy: Never`, env `TOKEN` from Secret `gitea-credentials` key `token`), running:

```bash
git -c http.extraHeader="Authorization: token $TOKEN" clone -q http://gitea.gitea.svc.cluster.local:3000/cel-lens/vscode-extension.git /tmp/w \
  && cd /tmp/w && npm ci --silent && make dist && ls -l dist/
```
Expected: one `.vsix` per platform in `dist/`. Delete the Job after reading its log.

- [ ] **Step 4: Save the evidence** in `docs/screenshots/v4-standard-change.txt`: `kubectl -n cel-lens get tasks`; both Tasks' `status.history` (phase, round, reason); the standard-change issue's state and labels and #4's (Gitea API through a port-forward as in Step 1); the Job's `ls -l dist/`.

- [ ] **Step 5: Write the results paragraph**: what ran, how long, rounds, what the agents did with the hook, and anything that went wrong, honestly.

- [ ] **Step 6: Commit**: `git add docs/screenshots/v4-standard-change.txt docs/results-v4.md`, message `docs: record #4 merging through a Standard change`.
