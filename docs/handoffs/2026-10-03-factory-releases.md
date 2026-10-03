# Handoff: keep the CEL Lens releases moving through the factory

## Goal

The user wants the factory to run without them: Crews work the Gitea milestones
release by release, and the operator gets fixed when watching shows a flaw. Work
autonomously (memory `work-autonomously`); never use the Fable model.

## Where it stands

Branch `feat/issue-driven-factory`, draft PR #9. Relay session 9's last commit is
this handoff, pushed.

- **Server v0.1.1 is done** (0 open, 9 closed). The Crew crossed into v0.2.0
  without a person.
  - #59 merged at 03:34Z (round 1, PR #92, `c927061`).
  - #60 merged at 03:48Z (round 1, PR #94, `d3c9417`). **`d3c9417` is the v0.1.1
    tag target.**
  - `cel-language-server-45` (v0.2.0) started at 03:48Z. Its round 1 was sent back
    at about 04:06Z, and round 2 was implementing when this was written.
  - #49 and #50 wait for a free place. v0.3.0's #46 and #53 wait "for release
    v0.2.0"; #57 waits on #46.
  - Recorded in docs/results-v4.md (seventh part, `367da04`, `9c29f56`) and
    docs/screenshots/v4-release-order.txt.
- **Operator fix deployed** (`2eb3283`, deployed 03:28Z): while Working, the Crew
  message now counts issues on open dependencies, as the Idle one did. Verified
  live: "...; 1 waits on open dependencies; 2 ready issues wait for release v0.2.0".
- **Reviewer follow-ups** filed as `status/proposed` under Later and left there,
  like the earlier ones:
  - #93: test that member completion leaves out internal functions.
  - #95: README hover-docs wording.
- **Extension**: v0.1.0 and v0.2.0 milestones have 0 open issues. The Crew is Idle;
  only proposed `Later` issues are left.
- **Milestone due dates** (they set the order and promise nothing):
  - server: v0.1.0 2 Oct, v0.1.1 4 Oct, v0.2.0 6 Oct, v0.3.0 8 Oct
  - extension: v0.1.0 3 Oct, v0.2.0 4 Oct

## Live thread

- #45 round 2. Round 1 met every criterion except one: a plain `.` after an
  optional receiver didn't behave like a normal select.
  - `object.?spec.|` completed `hasValue/orValue/value` instead of the
    DeploymentSpec fields.
  - Hover on `object.?spec.repl|icas` returned nil.
  - The cause: `unwrapOptional` runs only when `cur.optional` is set
    (complete.go:448, hover.go:93).
  - Read it with `kubectl -n cel-lens get task cel-language-server-45 -o jsonpath='{.status.review.summary}'`.
- The extension v0.2.0 `.vsix` bundles server **v0.1.0** (`server.json` pins it).
  Server v0.1.1+ reaches users only through an extension release that bumps
  `server.json`, and that needs the server tag first.
- `make deploy` takes about 6 min. The first `lastPolled` after it can come from
  the old pod: wait for a poll after the new pod's start time.
- Bash foreground commands time out at 600 s. Wait on a Task in loops of about
  570 s.
- Host memory is tight, and `/tmp` is a RAM-backed tmpfs.
- The Gitea admin password contains `/`: use `curl -u`, never a URL or sed pattern.
- The VS Code smoke recipe is in the previous handoff's history (`7a41365`) and in
  results-v4.

## Decisions

- Made by the user: full autonomy; skip e2e; never Fable; release plan approved;
  server Crew `parallel: 1`, extension `parallel: 2`.
- Mine (sessions 7 and 8):
  - Releases are ordered by due date.
  - `needs-human` frees its release.
  - An unreadable active Task stops new starts.
  - Extension #58 stays open; no bump issue until there is a server tag.
- Mine (session 9):
  - Deployed the message fix on its own: cheap, and Tasks carried on.
  - Reviewer follow-ups (#93, #95) stay proposed. Adding them to a release would
    change the plan the user approved.
- **Open, for the user: create the tags.** Auto mode refuses tag creation to
  headless sessions.
  - Extension (cel-lens/vscode-extension): `v0.1.0` → `4540ec5`, `v0.2.0` → `4411758`.
    For each, check the release run and its 5 `.vsix` assets, then close the
    milestone.
  - Server (cel-lens/cel-language-server): **`v0.1.1` → `d3c9417`**, then close
    the v0.1.1 milestone. For `v0.2.0` / `v0.3.0`, tag the merge commit of each
    milestone's last issue once it closes.

## Next steps

1. Run `kubectl -n cel-lens get crews,tasks`. Follow #45 round 2, then #49 and #50.
   Record merges (PR, SHA), rounds and any stall in docs/results-v4.md, seventh
   part.
2. When v0.2.0's last issue merges:
   - Note its merge commit, the tag target for the user.
   - Check the Crew starts v0.3.0 (#46 first, then #53; #57 after #46).
   - Add both to results-v4 and the evidence file.
3. If #45 fails three times or goes `needs-human`, read the reviews. Split or
   clarify the issue as its maintainer and ready it again.
4. Once the user has tagged server v0.1.1 (check `/repos/cel-lens/cel-language-server/tags`):
   - File and ready an extension issue to bump `server.json` to it.
   - It needs a milestone with a due date: create v0.2.1 (due after v0.2.0) only
     if the user hasn't scoped the extension's next release.
5. Operator flaws: fix test-first (model table tests, envtest), then `make check`,
   then `make deploy`.

## State outside git

- Kind cluster `c4factory`: Gitea, operator (`2eb3283`), runner-go, UI.
- Both Crews are running. The etcd no-fsync edits are still in the node (originals
  in `/root/manifests-backup/`).
- Secrets: `cel-lens/claude-credentials`, `gitea/gitea-admin` (c4admin),
  `cel-lens/gitea-credentials`, `cel-lens/*-agent-git`. GitHub token comes from the
  git credential helper (memory `c4factory-repo`).
- No port-forwards or containers of mine. The user's `make gitea-forward` (3000)
  is running.
- Relay: `.relay/relay.log`. This handoff ends relay session 9.

## References

- docs/design.md (Crew bullets), docs/plans/release-order.md
- docs/results-v4.md (findings table; fifth to seventh parts)
- docs/screenshots/v4-release-order.txt, v4-extension-smoke.txt,
  v4-extension-vscode.txt (+ the three v4-extension-*.png)
- PR https://github.com/JairJosafath/c4factory/pull/9
- Gitea cel-lens/cel-language-server: milestones v0.1.1 (done), v0.2.0, v0.3.0;
  issues #45 #49 #50 #93 #95; PRs #92, #94
- Gitea cel-lens/vscode-extension: #58 (comment 1474), milestones v0.1.0, v0.2.0

## For the next session

Skills: `factory-ops`, `handoff`. Docs on demand: docs/dev/testenv.md,
docs/architecture.md ("Flow of one Task"). One task per session; hand off past
about 100k tokens.
