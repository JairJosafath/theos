# Fresh start: the reference app is the definition of done

Parent issue: #1426, pull request #1427. Written 2026-10-19 by a session
the owner ran, and revised the same evening after a first run of its step 0
showed what the process must handle. The owner's words for it: "please
autonomously make decisions and act during the plan, I do not want to be
involved, I just want to check when all is done."

## How this plan runs without the owner

There is no orchestrator agent. The relay and its maintainer are the
orchestrator (GOVERNANCE.md); a plan only has to be written so that they
can run it. The roles:

| Role | Who | Does |
| --- | --- | --- |
| Owner | the person | States the goal. Marks the plan binding once. Keeps Claude Code logged in. Starts the verifier session when a step needs a cluster. Reads the pinned Maintainer report when they choose. |
| Planning session | a session the owner runs | Writes the plan with every decision made. Never executes it. |
| Step-0 session | one headless session, under an hour | Files what the plan says, lands the plan, readies the steps, installs the relay watch, hands off, ends. |
| Relay workers | `hack/relay.sh` | Work the step issues, one per session. |
| Maintainer | `hack/relay.sh --maintain` | Closes what finishes, decides escalations, lists what only the owner can do under *Needs you*, cuts releases. |
| Verifier session | one session the owner starts with `C4_HUMAN_CLUSTER=1` | The one agent that touches a cluster: builds the reference app on Kind, judges it, files the gaps, releases the cluster, ends. |

The rules, each from something the first run hit on 2026-10-19:

1. **The plan decides; nobody asks the owner.** What GOVERNANCE.md reserves
   to the owner is either decided in the plan (priorities, abandoning a
   plan) and covered by the owner's one `binding` comment on the parent
   issue, or left out (deleting a branch, credentials, money).
2. **Sessions start from the project environment, and run project
   commands bare.** The allow rules in `.claude/settings.json` match the
   literal start of a command: `hack/lib/github.sh close 1388 FILE`
   matches, `cd ~/c4factory && ./dev run hack/lib/github.sh close ...`
   does not, and auto mode's classifier then refuses closes, branch
   deletions and more. `jq` and the tools live in `.dev/bin`, so the
   session is started through `./dev run` (below), and inside it every
   project command is written bare, with no `cd ... &&`, `./dev run` or
   `VAR=... ` in front.
3. **The allow rules are committed before any session lands.** `land`
   refuses while a file is uncommitted, and committing
   `.claude/settings.json` is refused to agents (self-modification). That
   commit is the owner's, once.
4. **Issues close through the process, not by hand.** A session closes the
   issue it took with `hack/lib/github.sh close`; a plan's parent closes
   with its last step; the maintainer closes what a plan abandons. A
   session never closes an issue it neither filed nor took.
5. **An agent never speaks as the owner.** A comment that says "Owner's
   note" or binds the maintainer is refused (instruction poisoning) and
   would be wrong anyway. The owner's `binding` comment is the owner's
   word; agents cite it.
6. **A cluster is touched by the verifier session only,** which the owner
   starts. AGENTS.md gets that exception (step 0 adds it to this pull
   request before landing).

What was already done on 2026-10-19 by the stopped first run, and stays:
the milestone *Reference app: the webshop ships* (number 12); the step
issues #1428 to #1433, now `status/proposed`; the priority moves of step 2;
the comments of steps 1, 2, 9 and 10 on #1112, #1116, #1424, #1425, #1379
and #261; the git identity in this checkout's repository config on 3st8JJ.
Not done: landing this plan and #1412, the AGENTS.md exception, readying
the steps, installing the watch, closing #1388 and #1389, removing `dev`.

## Goal

The factory's work is judged by one application it builds, the webshop, and
by nothing else: every open issue either closes a gap between what the
factory builds from the webshop's Brief and what a senior engineer would
ship, or waits. The spec agents read describes the factory as it is. The
relay runs again, small, on a logged-in device, and grows only while the
webshop's gap list shrinks.

## What was found (2026-10-19)

