# Plan: issues, crews and roles (v4)

Design: [../superpowers/specs/2026-10-02-issue-driven-factory-design.md](../superpowers/specs/2026-10-02-issue-driven-factory-design.md).
Branch `feat/issue-driven-factory`, stacked on `feat/factory-monitoring` (PR #8).

## Goal

Agents build CEL Lens from a backlog of Gitea issues, picked up by Crews, with
separate implementer and reviewer Agents; the factory learns from the run.

## Steps

1. **API.** Container types `cli` and `extension`; Agent `harness` and `model`;
   Task `issue`, `reviewerRef`, `status.issueTitle` and `issueURL`; the `Crew` kind
   (`kubebuilder create api`). `make manifests generate`.
2. **Rules** (`internal/model`, table tests): issue readiness, pick-up order with
   dependencies and attempts, Task names for issues, the C4 context text.
3. **Git host** (`internal/gitprovider`): issues, labels, dependencies, comments,
   closing, creating, assigning, pull request reviews. Tests against an
   `httptest` Gitea and, with `make test-gitea`, the real one.
4. **Runner** (`internal/agentrunner`, `cmd/c4agent`): model and harness, role
   guides read from the base branch, related repositories cloned read-only, issue
   and acceptance criteria in the prompts, follow-ups in the verdict.
5. **Controllers:** Tasks from issues (labels, comments, Gitea reviews, close,
   follow-ups, needs-human on failure), the reviewer Agent, the Crew controller.
   envtest with the fake Git host.
6. **Images:** the Go and Node toolchain, `agent-go`, a runner labelled `go`.
7. **Templates:** `go-cli`, `go-standard`, `vscode-extension`, `ts-standard`,
   `gitea-release`; rendered with `hack/render` and `make check` green in the
   toolchain image before tagging.
8. **Sample:** `config/samples/cel-lens` (story, catalog, Agents, Crews), the seed
   knows the project, the backlog files and `make cel-lens-backlog`.
9. **Run it:** scaffold, file the backlog, prove the loop with a stand-in Crew,
   then the Claude Crews. Fix what the run finds; record it in
   `docs/results-v4.md`.
10. **Docs:** design.md v4, architecture flows for Crews and issues, AGENTS.md.

## Done when

The spec's "Done when" list holds, `make check` and `make ui-test` pass, and the
evidence is in `docs/screenshots/v4-*` and `docs/results-v4.md`.
