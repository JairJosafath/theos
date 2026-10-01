# Results: the agent factory (v2)

What happened when agents built the toolbox apps on C4 Factory, what it taught us
about the platform, and what changed because of it. Evidence is in
`docs/screenshots/v2-*`.

## The loop, proven

| Run | Agent | Rounds | Outcome |
| --- | --- | --- | --- |
| `stand-in-loop` on image-shrink | stand-in | 2 (changes asked once, by design) | Merged |
| `image-shrink-v1` | Claude Code | 3 | Merged |

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
