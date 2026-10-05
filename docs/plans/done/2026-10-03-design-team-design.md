# C4 Factory v5: a design team per Container

Oct 3, 2026 · asked for by @Jeremy Jo., designed together with Claude in one
session. Every decision is in the table below so it can be overruled.

## Goal

The factory's products work, but nobody owns their design. A person or a Claude
session writes every issue, and the implementer and reviewer only check that its
acceptance criteria are met. CEL Lens shows the gap: its hover works, yet shows far
too much text, and nobody looked at VS Code's guidelines or at how popular
extensions do hovers.

v5 adds the design stage, the first of three (design, implementation, quality
control); the last one comes later.

1. **Ideas in, ready issues out.** A person files a short idea; a designer Agent
   researches it and turns it into ready issues, so the c4admin side no longer
   does all the design thinking.
2. **The factory picks its own experts.** When a repository gets its first idea,
   the designer proposes a small team of experts fitted to the project (for CEL
   Lens: VS Code DX, Kubernetes CEL, extension engineering), each defined by what
   it reads and checks, not by its title.
3. **Light, not overwhelming.** One extra Agent run per idea, one Agent wearing
   every expert's hat, no extra review rounds.

## What the research says

| Finding | Source | What v5 does with it |
| --- | --- | --- |
| About 42% of multi-agent failures are specification problems, 37% agents misaligned with each other | MAST, Cemri et al. 2025, <https://arxiv.org/abs/2503.13657> | Put the effort before `status/ready`, into the issue |
| Extra agents made sequential tasks 39-70% worse; independent agents amplified errors 17x | Google/MIT, "Towards a Science of Scaling Agent Systems", <https://arxiv.org/abs/2512.08296> | One designer wearing several hats, not one Agent per expert |
| Frameworks that work hand over documents, not chat: PRD, design, story files | MetaGPT <https://arxiv.org/abs/2308.00352>, BMAD, GitHub Spec Kit <https://github.com/github/spec-kit> | The designer's output is the issue itself, plus profile files |
| BMAD's UX expert works in planning, not on every story | BMAD Method | Experts shape the issue; the reviewer is unchanged |
| VS Code has no official rule on hover length; the norm lives in the top extensions: the type first, one sentence, more on demand, a setting to make it shorter | <https://code.visualstudio.com/api/language-extensions/programmatic-language-features>, rust-analyzer <https://rust-analyzer.github.io/book/configuration.html>, yaml-language-server <https://github.com/redhat-developer/yaml-language-server> | An expert's profile names its sources and its reference products, and the designer must cite them |

## Decisions

