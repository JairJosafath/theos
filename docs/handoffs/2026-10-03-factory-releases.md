# Handoff: keep the CEL Lens releases moving through the factory

## Goal

The user wants the factory to run without them: Crews work the Gitea milestones
release by release, and the operator gets fixed when watching shows a flaw. Work
autonomously (memory `work-autonomously`); never use the Fable model.

## Where it stands

Branch `feat/issue-driven-factory`, draft PR #9, pushed through `eeb2cce`.

- **Releases go one at a time** (relay session 7), deployed at 02:38Z.
  - What happened: the Crew started `cel-language-server-44` (v0.2.0) ahead of
    v0.1.1's fixes because it picked by issue number alone.
  - The fix (`eb34cdd`): a milestone with a due date is a release. Only the
    earliest release with work starts; later ones wait in `status.waiting`.
  - Never held: Standard changes with their allowance, and blockers of the
    current release, followed transitively.
  - Spec: docs/design.md, Crew section, "Releases go one at a time"; plan in
    docs/plans/release-order.md.
  - Reviewed by an Opus subagent; its two deadlocks were fixed and tested.
    `make check` and `make ui-test` pass.
  - Live evidence: docs/screenshots/v4-release-order.txt.
- **Milestone due dates** (they set the order and promise nothing):
  - server: v0.1.0 2 Oct, v0.1.1 4 Oct, v0.2.0 6 Oct, v0.3.0 8 Oct
  - extension: v0.1.0 3 Oct, v0.2.0 4 Oct
  - Server #44 moved v0.2.0 → v0.1.1 (it merges before v0.1.1's fixes; comment on #44).
- **Server queue**:
  - v0.1.1: #44 in progress (round 2 Reviewing at 02:43Z), then #58 #59 #60.
  - v0.2.0: #45 #49 #50 wait.
  - v0.3.0: #46 #53 #57 readied (#46 blocks #57); waiting.
  - Server Crew `parallel: 1`.
- **Extension**:
  - v0.1.0 and v0.2.0 milestones: 0 open.
  - v0.2.0 prep was #62 (filed by me, merged in one round as main `4411758`). It
    folded in #53 (duplicate `### Added`), which I closed.
  - `make dist VERSION=v0.2.0` on `4411758` builds five `.vsix`.
  - Crew Idle; only `Later` issues are left, all proposed.
- **Smoke test done**: v0.1.0 `.vsix` built and its bundled server driven over LSP
  stdio (diagnostics, hover, completion, `check`):
  docs/screenshots/v4-extension-smoke.txt. Not tried in a real VS Code.
- **Server releases need no prep issue**: the version comes from the tag
  (`-X main.version`); no version file or changelog in the repo.

## Live thread

- Nothing mid-way.
- Tagging older commits later is fine: the release workflow checks out the tagged
  commit.
- `make deploy` ends with a rollout restart (~6 min in total). The first
  `lastPolled` after it can still come from the old pod: wait for a later poll
  before judging.
- Host memory is tight: one `make test` died with `readdirent ... cannot allocate
  memory`; a rerun passed.
- `make dist` outside the cluster: copy the extension repo and point
  `server.json`'s `releaseUrl` at `http://localhost:3000` (the user's forward);
  never commit that. Clones in `/tmp/smoke/` (throwaway).
- Gitea admin password contains `/`: use `curl -u` or a Basic header, never a
  URL or sed pattern.

## Decisions

- Made by the user: full autonomy; skip e2e; never Fable; release plan approved;
  server Crew `parallel: 1`, extension `parallel: 2`.
- Mine, this session:
  - Releases ordered by milestone due date, not by title.
  - An issue handed back to a person (`needs-human`) frees its release, so the
    factory keeps working.
  - An unreadable active Task's issue stops new starts and says so (fail safe).
  - #44 counted into v0.1.1.
- **Open, for the user: create the tags.** Auto mode refused tag creation to
  headless sessions.
  - Extension `v0.1.0` → `4540ec5`, `v0.2.0` → `4411758`
    (cel-lens/vscode-extension). For each, check the release run and its 5
    `.vsix` assets, then close the milestone.
  - Server `v0.1.1` / `v0.2.0` / `v0.3.0`: tag the merge commit of each
    milestone's last issue once it closes.

## Next steps

1. `kubectl -n cel-lens get crews,tasks`. Confirm the server Crew moves from #44
   to #58–#60 and keeps v0.2.0 waiting until v0.1.1 has no work. Record stalls in
   docs/results-v4.md.
2. When v0.1.1's last issue merges: note its merge commit (the tag target) and
   check the Crew starts v0.2.0 (#45 first). Add both to results-v4.
3. If an issue still lands in the wrong release (as #44 did, started before the
   fix), move it to the release whose tag will hold it and comment why.
4. Extension: nothing ready. Leave `Later` (#49 publishing, #50 win32-arm64, …)
   proposed unless the user asks. A v0.3.0 for the extension would need a scope
   (and due date) from the user.
5. Operator flaws: fix test-first (model table tests, envtest), `make check`,
   `make deploy`.

## State outside git

- Kind cluster `c4factory`: Gitea, operator (`eb34cdd`), runner-go, UI.
- Both Crews running. Etcd no-fsync edits still in the node (originals in
  `/root/manifests-backup/`).
- Secrets: `cel-lens/claude-credentials`, `gitea/gitea-admin` (c4admin),
  `cel-lens/gitea-credentials`, `cel-lens/*-agent-git`. GitHub token from the git
  credential helper (memory `c4factory-repo`).
- No port-forwards of mine. The user's `make gitea-forward` (3000) is running.
- Relay: `.relay/relay.log`; this handoff ends relay session 7.

## References

- docs/design.md (Crew bullets), docs/plans/release-order.md
- docs/results-v4.md (findings table; fifth to seventh parts)
- docs/screenshots/v4-release-order.txt, v4-extension-smoke.txt
- PR https://github.com/JairJosafath/c4factory/pull/9
- Gitea cel-lens/cel-language-server milestones v0.1.1 v0.2.0 v0.3.0, #44 #46 #57;
  cel-lens/vscode-extension #53 #62, milestones v0.1.0 v0.2.0

## For the next session

Skills: `factory-ops`, `handoff`. Docs on demand: docs/dev/testenv.md,
docs/architecture.md ("Flow of one Task"). One task per session; hand off past
~100k tokens.
