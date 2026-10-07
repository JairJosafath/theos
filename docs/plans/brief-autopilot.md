# A Brief looks after its own handed-back work

GitHub issue #486, reported by gray-lenovo on v0.5.0. Agent: otter-486.
Builds on [done/brief-failed-issues.md](done/brief-failed-issues.md) (#429): that plan
lists a Brief's failed issues and lets a prompt retry or close them. This plan
has the Brief do that by itself, the way this repository's maintainer decides
for its owner, and bring the person only what only they can decide.

## Goal

A Brief's owner stays out of the day-to-day. Issues handed back
(`status/needs-human`) and the Brief's items on the Factory view's *Needs a
look* list are picked up without a person: what heals by itself is left
alone for a while, the rest is decided (retry with what was learned, split,
close, or wait), the three-tries guard still holds, and what only the owner
can decide lands in one *Needs you* list with the choice the Brief would make
and why. Every action is reported, so a prompt can overrule it, and a Brief
field turns this `Off`, `Ask` or `On`.

Done when, in envtest, a Brief in `On` whose Container has a failed Task
starts a round with no new prompt, applies the planner's retry, and the Crew
starts the next Task; a Brief in `Ask` lists the same answer under *Needs you*
and applies nothing until a prompt says so; a Task waiting for its agent
(`AgentUnavailable`) or for a Standard change is left alone inside its window
and listed under *Needs you* past it; an issue whose three tries are used is
never retried; and a failed issue answered `wait` starts no second round
until something changed. The card shows *Needs you* and what the Brief did.

## Decisions, and why

1. **The field: `spec.autopilot`, `Off`, `Ask` or `On`.** An enum, mutable
   (the person changes their mind), written by kubectl, GitOps or the UI's
   create form; the UI server still writes nothing but creating a Brief and
   appending a prompt. Unset means the operator's default, `Ask`, so the
   default can change in a release without rewriting stored Briefs (no CRD
   default). `Off` is today: nothing happens without a prompt. `Ask` runs the
   same rounds as `On` but applies none of the answers; they become *Needs
   you* entries with the recommendation, and the person answers in a prompt
   ("do what you suggest", "close vscode#3 instead"). `On` applies them. Until
   the last step of this plan, unset means `Off`, so nothing changes for
   anyone before the whole loop has landed.
2. **What the Brief watches is its own.** Only things the Brief made (label
   `c4.factory.io/brief`): failed issues (`status.failed`, #429), its
   Containers' Tasks, its Crews and its Containers. Node memory, node
   pressure, components and the API server are the factory's, not a Brief's:
   they stay on the Factory view, and the Brief never acts on them. A memory
   spike that stalls a Task shows up as that Task waiting, which the Brief
   does watch.
3. **Heal windows, in `internal/model`, one table.** The operator classifies
   each item every reconcile (`model.BriefAttention`) as *healing* (left
   alone, with when it will act) or *due*. Windows, from when the item began:

   | Item | Heals by itself because | Window |
   | --- | --- | --- |
   | Task `AgentUnavailable` | usage limit resets at `status.agentBackAt` | until `agentBackAt` + 1h |
   | Task `WaitingForSlot` | a slot frees, longest waiter first | 4h |
   | Task Blocked, `WaitingForStandardChange` | the change is in review | while the change's Task is not final, then 1h |
   | Task in Checking | the operator already fails it at 30m | none (never due; it fails or passes) |
   | Task stuck in a stage | the UI's `STUCK_AFTER_MS` thresholds | that threshold × 2 |
   | Crew Ready=False `GitHostUnavailable`, `ContainerNotReady` | the host or scaffold comes back | 1h |
   | Crew Ready=False `*NotFound` | does not heal | due at once |
   | Container `Failed` | does not heal | due at once |
   | failed issue | does not heal | due at once |

   Waits the operator orders itself (an `after:` blocker, a `uses:` app with
   no release, #428/#430) are not items: the issue is simply not ready yet.
   The windows are generous on purpose: acting late costs an hour, acting on
   something that would have healed costs a Task and a try.
4. **Who decides what.** The planner decides only failed issues, because only
   there is a judgment to make (retry with what clarification, split, close,
   wait) and #455 already applies the answers. Everything else that is due
   is a fact, not a judgment, and goes straight onto *Needs you* with a
   recommendation from the table in `model` ("Crew `cel-lens-cli` can't find
   Agent `implementer`: create it or name another in the Brief"; "Task
   `cel-lens-core-8` has waited 26h for Standard change #27: review or close
   it"). No model call is spent on what has one answer, and no agent touches
   credentials, Agents or money (design: "it asks only for what costs money or
   touches access").
5. **A tending round.** A round of the planner with no new prompt, started
   when the last round is over, no prompt is waiting (a prompt round goes
   first and answers the same issues), `autopilot` is `Ask` or `On`, and at
   least one failed issue is *due and unanswered*. It is a Plan Task like any
   round (`<brief>-plan-<round>`, label `c4.factory.io/brief-round-kind: tend`),
   so cost, history and timeline need nothing new, and its workspace is the
   same plus a line in `brief.md`: "No new prompt: decide the failed issues in
   `model/failed.yaml`". The plan it may write is `plan/failed.yaml` and
   `plan/summary.md`, plus new issues for a split; any story change refuses
   it (a tending round has no say on the product).
6. **Answers: `retry`, `close`, `wait`, `ask`.** #429's `retry` and `close`,
   with split as `close` plus new issues, as there. Two more:
   - `wait`, with `why`: the issue stays handed back and is not due again
     until something changed: a Task on one of the Brief's Containers merged,
     one of its apps made a release, or 24h passed. This is "the engine had
     no release yet".
   - `ask`, with `why` and `recommend` (one of the other answers, with its
     clarification): it needs the owner (product direction, money, access);
     it goes onto *Needs you* as is and is not due again until a prompt
     answers it.
   In `Ask` mode every `retry` and `close` is kept as an `ask` with that
   recommendation. A prompt round answers *Needs you* entries like any failed
   issue, and its workspace's `model/failed.yaml` carries each one's
   recommendation so "do what you suggest" is enough.
7. **It doesn't loop.** Three guards, each on its own enough:
   - the Crew's three tries stay: `CheckFailedAnswers` already refuses a retry
     of an `exhausted` issue, and only a close and a new issue (reworded or
     split) goes on, which is what the guard asks a person to do;
   - an item is answered once per state: the Brief remembers each answered
     issue with the number of failed Tasks it had (`status.tended`), and it is
     due again only when that number grows (the retry failed too) or its
     `wait` lapses;
   - at most one tending round per hour (`TendInterval`), and a tending round
     that fails or is refused waits that hour, with its reasons in the next
     round's workspace like any refused plan.
   A round that answers nothing (the planner writes no `plan/failed.yaml`) is
   a valid round: every due issue then counts as answered `ask`, recommend
   none, so it is not raised again on its own.
8. **One place: `status.needsYou`.** Each entry: what (`container#number`,
   or a Task, Crew or Container by kind and name), the question in one
   sentence, the recommendation and why, and since when. At most 50. A
   condition `NeedsYou`, True while any is listed, says the first and "N
   more"; a printer column `Needs you` on `kubectl get brief`; a Normal Event
   `NeedsYou` when an entry first appears. The operator's own `NeedsInput`
   (a missing Agent or Secret) stays as it is: it already stops the round,
   and the card shows both in the same box.
9. **It tells what it did.** Each tending round is a history entry, outcome
   `Tended`, its summary the planner's `plan/summary.md` or, when it wrote
   none, one line per answer ("Tried cel-lens-cli#1 again: the engine's
   checker is merged now"); an Event `Tended` counts what it retried, closed,
   waited on and asked. Comments on the issues quote the why, as in #455,
   naming the round as a tending round instead of quoting prompts.
   `status.attention` lists the healing items with when the Brief will act,
   so "why is nothing happening" has an answer. Overruling is a prompt: the
   next prompt round sees the last tending round's answers in
   `model/tended.yaml` and can file a closed issue again or retry a waiting
   one.
10. **The Factory view says who is on it.** A *Needs a look* item of a
    Brief's Task, Crew or Container names the Brief and what it will do
    ("Brief `cel-lens` acts after 14:00 if this hasn't healed", or "on Brief
    `cel-lens`'s Needs you list"), read from the Brief's `status.attention` and
    `needsYou`; the list stays read-only.
11. **`By hand:` issues (#474)** are a person's by design: when that lands,
    they are listed under *Needs you* and never retried. Nothing to build
    here before #474.
12. **Stand-in planner.** It answers nothing in a tending round, as with
    suggestions: every due issue becomes an `ask` (decision 7). The loop is
    tested in envtest with the plan written by the test.

## Steps

Each lands on its own and leaves main working; `autopilot` unset means `Off`
until step 4.

1. **Model** (#503) (`internal/model`): `BriefAttention` and the heal-window table,
   the *Needs you* entries and their recommendations, `StartsTend` (decisions
   5 and 7), `wait` and `ask` in `plan/failed.yaml` and `CheckFailedAnswers`,
   `Ask` turning answers into asks, `model/tended.yaml`; table tests. Nothing
   calls it yet. Depends on #455.
2. **The Brief tends its failed issues** (#504) (API and controller):
   `spec.autopilot`, `status.attention`, `status.needsYou`, `status.tended`,
   the `NeedsYou` condition, column and Event; tending rounds, applying their
   answers by mode, the `Tended` outcome and Event; the planner guide's
   section on tending rounds; envtest for `On`, `Ask`, the exhausted guard,
   `wait`, and Tasks inside and past their windows. Depends on step 1.
3. **The card and the Factory view** (#505) (`ui/`): the Brief's *Needs you* box
   (with `NeedsInput`) and what it did, the autopilot mode and the create
   form's choice, and *Needs a look* naming the Brief; `ui/src/model` tests.
   Depends on step 2 and #456.
4. **Turn it on** (#506): unset `autopilot` means `Ask`; `docs/design.md` and the
   getting-started guide say so; envtest of a Brief with no field set.
   Depends on step 3. Closes #486.

What only a human run on Kind would show: a real planner deciding well
between retry, split, close and wait, and the windows feeling right on a
laptop factory.