| Measure | Value |
| --- | --- |
| Commits, 2026-09-30 to 2026-10-17 | 2,255 |
| Relay and landing scripts in `hack/`, without the kubebuilder template | 18,300 lines, 7,700 of them tests |
| `internal/model` and `internal/scaffold`, the business rules | 29,400 lines |
| `docs/design.md` | 1,800 lines, in layers named v2, v3, v4, Briefs, UI, UI actions, ports, deploys |
| `docs/dev/testenv.md` | 1,165 lines, half of them one device's memory, disk and CPU tuning |
| Templates under `templates/` | 29 |
| Full sample applications | 1, `config/samples/webshop`, with one Container |
| Open issues before this plan | 84: 2 at priority/1, 29 at priority/2, 38 at 5, 13 at 8 and 9 |
| Open pull requests | #1412 green; #1423 fails Vulnerabilities; #1411 fails Tests |
| Relay | stopped since 2026-10-17 06:10 on every device; the night of 2026-10-16 Skinny-vivobook ran logged out: about 190 strike comments, 15 false escalations across 19 issues |
| Relay login | the installed watch (`hack/relay-watch.sh --install`) gives its unit PATH but no `CLAUDE_CONFIG_DIR`, so its sessions use `~/.claude`, the same login as VS Code; the project shell's `CLAUDE_CONFIG_DIR=.dev/claude` is a separate, empty login on 3st8JJ |
| Git identity on 3st8JJ | was missing in every shell; set in this checkout's repository config on 2026-10-19, which the relay's worktrees share (#1289) |
| Self-hosted runners | 3st8JJ's three online; Skinny-vivobook's and gray-lenovo's offline |
| `main` | green: test.yml passed on de933e4b; v0.27.0 is the newest tag |
| ci-gate | half-built: #1381 on `dev` only, `dev` 25 commits behind main, no `promote`, `dev` unprotected, default branch still main |

The factory has been building itself: 65 finished plans in 18 days, most of
them about the relay, landing, CI and the Git host. Nothing measures the
quality of what the templates build, and the goal (ROADMAP.md, #1424) is the
quality of the applications.

## Decisions

1. **No new repository, no rewrite.** The operator, the templates and the
   tests are sound. What changes is what "done" means, the spec and how the
   relay runs.
2. **The webshop is the reference application and defines done.** The
   milestone *Reference app: the webshop ships* holds its path at
   `priority/2`, and so does a `kind/bug` whose fix is under `templates/`.
   Every other issue that was at 2 moved to `priority/4`: above the default,
   below the webshop. The owner's sweep of 2026-10-19 put all of them at 2
   for the same goal; this narrows it to one application at a time, because
   a relay of two workers closes a handful of issues a day and the order
   decides what the next release shows.
3. **The webshop deploys to Kubernetes, the Kind cluster it is built on.**
   Not AWS: an AWS account is money and credentials, reserved to the owner,
   and the AWS templates have their own open bugs. #1072 is its deploy plan;
   #1070 and #1071 are at 4.
4. **ci-gate is abandoned.** Its goal was for merging to wait on nothing, so
   that six workers could land faster. With two workers the merge turn it
   removes is no longer the bottleneck, and a main that is always green is
   worth more to a released template than minutes per merge. This reverses
   the owner's word of 2026-10-16; the owner's `binding` comment on #1426
   covers it. The maintainer closes #1388 and #1389 as not built (asked on
   #1379); #1379 closes with them. `dev` holds only #1381's change
   (5fd71b34, PR #1414), which nothing uses; it stays until the owner
   removes it, and nothing waits on that.
5. **The relay restarts small.** One device whose `~/.claude` is logged in
   (3st8JJ), two workers, triage on, the maintainer sweep at its default
   (every two hours). It grows by one worker at a time, only while test.yml
   on main has been green for three days and the milestone's open count
   fell in the last week, and shrinks on any strike from a login or
   identity failure. #1421 (a zero-turn failed session must pause its
   device, not strike) is priority/1 and the relay's first or second pick.
6. **The spec is rewritten in the present tense.** `docs/design.md` becomes
   what the factory is now, under 600 lines; the old text moves to
   `docs/design-history.md` unchanged. Every agent session reads the spec;
   four stacked versions cost every one of them.
7. **The verifier session is the one agent that touches a cluster.** The
   owner starts it with one command when #1429 reaches *Needs you* and
   leaves it alone. It is the only thing in the plan that waits on Kind.
8. **No new relay, landing or CI feature until the milestone closes.** The
   rule goes into AGENTS.md through #1424 (asked there).
9. **The 190 strike comments stay.** They are the relay's record.

## Steps

### Step 0: the step-0 session

Started by the owner, from a plain shell in the checkout on 3st8JJ, after
the owner's part below is done:

```bash
cd ~/c4factory && ./dev run env CLAUDE_CONFIG_DIR="$HOME/.claude" \
  claude -p --permission-mode auto --model opus \
  "Run step 0 of docs/plans/fresh-start.md, then end. Project commands bare, as the plan says."
```

The session, every command bare:

1. `hack/pull.sh`. `git status --short` must print nothing; if it prints
   `.claude/settings.json`, stop and say so in a comment on #1426: the
   owner's commit (their part, item 1) is pending.
2. `hack/lib/github.sh take 1426`, `git switch chore/1426-fresh-start-the-reference-app-is-the`,
   `git merge origin/main`. Add to AGENTS.md, at the end of "Agents Never
   Touch a Cluster": *The one exception is a verifier session: one the
   owner starts from their own shell with `C4_HUMAN_CLUSTER=1` to run one
   plan step that needs a cluster (its issue names the step). It leases a
   pool cluster, does only what the step says, releases the cluster and
   ends.* Commit with an `Agent:` trailer, push, `hack/lib/github.sh land 1427`.
3. `gh pr checkout 1412`, `hack/lib/github.sh land 1412`. It closes #1408,
   the only priority/1 fix besides #1421. `hack/lib/github.sh red test.yml`
   after it.
4. Ready the steps: `hack/lib/github.sh status N ready` for 1428, 1430,
   1431, 1432 and 1433. For 1429: `hack/lib/github.sh status 1429
   needs-maintainer` after a comment saying "verifier session: needs the
   owner to start it when #1428 is closed; please list it under *Needs
   you* with the command from the plan's owner's part."
5. Add to #1426's body (`gh issue edit 1426 --body-file FILE`, the rest
   kept) one line each: `Depends on #1428`, `#1429`, `#1430`, `#1431`,
   `#1432`. Post a handoff naming the steps, then
   `hack/lib/github.sh status 1426 waiting`. Never close #1426 by hand.
6. `RELAY_DRY_RUN=1 hack/relay.sh` to see the first pick (expected: #1421,
   else the milestone). Then `hack/relay-watch.sh --install --workers 2`.
   If the install is refused, `hack/relay-watch.sh --workers 2` in the
   same session and say so in the handoff: the owner installs it later.
7. Hand off on #1426 (the `handoff` skill) with: what landed, the first
   pick, the device, and anything refused. End.

### Step 1: clear the deck (done, except the lands)

#1112 and #1116 carry the comment that their pull requests fail checks and
that the second to land takes aws-standard v1.13.0's successor. #1379
carries decision 4. Landing #1412 is step 0's.

### Step 2: the milestone and the priorities (done)

Milestone *Reference app: the webshop ships*, holding #1072, #1161, #1162,
#1163, #1069, #1150, #1428, #1429 and #1430 at `priority/2`. Moved from 2
to 4: #1393, #1394, #1229 to #1234, #1070, #1071, #1420, #1209, #1210.
Stayed at 2: #1424, #1112, #1116, #1305, #1267, #1266, #1249, #1215.
#1424 carries the narrowed rule to write into ROADMAP.md and the skills.

### Step 3: the reference Brief (#1428, relay)

`config/samples/webshop` grows to a storefront (react-vite), orders-api
(python-fastapi, as today), a worker (python-cli or node-cli) and a Postgres
DataStore (the `postgres` template), with the Relationships between them;
a Brief next to the model whose words describe the shop a customer uses,
with an external payment provider; and a deploy to the Kind cluster through
`k8s-manifests` once #1162 lands. Done when `make validate-yaml` and `make
check` pass with it, `docs/getting-started.md` names it as the example, and
the stand-in planner's plan lists the four Containers and the DataStore.

What #1428 chose: the fourth Container is `catalog-api`, the catalog behind
"browse", so `orders-api` keeps orders and accounts; both keep their data in
one Postgres DataStore, `shop-db`, a schema each; the worker is
`payments-worker`, which confirms payments with the provider and marks orders
paid through `orders-api`. The Brief sits next to the model, so the planner
keeps the architecture and the build is the same each time. No Framework runs
a worker on Kubernetes, so `payments-worker` waits on #1468; no DeployTarget
ships to a cluster (only GitHub), so the deploy line waits on #1072, not
#1162. `TestWebshopSampleIsTheReferenceApp` holds the sample to all of it.

### Step 4: the verifier session (#1429, the owner starts it)

The owner's one command, from their own shell on 3st8JJ, once #1428 is
closed and #1429 is under *Needs you*:

```bash
cd ~/c4factory && C4_HUMAN_CLUSTER=1 ./dev run env CLAUDE_CONFIG_DIR="$HOME/.claude" \
  claude -p --permission-mode auto --model opus \
  "You are the verifier session of docs/plans/fresh-start.md, step 4, issue #1429. Take it, do what it says, close it when its Done when holds, end."
```

The session leases a fresh bare pool cluster, installs the newest release
as `docs/getting-started.md` says, applies the webshop Brief, lets the
Crews build and release it (the `factory-ops` skill), judges what came out
against what a senior engineer would ship, files one issue per gap in the
milestone at `priority/2` with the evidence, lists them in a comment,
releases the cluster and closes #1429. The issue body has the detail.

### Step 5: the gaps (folded into step 4)

### Step 6: the template scorecard (#1430, relay)

`docs/templates.md`: one row per template, one column per promise (CI
passes, tests, lint, README, deploy, logging, auth), each filled cell naming
its evidence, each empty cell an issue. Done when every template has a row
and every empty cell has an issue number.

### Step 7: the spec in the present tense (#1431, relay)

`docs/design.md` rewritten as what the factory is, under 600 lines, no
version headings; the old text to `docs/design-history.md`; every anchor
link still resolves; a review pass before `land` (over 400 lines).

### Step 8: the test environment doc (#1432, relay, after #1431)

The device tuning moves to `docs/dev/devices.md`; testenv.md under 700
lines.

### Step 9: plans off the path (done)

#1425 carries the two extra plans to move: `ci-gate.md` and
`ci-runner-isolation.md`. `git-host-ports.md`, `git-events.md` and
`model-proxy.md` stay.

### Step 10: the rules in AGENTS.md (done, through #1424 and step 0)

#1424 carries decision 8's line. Step 0 adds the verifier exception.

### Step 11: the relay (step 0, item 6)

### Step 12: grow, and the first release (maintainer)

The maintainer's sweep raises `--workers` by one, on 3st8JJ or a second
logged-in device, only when test.yml on main has been green for three days
and the milestone's open count fell in the last week; it lowers it on any
strike from a login or identity failure. The first release after #1429's
gap issues close ships the webshop as the getting-started example and names
the milestone. #1433 (the workflow filters ci-gate left) is an ordinary
ready chore at 4.

## The owner's part

Three things, once. Then nothing until *Needs you* says so.

1. **One commit on main, by you,** of `.claude/settings.json` as you
   already edited it (the six added allow rules) and of GOVERNANCE.md with
   two changes: the sentence under "Reserved to the owner" that names the
   two allowed commands becomes *Today they allow `hack/lib/github.sh`,
   `hack/relay.sh`, `hack/relay-watch.sh`, `hack/release.sh`, `gh issue
   edit`, `gh pr edit` and the milestones API, which the owner gave the
   relay and the sessions that run a binding plan; no agent widens them*;
   and under "How decisions are made" one bullet: *A binding plan is the
   owner's word. When the owner comments `binding` on a plan's parent
   issue, the plan's decisions are the owner's: the maintainer and triage
   follow them, and a session may make the calls the plan assigns it
   without a further owner comment.* Agents cannot make this commit.
2. **One comment on #1426**, from your account, saying `binding`.
3. **Start the step-0 session** with the command in step 0. Later, when
   #1429 is under *Needs you*, start the verifier session with the command
   in step 4.

Optional, whenever convenient: remove the `dev` branch (`git push origin
--delete dev`); keep Claude Code logged in on 3st8JJ (`claude -p ok` from a
plain shell answers); log it in on Skinny-vivobook before any relay runs
there.

## Done when

- This plan and #1412 landed; main green; #1388, #1389 and #1379 closed.
- The milestone holds the webshop's path at `priority/2`; every other
  issue at 2 is a template bug, a template pull request or #1424.
- `config/samples/webshop` describes the four-Container webshop and passes
  `make check` (#1428).
- The verifier session built it once on Kind from the published release and
  every gap is an issue in the milestone (#1429).
- `docs/templates.md` has a row per template with an issue per empty cell
  (#1430).
- `docs/design.md` is under 600 lines with no version headings, and
  `docs/design-history.md` holds the old text (#1431); testenv.md is split
  (#1432).
- The relay runs on 3st8JJ with two workers under the installed watch, and
  no strike since the restart came from a login or identity failure.
- The first release after the milestone closes ships the webshop as the
  example in `docs/getting-started.md`.
