# Handoff: deploy the review fixes, then ship CEL Lens extension v0.1.0

## Goal

The user wants the factory to run without them. The Standard-change plan is done;
what's left: deploy its final-review fixes to Kind, ship the CEL Lens extension
v0.1.0, and keep the factory working through the next milestones. Work
autonomously (memory `work-autonomously`); never use the Fable model.

## Where it stands

Branch `feat/issue-driven-factory`, draft PR #9, pushed through `2de5e64`.

- **Standard changes: done.** Plan docs/plans/standard-change.md, Tasks 1–18,
  each task-reviewed; final whole-branch review (opus) fixed in `c08e0e8..20af81b`
  (gated merge requires each `check*` context, PR files include rename old
  paths, gated prompts ask for no tests, change issues start first, title cap,
  no-Crew Tasks can't propose) and re-reviewed clean. Every ruling is in the PR #9
  comment https://github.com/JairJosafath/c4factory/pull/9#issuecomment-5963918628;
  the SDD workspace is deleted.
- **Live proof:** cel-lens/vscode-extension #4 went through the factory with no
  person acting: `vscode-extension-4-2` filed Standard change #46 (Makefile),
  `Blocked` 6 min, `vscode-extension-46` merged (PR #47), 4-2 resumed and merged
  (PR #48), 24 min total, 2 rounds each (docs/results-v4.md, fifth part;
  docs/screenshots/v4-standard-change.txt). `make dist` on main builds 5 `.vsix`.
- **Not deployed:** the fix wave (`c08e0e8..20af81b`) is not in Kind yet. The
  operator and runner image in Kind are from `20464b3` (Task 17).
- Follow-up GitHub issues filed: #10 (killed agent spends a round), #11 (stale
  `standard/*` branches), #12 (v1.1.0 guides state the hook rule backwards; fix
  in a new template version), #13 (gated reviewer asks to fix the proposal branch).

## Live thread

- Task 17's way to deploy worked in 10 min: pause both Crews
  (`spec.paused: true`), wait until no Task is Implementing/Checking/Reviewing,
  `make agent-image-go`, `make deploy`, rollout status, unpause, `make doctor`.
  No `make seed` needed now (templates unchanged). Never leave the Crews paused.
- `make doctor` warns "optional Release runner not installed". The extension's
  release on a `v0.1.0` tag may need it; check before tagging (docs/dev/testenv.md,
  the `factory-ops` skill).
- Extension follow-ups the reviewer filed: #49 (publish packages from a release
  workflow), #50 (win32-arm64 package), both `status/proposed`.
- At 01:03Z the server Crew was on `cel-language-server-37` (Checking, round 3).
- Foreground waits: `timeout 540 bash -c 'until …; do sleep 20; done'` works in
  one Bash call (plain foreground `sleep` is blocked).

## Decisions

- Made by the user: full autonomy; skip e2e (memory `skip-e2e`); never Fable;
  release plan approved ("go! release plan is good"); the user unpaused both
  Crews (server `parallel: 1`, extension `parallel: 2`); don't change that unless
  I/O pressure stays low (`cat /proc/pressure/io`).
- PR #9 stays draft; merging is the user's call.
- Open, mine: tag extension v0.1.0 as c4admin once its milestone is closed. If
  auto mode refuses the tag, leave it open here for the user, citing their
  approval.

## Next steps

1. Deploy the fix wave as above (pause, wait ≤20 min, `make agent-image-go`,
   `make deploy`, unpause, `make doctor`); ledger nothing, note it here.
2. Extension v0.1.0: check the Gitea milestone `v0.1.0` has no open issues;
   close PR #29 if still open; check the Release runner; tag `v0.1.0` on main;
   check the release and its assets; install the `.vsix` here
   (`code --install-extension`) and see diagnostics from the server.
3. Factory: server v0.3.0 (#46 #53 #57 in cel-lens/cel-language-server) once the
   fixes are deployed; extension v0.2.0 (#9 #11 #13) after v0.1.0 ships. Ready
   issues per the release plan; watch, don't fix by hand; record stalls.
4. Update memory `issue-driven-factory-state` with where this lands.

## State outside git

- Kind cluster `c4factory`: Gitea, operator, runner-go, UI. Both Crews
  **running**. Etcd no-fsync edits still in the node (originals in
  `/root/manifests-backup/`).
- Secrets: `cel-lens/claude-credentials`, `gitea/gitea-admin` (c4admin; password
  contains `/`, use `curl -u`), `cel-lens/gitea-credentials`, `cel-lens/*-agent-git`.
  GitHub token: from the git credential helper (memory `c4factory-repo`).
- No port-forwards of mine. The user runs `make ui-forward` (8080) and
  `make gitea-forward` (3000).
- Relay: `.relay/relay.log`; this handoff ends relay session 5. No peer session
  (`c4factory-a7`) was running.
- After a PC restart the devcontainer's dockerd can lose a race with containerd;
  re-run `/usr/local/share/docker-init.sh`.

## References

- Spec docs/superpowers/specs/2026-10-02-standard-change-design.md; plan
  docs/plans/standard-change.md; results docs/results-v4.md
- PR https://github.com/JairJosafath/c4factory/pull/9; issues #10–#13
- Gitea cel-lens/vscode-extension #4, #46, PRs #47 #48; milestones in both
  cel-lens repos

## For the next session

Skills: `factory-ops`, `handoff`. Docs on demand: docs/dev/testenv.md,
docs/architecture.md ("Flow of one Task"). One task per session; hand off past
~100k tokens.
