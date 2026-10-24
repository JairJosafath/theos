# Plan: before clients return

Issue #1096. On 2026-10-15 the owner paused issue work to look back at three
weeks of the relay before agents resume and before clients install the release
again. This plan records what the look back found, settles the four decisions
it raised, and names the steps. The owner steers here: a comment on #1096
overrides any choice below.

## Goal

A client who installs the release knows what their objects can expect across
upgrades and what the factory trusts on their device. The relay lands and
releases on its own again, and its gates catch what cost the most time:
rework from untested merges, flaky envtest specs, outages nobody saw, and
user bugs that came back.

Done when every step below has landed, #1079 and #1081 are closed, v0.24.0 has
shipped, and `main` has a ruleset that blocks force-pushes and deletion.

## What happened

Between 2026-09-30 and 2026-10-13 (1,789 commits):

| Measure | Value |
| --- | --- |
| Issues closed | 520, of which 210 bugs (40%) |
| Pull requests merged | 542, median 161 lines changed, top quarter over 392 |
| Tags cut | 33, v0.1.0 to v0.23.0 |
| Median time from filing an issue to closing it | 1.2 h |
| Flaky or timeout issues | about 24, open about 150 to 190 h in total |
| Tests runs on main | 337: 248 green, 47 red, 42 cancelled before they ran |
| Relay sessions that ended within a minute | 47, on 17 issues, one afternoon |
| `source/user` issues | 92, all closed |

