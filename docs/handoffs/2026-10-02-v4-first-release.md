# Handoff: v4 CEL Lens run, finishing the first release

## Goal

The user asked (2026-10-02) for the factory to build CEL Lens from a backlog of
Gitea issues worked by Crews, autonomously, improving the factory from what the
run finds; never Fable. Now: finish release v0.1.0 (the extension bundles the
server), record it, keep draft PR #9 current.

## Where it stands

Branch `feat/issue-driven-factory`, pushed, draft PR #9. `make check` passes.
`make e2e` not run (the cluster holds the live run).

- Done this session (all deployed):
  - `1ec4fe3` fix(model): the CI-log excerpt in round feedback knew only
    Flutter; it now picks Go, Vitest, tsc, ESLint and pytest failures.
  - `61c8cb2` feat(controller): reviewers' follow-ups are filed in a `Later`
    milestone (created if missing). Spec, design and architecture docs updated.
  - `1c5e8c4` fix(gitea): readiness probe 5 s timeout, 6 failures. Applied live
    with `kubectl patch`, which restarted Gitea once.
  - `004ad6d`, and the results commit after it: results-v4 "Third part" and four
    rows in the findings table.
- The run: server M2 done (#8: 3 rounds, #9: 2, #10: 1). M4 #11 (user docs): 2
  rounds. #24 (timing tests) failed once (see Decisions); its retry
  `cel-language-server-24-2` merged in 1 round. **Server v0.1.0 is tagged and
  released** at `694085f`, with six binaries and SHA256SUMS.
- In progress: Task `vscode-extension-4` (bundle the server in platform
  packages), the last issue of the extension's v0.1.0. Started 16:40.
- For the user to test locally: `dist/cel-lens/` (git-ignored) holds the server
  `cel-language-server_linux_amd64` (checksum verified), `vscode-extension-0.0.1.vsix`
  built from the extension's main, and `example-policy.yaml` (typo on line 14).
  `check` on it reports `undefined field 'replica'`.

## Live thread

- Milestones in both repos: `v0.1.0` (fixed scope) and `Later` (everything
  else). M1, M2 and M3 are closed. Every open issue has a milestone. The server's
  v0.1.0 has no open issues; the extension's has #4.
- The first release job failed mid-upload: Gitea's readiness probe (1 s default)
  removed Gitea from its Service during 48 MB uploads. After the probe fix I
  deleted the release and the tag, tagged the same commit again, and run 36
  succeeded. Not yet recorded in results-v4.
- The round-1 CI failure of #8 came from my own backlog: #7 and #8 ask for
  wall-clock bounds "on CI". #24 replaced them with best-of-N tests and
  budgeted benchmarks (`go test -run '^$' -bench . ./...`).
- How the round-2 reviewer of #8 read the CI log is unknown; the sandbox pods
  were already gone.
- Gitea's `actions/tasks` ids are not job ids. For a log, take the run id from
  the task's `url`, then `GET actions/runs/<run>/jobs`, then `jobs/<id>/logs`.

## Decisions

- Made by the user: promote server #24, widened to all timing tests; fix my
  `make bench` criterion rather than change the Standard; re-ready #24 rather
  than merge by hand; file follow-ups in `Later` from the operator; tag server
  v0.1.0 and ready extension #4.
- Open, the user's: a "blocked, a person must act" outcome for agents. Today an
  implementer that can't proceed (here, a criterion that needs a protected file)
  spends rounds, and "no changes" in the last round fails the Task. I recommend
  it as the next factory change, planned first in `docs/plans/`.
- Open, the user's: tagging the extension v0.1.0 when #4 merges (a person's step).
  Merging #7, then #8, then #9 is also theirs.

## Next steps

1. `kubectl -n cel-lens get tasks | grep -v Merged`. Follow `vscode-extension-4`
   to its merge. Its follow-ups should land in `Later` (first live check of `61c8cb2`).
2. Add the release to results-v4: the tag, the failed upload and the probe fix,
   then #4's outcome. Push.
3. When #4 merges, ask the user about tagging the extension v0.1.0, then check
   its release, then spec item 5: the `.vsix` installed in VS Code shows diagnostics.
   `code --install-extension <vsix>` works from this devcontainer.
4. Ask the user about the "blocked" outcome (Decisions), and if yes, plan it.
5. Before PR #9 leaves draft: `make e2e` on a fresh cluster.

## State outside git

- Kind cluster `c4factory`: Gitea, the operator (4 agent slots), runner-go, the
  UI. Both Crews unpaused, `parallel: 2`. Static pod manifests edited live
  earlier (etcd no-fsync; originals in `/root/manifests-backup/` in the node).
- Gitea's live Deployment matches `config/gitea/deployment.yaml` again.
- Secrets: `cel-lens/claude-credentials`, `gitea/gitea-admin` (c4admin, also the
  only user allowed `v*` tags), `cel-lens/gitea-credentials` (the operator's
  token), `cel-lens/*-agent-git` (agent bots). Read into variables; never print.
- No port-forwards or monitors of mine are running. The user runs
  `make ui-forward` (:8080) and `make gitea-forward` (:3000).
- Scratchpad clone of vscode-extension (for the `.vsix`): disposable.

## References

- PR: https://github.com/JairJosafath/c4factory/pull/9
- Spec: docs/superpowers/specs/2026-10-02-issue-driven-factory-design.md
- Plan: docs/plans/issue-driven-factory.md; results: docs/results-v4.md
- Gitea: cel-lens/cel-language-server (release v0.1.0, milestones), cel-lens/vscode-extension #4

## For the next session

Skills: `factory-ops` and `handoff`. Docs: docs/architecture.md ("Flow of one
Task", "Flow of one Crew"). Keep to one piece of work per session.
