# Standard updates: a Standard's fixes reach the Containers that use it

Issue [#65](https://github.com/JairJosafath/c4factory/issues/65). Agent: otter-65.

## Goal

A Standard's files (its agent guides, `AGENTS.md`, Makefile, lint config, CI) are
rendered into a Container's repository once, at scaffold. A fix to a Standard
in `templates/` reached only Containers scaffolded later: #18's rule for
implementers never reached `cel-lens/cel-language-server`, the Crew it was meant
for. From now on, bumping a Standard's pinned `ref` carries the change to every
Container that uses it, through a pull request the operator opens and merges.

## Decisions

Taken by otter-65 in a headless session (the user was asleep); the user can
revisit them on the pull request.

- **The trigger is the Standard's own `spec.template.ref`.** The catalog entry is
  the one place that pins a Standard; bumping it means every Container on that
  Standard should follow. There is no per-Container switch.
- **The operator writes the change, no agent.** The update is the template's
  own content, reviewed in this repository where the template changed. Agents
  may never change `AGENTS.md` or `docs/agents/`, the files most updates touch,
  so an agent-run gated Task would have to break that rule. The operator opens
  the pull request, waits for CI and merges it through the gated merge.
- **Three-way by whole files.** For each path the Standard renders at either
  ref: if the repository still holds the old render (or neither has it), the
  update writes the new render, or removes the file when the new ref no longer
  renders it; if it already holds the new render, nothing changes; otherwise
  the repository changed the file since (a Standard change or a person), and
  the update leaves it out and names it. A person brings a left-out file up to
  date by hand, like any protected change a person makes. A line-based merge is
  a possible follow-up.
- **One update per Container at a time, recorded in Kubernetes.**
  `status.standard` holds the Standard's name, the ref its files are at, and the
  update in flight. `scaffoldedFrom` and `.theos.yaml` keep recording the
  scaffold.
- **A person can stop it.** Closing the update's pull request drops it until the
  Standard's ref changes again. Failed CI is not final: the update merges once a
  re-run passes.

## The flow

For a Ready Container whose `spec.standardRef` names a Standard:

1. **Where it is.** `status.standard.ref`, or, before the first update, the
   Standard's entry in `status.scaffoldedFrom` (`<name>@<ref>`). A Standard that
   was not part of the scaffold is reported (`AddedAfterScaffold`) and left alone.
2. **Up to date** when that ref equals the Standard's `spec.template.ref`.
3. **Plan.** Fetch the Standard's template at both refs, render it with the
   Container's values (`scaffold.RenderLayer`), read `main`'s copy of every path
   either render has, and plan the update (`scaffold.PlanUpdate`). Nothing to
   write: record the new ref, done.
4. **Open.** Push one commit, authored by the operator, onto the `main` commit
   it read, as branch `standard-update/<standard>-<ref>`, and open a pull request
   into `main`: "Update the Standard go-standard to v1.1.1", its body listing the
   paths and those left out. Record it in `status.standard.update`.
5. **Merge.** Each reconcile reads the pull request. Merged (by anyone): record
   the new ref. Closed: stop until the ref changes. Conflicting with `main`: close
   it, delete the branch and plan again from the new `main`. Open: once CI on its
   commit is green with every check `main` requires, and the pull request changes
   only the planned paths, merge it as admin at that commit
   (`ForceMergePullRequest`). CI pending or failed: wait.
6. **The Standard moves again** while an update is open: close it, delete its
   branch and plan towards the new ref.

The condition `StandardUpdated` reports it: `UpToDate` (naming the paths the last
update left out, if any), `Updating`, `ChecksFailed`, `UpdateClosed`,
`UpdateRefused`, `AddedAfterScaffold`, `NotFound`, `UnpinnedRef`, `Failed` (the
template doesn't render), or `GitError` (retried at the next recheck).

## Steps

1. **Rules** (`internal/scaffold`, `internal/model`): `RenderLayer`, `PlanUpdate`,
   the update's commit message; the branch name, the pull request's text, and
   the merge rule (CI and required checks shared with `GatedMergeRefusal`, paths
   inside the plan). Table tests.
2. **Git** (`internal/gitprovider`): `ReadFiles` (a branch's copy of some paths
   and its head commit) and `PushBranch` (one commit on a given base, force-pushed
   to a branch), with go-git; tests against a local repository.
3. **Operator** (`api/v1alpha1`, `internal/controller`): `status.standard`, the
   condition, and the flow above in `container_standard.go`; the fake Git host
   learns the two calls; envtest for plan, merge, nothing to do, left out,
   closed, Standard moved, conflict and a Standard added after scaffold.
4. **Docs:** `docs/design.md`, `docs/architecture.md`, AGENTS.md's "Scaffold
   once", the factory-ops skill.
5. **Live** (needs the cluster): deploy, check the cel-lens Standards are at
   v1.1.1 in Kubernetes and in the templates' Gitea, and see the update merge in
   `cel-lens/cel-language-server` and `cel-lens/vscode-extension`, #18's rule in
   `docs/agents/implementer.md` on `main`. Evidence in `docs/screenshots/`.

## Done when

`make check` passes with the envtest above, and on the cluster
`cel-language-server`'s `docs/agents/implementer.md` on `main` has #18's rule
through a merged pull request, with `status.standard.ref` at `v1.1.1`.
