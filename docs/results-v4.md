# Results: issues, crews and roles (v4)

What happened when agents built CEL Lens on C4 Factory from a backlog of Gitea
issues, what it taught us about the factory, and what changed because of it. The
design is [superpowers/specs/2026-10-02-issue-driven-factory-design.md](superpowers/specs/2026-10-02-issue-driven-factory-design.md);
evidence is in `docs/screenshots/v4-*`.

## The product, and why it suits agents

CEL Lens gives CEL inside Kubernetes YAML the support a language deserves: the
errors the API server would report, at the right line and column, completion and
hover, offline. It is two Containers joined by LSP: a Go language server (also a
CI linter) and a VS Code extension. Unlike v2's mobile apps, every behaviour can
be tested headless: Go tests against Kubernetes' own CEL libraries, protocol
tests, grammar tests and a packaging check.

Before writing issues, a spike checked the risky part: Kubernetes' own type
checker (`validating.TypeChecker`) checks a policy against Deployment's schema
from a local OpenAPI file, offline, with the API server's messages and positions;
completion works by compiling the receiver and listing its type's fields. The
issues carry those findings as notes.

## The issue loop, proven with stand-ins

A throwaway Container with a stand-in Crew ran the whole loop in Kind
([evidence](screenshots/v4-issue-loop.txt)): the Crew handed an issue without
acceptance criteria back with `status/needs-human`, started the ready one, waited
for the dependent one until the first closed, and each Task went through two
rounds (the stand-in asks for changes once). Gitea shows it as a person would
follow it: the issue labelled in progress and assigned to the agent bot, a
comment naming the Agents, a pull request by the agent that closes the issue, a
"request changes" review and an approval naming the reviewer Agent, and the
issue closed with a comment.

## Platform findings, and what changed

