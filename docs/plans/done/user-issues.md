# Issues from users, and a factory without a product

**Goal:** This repository is about developing theos and nothing else.
Products are built on other devices that run theos as a Helm release, and
those devices report their problems here as issues. Every issue says at a
glance whether it came from developing the factory or from a device using it,
and which device.

**Why:** Until now the factory was developed on the same device that built its
products (webshop, then toolbox, then CEL Lens). Their samples, backlogs,
release backups, fixtures and run reports sit in the repo, the evaluator
judges the factory by how the current product's run went, and every issue is
authored by `JairJosafath`, so nothing says where it came from.

## The rules

### Two sources of issues

- Every issue carries exactly one source label:
  - `source/dev`: made while developing theos (by a person, an agent
    session or the relay).
  - `source/user`: reported from a device that runs the Helm release.
- An issue with `source/user` also carries exactly one `user/<name>` label
  naming the device, e.g. `user/laptop-2`. `<name>` is the reported name in
  lower case, with every run of characters outside `[a-z0-9]` replaced by `-`,
  trimmed of `-` and cut to 40 characters. All `user/*` labels share one color.
- `source/evaluator` goes away with the evaluator (below). GitHub's ten default
  labels (`bug`, `enhancement`, …) are unused and are deleted.
- `hack/lib/github.sh` stays the one place that defines the label set; the
  `labels` subcommand creates `source/dev` and `source/user`. `user/*` labels
  are created on demand by intake.

### How a device reports

A device needs no checkout of this repo. It files an issue on
`JairJosafath/theos` in one of two ways:

- the issue form **Report from a device** (`.github/ISSUE_TEMPLATE/user-report.yml`)
  with the fields *Reported by* (required), *theos version*, *What
  happened*, *What you expected* and *Logs*; the form applies `source/user`, or
- `gh issue create -R JairJosafath/theos --title … --body-file …` with a
  body in the form's layout. The one required part is the heading and the
  device name under it:

  ```
  ### Reported by

  laptop-2
  ```

The README's "Report an issue" section shows both, with a body to copy.

### Intake

`.github/workflows/issue-intake.yml` runs on `issues: [opened, edited]` and
calls `hack/lib/github.sh intake <N>` with the workflow's token. The workflow
passes only the issue number; the script reads the issue with `gh`, so no
issue text is ever expanded into the shell.

`intake N`:

1. Does nothing unless the issue's author association is `OWNER`, `MEMBER` or
   `COLLABORATOR`, so a stranger cannot create labels or move issues.
2. Reads the first line of text under `### Reported by`. Empty or
   `_No response_` means no reporter.
3. With a reporter: creates `user/<name>` if it is missing, makes it the
   issue's only `user/*` label, adds `source/user` and removes `source/dev`.
4. Without a reporter and without any `source/*` label: adds `source/dev`.
5. Without any `status/*` label: adds `status/proposed`.

Running it twice changes nothing the second time. Its cases are tested in
`hack/lib/github_test.sh`, like the other subcommands.

`github.sh create` adds `source/dev` unless the labels it is given include a
`source/*` label, so agent sessions and the relay file `source/dev` issues
without being told.

### The queue does not change

User reports land as `status/proposed`; the owner readies them like any other
issue. `pick`, `take`, claims and the relay ignore `source/*` and `user/*`.
Find a device's reports with `label:user/laptop-2`, all user reports with
`label:source/user`.

### What leaves the repository

Before anything is removed, the last commit of main that still holds it is
tagged `archive/product-coupled` and the tag is pushed; everything below is one
`git checkout` away.

