# Fresh start: the reference app is the definition of done

Parent issue: #1426. Written 2026-10-19 by
a session the owner ran, at the owner's request, for the session that
orchestrates it. That session is one the owner starts in this checkout and
hands this file to; the hand-over is the owner's word to take the parent
issue and work the orchestrator's steps (AGENTS.md, "Issues are the relay's").

## How to run this plan

- Read "The owner's checklist" first and check each line. What is not done
  there, say in your first handoff. Steps 11 and 12 wait on it; nothing else
  does.
- One step per session, the `handoff` skill between them. Step 0 and step 1
  fit one session. Steps marked *relay* are issues you file; the relay works
  them. Steps marked *owner* are the owner's: you file the issue and send it
  to the maintainer, who sends it to the owner.
- Every command below runs in the project shell (`./dev shell`, or
  `./dev run CMD`), from this checkout.

## Goal

The factory's work is judged by one application it builds, the webshop, and
by nothing else: every open issue either closes a gap between what the
factory builds from the webshop's Brief and what a senior engineer would
ship, or waits. The spec agents read describes the factory as it is. The
relay runs again, small, on a device that is logged in, and grows only while
the webshop's gap list shrinks.

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
| Open issues | 84: 2 at priority/1, 29 at priority/2, 38 at 5, 13 at 8 and 9 |
| Open plans | 12: 4 have closed parents (#1425 moves them), 1 is ci-gate, 1 is the model proxy on the 1.0 path |
| Open pull requests | 3: #1412 green; #1423 fails Vulnerabilities; #1411 fails Tests |
| Relay | stopped since 2026-10-17 06:10 on every device; the last incident, the night of 2026-10-16: Skinny-vivobook ran logged out, about 190 strike comments and 15 false escalations across 19 issues |
| Relay login | the installed watch (`hack/relay-watch.sh --install`) gives its unit PATH but not `CLAUDE_CONFIG_DIR`, so its sessions use `~/.claude`, VS Code's login; a watch started by hand from the project shell would use `.dev/claude` (hack/dev/env.sh), which does not exist on 3st8JJ |
| Git identity on 3st8JJ in the project shell | none: `git var GIT_COMMITTER_IDENT` fails, so the relay would start no worker there (#1289) |
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
2. **The webshop is the reference application and defines done.** A
   milestone, *Reference app: the webshop ships*, holds its path. Its issues
   carry `priority/2`. Every other issue at `priority/2` today that is not a
   `kind/bug` whose fix is under `templates/` moves to `priority/4`: above
   the default, below the webshop. The owner's sweep of 2026-10-19 put all
   of them at 2 for the same goal; this narrows it to one application at a
   time, because a relay of two workers closes a handful of issues a day and
   the order decides what the next release shows. A template bug stays at 2:
   it is the quality of what the templates build. The owner's comment on the
   Maintainer report (#261, the checklist) makes this binding for triage and
   the maintainer.
3. **The webshop deploys to Kubernetes, the Kind cluster it is built on.**
   Not AWS: an AWS account is money and credentials, reserved to the owner,
   and the AWS templates have their own open bugs. #1072 is its deploy plan;
   #1070 and #1071 go to 4.
4. **ci-gate is abandoned unless the owner says finish.** Its goal was for
   merging to wait on nothing, so that six workers could land faster. With
   two workers the merge turn it removes is no longer the bottleneck, and a
   main that is always green is worth more to a released template than
   minutes per merge. This reverses the owner's word of 2026-10-16, so it is
   the owner's: their comment on #1379 decides. Abandoning loses only
   #1381's changes to `hack/pull.sh` and `hack/relay.sh`, which exist on
   `dev` alone and nothing uses.
5. **The relay restarts small.** One device whose `.dev/claude` is logged
   in, two workers, triage on, one maintainer sweep a day
   (`WATCH_MAINTAIN=86400`). It restarts after #1421 lands, so a zero-turn
   failed session pauses its device instead of striking the issue. It grows
   by one worker at a time, only while test.yml on main has been green for
   three days and the milestone's open count fell in the last week.
6. **The spec is rewritten in the present tense.** `docs/design.md` becomes
   what the factory is now, under 600 lines; the old text moves to
   `docs/design-history.md` unchanged. Every agent session reads the spec;
   four stacked versions cost every one of them.
7. **One Kind run per reference-app release is the owner's.** It is the only
   thing in the project that waits on Kind, and it waits on the owner, never
   on an agent (AGENTS.md: agents never touch a cluster). Its evidence, the
   gaps, is what the relay works.
8. **No new relay, landing or CI feature until the milestone closes.** The
   rule goes into AGENTS.md through #1424, which already rewrites the
   priority rule there. The 2026-10-16 incident came from that machinery.
9. **The 190 strike comments stay.** They are the relay's record.

## Steps

Who does each: *orchestrator* is the session the owner hands this file to;
*relay* is an issue the orchestrator files `status/ready` for the relay;
*owner* is the owner at a keyboard.

### Step 0: file and land this plan (orchestrator)

1. `hack/pull.sh`, then file the parent:
   `hack/lib/github.sh create "Fresh start: the reference app is the definition of done" FILE status/ready kind/chore source/dev priority/2`,
   FILE being the Goal and the Decisions above in short, with the line
   "The plan is docs/plans/fresh-start.md". Write its number at the top of
   this file.
2. `hack/lib/github.sh take N`, commit this file on the branch it names, push,
   open a pull request whose body says `Refs #N` (never a closing word next
   to #N), and `hack/lib/github.sh land PR`.
3. File steps 3, 6, 7 and 8 as issues now (their commands are in their
   steps), each with the `Depends on #S` lines its step says. Then add a
   `Depends on #S` line per step to N's body (`gh issue edit N --body-file
   FILE`, the rest kept), post a handoff, and `hack/lib/github.sh status N
   waiting`. Never close N by hand; it closes with its last step.

### Step 1: clear the deck (orchestrator, same session)

1. `hack/lib/github.sh land 1412`: it closes #1408, the only priority/1 fix.
   `hack/lib/github.sh red test.yml` after it, to see main stayed green.
2. #1423 (for #1112) fails Vulnerabilities and #1411 (for #1116) fails Tests.
   Don't land them. Comment on #1112 and #1116 what fails and that the branch
   must merge main first; the one that lands second takes aws-standard
   v1.14.0 (said there already). They keep `priority/2`.
3. ci-gate, by the owner's comment on #1379:
   - *abandon* (the recommendation): the owner deletes `dev`
     (`git push origin --delete dev`, reserved to the owner; or their comment
     says you may). Then `hack/lib/github.sh close 1388 FILE` and
     `hack/lib/github.sh close 1389 FILE`, in that order (1389 depends on
     1388), FILE saying "Superseded by docs/plans/fresh-start.md, decision 4:
     not built." #1379 closes itself (`parents --apply`). Step 9 moves
     `docs/plans/ci-gate.md`.
   - *finish*: `hack/lib/github.sh status 1388 ready`, leave #1389 as it is;
     the relay works them at their priority. Decision 5 still holds.
   - no comment yet: leave the three as they are and say so in the handoff;
     nothing else waits on it.

### Step 2: the milestone and the priorities (orchestrator)

1. `gh api repos/JairJosafath/c4factory/milestones -X POST -f title="Reference app: the webshop ships" -f description="Done when: the webshop Brief in config/samples/webshop builds four Containers, a DataStore and a Kubernetes deploy on a Kind cluster from the published release; every gap the owner found in that build is closed; and a release ships it as the getting-started example."`
2. Into it, with `hack/lib/github.sh milestone N "Reference app: the webshop ships"`:
   the issues of steps 3, 4 and 6; #1072, #1161, #1162, #1163 and #1069 (the
   deploy chain, for Kubernetes); #1150 if the webshop's Brief names an external system (step 3
   decides). Every issue in it carries `priority/2`. #1215 and #1286 stay in the
   1.0 milestone (an issue has one milestone); #1215 stays at 2.
3. Every other open issue at `priority/2` that is not a `kind/bug` under
   `templates/` goes to 4: `hack/lib/github.sh label N priority/4`, then
   `gh issue edit N --remove-label priority/2`. Today: #1393, #1394, #1229 to
   #1234, #1070, #1071, #1420, #1209, #1210, #1150 (unless in the milestone),
   #1388 and #1389 if still open. Stay at 2: #1424 (carries the rule), #1112
   and #1116 (template features with open pull requests), #1305, #1267,
   #1266, #1249 and #1215 (template bugs). Say the lists in the handoff.
4. Comment on #1424 with decision 2 in one paragraph, so the rule it writes
   into ROADMAP.md and the skills says: the milestone's issues and template
   bugs first, at 2; the rest of the goal's work at 4; refactors at 8 and 9.

### Step 3: the reference Brief (relay, `kind/feature`, in the milestone)

Title: "The webshop is the reference application: four Containers, a
DataStore and a Kubernetes deploy target in config/samples/webshop". Body:
the webshop's `story.yaml` grows to a storefront (react-vite), orders-api
(python-fastapi, as today), a worker (python-cli or node-cli) and a Postgres
DataStore (the `postgres` template), with Relationships between them, and a
deploy to the Kind cluster through `k8s-manifests` once #1162 lands; until
then the Brief says what it will deploy to. The Brief's words describe the
shop a customer uses, not the stack. Done when `make validate-yaml` and
`make check` pass with the sample, `docs/getting-started.md` names it as the
example, and the stand-in planner's plan for the Brief lists the four
Containers. Depends on nothing; the deploy line follows #1162 later.

### Step 4: build it on Kind and write down every gap (owner)

File it `kind/chore`, in the milestone, `Depends on #<step 3>`, then
`hack/lib/github.sh status N needs-maintainer` with a comment: "Needs the
owner at the cluster device; agents never touch a cluster." The maintainer
sends it to the owner under *Needs you*.

The owner, on 3st8JJ, in their own shell with `C4_HUMAN_CLUSTER=1`:
`hack/lib/github.sh cluster acquire --fresh --bare`, install the newest
release as `docs/getting-started.md` says, apply the webshop Brief, let the
Crews build it and release it (the `factory-ops` skill). They comment on the
issue what came out: each repository's CI result, what the app does when
run, and what is missing against what they would ship (tests, a README that
works, migrations, auth, logging, a deploy that comes up, a storefront that
orders). Then `hack/lib/github.sh cluster release`. Done when that comment
exists.

### Step 5: the gaps become issues (orchestrator, or the owner's session)

From the step 4 comment, one issue per gap:
`hack/lib/github.sh create TITLE FILE status/ready kind/<bug|feature> source/dev priority/2`,
milestone set, body quoting the evidence. A gap in a template is a template
bug; a gap in what the Crews did is a factory issue; a gap in the Brief's
plan is a planner issue. Done when every gap in the comment has an issue
number next to it.

### Step 6: the template scorecard (relay, `kind/docs`, in the milestone)

`docs/templates.md`: one row per template under `templates/`: what it is
(Standard, Framework, Connector, deploy target); whether its scaffold passes
its own CI, has tests, lint, a README a person can follow, a deploy, logging,
and auth where it serves users. Each cell says where the evidence is (a test
name, a workflow). Empty cells are the gaps; each gets an issue at 2 if the
webshop uses that template, else at 4. Done when every template has a row
and every empty cell has an issue number.

### Step 7: the spec in the present tense (relay, `kind/docs`, priority/2)

`docs/design.md` is rewritten as what the factory is now: the model, how it
works, agents and Crews, Briefs, the UI, components, deploys, distribution,
compatibility, test tiers; one section per thing, no version headings, under
600 lines. The old file moves to `docs/design-history.md` unchanged, with
one line at its top. Every link to a `docs/design.md` anchor still resolves
(`grep -rn 'design.md#'` over the repo and `.claude/skills`). Over 400
changed lines, so it needs a review pass before `land` (the `handoff` skill,
"Review before landing"). Done when `make check` passes, the file is under
600 lines, and `grep -c '^## v' docs/design.md` prints 0.

### Step 8: the test environment doc (relay, `kind/docs`, priority/4)

`docs/dev/testenv.md` keeps the tiers, local Gitea, deploys, the relay and
the runners; the device tuning (memory, disk and CPU on the cluster device)
moves to `docs/dev/devices.md`, and AGENTS.md's table gets the new file.
Done when testenv.md is under 700 lines and every link resolves. Depends on
#<step 7>, so the two don't collide.

### Step 9: plans off the path (orchestrator, a comment on #1425)

#1425 already moves the four finished plans. Comment that it also moves
`docs/plans/ci-gate.md` (once step 1 abandoned it) and
`docs/plans/ci-runner-isolation.md` when its issues are closed or at 8 or
lower, each with one line at the top: "Superseded 2026-10-19 by
fresh-start.md; reopens when the reference app needs it."
`git-host-ports.md` and `git-events.md` stay: their chains are open at 8 and
9. `model-proxy.md` stays: it is on the 1.0 path.

### Step 10: the rule in AGENTS.md (through #1424)

Comment on #1424: its AGENTS.md change also adds, under "How to Work", one
line: "No new relay, landing or CI feature until the reference-app milestone
closes; file the idea `status/proposed` at priority/8."

### Step 11: restart the relay (orchestrator, after the checklist and #1421)

1. Wait for #1421 to close: the relay's own bug, so a zero-turn exit-1
   session pauses its device and never strikes. If the relay is not yet up
   your next session may take it (`hack/lib/github.sh take 1421`); it is
   priority/1.
2. On the device the owner logged in (3st8JJ today; Skinny-vivobook once the
   owner logs it in): `./dev run claude -p ok` answers;
   `./dev run git var GIT_COMMITTER_IDENT` prints a name;
   `hack/lib/github.sh claims` shows nothing stale.
3. `RELAY_DRY_RUN=1 hack/relay.sh` shows the issue the first session would
   get; it should be at priority/1 or in the milestone.
4. `WATCH_MAINTAIN=86400 hack/relay-watch.sh --workers 2`, in the project
   shell, in a terminal that stays open (`tmux`). After a day without a
   strike from a login or identity failure,
   `hack/relay-watch.sh --install --workers 2` keeps it up across reboots.
   `touch .relay/stop` stops it.
5. Say in the parent's handoff: the device, the workers, the first issue
   picked.

### Step 12: grow, and the first release (maintainer, by the rule)

The maintainer's sweep raises `--workers` by one, on the same device or a
second logged-in one, only when test.yml on main has been green for three
days and the milestone's open count fell in the last week; it lowers it on
any strike from a login or identity failure. The first release after step
5's issues close ships the webshop as the getting-started example and names
the milestone.

## The owner's checklist

Only the owner can do these (GOVERNANCE.md, "Reserved to the owner"). Each
line says how to check it.

1. **Keep Claude Code logged in on each relay device.** The installed watch
   runs its sessions with `~/.claude`, the same login as VS Code; Skinny-vivobook
   ran logged out on 2026-10-16 because that login had expired there. Install
   the watch (`--install`) rather than starting it by hand from the project
   shell, whose `CLAUDE_CONFIG_DIR=.dev/claude` is a separate, empty login.
   Check: `claude -p ok` from a plain shell prints an answer, not "Not logged in".
2. **Give git an identity on each relay device.** In a plain shell, not the
   project shell, so it lands in `~/.gitconfig`, which both read:
   `git config --global user.name "..."` and
   `git config --global user.email "..."`. Check:
   `./dev run git var GIT_COMMITTER_IDENT` prints it. Without it the relay
   starts no worker (#1289).
3. **Allow the orchestrator's commands in `.claude/settings.json`.** Today
   it allows only `hack/lib/github.sh land` and `hack/release.sh`; auto mode
   may refuse the rest, and no agent widens the list. Add to
   `permissions.allow`:

   ```json
   "Bash(hack/lib/github.sh:*)",
   "Bash(hack/relay-watch.sh:*)",
   "Bash(hack/relay.sh:*)",
   "Bash(gh issue edit:*)",
   "Bash(gh pr edit:*)",
   "Bash(gh api repos/JairJosafath/c4factory/milestones:*)"
   ```

   The hooks (`close-guard`, `heavy-guard`, the heartbeat) still run on every
   call, and `github.sh`'s own refusals stay. Update the sentence in
   GOVERNANCE.md that names the two commands; only the owner edits that
   file. Commit both on main, or let the orchestrator carry them in step 0's
   pull request.
4. **Decide ci-gate on #1379** with one comment in your own account and no
   `Agent:` line: "abandon: delete dev" or "finish". If abandon, delete the
   branch yourself or say the orchestrator may.
5. **Make this plan binding** with one comment on #261, the Maintainer
   report: "docs/plans/fresh-start.md is the owner's decision; the
   maintainer and triage follow its decisions 2 to 9 until I say
   otherwise." Without it the maintainer may read decision 2 as reversing
   your 2026-10-19 sweep.
6. **Money.** Two workers on `--model opus` bill your account; starting the
   watch is the approval. The Kind run in step 4 is yours too.
7. **The Kind run** (step 4), when its issue reaches *Needs you*.

## Done when

- #1412 landed and main green; ci-gate finished or abandoned, with none of
  its issues half-built.
- The milestone exists with its Done when, holds the webshop's path, and
  every issue in it is at priority/2; every other issue at 2 is a template
  bug, a template pull request or #1424.
- `config/samples/webshop` describes the four-Container webshop and passes
  `make check`.
- The owner built it once on Kind from the published release, and every gap
  is an issue in the milestone.
- `docs/templates.md` has a row per template, and every empty cell has an
  issue.
- `docs/design.md` is under 600 lines with no version headings, and
  `docs/design-history.md` holds the old text.
- The relay runs on one logged-in device with two workers and a daily
  maintainer sweep, and no strike since the restart came from a login or
  identity failure.
- The first release after the milestone closes ships the webshop as the
  example in `docs/getting-started.md`.