| Finding | How it showed | Change |
| --- | --- | --- |
| Every pull request comment since v2 failed: round feedback, approvals | Probing Gitea's issue API with the operator's token: "token does not have at least one of required scope(s), required=[write:issue]". The toolbox's pull requests had zero comments; the failure was only logged | The seed's tokens get `write:issue`; a reseed replaces tokens whose scopes differ |
| The operator opened pull requests as itself, so Gitea refused its reviews | The first stand-in run: "approve your own pull is not allowed", and no reviews on the pull request | Pull requests are opened with the Container's agent bot token. The fake Git host in envtest refuses self-reviews now, so the specs catch it |
| A freshly scaffolded Container never read its CI result | cel-language-server showed no checks while its run had passed in 38 s | Finishing a scaffold schedules the first read. The envtest spec used to nudge the Container with an annotation, which hid it |
| Compiling the Kubernetes CEL libraries cold takes about 7 minutes and 2.9 GB (half for `-race`); every agent sandbox starts cold | Measured in the toolchain image with a program importing the libraries | The toolchain image ships the build cache warm (image 5.8 GB). CI on a scaffolded repository took 38 s |
| A usage limit would cost a round, so a Crew would burn its rounds and hand back every issue in turn | Found while planning a run of many hours | `unavailable` results keep the round; the Task waits until the reported reset (or 15 minutes) and tries the same step again |
| Loading a 6 GB image into Kind restarted Gitea | Gitea's restart count while `make runner-go` loaded its image | Open: build and load the big images rarely, never while Tasks run. Loading even the UI image still stalls etcd (16 s), but with the longer leases below nothing restarted |
| Local agent worktrees inside the repository broke `controller-gen` and Docker builds | I/O errors from a worktree's `node_modules` | `.claude/` is ignored by Git and every image's build context |
| A Go fuzz test took 11 of the node's 12 cores in one sandbox | `kubectl top` while a cel-language-server Task ran its tests | Sandboxes had no CPU limit. The sample Agents have one now: implementer 4 cores, reviewer 2 |
| A Task could read the pod of an earlier sandbox with the same name as its new run's result | A conflict round reuses its round's sandbox names. The envtest spec for it timed out in the full suite: within one second the Task reread the old implement result and merged off the old review | The operator told pods apart by creation time, which counts whole seconds. It now reads only the pod its sandbox controls, as Agent Sandbox sets the owner |
| **With four sandboxes the API stalled for up to 10 s again**, and the scheduler and controller-manager crash-looped (104 restarts), so two sandbox pods sat `Pending` for 5 minutes | The Factory view: "The API server did not answer for 11 s". CPU stayed under 4.2 of 12 cores and memory under 6.7 GiB, but etcd logged 83 disk syncs over 1 s in 25 minutes, up to 7 s; they lost their 15 s leases | `hack/kind.yaml`: etcd runs with `--unsafe-no-fsync`, and both hold 60 s leases with a 40 s renew deadline; applied to the running cluster by editing its static pod manifests. Since then: no slow syncs, requests under 2 s with Tasks only, and no restarts. A test cluster can lose its newest writes in a hard crash; Gitea's repositories are on their own volume |
| The Story view's "Working now" said 0 while three Crew Tasks were at work | The tile beside the panel that listed them ([evidence](screenshots/v4-parallel-story.png), after the fix) | It counts active Sessions and the Tasks the project's Crews are on |
| The agent never saw why Go CI failed: the CI log excerpt in its feedback knew only Flutter's output | cel-language-server #8's round 1 failed on a timing test, but the feedback held only `make: *** [Makefile:41: test] Error 1`; the implementer guessed and fixed a different test | The excerpt also picks Go test failures with their log lines, panics, data races, compiler, vet and linter findings, and Vitest, tsc, ESLint and pytest failures. On the real log it gives the test, its message, the package and the make line |
| An acceptance criterion that needs a protected file fails the Task: the implementer stops, as its guide says, but "no changes" in the last round is a failure | cel-language-server #24 asked for `make bench`; the Standard owns the Makefile. Rounds 1 and 2 said so, the reviewer held the criterion, round 3 made no changes and the Task failed | The criterion was corrected and the issue re-readied; the new Task merged in one round. It happened again with vscode-extension #4, whose criterion was right: `make dist` must build the platform packages. A person's Makefile hook (PR #30) was never merged: an implementer that needs a protected file now goes `Blocked`, and the operator carries the change as a Standard change (fifth part below), so `vscode-extension-4-2` merged with no person acting |
| Gitea dropped out of its Service mid-upload, failing the server's first release job | Run of the `v0.1.0` tag: the upload of 48 MB binaries broke off; the readiness probe's 1 s default timed out under load | Readiness probe 5 s timeout, 6 failures (`1c5e8c4`); the release was deleted, tagged again, and run 36 succeeded |
| Reviewers' follow-ups piled up beside the release's issues (37 open), so the backlog looked endless | The user: "I fear that we will not ever complete the app" | Each repository has a `v0.1.0` milestone with a fixed scope and a `Later` milestone outside any release. The operator files follow-ups in `Later`, creating it if needed (`61c8cb2`) |
| A merged issue stayed open, so the Crew asked a person to close it | vscode-extension #19: PR #35 merged, follow-up #41 filed, but no "Merged in" comment and no close; the Crew labelled it `status/needs-human` | One 30 s context covered the merge and every issue call; Gitea under load answered #41's creation late and the comment and close failed on the spent context. Each issue call has its own time now (`c86c77d`); #19 was closed by hand |
| A Crew mixed releases: it picked the lowest-numbered ready issue whatever its milestone | The server's v0.1.1 (#58 #59 #60) and v0.2.0 (#44 #45 #49 #50) were both ready; `cel-language-server-44` (v0.2.0) started first, so v0.1.1's tag would hold a v0.2.0 change | Releases go one at a time: a milestone with a due date is a release, and later releases wait while an earlier one has a ready issue or a Task at work. Standard changes and issues the current release depends on are never held (`docs/plans/release-order.md`) |

## The CEL Lens run

Two Crews, one per Container: Claude Code with Sonnet implements, with Opus
reviews.

