# Handoff: keep the CEL Lens releases moving through the factory

## Goal

The user wants the factory to run without them: Crews work the Gitea milestones
release by release, and the operator gets fixed when watching shows a flaw. Work
autonomously (memory `work-autonomously`); never use the Fable model.

## Where it stands

Branch `feat/issue-driven-factory`, draft PR #9. Relay session 8's last commit is
this handoff, pushed.

- **Releases go one at a time** (`eb34cdd`, deployed 02:38Z). It works live:
  - #44 merged at 02:45Z (round 2, PR #86, `5e72839`).
  - #58 merged at 03:11Z (round 1, PR #90, `a6123db`).
  - `cel-language-server-59` started at 03:11Z. #60 is ready behind it.
  - v0.2.0's #45 #49 #50 and v0.3.0's #46 #53 wait "for release v0.1.1".
  - #57 waits on #46 (open dependency).
  - Recorded in docs/results-v4.md, seventh part.
- **Extension in a real VS Code** (session 8, `fa8cfdd`, `74a6da3`).
  - VS Code 1.140.0 desktop installed the v0.2.0 linux-x64 `.vsix` with its own CLI.
  - Diagnostics, hover, completion and Restart Server all work.
  - The status bar tooltip's Show Output and Restart Server links, clicked with
    xdotool, both work.
  - Evidence: docs/screenshots/v4-extension-vscode.{png,txt} and
    v4-extension-statusbar-{hover,restart}.png.
  - Commented on extension #58 (left open for a repeatable check).
- **results-v4 row 51** no longer says "Open": the Standard change answered it
  (`0b4bc56`).
- **Milestone due dates** (they set the order and promise nothing):
  - server: v0.1.0 2 Oct, v0.1.1 4 Oct, v0.2.0 6 Oct, v0.3.0 8 Oct
  - extension: v0.1.0 3 Oct, v0.2.0 4 Oct
- **Extension**: v0.1.0 and v0.2.0 milestones have 0 open issues. The Crew is Idle;
  only `Later` issues are left, all proposed.

## Live thread

- Nothing mid-way. #59 was in round 1 when this was written.
- The extension v0.2.0 `.vsix` bundles server **v0.1.0** (`server.json` pins it).
  Server v0.1.1+ reaches users only through an extension release that bumps
  `server.json`. That needs the server tag first, which only a person can create.
- VS Code smoke recipe (scripts in `/tmp/smoke/vscode`, throwaway):
  - Run a `node:24-bookworm` container with Xvfb, GTK, ImageMagick and xdotool.
  - Use `@vscode/test-electron@2`.
  - Set `DONT_PROMPT_WSL_INSTALL=1`: the VS Code CLI stops at a WSL prompt otherwise.
  - Never use the `code` on the host's PATH: it is the devcontainer's remote CLI and
    opens in the user's own VS Code.
  - The image and container were removed afterwards.
- `make deploy` ends with a rollout restart (about 6 min in total). The first
  `lastPolled` after it can come from the old pod: wait for a later poll.
- Host memory is tight, and `/tmp` is a RAM-backed tmpfs. Keep large downloads out
  of it, or delete them after use.
- `make dist` outside the cluster: copy the extension repo and point
  `server.json`'s `releaseUrl` at `http://localhost:3000`; never commit that.
- The Gitea admin password contains `/`: use `curl -u`, never a URL or sed pattern.
- Cosmetic, not fixed: while Working, the Crew message omits issues waiting on
  dependencies (the Idle message counts them). Not worth a redeploy alone.

## Decisions

- Made by the user: full autonomy; skip e2e; never Fable; release plan approved;
  server Crew `parallel: 1`, extension `parallel: 2`.
- Mine (session 7): releases ordered by due date; `needs-human` frees its release;
  an unreadable active Task stops new starts; #44 counted into v0.1.1.
- Mine (session 8):
  - Extension #58 stays open: the by-hand check is done, a repeatable one is not.
  - No issue filed yet for bundling the next server: it waits on the tag.
- **Open, for the user: create the tags.** Auto mode refuses tag creation to
  headless sessions.
  - Extension (cel-lens/vscode-extension): `v0.1.0` → `4540ec5`, `v0.2.0` → `4411758`.
    For each, check the release run and its 5 `.vsix` assets, then close the
    milestone.
  - Server `v0.1.1` / `v0.2.0` / `v0.3.0`: tag the merge commit of each milestone's
    last issue once it closes.

## Next steps

1. Run `kubectl -n cel-lens get crews,tasks`. Follow #59, then #60, and keep v0.2.0
   waiting until v0.1.1 has no work. Record merges (PR, SHA) and any stall in
   docs/results-v4.md, seventh part.
2. When v0.1.1's last issue merges:
   - Note its merge commit, the tag target for the user.
   - Check the Crew starts v0.2.0 (#45 first) and holds v0.3.0.
   - Add both to results-v4.
3. If an issue lands in the wrong release, move it to the release whose tag will
   hold it and comment why.
4. Once the user has tagged server v0.1.1: file and ready an extension issue to
   bump `server.json` to it. It needs a milestone with a due date: create v0.2.1
   (due after v0.2.0) only if the user hasn't scoped the extension's next release.
5. Operator flaws: fix test-first (model table tests, envtest), then `make check`,
   then `make deploy`.

## State outside git

- Kind cluster `c4factory`: Gitea, operator (`eb34cdd`), runner-go, UI.
- Both Crews are running. The etcd no-fsync edits are still in the node (originals
  in `/root/manifests-backup/`).
- Secrets: `cel-lens/claude-credentials`, `gitea/gitea-admin` (c4admin),
  `cel-lens/gitea-credentials`, `cel-lens/*-agent-git`. GitHub token comes from the
  git credential helper (memory `c4factory-repo`).
- No port-forwards or containers of mine. The user's `make gitea-forward` (3000)
  is running.
- Relay: `.relay/relay.log`. This handoff ends relay session 8.

## References

- docs/design.md (Crew bullets), docs/plans/release-order.md
- docs/results-v4.md (findings table; fifth to seventh parts)
- docs/screenshots/v4-release-order.txt, v4-extension-smoke.txt,
  v4-extension-vscode.txt (+ the three v4-extension-*.png)
- PR https://github.com/JairJosafath/c4factory/pull/9
- Gitea cel-lens/cel-language-server: milestones v0.1.1, v0.2.0, v0.3.0; issues
  #59, #60; PRs #86, #90
- Gitea cel-lens/vscode-extension: #58 (comment 1474), milestones v0.1.0, v0.2.0

## For the next session

Skills: `factory-ops`, `handoff`. Docs on demand: docs/dev/testenv.md,
docs/architecture.md ("Flow of one Task"). One task per session; hand off past
about 100k tokens.