| # | Topic | Decision |
| --- | --- | --- |
| 1 | Shape of the experts | One designer Agent wears every expert's hat; no Agent per expert |
| 2 | Hand-off | A person files an idea labelled `status/design`; the designer turns it into `status/ready` issues with no person in between |
| 3 | Picking the team | The factory proposes the team at kickoff and uses it at once; a person can edit or veto it any time, with no gate |
| 4 | Team scope | One team per Container repository, matching one Crew per Container; the second Container of a system may reuse the first one's domain profile |
| 5 | Team size | At most 3 experts; at most 10 checks per profile |
| 6 | What makes an expert | Its sources (guidelines and specs, with links), its reference products (2-3 to compare against) and its checklist; the title alone counts for nothing |
| 7 | Which hats per idea | The designer decides from the idea and names the hats it wore in the brief; no labels for a person to set |
| 8 | Where profiles live | `docs/agents/team/<name>.md`, read from main like `implementer.md` and `reviewer.md` |
| 9 | Who writes the team | The designer at kickoff only, when the repository has no team, and only under `docs/agents/team/`; the operator merges it. After that only people change it |
| 10 | Gaps in the team | The designer names an uncovered area in the brief and may file one `status/proposed` issue, "Add an expert for X", in the Later milestone |
| 11 | Big ideas | Split into at most 5 issues in all, linked as dependencies; the idea's own issue becomes the first |
| 12 | Across Containers | A split-off issue may go to the repository of a Container this one has a Relationship with (the hover text is the server's, the setting the extension's) |
| 13 | Original text | The rewritten issue keeps the person's idea under "Original idea" |
| 14 | Output channel | The designer pushes a `design/<issue>` branch that is read, never merged, and at kickoff a `team/<issue>` branch that the operator merges; the termination message (4 KB) only carries a short summary |
| 15 | Task kind | A Task gets `spec.kind`, `Implement` (default) or `Design`, immutable; no new kind of resource |
| 16 | Crew role | The Crew gets an optional `spec.designer`; without it, `status/design` issues are left alone |
| 17 | Order and capacity | Design issues follow the same order and release rules as ready issues, don't wait for dependencies, and count towards `parallel` |
| 18 | Kickoff races | While a repository has no team, the Crew runs one design Task at a time |
| 19 | Failure | Same as implement Tasks: usage limits wait, up to 3 tries, then `status/needs-human` with the reason |
| 20 | Questions | The designer hands an idea back (`status/needs-human`) only for a decision a person must make, such as product direction; never for something research can answer |
| 21 | Later stages | The reviewer and implementer are unchanged; giving them the profiles' checklists belongs to quality control, later |
| 22 | Existing repositories | Scaffold once still holds: Standards ship `docs/agents/designer.md` for new repositories; a person adds it to existing ones, such as CEL Lens |

## The flow of an idea

```
idea ─► status/design ─► designer ─► status/ready ─► implementer ─► reviewer ─► merge
                        (kickoff first if the repository has no team)
```

1. A person files a short issue labelled `status/design` in a Container's
   repository. One line is enough: "Hovers show too much text."
2. The Crew picks it up, labels it `status/in-progress` and starts a Task with
   `kind: Design` and the Crew's designer as its Agent.
3. If main has no `docs/agents/team/`, the run starts with the kickoff (below).
4. The designer reads the idea, the C4 context it gets today (the system's needs
   and KPIs, its users, the Container's Relationships, read-only clones of related
   repositories), the team's profiles and the code. It looks up each worn hat's
   sources and compares two or three reference products, then writes the issue.
5. The operator checks the output and applies it: it rewrites the issue, files
   the split-off issues with their dependencies, merges the kickoff's team, and
   labels the idea `status/ready`, or `status/needs-human` with the reason.
6. The Crew implements and reviews the ready issues as it does today.

### The rewritten issue

The Standard's issue template, plus a Design brief:

```markdown
## Context
## Goal
## Acceptance criteria
- [ ] ...          (checkable by a test or a screenshot)
## Out of scope
## Design brief
Hats: VS Code DX, Kubernetes CEL
### How others do it
- Red Hat YAML: <link> ... 
### Guidelines followed
- <link> ...
### Notes per hat
## Dependencies
## Original idea
> Hovers show too much text.
```

Limits that matter for quality, such as the hover's shape, become acceptance
criteria, so the reviewer already checks them.

## The kickoff and the team

The first design run in a repository with no team proposes one. The designer
reads the idea and the C4 model (users, needs, KPIs, the Container's type and
Relationships) and the teams of related repositories, then writes at most three
profiles:

```markdown
# VS Code DX

Why this project needs it: CEL Lens lives in the editor; every feature is a VS Code surface.

## Sources
- VS Code UX guidelines: https://code.visualstudio.com/api/ux-guidelines/overview
- LSP 3.17 specification: https://microsoft.github.io/language-server-protocol/...

## References
- Red Hat YAML (yaml-language-server): schema hovers on Kubernetes YAML
- rust-analyzer: configurable, capped hovers
- gopls: hoverKind from one line to full docs

## Checklist
- The right surface: problems as diagnostics, identity in hover, choices in completion or Quick Pick, tiny facts as inlay hints, actions as CodeLens or commands
- Native surfaces first, a webview last
- Theme colors and codicons only, nothing hard-coded
- Hover: the type on line 1, one sentence, more on demand, a setting to shorten it
- ...
```

