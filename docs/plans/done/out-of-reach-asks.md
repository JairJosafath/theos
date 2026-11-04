# Plan: a Brief never files an issue no change may make

GitHub issue #909, reported by gray-lenovo on 12 October 2026. Agent:
otter-909.

## Goal

When a prompt or a request asks for a change no pull request may make (text
in `AGENTS.md`, a role guide in `docs/agents/`, `.theos.yaml`), the
Brief says so in the round that plans it, with why and where such a request
can go, instead of filing an issue whose Task can only end `Blocked`. And a
project has a supported place to tell its agents something about its
workspace: a file of its own that any issue may change and every agent
reads.

Done when the two steps below are merged, and design.md (*Context comes from
the C4 model* and *Briefs*) says what they built.

## What happened

The reporter asked the Brief, by prompt, to put a note for the agents in the
extension's `AGENTS.md` ("run `make check` the way CI does", for #869's
workspace failure). Round 76 filed cel-lens-vscode#355, whose only change
was that note, as a ready issue in its own release milestone. Two days later
its Task ended `Blocked`: `AGENTS.md` is one of `model.AgentNeverPaths`,
which neither a normal pull request nor a Standard change may touch
(design.md, *A protected file without a person*). Nothing before that said
so: the planner's guide and `brief.md` never name those paths, the plan
check reads no issue's words, and no file exists where a project's own notes
for its agents would go. Closing #355 left its milestone with no merged
issue, which design.md leaves to a person (*A release is also left to a
person when it has no merged issue*); not filing such an issue is what spares
that.

## Decisions

1. **The paths out of reach are `model.AgentNeverPaths`, and only those.**
   `AGENTS.md`, `docs/agents/` and `.theos.yaml` change through no pull
   request at all. Every other protected path (the Makefile, a lint config,
   `.gitea/` workflows, `.devcontainer/`) may be what an issue needs: the
   implementer then makes it as a Standard change (its own gated Task), so
   the planner may file such an issue as today. The list is the one the
   runner and `SplitChange` already use, so a path added to it later is out
   of the planner's reach too, with no second list to keep. (#911's
   `FactoryOwnPaths` is a different set, what a published source leaves out,
   and adds the platform's paths a Standard change may touch; it is not the
   rule here.)

2. **The planner is told, in `brief.md`, from the model.** `brief.md` gets a
   section *What no issue can change*, written from `model.AgentNeverPaths`
   (not typed into the guide, so it never drifts): those paths, that they
   are the agents' rules and the platform's settings, which only the
   Standard's own release or the operator changes, and that notes for the
   agents go in the project's notes file (decision 4). The planner's guide
   gets a rule that reads it: an issue never asks to change those paths, in
   its title, body or criteria, and never names them in its criteria (the
   factory already keeps them unchanged, so a criterion "AGENTS.md is
   unchanged" is never needed).

3. **What can't be filed is answered where the person reads the round.** A
   prompt whose ask is a change to those paths gets no issue for it. The
   planner says so in `plan/summary.md` (one sentence: what wasn't planned
   and why) and in `plan/choices.md` (why, and where it can go instead):
   the Brief shows both on its card as the round's summary and choices, the
   places the reporter looked. A prompt asking for an agents' note becomes
   an issue that writes it in the notes file instead, and `choices.md` says
   so. A GitHub request asking for one is answered `skip` with that why in
   `plan/requests.yaml`, posted on the request as today, or `take` as a
   notes-file issue. A new plan file or Brief status field for declined
   asks was weighed and left out: the summary and choices already reach the
   card, and one more list would be a second place to look.

4. **A project's notes to its agents: `docs/project-notes.md`.** A file the
   project owns: no Standard protects it (no catalog Standard's
   `protectedPaths` covers it, today's or the chart's), so any issue, a
   Brief's included, changes it by a normal pull request, and a person can
   commit it like any file. The implement and review prompts, which the
   operator writes, tell the agent to read it when the repository has it,
   right after `AGENTS.md`: the project's own notes about its workspace and
   ways, such as how CI runs `make check`; where it and `AGENTS.md` or the
   role guide disagree, those win. The prompt says so, rather than each
   Standard's `AGENTS.md`, so it reaches every Container now, with no
   Standard `ref` bump and no Container left out. Scaffold once holds: the
   factory writes no such file; a project that wants one adds it. Rejected:
   a field on the Container or Crew (a setting in the cluster for what is
   prose about the repository, and #869 decision 8 already declined a
   per-repository check environment); a path under `docs/agents/` (out of
   reach by decision 1).

5. **The operator checks it, too.** The plan check refuses an issue whose
   title or `## Acceptance criteria` names a path under
   `model.AgentNeverPaths` (as a word: `AGENTS.md`, `docs/agents/...`,
   `.theos.yaml`, with or without backticks), naming the issue and the
   path, and saying that no change may make it and that agents' notes go in
   `docs/project-notes.md`. The planner's guide states the same rule
   (decision 2), so a sound plan never trips it, and a refused plan costs a
   planner round, not a Crew's tries and a release. The body outside the
   criteria may name them (an issue may say "as `AGENTS.md` says"): the
   title and criteria are what the work is checked against. The stand-in
   planner, which titles an issue from its prompt, is kept from tripping
   it: a stand-in issue whose prompt names such a path is titled without
   it. Matching an issue's prose against every protected path was weighed
   and left out: only the never paths can't be made; the others are a
   Standard change away.

## Steps

1. **A project's notes reach its agents** (#930, `internal/agentrunner`,
   `docs/design.md`). Decision 4: the implement and review prompts tell the
   agent to read `docs/project-notes.md` when the repository has it, after
   `AGENTS.md`, and that `AGENTS.md` and the role guide win where they
   disagree; the path a constant in `internal/model` (`ProjectNotesPath`).
   A model table test pins that no catalog Standard in `dist/chart/values.yaml`
   protects it and that it is not under `AgentNeverPaths` or
   `PlatformProtectedPaths`. Done when `internal/agentrunner` tests show both
   prompts naming the file and the order, design.md's *A project's notes to
   its agents* no longer marked *Planned*, and `make check` passes.

2. **The Brief never files an issue no change may make** (#931, `internal/model`,
   `internal/model/planner.md`, `docs/design.md`). Decisions 1 to 3 and 5:
   `brief.md`'s *What no issue can change* section from
   `model.AgentNeverPaths`; the planner's guide rule (no such issue; answer
   the ask in `summary.md` and `choices.md`; an agents' note goes in the
   notes file; a request is answered `skip` or taken as a notes-file
   issue); `CheckPlan` refusing an issue whose title or criteria name a
   never path; the stand-in planner's titles kept clear of them. Done when
   model table tests show a plan with an issue titled "Add a note to
   AGENTS.md" and one with a criterion naming `docs/agents/reviewer.md`
   refused with the issue and path named, a plan whose issue body (outside
   the criteria) says "as AGENTS.md says" accepted, a plan whose issue
   edits `docs/project-notes.md` accepted, `brief.md` holding the section
   with each never path, the guide's examples still passing the check,
   design.md's *A Brief files no issue that no change may make* no longer
   marked *Planned*, and `make check` passes. Depends on step 1 (the refusal and the guide name
   the notes file).

## Out of scope

- Moving work into a milestone left with no merged issue: design.md leaves
  that release to a person, and this plan stops the Brief from making one
  that way.
- A Standard protecting `docs/project-notes.md` later: step 1's test fails
  for the chart's catalog, which is the catalog the factory ships; a
  person's own Standard that protects it only makes it a Standard change.
