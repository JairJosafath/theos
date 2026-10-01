# Results: the agent factory (v2)

What happened when agents built the toolbox apps on C4 Factory, what it taught us
about the platform, and what changed because of it. Evidence is in
`docs/screenshots/v2-*`.

## The loop, proven

| Run | Agent | Rounds | Outcome |
| --- | --- | --- | --- |
| `stand-in-loop` on image-shrink | stand-in | 2 (changes asked once, by design) | Merged |
| `image-shrink-v1` | Claude Code | 3 | Merged |
| `audio-cutter-v1` | Claude Code | 3 (round 2 failed CI on a flaky widget test) | Merged |
| `bg-eraser-v1` | Claude Code | 3 | Merged |
| `doc-scanner-v1` | Claude Code | 2 | Merged |
| `photo-editor-v1` | Claude Code | 3 | Merged |
| `qr-scanner-v1` | Claude Code | 4 (the limit in the sample) | Failed: each fresh reviewer found new points; the last round asked for an auto-backup exclusion and two style fixes |
| `qr-scanner-v2` (same goal, converging reviewer) | Claude Code | 1 | Merged; the reviewer listed the backup exclusion as non-blocking, so it became a follow-up Task |
| `qr-scanner-backup` (follow-up) | Claude Code | 1 | Merged |

### image-shrink-v1, round by round

1. **Implement:** a complete v1 in the layered architecture from `AGENTS.md`
   (services, repositories returning `Result`, a view model), with restoration after
   process death, a draft, clear errors for bad input, and about 1,300 lines of
   tests including accessibility guidelines, dark theme and 200% text. `make check`
   green, no INTERNET permission. The agent said plainly what it could not verify:
   the native encoder, picker, gallery and share sheet, since it can't build an APK.
   **Review: changes.** A corrupt image header could throw past the repository and
   leave the UI stuck on "shrinking"; asked for the fix plus a repository and a view
   model test.
2. **Fixed, CI green. Review: changes.** The Android plugin's scaling rule would
   shrink every photo to about one pixel when no resize was wanted, which the fake
   codec in the tests could not catch; asked for a test pinning the parameters sent
   to the plugin, and to move 100 MB file reads off the UI isolate.
3. **Fixed, CI green. Review: approve.** Squash-merged.

The reviewer caught two bugs that CI could not: one about error paths, one about a
third-party plugin's behaviour. That is the point of an agent reviewer next to CI.

## Platform findings, and what changed

| Finding | Change |
| --- | --- |
| The full Flutter + Android image is 19.6 GB; flattening it restarted the laptop twice | CI checks and agents run on a slim image without the Android SDK (2.35 GB). Release builds get their own runner on the full image (`flutter-android`, android-release v1.1.0). |
| Six Tasks would start six agents at once | The operator runs one agent sandbox at a time by default (`--max-agent-sandboxes`). |
| A Task waited forever when no runner picked up its CI | It fails after 30 minutes without a CI result, without costing a round. |
| Template repos ran their own CI workflow, taking the only runner | The seed turns Actions off in template repositories. |
| Creating the agent's Git Secret through the cache needed RBAC the operator doesn't have; envtest runs as admin, so only Kind showed it | The Secret is written without the cache. |
| A sandbox got the Task's round from before the step: round 1 ran as round 0 | Sandboxes take the step's status; envtest checks their environment. |
| Agents can't build an APK on the slim image, so native plugin wiring is untested until a release | Open. Options: an on-demand APK build job, or let the reviewer ask for `make build` on the release runner. |
| Squash merges are authored by the operator's Git user, not the agent | The merge message credits the agent's bot user with a Co-authored-by trailer. |
| The Claude token is readable inside the sandbox | Open: inject it at the egress (Agent Substrate) later. |
| A Task in the middle of its rounds kept taking the one agent slot while others waited | Tasks queue for a slot and the longest waiter goes first (`model.HasSlotTurn`, `status.slotWaitSince`). Sandboxes are counted live from the API server. (The commit says two sandboxes ran at once; that was a misreading: the second Task was only *Implementing* while waiting for a slot. Counting live closes the cache-lag risk anyway.) |
| A widget test passed in the agent's sandbox and failed in CI (flaky), and the agent got only a link it can't open | The operator reads the failed CI job's log and passes the failing tests, analyzer findings and errors as feedback (`model.CheckFailures`). |
| The operator restarted 12 times in 100 minutes: with CI and an agent running, the single-node API server stalled past the 10 s lease renewal and the manager exited ("leader election lost"). Idempotent steps meant no Task broke | The lease lasts a minute by default (`--leader-elect-lease-duration`), renewed within 40 s. |
| A Task failed after four rounds although every round fixed what was asked: each review starts afresh, didn't know the round or what was asked before, and blocked on style points as hard as on bugs | The reviewer gets the round, the limit and the previous feedback (`C4_MAX_ROUNDS`, `C4_FEEDBACK`). It reports everything at once, blocks only on defects, lists the rest as non-blocking, and knows when "changes" fails the Task. |
| The Standard told apps to keep user data in Auto Backup, with no exception for secrets: QR scan history with Wi-Fi passwords would go to the cloud | flutter-standard v1.1.0 requires excluding secrets from backups, with a manifest test. Scaffold-once means only new Containers get it. |
| With the converging reviewer, qr-scanner-v2 merged in one round, but the backup exclusion for Wi-Fi passwords, a privacy point, went into "Non-blocking". The repository still has flutter-standard v1.0.0, which says to keep user data in backups, and the reviewer followed it | Follow-up Task `qr-scanner-backup`. The lesson: the reviewer's bar is only as good as the Standard in the repository; scaffold-once means a Standard fix needs follow-up Tasks for existing apps. |
| Branch review: the bot user could merge its own pull request (any write collaborator may merge once checks pass), and could change `.gitea/` or the Makefile to make the checks pass; protected paths were only checked inside the sandbox | The Git host enforces it: only the operator's user merges, only the reviewed commit; changes to the platform's files and the Standard's new `protectedPaths` can't be merged; `v*` tags are the operator's. Applied at every Task start, so older repositories catch up. |
| Branch review: a Task on a Container without a Standard ran on an unprotected `main` | It fails at once. |
| Branch review: a Task waiting for something other than a slot (CI, an earlier Task) still held a place in the slot queue, which could deadlock two Tasks on one Container | Only a Task about to start a sandbox queues (`TaskStep.SlotWait`). |
| Branch review: a pull request closed by hand left the Task in Reviewing, posting an approval on every retry | The Task fails with `PullRequestClosed`; the approval is posted after the merge. |
| Branch review, open: the agent's code runs on a shared CI runner in host mode; the bot token lives in the agent's environment and is reused across runs | Next: ephemeral CI job containers or a runner per project, and a token per run. |
| Building the six APKs (by hand, in the full toolchain image): every build downloaded an Android NDK and CMake the image lacks, and QR Scanner didn't build: the agent chose `permission_handler_android` 14.1.0, which needs compileSdk 37 while the template compiles against 36. CI runs `make check`, never `flutter build`, so no one saw it | Open, and the case for the APK gap: add the NDK, CMake and platform 37 to the full image, run `make build` in CI on the release runner (or a nightly), and a Task to fix QR Scanner's compile SDK. The test APK was built with compileSdk 37 locally. |