**First part, one Task per Crew at a time.** The Crews merged issues #1 to #4 of
cel-language-server and #1, #2, #3 and #5 of vscode-extension before they were
paused for the parallel change. The server's issues took one round each, except
#1, which took two. The extension's first three took three rounds each, and #5
(user docs) took one. Each Task filed two or three follow-ups as `status/proposed`.

The reviewer held the bar where it matters:

- **A padded benchmark.** Issue #1 asks for a benchmark on a 1,000-line file
  with 50 expressions, under 5 ms. The round-1 benchmark had 954 lines, about 900
  of them `# padding` comments, and measured 0.35 ms per run. On a realistic
  1,011-line CRD the reviewer measured 1.4 ms per run, 4 times more, and marked
  the criterion not met. Round 2 benchmarks real files: 1.44 to 1.53 ms.
- **A race in the extension.** In vscode-extension #1 the reviewer reproduced
  two overlapping starts creating two language clients. Round 2 shares one start
  promise between them.

Where vscode-extension stands: #4 (platform packages) waits as `status/proposed`
until the server has releases, and the rest of its backlog is reviewer
follow-ups, also proposed. Promoting them is a person's decision.

**Second part, two Tasks per Crew.** With four agent slots and `parallel: 2`, the
Crews merged six issues between 12:40 and 13:46: the server's #5, #6 and #7, and
the extension's #7, #8 and #15. To give the extension Crew work, a person
promoted #7, #8 and #15 from the reviewers' follow-ups, with acceptance criteria
that split #8 and #15 by test so they would not do the same work. All but the
server's #7 took one round. The extension's Tasks took 8 to 11 minutes each; the
server's took 24 to 65, longer because Go builds, and #5 lost 5 minutes to the
stall below. Each time a Task merged, the Crew started the next ready issue at
once (#6 when #5 merged, #8 when #7 merged).

A conflict, and how it resolved ([evidence](screenshots/v4-parallel-conflict.txt)):
the server's #6 (a `check` command) merged at 13:33 while #7 (`serve --stdio`)
was in its second review. The reviewer approved, but the pull request now
conflicted with `main`, so the Task stayed in round 2 with reason
`ResolvingConflicts`. The implementer merged `main`, resolved `main.go` and
`main_test.go` by keeping both commands, and named the conflicts in its account.
Checks passed in 15 s, the reviewer approved again, and it merged 10 minutes
after the conflict, without spending a round. The extension's #8 and #15 touch
the same test file but did not conflict: #15 started after #8 had merged.

The reviewer caught another real defect: in #7's first round, one malformed
header line put the JSON-RPC reader out of step for good, so every later
message failed. It reproduced this with a crafted stream and named the test to
add; round 2 skips the body after a bad header.

Four sandboxes fit. In the samples the UI kept (12:40 to 12:59 and 13:40 to
13:51; it loses its history when it restarts), the node used at most 4.1 of 12
cores and 6.7 GiB. With four implementers running the host still had 9.9 GB
available, and a watch for less than 1.5 GB never fired. The disk was the limit,
not CPU or memory: see the stalls in the findings above. The Story view showed the work as
it happened ([evidence](screenshots/v4-parallel-story.png),
[Factory view](screenshots/v4-parallel-factory.png)).

**Third part, M2: a misleading CI failure.** Issue #8 (completion) took three
rounds, 13:46 to 14:40. Round 1 failed CI on `TestLargeFileLatency`, a test from
#7 that times one change against 500 ms: 520 ms on CI, passing locally. The
implementer's feedback held only the `make` error line (see the finding above),
so it guessed. It made a different timing test take the fastest of five, and
said so plainly: "I'm assuming the cause is a timing test, which I haven't
confirmed." This time CI passed by luck. The reviewer did not let it through:
it named the test and its 520 ms line, measured it at 256 to 358 ms under
`-race` on 2 CPUs depending on what ran alongside, and asked for a fix. Round 3
took 5 minutes and merged.

The root cause is the backlog, not the agents: #7 and #8 ask for wall-clock
bounds "on CI", on a runner that shares the node with four sandboxes. A person
promoted the reviewer's follow-up #24 and widened it to all three single-sample
timing tests, blocked by #8. The Crew started it next to #9 (hover) as soon as
#8 merged. Until then, three of the four slots had been idle: the server's M2
issues form a chain, and the extension Crew had nothing ready.

Then #24 failed. Its criterion asked for a `make bench` target, but the Go
Standard protects the Makefile. The implementer said so in rounds 1 and 2, the
reviewer rightly would not pass a README that documented a missing target, and
round 3 made no changes, which fails a Task in its last round. The criterion was
the issue author's mistake. Corrected and readied again, #24 merged in one
round from `main`; the work of the failed Task was redone.

To make the end visible, both repositories now have a `v0.1.0` milestone with a
fixed scope (the server's #11 and #24; the extension's #4) and a `Later`
milestone for everything else. M2 closed when #10 merged at 15:35 in one round, and the
server's `v0.1.0` milestone closed at 16:20, when #11 (user docs, two rounds)
and #24 had merged. What is left of the first release: a person tags the server
`v0.1.0`, the extension's #4 bundles it, and a person tags the extension.

**Fourth part, the release.** A person tagged the server `v0.1.0` at `694085f`.
The first release job failed halfway through its upload: Gitea's readiness probe
(1 s timeout) took Gitea out of its Service during the 48 MB uploads (see the
finding above). After the probe fix the release and tag were deleted, the same
commit tagged again, and run 36 attached six binaries and `SHA256SUMS`.

The extension's #4 (one `.vsix` per platform, server bundled) then failed the
way #24 did. Its goal says `make dist` builds the packages, since the release
workflow runs `make dist`, but the Standard owns the Makefile. The implementer
wrote `scripts/dist.mjs` behind `npm run dist`, fixed every other review point
in round 2, and said it was blocked on the Makefile. The reviewer, rightly,
still required `make dist` to build the packages, and round 3 made no changes.
This time the criterion was right; what was missing was a person's edit. A
person added a hook to the Makefile, so `make dist` also runs the project's
`npm run dist` when there is one (vscode-extension PR #30), and #4 is to be
readied again on top of it.

**Fifth part, a Standard change.** The hook in PR #30 was never merged. The
factory now carries a protected-file change as a Standard change, so PR #30 was
closed unmerged and #4 was labelled `status/ready` again, the last thing a person
did. At 00:06 `vscode-extension-4-2` started. It wrote `scripts/dist.mjs`, saw it
needed a change to the `Makefile`, and went `Blocked` at 00:11 with
`status.standardChange` naming the `Makefile`. The operator filed issue #46 and
`vscode-extension-46` ran with `allowedProtectedPaths: [Makefile]`. It merged at
00:17 (PR #47), and 4-2 resumed round 1 the same minute, reached `Merged` at 00:30
(PR #48), 24 minutes after it started, with no person acting. Both Tasks took two
rounds. #46's first round replaced the recipe with a bare `npm run dist
--if-present`; the reviewer found that on `main`, which has no `dist` script, `make
dist` then does nothing and exits 0, so the release workflow would fail. Round 2
added a fallback to the old single `.vsix` when `package.json` has no `dist`
script. #4's round 1 was rejected for a stale README: it still told users to
download `vscode-extension-<version>.vsix` and the server separately. Round 2
rewrote the Install section (and added a test for it), moved the cache decision
into `verifiedAsset` with injected I/O, and wrote the CHANGELOG for users. The
hook in the end is a small change in the Makefile (PR #47: six lines added, two
removed): `make dist` runs `npm run dist` if the script exists, else the old
packaging. The extension's `dist` script uses it, and a throwaway Job running `make
dist` on `main` afterwards built five `.vsix` files, one per platform, each with
the server inside (`docs/screenshots/v4-standard-change.txt`). Two things were not
clean. The reviewer's request in #46 to correct the proposal branch
`standard/vscode-extension-4-2` as well was not met: the implementer was told to
stay on its own branch, so the proposal branch kept the bare `--if-present` form
(the merged `Makefile` is right). And nobody has tried a `.vsix` in a real VS Code.
The reviewer filed two follow-ups as `status/proposed` (#49, publishing the
packages from a release workflow; #50, a package for win32-arm64).

**Sixth part, ready to tag.** On 3 October the fix wave was deployed (Crews
paused for 9 minutes while `cel-language-server-37` finished). The extension's
`v0.1.0` milestone still held #19, whose PR had merged but whose issue never
closed (the finding above), and the superseded PR #29; both were closed by hand.
`make release-version` refuses a tag that differs from `package.json`, which
still said `0.0.1`, so #51 (version `0.1.0` and a dated changelog) was filed and
readied; `vscode-extension-51` merged it in one round, the reviewer confirming
`make dist VERSION=v0.1.0` built five `.vsix` named with `0.1.0`. The tag itself
was refused to the session by auto mode, so `v0.1.0` at `4540ec5` waits for a
person. The release job runs on the `go` CI runner; the optional Release runner is
for Android only.

**The packages, tried without a GUI.** "Nobody has tried a `.vsix`" was half
closed the same night. `make dist VERSION=v0.1.0` at `4540ec5`, outside the
cluster with `server.json` pointed at a port-forward, built the five `.vsix`, each
marked with its `TargetPlatform` and holding its own server binary at the path
`src/server_path.ts` looks up. The server unzipped from the linux-x64 package, still
executable, reported `v0.1.0`; a small LSP client over stdio opened a
ValidatingAdmissionPolicy and got diagnostics on exactly the two broken
expressions, schema-aware hover (`replicas: int` with the Deployment field's
description) and completion of `object.` (`apiVersion, kind, metadata, spec,
status`); `check` reported the same two errors and exited 1
(`docs/screenshots/v4-extension-smoke.txt`). The extension inside a real VS Code
followed later that night: VS Code 1.140.0 desktop under Xvfb, in a throwaway
container, installed the v0.2.0 linux-x64 `.vsix` with its own CLI, and a test in
the extension host opened the same policy. The extension activated on YAML, started
its bundled server, and VS Code showed squiggles and Problems entries on exactly the
two broken expressions, the schema hover on `replicas` and the five `object.`
completions; "CEL Lens: Restart Server" brought the diagnostics back
(`docs/screenshots/v4-extension-vscode.png` and `.txt`). That package bundles
server v0.1.0, the only server tag: a later server release reaches users through
an extension release that bumps `server.json`.

**Seventh part, releases in order.** With two server releases ready, the Crew
started `cel-language-server-44` (v0.2.0) ahead of v0.1.1's three fixes: it picked
by issue number alone (the finding above). The operator now works releases one at
a time, ordered by the milestones' due dates, which were set for the purpose
(v0.1.1 4 October, v0.2.0 6 October, v0.3.0 8 October; dates order the work,
they promise nothing). #44 was already in its second round and merges before
v0.1.1's fixes, so it moved to v0.1.1, whose tag will hold it. After the deploy
the Crew listed v0.1.1's #58 #59 #60 as ready and v0.2.0's as waiting "for release
v0.1.1", and v0.3.0 (#46 #53 #57, #46 blocking #57) could be readied at once
instead of waiting for a person at each release boundary
(`docs/screenshots/v4-release-order.txt`). #44 merged at 02:46 in its second
round (PR #86, `5e72839`); the reviewer had sent round 1 back because hover
renamed library types such as `kubernetes.authorization.Authorizer` to `object`.
Within a minute the Crew started `cel-language-server-58`, the first of v0.1.1's
fixes, with #59 and #60 ready behind it and v0.2.0 still waiting. The extension's v0.2.0 milestone closed
when `vscode-extension-62` (version 0.2.0, dated changelog, one `### Added` under
0.1.0) merged in one round; its tag waits for a person, like v0.1.0's.
