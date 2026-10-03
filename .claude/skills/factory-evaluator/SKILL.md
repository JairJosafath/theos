---
name: factory-evaluator
description: Evaluate how the C4 Factory has run since the last evaluation (Tasks' rounds and waits, Crew stalls, operator errors, resource use, CI and release times, relay cost), report it as one comment on the pinned "Factory evaluations" GitHub issue, and file up to five concrete improvement issues. Read-only towards the factory. Use when asked to evaluate or audit how the factory runs, or when hack/evaluate.sh starts a headless evaluation.
---

# Evaluating the factory

The factory gets better only if someone looks at how it ran and turns what went
wrong into work. This is that look: gather the facts, compare them with the last
evaluation, report, and file the few issues worth fixing.

## Rules

- **Read-only towards the factory.** Only `kubectl get/describe/logs/top` and
  Gitea GETs. Never patch, pause, deploy, label or comment in Gitea, and never
  change the repository (no edits, no commits). The only writes are on GitHub:
  labels, issues and comments, as below.
- **Never print a secret.** `GH_TOKEN` and the Gitea password stay in variables.
- **Scope: the factory, not the apps.** The operator, how Crews and Tasks behave,
  the agents' guides and Standards, the templates, the UI, the relay, resource
  use. Not CEL Lens's features. Look at the `cel-lens` project only; ignore
  toolbox, webshop and `loop-check`.
- **Cheap and short.** Never the Fable model; start no subagents. Read
  `summary.md` once and dig only where a finding needs evidence. Keep outputs
  small (`jq`, `--tail`, `cut -c1-300`). Aim for about 40 turns.
- **Headless:** nobody answers questions. If an action is refused, don't retry
  it; say so under "Not checked" in the report.

GitHub: the repository is `JairJosafath/c4factory` (private). `gh` needs
`GH_TOKEN`; `hack/evaluate.sh` exports it. When it isn't set (a person invoked
the skill), run `.claude/skills/factory-evaluator/gh.sh` in place of `gh`: it
reads the token from the git credential helper.

## 1. Labels and the evaluations issue

Make sure the labels exist; another agent creates the same set, so an existing
label is fine:

```bash
have=$(gh label list -R JairJosafath/c4factory --limit 200 --json name --jq '.[].name')
while read -r name color desc; do
  grep -qx "$name" <<<"$have" || gh label create "$name" -R JairJosafath/c4factory --color "$color" --description "$desc" || true
done <<'EOF'
status/proposed c5def5 Changes the design; a person decides
status/ready 0e8a16 Clear and within the design; ready to work on
status/in-progress fbca04 Someone is working on it
status/needs-human d93f0b Needs a person's decision or action
priority/urgent b60205 Do this first
kind/bug d73a4a Something does not work as designed
kind/feature a2eeef Something new
kind/chore cfd3d7 Upkeep with no behaviour change
kind/docs 0075ca Documentation
kind/improvement 1d76db Works, but could work better
source/evaluator 5319e7 Filed by the factory evaluator
EOF
```

Find the open issue titled exactly "Factory evaluations":

```bash
gh issue list -R JairJosafath/c4factory --state open --search '"Factory evaluations" in:title' \
  --json number,title --jq '.[] | select(.title == "Factory evaluations") | .number'
```

If there is none, create it (label `source/evaluator`; body: what this issue is
for, one comment per evaluation, and that `hack/evaluate.sh` or `make evaluate`
runs one) and pin it with `gh issue pin <n> -R JairJosafath/c4factory`.

The previous evaluation is its latest comment holding the marker
`<!-- factory-evaluator`; the marker carries the previous metrics as JSON:

```bash
gh api "repos/JairJosafath/c4factory/issues/<n>/comments?per_page=100" \
  --jq '[.[] | select(.body | contains("<!-- factory-evaluator"))] | last | {html_url, created_at, body}' | tail -c 3000
```

The window starts (`SINCE`) at the previous metrics' `window.until`; with no
previous evaluation, 24 hours ago.

## 2. Gather

