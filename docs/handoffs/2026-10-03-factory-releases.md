# Handoff: keep the CEL Lens releases moving through the factory

## Goal

The user wants the factory to run without them: Crews work the Gitea milestones
release by release, and the operator gets fixed when watching shows a flaw. Work
autonomously (memory `work-autonomously`); never use the Fable model.

## Where it stands

Branch `feat/issue-driven-factory`, draft PR #9, pushed through this handoff.

- **Fix wave deployed** (relay session 6, 01:06–01:20Z): Crews paused ~9 min while
  `cel-language-server-37` merged, `make agent-image-go`, `make deploy`, unpaused,
  `make doctor` healthy. The Release runner warning is irrelevant: the
  extension's release job runs on the `go` CI runner (`runs-on: go`).
- **Operator fix `c86c77d`**, deployed: one 30 s context covered merge + issue
  calls, so vscode-extension #19 stayed open after its PR merged (#41 filed late,
  comment and close failed). Each issue call now has its own time
  (`TaskReconciler.GitTimeout`); unit test `TestCloseIssueAfterALateAnswer`.
  `make check` passed. Recorded in docs/results-v4.md (finding + sixth part).
- **Extension v0.1.0: ready, not tagged.** Closed #19 and PR #29 by hand; filed
  #51 (version 0.1.0 + dated changelog), merged in one round. Milestone v0.1.0:
  0 open. Main at `4540ec5` has `package.json` 0.1.0. Creating the tag through the
  Gitea API was **refused by auto mode** ([External System Writes]).
- **Extension v0.2.0 readied**: #9, #11, #13 relabelled `status/ready`. At 01:47Z
  `vscode-extension-11` (round 2) and `-13` were Checking.
- **Server**: v0.1.1 (#38 #58 #59 #60) and v0.2.0 (#44 #45 #49 #50) issues are
  ready; `cel-language-server-38` was Reviewing at 01:47Z. v0.3.0 (#46 #53 #57)
  deliberately left `status/proposed` (see Decisions).

## Live thread

- Nothing mid-way. Tagging an older commit later is fine: the release workflow
  checks out the tagged commit (`gitea.sha`), so v0.2.0 work landing on main
  doesn't change v0.1.0.
- `make dist` downloads the server from `server.json`'s `releaseUrl`, the
  in-cluster Gitea URL; to build a `.vsix` outside the cluster, point a local copy
  at a port-forward (don't commit that).
- #46 is partly done already: `BenchmarkComplete` exists
  (`internal/analysis/complete_test.go`), `BenchmarkExplain` (hover) doesn't.
- #57 edits a workflow and #53 the Makefile: both likely go through Standard
  changes. #57 builds on #46's benchmarks; make #46 a blocker of #57 when readying.
- Gitea port-forward helper gotcha: `pgrep -f` with a pattern that also appears
  later in the same command line kills your own shell (exit 144).

## Decisions

- Made by the user: full autonomy; skip e2e; never Fable; release plan approved
  ("go! release plan is good"); server Crew `parallel: 1`, extension `parallel: 2`
  (raise only if `/proc/pressure/io` stays low; it was ~30% avg300 at 01:06Z).
- Mine: server v0.3.0 stays proposed until v0.1.1 and v0.2.0 are closed, because
  the Crew picks ready issues regardless of milestone and would mix releases.
- **Open, for the user:** create the tags. Auto mode refuses them for headless
  sessions. Extension `v0.1.0` → `4540ec5` (cel-lens/vscode-extension), then
  check run + 5 `.vsix` assets and close the milestone. Same for server `v0.1.1`
  / `v0.2.0` when their milestones close (server needs no version bump?
  check its Makefile's release check first, as the extension needed #51).

## Next steps

1. `kubectl -n cel-lens get crews,tasks`; record stalls in docs/results-v4.md;
   fix operator flaws test-first (envtest/unit), `make check`, `make deploy`.
2. Smoke-test the extension without a GUI: build the linux-x64 `.vsix` from main
   (see Live thread), unzip, run the bundled server with `--version` and an LSP
   `initialize` + `didOpen` over stdio, expect `publishDiagnostics`. Save the
   output in docs/screenshots/. Installing into VS Code is still the user's.
3. When server v0.1.1 closes: check the server's release-version rule and file a
   release-prep issue if needed (like extension #51); then the tag is the user's.
4. When server v0.2.0 closes: ready #46, #53, #57 (blocker #46 → #57).
5. Extension after v0.2.0: #49/#50 (publishing, win32-arm64) are proposed in
   Later; leave them unless the user asks.

## State outside git

- Kind cluster `c4factory`: Gitea, operator (`c86c77d`), runner-go, UI. Both Crews
  **running**. Etcd no-fsync edits still in the node (originals in
  `/root/manifests-backup/`).
- Secrets: `cel-lens/claude-credentials`, `gitea/gitea-admin` (c4admin; use
  `curl -u`), `cel-lens/gitea-credentials`, `cel-lens/*-agent-git`. GitHub token
  from the git credential helper (memory `c4factory-repo`).
- No port-forwards of mine. The user's `make gitea-forward` (3000) is running.
- Relay: `.relay/relay.log`; this handoff ends relay session 6.

## References

- docs/results-v4.md (findings table, fifth and sixth parts)
- PR https://github.com/JairJosafath/c4factory/pull/9; GitHub issues #10–#13
- Gitea cel-lens/vscode-extension #19 #29 #41 #51, milestones v0.1.0 v0.2.0;
  cel-lens/cel-language-server milestones v0.1.1 v0.2.0 v0.3.0

## For the next session

Skills: `factory-ops`, `handoff`. Docs on demand: docs/dev/testenv.md,
docs/architecture.md ("Flow of one Task"). One task per session; hand off past
~100k tokens.