Since 2026-10-13 nothing has merged, the relay watch is stopped, and the three
self-hosted runners are offline because Docker is down on the device, so 35
Issue intake runs sit queued. v0.24.0 is due and held by a refused
`hack/release.sh` (#261).

What the three weeks cost, most first:

1. **Rework.** Relay agents filed 77 bugs while landing other work, and at
   least 29 bug bodies name an earlier pull request as the cause. CI runs on
   pushes to main only, never on a pull request, and `test.yml` keeps one
   pending run per ref, so GitHub dropped 42 runs: those merge commits never
   got a CI verdict. On 2026-10-12 three pull requests merged within twelve
   minutes, each green on its branch, and broke vet on main for an hour
   (#946). `land` (#1038) now merges main and reruns `make check` first.
2. **Flaky envtest specs under load.** `suite_test.go:95` gives 756
   `Eventually` and `Consistently` calls a 10 s default; 363 serial specs
   take 737 s. Eight of the twelve most recent red Tests runs on main were a
   spec timing out at 10 s, a different spec nearly each time. Four of the
   eight got no issue: the watch files only when a workflow's two newest
   runs are both red. The suite grew from 279 specs on 2026-10-09 to 363 on
   2026-10-13; at that rate it reaches its 20 minute budget at about 550.
3. **Outages nobody saw.** On 2026-10-08 an expired login ended 47 sessions
   within a minute each, on 17 issues, retrying #615 fourteen times before
   the relay paused (#615 fixed that pause). Today's runner outage is
   invisible to the watch because queued runs never fail.
4. **Refused merges and releases.** Auto mode refused `github.sh merge` in
   #639, #656, #659, #739, #770 and #977, and `hack/release.sh` for the
   maintainer, so v0.24.0 never shipped. Twelve of 21 maintainer reports
   repeat the refusal, and three *Needs you* items waited on the owner for
   days.
5. **User bugs that came back.** #606 returned as #864, #835 as #1019, and
   #814, #870, #877, #957 and #959 were the host's memory and disk reported
   as CI failures. The maintainer corrected labels or states in 14 of 21
   sweeps.

What worked: the issue per piece of work, `take` and the handoff comments,
the plan parent that closes itself, `land` with the merge turn, and the
maintainer's sweeps. None of them changes here.

## Decisions

### 1. The API promise: additive within `v1alpha1`, `v1beta1` after the shapes settle

The 18 kinds serve only `v1alpha1`, with no storage version and no conversion.
Cutting `v1beta1` now would freeze `GitProvider.spec.provider` as a Gitea-only
enum and `Crew.spec.publishReleases.github`, the two shapes #1093 (ports) and
#1068 (the deploy model) are about to change. So:

- Within `v1alpha1`, a release never removes, renames or retypes a field,
  never adds a required field, never tightens a CEL rule so an object that
  validated before fails after, and never changes what a default means.
  Adding optional fields, enum values and conditions is fine.
- A test guards that under `make check`, against a baseline of the CRD
  schemas as of the last release (#1097).
- `v1beta1` (#1082) comes after #1093 and #1068, once, with conversion from
  `v1alpha1` for every kind. #1082 depends on both.

Why not a `v1beta1` now: two conversions instead of one, and the first would
promise shapes we know are wrong. Why not stay silent: a client's stored
object stopping to validate on upgrade is the one thing that makes them
leave.

### 2. The trust boundary: one trust domain per device, written where a client installs

The Claude token is readable inside a sandbox ([docs/design.md](../../design.md#the-fence),
closed by #1075) and the CI runners run the agents' code on the cluster
network (#1076). Both are planned, not done, and nothing a client reads says
so. The factory may be installed before they close, on these terms, written
in `docs/getting-started.md` and the chart's README (#1098): anyone who can
create a Project or a Brief in the cluster can run code that holds the Claude
token and reaches the cluster network, so run the factory on a device you
own, for people you would hand the token to, and never on a shared or
production cluster.

Why not wait for #1075 and #1076: today's clients are the owner's own
devices, where that boundary holds, and both fixes are large. Why write it
now: an unstated boundary is a promise nobody made.

### 3. Merge and release authority: the relay's, by the two commands it already has

AGENTS.md has agents merge with `hack/lib/github.sh land`, and ROADMAP.md has
the maintainer tag releases with `hack/release.sh`. Both are the owner's
settled decisions; auto mode's classifier doesn't know them, and refuses one
in six landings and every release. The fix is two allow rules in the
project's Claude settings, `.claude/settings.json`:

```json
"permissions": {
  "allow": [
    "Bash(hack/lib/github.sh land:*)",
    "Bash(hack/release.sh:*)"
  ]
}
```

**The owner adds them, by hand.** A session's attempt to add them is refused
as self-modification (this plan's session tried on 2026-10-15), and that is
right: no agent widens what agents may do. Protecting `main` is the owner's
too: a GitHub ruleset that blocks force-pushes and deleting the branch,
without requiring a pull request, because the relay merges its own. Until
the rules are in, a human runs `hack/release.sh minor` on green main; the
first one ships v0.24.0.

Why not a human merge for every pull request: 60 merges a day was the
relay's pace, and `land` already reruns `make check` on the merged tree.

### 4. The relay's gates

Each gate is one step, and each is tested without a cluster:

- Lint, vet and the UI check run on pull requests; `test-hack` runs in CI;
  main tests every merge commit; Tests stay off pull requests, because
  `land` runs `make check` and twelve minutes a run would saturate the
  runners (#1101).
- The default `Eventually` timeout becomes 30 s. A passing wait returns
  early, so the suite's time barely moves; a flake under load stops being
  an issue a day (#1099).
- The watch records every failed spec on main and files one bug when a spec
  fails twice in seven days, instead of nothing for a single flake (#1100).
- The watch notices runs queued for 30 minutes and says the runners are
  offline in the Maintainer report; `make gh-runner-status` says why (#1102).
- `land` refuses a bug fix that touches no test, unless the body says why,
  and the test names its issue (#1103).
- `land` refuses a pull request over 400 hand-written lines without a
  `Review:` comment from a `/code-review` subagent (#1104).
- Release dispatches Verify Release for every minor; 24 releases shipped
  unverified (#1105).

Left out, on purpose: running the envtest suite in parallel. It is the one
change that keeps the suite under its budget past about 550 specs, but it
costs memory per process on a device that already queues heavy jobs, and the
30 s default removes most of the pain first. It gets its own issue when the
suite passes 450 specs.

## Steps

| Step | Issue | How we know |
| --- | --- | --- |
| This plan | #1096 (`Refs`) | landed in `docs/plans/` |
| The `v1alpha1` promise and its guard | #1097 | design section; table tests refuse a removed, retyped or newly required field |
| The trust boundary in the user docs | #1098 (after #1081) | the sections exist; `make check` |
| 30 s `Eventually` default | #1099 | `make check`; the suite's time in the PR |
| Flake accounting in the watch | #1100 | `make test-hack` on fixture logs |
| Lint on PRs, `test-hack` in CI, every merge tested | #1101 | `hack/check-workflows.sh`; a PR shows Lint |
| Offline runners noticed | #1102 | `make test-hack`; DEVELOPMENT.md |
| Bug fixes carry a test | #1103 | `make test-hack`; AGENTS.md |
| Review pass over 400 lines | #1104 | `make test-hack`; the handoff skill |
| Verify Release per minor | #1105 | `hack/check-workflows.sh`; the next minor's run |
| Allow rules and the `main` ruleset | the owner, by hand | the maintainer's next sweep releases v0.24.0 |

Already filed and part of the outcome: #1079 (release installs never register
Sessions), #1081 (stale install docs), #1093 and #1068 (the shapes `v1beta1`
waits for), #1082 (`v1beta1`), #1075 and #1076 (the two trust gaps).

## Design

- **The compatibility guard** compares `config/crd/bases/` to a baseline kept
  in the tree, refreshed by `hack/release.sh` when it tags. A baseline works
  in CI's shallow clone, where the last tag isn't fetched. The step decides
  where the baseline and the test live; `internal/model` stays free of it.
- **The trust section is one paragraph,** not a threat model. It names what
  a Brief's author can reach and the two issues that narrow it.
- **The gates live in `hack/lib/github.sh land`** because every change passes
  it, on every device. Each refusal names its rule and the line to add.
- **The watch's new facts** (flakes, queued runs) go into the Maintainer
  report's Health, the one place the owner already reads.