```bash
OUT=${EVAL_DATA:-.relay/evaluate-$(date -u +%Y%m%d-%H%M%S)}   # EVAL_DATA comes from hack/evaluate.sh
bash .claude/skills/factory-evaluator/gather.sh "$OUT" "$SINCE"
```

It reads everything read-only (its own Gitea port-forward, killed when done)
and writes `$OUT/summary.md`: gathering failures, `metrics.json`, Crews, Tasks
with their minutes per phase, the reasons rounds were not accepted, grouped
operator errors, resource use and restarts, Gitea Actions and release run times,
open issues by milestone, and the relay's sessions with turns, minutes and cost.
Read `summary.md` with one Read; it is the base of the report.

For evidence, dig into the files it left (don't fetch again what is there):
`$OUT/raw/tasks.json` (whole Task objects), `$OUT/raw/operator.log`,
`$OUT/issues-open.json`, `$OUT/actions.json`, and the relay's `session-*.md`
accounts in `RELAY_DIR` (default `.relay/`). A Task's feedback or review:
`jq -r '.items[] | select(.metadata.name == "T") | .status.review.summary' "$OUT/raw/tasks.json" | cut -c1-1500`.
More from Gitea (a pull request's reviews, an issue's comments): the Gitea recipe
in `.claude/skills/factory-ops/SKILL.md`, with the port-forward killed by its PID
in the same command. To place a fix, read the code or docs it touches:
`internal/agentrunner/prompt.go` (agents' prompts), `templates/` (Standards and
their `docs/agents/*.md` guides), `docs/design.md`, `docs/results-v4.md`.

## 3. Analyse

The metrics (`metrics.json`, over Tasks finished in the window or still active):

| Metric | Meaning |
| --- | --- |
| `tasks.merged`, `tasks.failed` | Tasks merged; Tasks failed, which hands the issue back (`status/needs-human`) |
| `firstRoundApprovalPct` | merged Tasks the reviewer approved in round 1 |
| `roundsPerMerged` | mean rounds of a merged Task |
| `hoursPerMergedMedian` | median hours from a Task's creation to its merge |
| `waitMinutes` | minutes waiting: `unavailable` (usage limit, overload), `slot` (no free sandbox), `blocked` (Standard change), `conflicts` |
| `gitea.laterOpen`, `laterNew` | reviewer follow-ups open in `Later`, and how many came in the window |
| `gitea.needsHumanOpen` | issues handed back to a person |
| `operatorErrorLines`, `crewErrorLines` | operator ERROR lines in the window, all and the Crews' (the log starts at the pod's start) |
| `relay.sessions.usd`, `maintenanceUsdPerMerged` | relay sessions' cost, and that cost per merged issue |
| `agentCost` | always null: the agent runner drops Claude Code's cost; say "not recorded" |

A deploy in the window starts a new operator pod and loses the earlier log, so the
error counts cover only the time since then. Say so, and read the Crews'
conditions and the Tasks' histories for what the log would have shown.

Compare each with the previous evaluation's metrics. Then look for the patterns
below; these are where the useful findings usually are:

- **The same complaint across reviews**: a stale README, missing tests, an
  unchecked criterion. The implementer's guide in the Standard should say it.
- **Rounds lost to what the agent can't control**: timing tests on a shared CI
  runner, CI output missing from the feedback, a protected file, a criterion no
  agent can meet, an infrastructure kill.
- **Time when nothing worked**: long `checking` or `reviewing`, `slot` or
  `unavailable` waits, a Crew `Idle` or `Waiting` while issues were ready, a
  Crew condition `False` (`GitHostUnavailable`).
- **Errors that repeat** in the operator log, restarts marked `IN WINDOW`,
  failing probes, etcd overload warnings, a node near its CPU or memory.
- **Slow or failing CI and release runs.**
- **Follow-ups piling up**: `laterNew` well above `merged` means reviewers file
  faster than the factory can work, and the `Later` backlog stops being read.
- **Relay sessions** that cost much for little, hit their timeout, or ended in
  error.

Before calling something new, check `docs/results-v4.md`'s findings table and the
GitHub issues: a known problem already fixed is not a finding; one that came
back is (a regression). Every finding needs evidence: Task names, numbers,
timestamps, log lines. No evidence, no issue.

