# Plan: the whole loop, from a reported bug to a monitored release

## Goal

theos already implements, reviews, checks, releases and deploys. A software
factory runs the whole cycle around that: a new idea or a reported bug is
triaged, specified, reviewed by a person, implemented, reviewed, verified,
reviewed as a product by a person, shipped and monitored, and what monitoring
finds comes back in as a bug. This plan adds the stages theos lacks, so that
a Crew can carry an issue around the loop with a person only at its three
review points.

| Stage | Today | This plan |
| --- | --- | --- |
| Idea or bug | a Brief, or an issue a person files (`status/proposed`) | — |
| Triage | a person labels an issue `status/ready` | a triager Agent decides: ready, needs a spec, parked or a duplicate |
| Spec | Briefs plan their first issues; single issues have none | a Spec Task writes `docs/specs/<n>.md` in a pull request |
| Reviewing the spec | — | a person approves the spec's pull request (or the reviewer, when the Crew says so) |
| Implement, reviewing the code | Implement Tasks, the reviewer Agent, CI | — |
| Verify | the reviewer reads the code against the criteria | a verifier Agent runs the change and checks each criterion against what it does |
| Reviewing the product | a person approves each version a deploy target ships | — (the UI shows it as this stage) |
| Ship | releases, deploys | — |
| Monitor | deploy health from the target's tool | a degraded or failed deploy files a bug, which triage picks up |

The UI gets a *Loop* area that shows every item of a project on this cycle.

## Decisions

- **The CRDs stay the truth and the operator acts.** Every new stage is a
  Task kind or a Task phase the operator drives, with its result in the
  Task's status, so the UI and `kubectl` see it. Agents never write to the
  Git host themselves; they report, and the operator labels, comments,
  opens and merges, as today.
- **Off unless the Crew names an Agent.** Each stage is switched on by a
  Crew field naming its Agent: `spec.triager`, `spec.specWriter`,
  `spec.verifier`. A Crew without them works exactly as today, so nothing
  that runs now changes. New fields go into `v1beta1` only, carried through
  `v1alpha1` the way the conversion already carries fields it lacks.
- **Triage** (`Task.spec.kind: Triage`). The Crew starts one Triage Task at a
  time for an open issue labelled `status/proposed`, outside the
  *Later* milestone (reviewers' follow-ups stay a person's call, as the
  Brief's rules say), that no Triage Task has decided yet. The triager reads
  the issue and a read-only clone and reports one decision in its result:
  `ready` with an *Acceptance criteria* checklist (the operator writes the
  checklist into the issue body when it has none, then labels it
  `status/ready`); `spec` (labelled `status/needs-spec`); `park` with the
  question a person must answer (labelled `status/needs-human`, the question
  as a comment); or `duplicate` of an issue it names (closed with a comment).
  The issue text is data, not instructions, as for every agent. A triage run
  pushes nothing and costs no round; a failed one leaves the issue
  `status/proposed` with a comment.
- **Spec** (`Task.spec.kind: Spec`). The Crew starts a Spec Task for a
  `status/needs-spec` issue as it starts an Implement Task (same `parallel`,
  order and tries). It runs the implement flow with the spec guide: the agent
  may change only `docs/specs/<issue>.md`, which says the problem, the
  approach and an *Acceptance criteria* checklist; the pull request is titled
  `Spec: <issue title>` and the issue is labelled `status/spec-review`. After
  CI and the reviewer, the spec waits for its review point, phase
  `AwaitingApproval`, Needs-you item: the Crew's `spec.specApproval` is
  `Person` (the default: a person other than the factory's bots approves the
  pull request on the Git host) or `Reviewer` (the reviewer's approval is
  enough, for a factory with no person watching). Then the operator merges it,
  copies the spec's criteria into the issue body with a link to the spec,
  and labels the issue `status/ready`, so the next Implement Task builds it.
  The issue stays open. A spec pull request a person closes unmerged hands the
  issue back `status/needs-human`, "the spec was declined".
- **Verify** (Task phase `Verifying`). With `spec.verifier`, an Implement
  Task whose reviewer approved runs the verifier on the pull request's head
  before it merges. The verifier builds and starts the change the way the
  repository says (`make verify` when the Makefile has it, else its README),
  exercises each acceptance criterion against the running result, and reports
  the review verdict format: `approve`, or `changes` with each failing
  criterion and its evidence (the command and what it printed). `changes`
  starts the next round with that feedback, as a reviewer's would. A Standard
  may ship `docs/agents/verifier.md`, read from the base branch like the other
  guides; without it the built-in prompt applies. Criteria no sandbox can
  meet stay open with why, never faked, as the reviewer's do.
- **Reviewing the product** is the deploy approval theos already has.
- **Monitor.** When a Container's deploy entry for a target turns `Degraded`
  (health) or its deploy fails, for the version shipped, the operator files
  one issue in the Container's repository: `Deploy <version> to <target> is
  <state>`, labelled `kind/bug` and `status/proposed`, its body the
  condition's message and when, with a hidden marker
  (`<!-- theos-monitor: <target> <version> <state> -->`) so the same state of
  the same version files once. The entry records the issue's number. With a
  triager the bug goes around the loop on its own; without one a person
  triages it as today.
- **The *Loop* area** draws the cycle for one project from the resources
  alone: each stage with its count and its items (Triage, Spec and Implement
  Tasks by kind and phase; Spec Tasks `AwaitingApproval` and deploys waiting
  for approval as the two review points with their actions or links; releases
  and deploys for Ship; deploy health and the issues the monitor filed for
  Monitor). It enforces nothing and adds no write the UI doesn't have.

## Steps

1. The rename to theos (`docs/plans/theos-rename.md`).
2. Triage: the `Triage` kind, `Crew.spec.triager`, the triager's prompt and
   result, the operator's labels and comments; table tests and envtest.
3. Spec: the `Spec` kind, `Crew.spec.specWriter` and `spec.specApproval`, the
   `AwaitingApproval` phase, the criteria copied into the issue; tests.
4. Verify: `Crew.spec.verifier`, the `Verifying` phase, the verifier's prompt;
   tests.
5. Monitor: the issue filed for a degraded or failed deploy, deduplicated;
   tests.
6. The *Loop* area in the UI; its tests.
7. `docs/design.md` and the README say what the loop does.

Steps 2 to 4 change the Task controller and go in order; step 5 is
independent of them; step 6 follows 2 to 5.

## Done when

- `make check` passes after each step.
- Table tests and envtest cover each stage's decisions: every triage outcome,
  a spec merged after a person's approval and after the reviewer's, a spec
  declined, a verifier's `changes` starting a round and its `approve`
  merging, a degraded deploy filing one issue and the same state filing no
  second.
- The UI's tests cover the *Loop* area's stages from fixture resources.