- **CEL Lens and toolbox:** `config/samples/cel-lens/`,
  `config/samples/toolbox/`, `backups/`, `hack/backup-releases.sh`,
  `hack/backlog/`, the Makefile and testenv targets `cel-lens`,
  `cel-lens-backlog`, `toolbox` and `backup-releases`, `CLAUDE_NAMESPACE ?=
  toolbox`, the toolbox and cel-lens entries in `hack/seed/job.yaml` and
  `hack/seed/rbac.yaml`, the CEL Lens libraries in
  `hack/toolchain-go/warm.go.txt`, `ui/src/demo/celLensFixture.ts` and the
  toolbox apps in `ui/src/demo/factoryFixture.ts`.
- **webshop stays** as the one sample. `test/e2e/agents_test.go` and `make e2e`
  stop depending on toolbox: the agent loop it checks needs a Standard with CI,
  which webshop lacks, so it runs against a minimal Flutter fixture project
  `apps` under `test/e2e/testdata/apps/` (one Container and the stand-in Task).
- **The evaluator:** `.claude/skills/factory-evaluator/`, `hack/evaluate.sh`,
  and the `evaluate` and `backup-releases` steps in `hack/relay.sh`. The relay
  itself stays.
- **The template catalog stays:** it is what users build from. The CEL examples
  in `templates/go-standard/docs/style/go.md` and
  `templates/ts-standard/docs/style/vscode.md` become neutral ones.
- **`factory-ops` stays** as the skill for driving the factory in the Kind
  cluster, without its cel-lens references.
- **Docs:** `docs/results-v2.md`, `docs/results-v4.md`, their `v2-*` and `v4-*`
  screenshots, `docs/images/design-board.png` and `design-board-v2.*`, and
  screenshots nothing links to. Finished plans and the specs in
  `docs/superpowers/specs/` move to `docs/plans/done/`; `docs/plans/` keeps
  only plans not yet done (`design-team.md`, this one).

### Docs that change

- `docs/design.md`: drops "MVP v1 Plan" from its title, the dated timeline and
  the MVP plan; its toolbox and CEL Lens examples become webshop ones or go;
  and it gains a section *Distribution and issues*: the factory ships as a
  Helm release, devices build products with it, and their problems come back
  as `source/user` issues by the rules above.
- `docs/results.md` (v1, webshop) and `docs/results-v3.md` (monitoring) stay
  as evidence.
- `AGENTS.md`: drops the six-milestones rule, the evaluator row, "Product work
  stays in Gitea with the Crews" and `devcontainer up`; states the source rule
  in one line.
- `README.md`: status beyond MVP v1, and a *Report an issue* section.
- `docs/dev/structure.md`, `docs/dev/testenv.md`, the `handoff` skill: no
  product samples, backups or evaluator; follow-ups are `source/dev`.

### Existing issues

- Every open issue gets `source/dev`.
- #16 (Factory evaluations) is closed: the evaluator is gone.
- #80 (verify a Standard update reaches cel-language-server) is closed as
  product-specific; the archive tag keeps its context.
- Closed issues lose their `status/*` and `priority/*` labels.

### Not in this plan

The Helm chart, publishing images and the chart, a release workflow, and
redrawing the two Excalidraw boards without product names. They are filed as
`source/dev`, `status/proposed` issues and get a plan of their own. Until they
land, the README's install path stays the Kind one.

## Done when

- `gh label list` shows `source/dev` and `source/user`, and neither
  `source/evaluator` nor GitHub's defaults.
- A test issue filed with `gh issue create` and a `### Reported by` /
  `test-device` body gets `source/user`, `user/test-device` and
  `status/proposed` within a minute; it is then closed and its label deleted.
- Every open issue has exactly one `source/*` label.
- `git grep -il 'cel-lens\|cel lens\|toolbox'` finds only files under
  `docs/plans/` and the two boards in `docs/images/` (redrawn in a follow-up
  issue; editing Excalidraw text by hand breaks their layout). Go and UI tests
  that used `toolbox` or `cel-lens` as fixture names use neutral ones.
- `make check` passes, and `make e2e` passes in the Kind cluster.
- The tag `archive/product-coupled` is on GitHub.