The operator comments the team on the idea issue. A person edits a profile on
main (Gitea's web editor works) or deletes it to veto the expert. A team stays
inside its platform: a web UI Container gets a web UX expert, a CLI one works from
<https://clig.dev>.

## Operator mechanics

- **Crew**: an optional `spec.designer` beside `implementer` and `reviewer`. It
  polls `status/design` with `status/ready`; `status/design` joins the exclusive
  `status/*` labels, and the operator keeps it in every repository it polls.
- **Task**: `spec.kind` (`Implement` or `Design`, immutable). A design Task runs
  Pending → Designing → Designed, or Failed. Its Agent is the designer; it has no
  reviewer, no pull request and no checks.
- **Runner**: a design role (`C4_ROLE=design`), `docs/agents/designer.md` in
  `roleGuides`, read from the base branch, and a design prompt that carries the
  team's profiles.
- **Output**: the designer pushes `design/<issue>` with `.design/issue.md` (the
  idea's issue) and `.design/issues/NN-<slug>.md` (the others; front matter:
  title, target repository, the pieces it depends on). The operator reads it and
  never merges it. At kickoff only, it also pushes `team/<issue>` whose one change
  is `docs/agents/team/*.md`.
- **Checks before applying**, as pure functions in `internal/model`: every issue
  passes `MissingAcceptanceCriteria`; at most 5 issues, 3 experts, 10 checks per
  profile, one proposed expert; team files only at kickoff; target repositories
  only the Container's own or related ones; no other paths. A failed check costs a
  try, with the reason as feedback.
- **Applying**: rewrite the issue, file the split-off issues (same milestone,
  `status/ready`, dependencies), open a pull request from `team/<issue>`, confirm
  with its files that it touches only `docs/agents/team/` and merge it as the
  operator merges Standard changes, comment the team, delete both branches, label
  the idea.
- **GitProvider** gains four calls: read the files under a path at a ref, edit an
  issue's body, delete a branch, and add a dependency on an issue in another
  repository (`AddIssueDependency` only knows one repository today).

## Testing

- **Table tests in `internal/model`**: Crew picking (design issues ordered and
  released like ready ones, no wait for dependencies, counted towards `parallel`,
  ignored without a designer, one at a time before kickoff); the design Task's
  steps; the output checks.
- **Envtest**: a design Task end to end against a fake GitProvider: branch read,
  issue rewritten, split-off issues filed with dependencies, team merged, branch
  deleted, labels set; a failed check retries with feedback.
- **Runner tests**: the design prompt carries the profiles and the C4 context.
- **`make e2e`** is manual-only for now and not a gate; the pull request says so.

## Done when

In the Kind cluster:

1. A person adds `docs/agents/designer.md` to the two CEL Lens repositories
   (decision 22) and names a designer Agent (Opus) on both Crews.
2. A person files "Hovers show too much text" as `status/design` in the CEL Lens
   server repository.
3. Kickoff: `docs/agents/team/` lands on main with at most three profiles, and the
   team is commented on the issue.
4. The issue becomes `status/ready` with a Design brief citing the VS Code
   guidelines and at least two reference extensions, and checkable criteria (the
   type on line 1, a one-sentence summary, a link to more, a setting defaulting to
   a summary). The setting becomes a linked issue in the extension repository.
5. The Crews implement and merge both; screenshots of the hover before and after
   are in `docs/screenshots/`.
6. The design run's tokens are recorded, so the evaluator can report the cost of
   design per idea.
7. `make check` is green.

## Later

- **Quality control**: the reviewer gets the team's checklists; screenshots as
  review evidence.
- **Teams per system**: one shared domain expert for all of a system's Containers,
  if copying profiles turns out to drift.
- **Ideas at system level**: filing an idea once for a whole system instead of in
  one Container's repository.