## 4. File improvement issues (at most 5)

Pick the findings that cost the most rounds, hours or money, or that will get
worse. **Dedupe first**: list the issues, open and closed in the last 30 days, and
search for each finding's key words:

```bash
gh issue list -R JairJosafath/c4factory --state all --limit 100 --json number,title,state,closedAt \
  --jq '.[] | "\(.number) \(.state) \(.closedAt // "") \(.title)"'
gh issue list -R JairJosafath/c4factory --state all --search '<key words>' --json number,title,state
```

- An open issue covers it: add one comment with the new evidence (date, Task
  names, numbers) instead of a new issue.
- A closed issue covers it and it is back: file a new issue that says "came back
  after #N" with the new evidence.

Each new issue is **one concrete, testable problem** a session can fix, titled
as the problem ("Crew reads fail for minutes while Gitea restarts"), with this
body (write it to a file and pass `--body-file`):

```markdown
## Context
What part of the factory, and how it should behave (cite docs/design.md when it says).

## Evidence
What the evaluation saw: Task names, numbers, timestamps, log lines (short, in code blocks).

## Goal
The behaviour wanted, in one or two sentences.

## Acceptance criteria
- [ ] A check someone can run or observe, e.g. a test that fails today and passes after the fix
- [ ] ...

## Scope
In: the files or parts to change, as far as known. Out: what this issue does not cover.

_Filed by the factory evaluator; see the evaluation of <date> in #<evaluations issue>._
```

Labels: `source/evaluator`, one of `kind/bug`, `kind/improvement`, `kind/chore`,
`kind/docs`, and a status:

- `status/ready` when it fixes a flaw within the current design: a bug, a stall,
  wasted resources, a confusing message.
- `status/proposed` when the fix would change docs/design.md. The design doc is
  the spec, and a person decides those. When unsure, proposed.

```bash
gh issue create -R JairJosafath/c4factory --title '<problem>' --body-file "$OUT/issue-1.md" \
  --label source/evaluator --label kind/bug --label status/ready
```

When the cause is not known yet, say so under Scope, and make the first criterion
the measurement that finds it and records it in the issue. Then the issue is a
problem with a test, not an open-ended "investigate".

Don't file app features, "investigate X" without a concrete problem, or more than
five. Fewer good issues beat five weak ones; filing none is fine.

## 5. Report: one comment on the evaluations issue

Write `$OUT/comment.md` and post it with
`gh issue comment <n> -R JairJosafath/c4factory --body-file "$OUT/comment.md"`
(it prints the comment's URL):

```markdown
## Evaluation <date> (<SINCE> to <until>)

| Metric | Now | Last | Trend |
| --- | --- | --- | --- |
| Merged issues | 15 | 12 | up |
| First-round approval | 60% | 70% | worse |
| Rounds per merged issue | 1.5 | ... | ... |
| Hours per merged issue (median) | 0.23 | ... | ... |
| Hand-backs (failed Tasks / needs-human open) | 0 / 0 | ... | ... |
| Waiting minutes (unavailable / slot / blocked) | 0 / 0 / 6 | ... | ... |
| Operator errors / Crew stalls | 18 / 2 | ... | ... |
| Relay cost (sessions) / per merged issue | $27.67 (5) / $1.84 | ... | ... |
| Agent cost | not recorded | | |
| Follow-ups open in Later (new) | 61 (+20) | ... | ... |

### Findings
1. **<finding>**: <evidence in a sentence or two>. Filed #N / commented on #M / no issue (why).
2. ...

### Not checked
What could not be gathered or was refused, if anything.

<!-- factory-evaluator {"version":1,"metrics":<metrics.json, compact>} -->
```

With no previous evaluation, leave "Last" and "Trend" as "–" and say it is the
baseline. Put the compact `metrics.json` (`jq -c . "$OUT/metrics.json"`) in the
marker; the next evaluation compares with it.

## 6. End

End your turn with a short account, and these three lines last (hack/evaluate.sh
reads them):

```
EVALUATION: <comment URL>
FILED: #<n> #<n> (or none)
COMMENTED: #<n> (or none)
```
