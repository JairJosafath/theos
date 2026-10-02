# Handoff: v4 CEL Lens run continues, draft PR #9 open

## Goal

The user asked (2026-10-02) for the factory to build CEL Lens from a backlog of
Gitea issues worked by Crews, with reviewers and the right model per role,
autonomously, improving the factory from what the run finds; never Fable. Now:
keep the run going through M2 and M4, record it, and keep PR #9 current.

## Where it stands

Branch `feat/issue-driven-factory`, pushed, draft PR #9 (base
`feat/factory-monitoring`, PR #8, which sits on PR #7). `make check` and the UI's
`npm run check` pass. `make e2e` was not run: the cluster holds the live run.

- Done this session:
  - `7f95355` fix(kind): etcd `--unsafe-no-fsync`, 60 s leases (40 s renew) for
    the scheduler and controller-manager. The user chose this. It was applied live
    by editing `/etc/kubernetes/manifests/*.yaml` in the node; the originals are
    in `/root/manifests-backup/` there (keep them out of the manifests directory).
    It was checked on a throwaway cluster made from `hack/kind.yaml`.
  - `f709bd1` fix(ui): "Working now" counts Crew Tasks (`workingNow` in
    `ui/src/model/story.ts`); deployed.
  - `0004bb9` docs: results-v4 "Second part", two findings, and evidence in
    `docs/screenshots/v4-parallel-{conflict.txt,story.png,factory.png}`.
- The run: 6 issues merged 12:40 to 13:46, and one conflict was resolved in its
  round (server #7 after #6). M1 Diagnostics and M3 VS Code are complete. Now
  Crew cel-language-server works on #8 (M2), in Checking, round 1.
  vscode-extension is Idle: what's left is `status/proposed`.

## Live thread

- Since the etcd change there have been no slow syncs and no restarts, and API
  requests stay under 2 s with Tasks only. The worst stalls since came from my
  own actions: a throwaway Kind cluster, and loading the UI image (16 s, which
  nothing survived badly). The disk (WSL vhdx) is slow: about 14 MB/s of writes
  gave 50% I/O pressure, mostly the Go implementer's build cache on the image
  layer. If stalls come back, measure per-pod writes from the node's cgroup
  `io.stat` (`kubelet.slice/.../*-pod<uid>.slice/io.stat`, summing `wbytes`).
- Playwright's Chromium needed `npx playwright install chromium` and
  `sudo env "PATH=$PATH" npx playwright install-deps chromium` after the
  devcontainer rebuild. Both are done.

## Decisions

- Made: the user promoted vscode-extension #7, #8 and #15 (all merged); I wrote
  their acceptance criteria. Etcd without fsync (user). PR #9 as a draft on #8 (user).
- Open, the user's: what else the extension Crew should do. #4 (platform
  packages and the `.vsix`) needs server releases (M4) first. The other follow-ups
  (#9, #11 to #13, #16, #18 to #20) are proposed. Ask before promoting any;
  each needs an "Acceptance criteria" checklist.
- Open: merging #7, then #8, then #9 is the user's call.

## Next steps

1. Check progress: `kubectl -n cel-lens get crews,tasks`, and `free -m`.
2. As server issues land (#8, then the ones that depend on it, then M4
   release), add a line or two to results-v4's "Second part", or start a third
   part for M2 and M4. Push, and the PR follows.
3. When the server has a release, ask the user about promoting extension #4,
   which leads to spec item 5: a `.vsix` installed in VS Code showing diagnostics.
4. Before the PR leaves draft, run `make e2e` on a fresh cluster. That also
   proves `hack/kind.yaml` end to end.

## State outside git

- Kind cluster `c4factory`: Gitea, the operator (4 agent slots), runner-go, the
  UI. Both Crews are unpaused. The static pod manifests were edited live (above).
- Secret `cel-lens/claude-credentials`, copied from toolbox; never print it.
  Gitea admin: `kubectl -n gitea get secret gitea-admin`; read it into a variable.
- Port-forwards: the user runs `make ui-forward` (:8080) and `make gitea-forward`
  (:3000). Reuse them read-only; open your own only with a captured PID, and
  kill it in the same step.

## References

- PR: https://github.com/JairJosafath/c4factory/pull/9
- Spec: docs/superpowers/specs/2026-10-02-issue-driven-factory-design.md
- Plan: docs/plans/issue-driven-factory.md; results: docs/results-v4.md
- Evidence: docs/screenshots/v4-*

## For the next session

Skills: `factory-ops` (Crews, Tasks, Gitea, memory) and `handoff`. Docs:
docs/architecture.md ("Flow of one Task", "Flow of one Crew").
