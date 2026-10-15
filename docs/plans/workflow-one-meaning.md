# Workflow: one meaning per label, one home per rule

Parent issue: #1120. Written 2026-10-15, the day the owner asked for the
agent workflow to be looked over before the relay workers start again:
keep what works, change what doesn't.

## What was found

Three audits were made on 2026-10-15: of the scripts (`hack/lib/github.sh`,
`hack/relay.sh`, `hack/relay-watch.sh`, the hooks), of the process docs
(AGENTS.md, GOVERNANCE.md, ROADMAP.md, the `handoff` and `maintainer`
skills, the retrospective plan `before-clients-return.md`), and of the live
tracker (51 open issues, 516 closed since 2026-10-01, no open pull request).

The mechanics are sound and stay: the issue per piece of work, `pick` and
`take` with the claim heartbeat, the handoff comment, the plan parent that
closes itself, `land` with the device-wide merge turn, `close-guard`, the
watch with its triage and maintainer sessions, three strikes, the pause on a
usage limit. Nothing below changes how any of them works.

What is wrong is the docs disagreeing with each other, and nine defects in
the scripts:

- `status/proposed` had four meanings: "waits for the user" (handoff skill),
  "the relay's triage readies it" (AGENTS.md), "the maintainer decides it
  after 14 days" (maintainer skill), "what relay sessions file" (relay.sh).
  New issues were `ready` in one doc and `proposed` in another.
- The handoff skill named a tag as something only the user can give;
  GOVERNANCE.md and ROADMAP.md give tagging to the maintainer.
- AGENTS.md said hand off "when work stops mid-way" and "never inside a
  task" six lines apart.
- The maintainer skill said "you commit nothing" and "the one commit you
  make" in the same file.
- The handoff skill restated AGENTS.md's land and plan rules, and carried a
  human-only cluster section that docs/dev/testenv.md already has.
- AGENTS.md, loaded into every session, carried relay internals
  (`WATCH_MAINTAIN`, `RELAY_IDLE`, `paused-until`).
- No doc said what to do when auto mode refuses a command. That stalled six
  landings and every release until the owner added the allow rules.
- Triage had no skill and no role in GOVERNANCE.md; its rules lived in a
  heredoc in `relay.sh`, which also told it to send issues "to the user".
- `needs/cluster` was a label that gated nothing, explained away in two docs.
- Ten plans whose parents closed were still in `docs/plans/` (#1073 moves
  them).
- The scripts: a plan parent whose plan is only a design change never closes
  itself; a session ending in `waiting` counts as a failure; the status swap
  isn't atomic (the maintainer corrected labels in 14 of 21 sweeps); `pick`
  puts an unclaimed in-progress issue ahead of an urgent one; the relay runs
  whatever branch the main checkout has; `gh issue close` isn't guarded; dead
  code around claims and cluster leases; no test for `take`'s race or
  `beat`'s takeover.

## Decisions

1. **One meaning per status, stated once, in AGENTS.md.** `proposed` is
   filed and not yet decided; whoever may decide readies it: the owner (a
   session the user runs files what the user decided as `ready`), the
   maintainer, or the relay's triage. An agent's follow-up is `proposed`.
   `ready` is decided and unblocked. `in-progress` is claimed. `waiting` is a
   plan's parent. `needs-maintainer` is escalated by an agent; `needs-human`
   is sent to the owner by the maintainer, who alone sets it. The label
   descriptions on GitHub say the same (`hack/lib/github.sh labels`).
2. **One home per rule.** AGENTS.md holds the rules every session needs and
   gets shorter; the `handoff` skill holds the procedure (take, land, the
   plan parent, the refusals); docs/dev/testenv.md holds the relay's
   internals; GOVERNANCE.md holds who decides. Each points to the others
   instead of repeating them.
3. **Triage is the maintainer's, as a skill.** `.claude/skills/triage/` holds
   the rules; `hack/relay.sh --triage` loads it, as `--maintain` loads the
   maintainer skill. GOVERNANCE.md's roles table says so. A user session can
   run it too.
4. **Merge and release authority is the relay's, by two allow rules the
   owner commits.** The rules for `hack/lib/github.sh land` and
   `hack/release.sh` are in the owner's working copy; a session may not add
   them (auto mode refuses it as self-modification, rightly), so the owner
   commits them. GOVERNANCE.md lists the rules among what is reserved to the
   owner. A ruleset protecting `main` from deletion and force-push is the
   owner's too; a session's attempt was refused.
5. **A refused command is not a failed one.** AGENTS.md says it: name the
   command once, go on with what you can, escalate if it blocks; never edit
   the allow rules, never do the refused thing another way. The maintainer
   lists a refusal under *Needs you* once and doesn't retry every sweep.
6. **The device's failures are not bugs.** A run that died of no disk, no
   memory or a runner going offline is reported under Health with the
   device's name, not filed as a bug (the maintainer skill).
7. **`needs/cluster` goes.** Deleted from GitHub by the owner (a session's
   `gh label delete` is refused), and from every doc and test that explained
   it away.
8. **The script defects are steps,** one issue each, with the test that
   proves each (`make test-hack`).

## Steps

| Step | Issue | How we know |
| --- | --- | --- |
| This plan, the docs, the skills, the label descriptions | #1120 (`Refs`) | AGENTS.md shorter than 224 lines; `.claude/skills/triage/` exists; `make test-hack` |
| Plan parents with a design-only plan, or a step closed as not planned, close themselves | #1121 | `make test-hack` |
| A session that ends in `waiting`, or commits on a renamed branch, is not a failure | #1122 | `make test-hack` |
| Strikes expire and add up across devices | #1123 | `make test-hack` |
| The status swap is one call | #1124 | `make test-hack` |
| `pick`: urgent first | #1125 | `make test-hack` |
| The relay runs origin/main's scripts | #1126 | `make test-hack` |
| `close-guard` denies `gh issue close` on a parent | #1127 | `make test-hack` |
| Dead code and lease code on agent paths removed | #1128 | `make test-hack` |
| Tests for `take`, `beat`, `claims`, `show`, `labels`, the branch switch | #1129 | `make test-hack` |
| Allow rules committed, `needs/cluster` deleted, `main` ruleset | the owner, by hand | `git log -- .claude/settings.json`; `gh label list`; `gh api repos/{owner}/{repo}/rulesets` |

## Done when

Every status label's meaning is stated once and the label descriptions on
GitHub match; AGENTS.md is shorter than before; `relay.sh --triage` loads the
`triage` skill; the nine steps are closed; the owner's three acts are done.
