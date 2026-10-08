# A Brief's Crews build several issues at once

GitHub issue #533, reported by gray-lenovo on v0.5.0. Agent: otter-533.

## Goal

A Brief makes every Crew with `parallel: 1` (`ensureCrew` in
`internal/controller/brief_apply.go`), so a part with three ready issues
builds one while agent slots sit free, and nothing in the Brief changes it.
After this plan a Brief's Crews take several ready issues at once by default,
and the person says how many in plain words, in a prompt, or on the Brief
itself, without going around it.

Done when, in envtest: a Brief with no say makes its Crews with
`parallel: 3`; `spec.parallel: 2` makes them with 2, and changing it later
patches every Crew the Brief made, those of earlier rounds included, and
leaves a Crew it didn't make alone; a round whose new prompt asks for "up to
two at a time" applies a planner's `plan/parallel.yaml` of 2 the same way;
a plan writing it with no new prompt, or out of 1 to 5, is refused; and
`status.apps` shows each Crew's `parallel`.

## Decisions, and why

1. **The Brief owns its Crews' `parallel`.** A Crew the Brief made (label
   `c4.factory.io/brief`) has its `spec.parallel` set from the Brief every
   reconcile, whatever the round is doing, as `syncReleases` does for
   releases: a change reaches Crews of earlier rounds without waiting for
   one. The reporter's Crews exist already; a setting that only reached new
   Crews would fix nothing for them. A hand edit of such a Crew's `parallel`
   is set back, with a log line, because two writers of one field never
   settle; the Brief is where its Crews are steered. A Crew the Brief didn't
   make is never touched.
2. **Unset means 3, the operator's default.** `model.BriefParallelDefault`,
   no CRD default, so a release can change it without rewriting stored
   Briefs (as `spec.autopilot` in [brief-autopilot.md](brief-autopilot.md)).
   Three because a round files a handful of issues per Container and the
   reporter's part had three ready; the Crew already sends a change that
   conflicts with one merged before it back to its implementer to merge
   main without costing a round, `after:` keeps ordered issues waiting for
   their blockers, and the device-wide agent-slot queue (`WaitingForSlot`)
   still caps how many agents run, so a higher default can't overrun the
   machine. Total cost doesn't grow, the same issues are built; only how
   much is spent at once does. Upgrading turns it on for existing Briefs,
   which is what the reporter asked for, and the release notes say so.
3. **`spec.parallel`, 1 to 5, mutable**, the Crew's own bounds. It is the
   person's say from kubectl, GitOps or the UI's create form; the UI server
   still writes nothing but creating a Brief and appending a prompt. One
   number for the whole Brief, not per Container: the report asks for "how
   much may be built at once", and a per-Container list can follow if
   anyone asks.
4. **A prompt can say it too, and the planner relays it.** A Crew's size
   costs money, so it stays out of the planner's own choices (design,
   *It asks only for what costs money or touches access*). A number the
   person gives in a prompt is the person's say, not the planner's, like
   turning releases on with a prompt: when a **new** prompt of the round asks
   how much may be built at once, the planner writes `plan/parallel.yaml`,
   `parallel: N` and `why:` quoting the prompt. The operator refuses the plan
   when N is outside 1 to 5, or when the round has no new prompt (a tending
   round never changes it). Applying the round, it records N in
   `status.parallel` with the round, and that sets the Crews as `spec`
   does. The workspace's `brief.md` says what the Crews build at once now
   and where that comes from, so the planner knows.
5. **`spec.parallel` wins over a prompt.** A person who set it on the Brief
   chose that place; the workspace says so, and the planner doesn't write
   `parallel.yaml` but says in its choices that the number is set on the
   Brief. A plan that writes it anyway is refused with that reason, so the
   next round knows. Removing `spec.parallel` brings back the prompt's
   number, else the default. One rule, `model.BriefParallel(spec, status)`.
6. **The card says it.** `status.apps[].parallel` is copied from the Crew,
   which holds every fact, as releases are; the card can show "3 at a time"
   with no other read.

## Steps

Each is its own issue and pull request, and leaves main working.

1. **The Brief sets its Crews' parallel** (#554, done) (API, controller, model):
   `spec.parallel` (1 to 5, optional), `model.BriefParallelDefault` = 3 and
   `model.BriefParallel`; `ensureCrew` makes Crews with it, and a sync each
   reconcile patches the Crews the Brief made; `status.apps[].parallel`;
   `docs/design.md`'s Brief section says the default and the sync. Done
   when the envtest cases of the goal without a prompt pass: default 3,
   `spec.parallel` 2 on new and earlier Crews, a Crew not the Brief's left
   alone, a hand edit set back, `status.apps` showing it.
2. **A prompt says how much at once** (#555, done) (model, controller, planner):
   `plan/parallel.yaml` parsed strictly in `internal/model`, the plan's
   checks (1 to 5, a new prompt in the round, `spec.parallel` unset),
   `status.parallel` recorded on apply and used by `model.BriefParallel`,
   the workspace's `brief.md` line, and `planner.md`'s instruction and
   example. Done when model table tests cover the parsing and each refusal,
   and envtest applies a plan of 2 from a prompt to the Brief's Crews.
   Depends on step 1. Closes #533.

## What only a human run would show

Whether three Tasks of one Container on a real node merge cleanly in turn
(conflicts going back to the implementer) is the Crew's behaviour, tested in
envtest with faked Tasks; a Kind run of a Brief with three ready issues in
one part would show it end to end.
