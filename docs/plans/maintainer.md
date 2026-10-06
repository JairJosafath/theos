# Plan: a maintainer for the relay

## Goal

The owner does nothing day to day and steps in only when they choose to. A
headless **maintainer** session, run by the relay, acts as the project's
maintainer on the owner's behalf: it decides what the design leaves open,
corrects what other agents get wrong, answers reporters, keeps the project's
hygiene and releases, and sends the owner only what is reserved to them, each
item with its recommendation, in one pinned report.

What prompted it: triage sent #240 to `status/needs-human` with a list of open
design questions, and the owner had to clean it up by hand.

## Decisions

- **Grounded in CNCF practice.** Roles and decision rules follow the CNCF
  project template ([GOVERNANCE-maintainer.md](https://github.com/cncf/project-template/blob/main/GOVERNANCE-maintainer.md)),
  adapted to one owner as
  [Minimum Viable Governance](https://github.com/github/MVG) does; duties
  follow [Duties of Maintainers](https://contribute.cncf.io/maintainers/lifecycle/duties/)
  and the [technical leadership principles](https://contribute.cncf.io/maintainers/leadership/technical-leadership-principles/);
  triage follows the [Kubernetes triage guide](https://www.kubernetes.dev/docs/guide/issue-triage/);
  reviews the [Kubernetes pull request guide](https://www.kubernetes.dev/docs/guide/pull-requests/);
  conduct the [CNCF Code of Conduct](https://github.com/cncf/foundation/blob/main/code-of-conduct.md);
  security and release hygiene the [OpenSSF Best Practices](https://www.bestpractices.dev/en/criteria/0)
  passing criteria, [SemVer](https://semver.org/) and
  [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); response times are
  measured as [CHAOSS Time to First Response](https://www.chaoss.community/kb/metric-time-to-first-response/).
- **The maintainer decides; it doesn't build.** It triages, decides, reviews,
  communicates and releases. Code and docs change only through issues the
  relay works. It commits nothing.
- **Escalation has two levels.** Agents escalate to the maintainer
  (`status/needs-maintainer`); only the maintainer escalates to the owner
  (`status/needs-human`), and only for what [GOVERNANCE.md](../../GOVERNANCE.md)
  reserves to the owner or a direction it is unsure of, with the choice it
  would make.
- **One report, not a stream of pings.** The owner reads one pinned issue,
  *Maintainer report* (label `maintainer/report`), whose body the maintainer
  rewrites each sweep: *Needs you* first (empty is the goal), then health, then
  what it did since the last sweep.
- **The owner's word wins.** A comment from the owner (no relay marker) on an
  issue is binding; the maintainer follows the owner's newest word and never
  reverses it.
- **It runs on activity, not on a clock alone.** A sweep every 2 hours when
  anything changed (an issue, a pull request or main), and soon after an issue
  enters `status/needs-maintainer`. Quiet nights cost nothing.
- **The license is proprietary, all rights reserved** (the owner's choice).
  The owner holds every copyright, so open-sourcing later (CNCF projects use
  Apache-2.0) stays a relicense the owner can make at any time. Only the owner
  changes the license; the maintainer never proposes it on its own.

## Steps

### 1. The charter (#245; docs only, nothing runs)

GOVERNANCE.md (roles, authority, what is reserved to the owner, decisions,
escalation, changing governance), MAINTAINERS.md, CODE_OF_CONDUCT.md (CNCF
v1.3 by reference), SECURITY.md, CONTRIBUTING.md, and the `maintainer` skill
(`.claude/skills/maintainer/SKILL.md`): the sweep, the standards, the voice,
the report. AGENTS.md names the skill.

Done when: the files are on main and `make check` passes.

### 2. The relay runs the maintainer (#246)

- `hack/relay.sh --maintain`: a session like `--triage`
  (`maintain()`, `maintain_prompt()`), on origin/main for reading only, in its
  own worktree `.claude/worktrees/relay-maintainer` under its own lock
  (`.relay/maintain.lock`), not counted against `RELAY_MAX_WORKERS` (it must
  run while every worker is busy). It goes through `admit` (pause, memory,
  start gap), and goes first: while it waits, no worker takes the start slot
  (`.relay/admit-first`, #262). Model opus. Its prompt: you are the project's maintainer; follow
  GOVERNANCE.md and the maintainer skill; the time of the last sweep; the
  headless rules the triage prompt has; end with a short account. At its end it
  writes `.relay/maintained`: `<epoch> <activity>`.
- `hack/lib/github.sh activity`: one line that changes when anything changes:
  the newest `updated_at` among issues and pull requests (the report issue
  excluded) and origin/main's SHA.
- `hack/lib/github.sh report FILE`: set FILE as the body of the open issue
  labeled `maintainer/report`, creating (and pinning) it the first time.
  `maintainer/report` joins `GH_LABELS`; the report issue carries no status,
  so nothing picks or triages it.
- `hack/relay-watch.sh`: each pass (not only when idle; never while paused or
  stopped), start the maintainer when none is running, at least
  `WATCH_MAINTAIN` (default 7200 s; 0 turns it off) has passed since the last
  sweep, and `activity` differs from the one recorded. A seam `run_maintain`
  like `run_triage`. One log line per start.
- Tests: relay_test.sh (the dry-run prompt names GOVERNANCE.md, the
  maintainer skill and `github.sh report`, and says it commits nothing),
  relay-watch_test.sh (starts when due and changed; not when unchanged, paused,
  off or already running), github_test.sh (`report` creates once, then edits).
- Docs: the maintainer in docs/dev/testenv.md's relay section and in the
  AGENTS.md relay bullet.

Done when: `make check` passes, the tests above pass, and after the watch
restarts on the new code, its log shows a maintainer start and the pinned
report issue exists (its URL in the pull request).

### 3. Agents escalate to the maintainer (#247)

- `status/needs-maintainer` joins the statuses (`GH_LABELS`, `gh_set_status`,
  the usage text); setting it releases the claim and lease, as needs-human
  does. The relay's `outcome` treats it as a finished session.
- Relay sessions, triage, the handoff skill and AGENTS.md escalate to
  `status/needs-maintainer`; three failed sessions in a row and `cycles
  --report` set it instead of needs-human. Only the maintainer sets
  `status/needs-human`.
- The watch starts the maintainer within a pass when an issue is in
  `status/needs-maintainer`, at most once per `WATCH_RETRY`.
- Tests pin each of these; docs/dev/testenv.md and the handoff skill say it.

Done when: `make check` passes, the tests above pass, and an issue set to
`status/needs-maintainer` on the running relay gets a maintainer decision
(its link and the watch log line in the pull request).

## How we'll know it works

A night of the relay ends with nothing in *Needs you* that the maintainer
could have decided, no agent comment left asking the owner how to build
something, and every reporter answered.
