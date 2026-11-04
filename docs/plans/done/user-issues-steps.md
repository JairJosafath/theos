# Issues from users, and a factory without a product: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Issues say whether they come from developing theos or from a
device using it (and which device), and the repo holds no product.

**Architecture:** Labels and intake live in `hack/lib/github.sh` (new
`intake` subcommand, tested in `hack/lib/github_test.sh`), called by a
workflow on `issues: [opened, edited]`; a GitHub issue form produces the body
layout intake reads. The cleanup is five pull requests, one GitHub issue each,
landed in order.

**Tech Stack:** bash, `gh`, `jq` (`.dev/bin/jq`; put `.dev/bin` on `PATH`),
GitHub Actions and issue forms, Go/Ginkgo e2e, the React UI under `ui/`.

**Spec:** [docs/plans/done/user-issues.md](user-issues.md). Read it first.

## Global Constraints

- Labels, verbatim (name|color|description):
  - `source/dev|bfd4f2|Made while developing theos`
  - `source/user|f9d0c4|Reported from a device that runs theos; user/<name> names it`
  - `user/<name>`: color `fef2c0`, description `Reported by the device <name>`
- Reporter heading: a line exactly `### Reported by` (after stripping `\r` and
  trailing spaces), outside fenced code blocks.
- Slug: lower case; each run of chars outside `[a-z0-9]` becomes `-`; trim `-`;
  cut to 40; trim a trailing `-` again.
- Trusted author associations: `OWNER`, `MEMBER`, `COLLABORATOR`.
- Archive tag: `archive/product-coupled` on `1437554` (main before any removal).
- New neutral fixture names: project `apps` (was `toolbox`), `tools` (was
  `cel-lens`) where a Go or UI test needs a second project.
- Every commit ends with `Agent: <name>` and the Co-Authored-By trailer; every
  PR body says `Closes #N` and is merged with a merge commit once checks are green.
- On this device check `free -g` before `make check`, image builds or
  `make e2e`; never run two at once.

## Review Focus

- A dev issue that *quotes* the reporter format inside a ``` fence must stay
  `source/dev` (Task 1 test "fenced heading").
- Form bodies and `gh` bodies with CRLF line endings or trailing spaces still
  parse (Task 1 test "crlf").
- A reporter name that slugs to nothing (`!!!`) adds no `user/*` label and is
  treated as no reporter; a 60-char name is cut to 40 without a trailing `-`
  (Task 1 tests "slug empty", "slug long").
- Editing an issue to change its reporter leaves exactly one `user/*` label
  (Task 1 test "reporter changed").
- Intake run twice, or by a stranger's issue, makes no change (Task 1 tests
  "idempotent", "stranger").

---

## Per-issue procedure (Tasks 1, 3, 4, 5, 6)

Each task is one GitHub issue filed in Task 0. Its subagent:

1. `hack/pull.sh`, then `hack/lib/github.sh take N` (prints the agent name and
   branch `<type>/<N>-<slug>`; switch to that branch from up-to-date main).
2. Does the task's steps, committing as it goes, pushing after every commit.
3. Runs the task's checks plus `make check`; pastes failing output in the PR
   if any check is skipped or fails.
4. Opens the PR (`Closes #N`, agent name), waits for green checks
   (`gh pr checks --watch`), merges with `gh pr merge --merge`.

---

### Task 0: Kick-off (controller, no subagent)

- [ ] **Step 1:** `git tag archive/product-coupled 1437554 && git push origin archive/product-coupled`
- [ ] **Step 2:** Create the two source labels now, with the Global Constraints values, so issues can carry them before Task 1 lands:
  `gh label create source/dev -R JairJosafath/theos -c bfd4f2 -d 'Made while developing theos'` (and the same for `source/user`).
- [ ] **Step 3:** File the five issues with `hack/lib/github.sh create TITLE FILE status/proposed source/dev kind/<kind>`; each body links this plan's task and, from Task 3 on, says `Depends on #<previous>`:
  - Task 1: "Label issues by source and name the reporting device" (`kind/feature`)
  - Task 3: "Remove the evaluator and release backups" (`kind/chore`)
  - Task 4: "Remove the CEL Lens and toolbox products" (`kind/chore`, `needs/cluster`)
  - Task 5: "The UI demo shows webshop only" (`kind/chore`)
  - Task 6: "Docs describe a factory without a product" (`kind/docs`)
- [ ] **Step 4:** Push `docs/user-issues-plan`, open a PR with the spec and this plan, merge it.

### Task 1: Labels, intake, issue form

**Files:**
- Modify: `hack/lib/github.sh` (`GH_LABELS` :83-97, `gh_create` :596-612, header usage :2-55, dispatch :614-638)
- Modify: `hack/lib/github_test.sh` (stub `gh_api` :19-42)
- Create: `.github/ISSUE_TEMPLATE/user-report.yml`, `.github/workflows/issue-intake.yml`
- Modify: `README.md` (new section *Report an issue*), `.claude/skills/handoff/SKILL.md:26`

**Interfaces:**
- Produces: `gh_reporter` (body on stdin → prints the raw reporter name or nothing), `gh_user_slug NAME` (prints slug or nothing), `gh_intake N` (subcommand `intake N`), `GH_USER_COLOR=fef2c0`.

- [ ] **Step 1: Extend the test stub.** In `github_test.sh`'s `gh_api`: append every call whose args contain `--method POST` or `--method DELETE` to `$T/calls` as `METHOD URL`, followed by ` ` and `jq -c .` of stdin when `--input -` is given. Add cases before `*)`: `*/issues/9[0-9]) cat "$T/issue-${url##*/}.json"`; `*/labels/user%2Flaptop-2) echo '{}'`; `*/labels/user%2F*) return 1`. Add a toggle so the open-issues call in `gh_create` can return nothing, and make `POST */issues` print `99`.

- [ ] **Step 2: Write the failing tests** (after the existing ones, before `exit $fail`), with `R=repos/JairJosafath/theos` and a helper `issue N ASSOC LABELS BODY` that writes `$T/issue-N.json` as `{number, author_association, body, labels:[{name}]}` and empties `$T/calls`:

  ```
  form=$'### Reported by\n\nlaptop-2\n\n### What happened\n\nCrew stalls'
  check "reporter form" "$(gh_reporter <<<"$form")" laptop-2
  check "reporter crlf" "$(gh_reporter <<<$'### Reported by  \r\n\r\nlaptop-2\r\n')" laptop-2
  check "reporter none" "$(gh_reporter <<<$'### Reported by\n\n_No response_\n')" ""
  check "reporter next heading" "$(gh_reporter <<<$'### Reported by\n\n### What happened\nx')" ""
  check "fenced heading" "$(gh_reporter <<<$'```\n### Reported by\n\nlaptop-2\n```')" ""
  check "slug" "$(gh_user_slug '  WSL_box.2 ')" wsl-box-2
  check "slug empty" "$(gh_user_slug '!!!')" ""
  check "slug long" "$(gh_user_slug "$(printf 'a%.0s' {1..39})-bbbb")" "$(printf 'a%.0s' {1..39})"
  ```
  Intake cases (each asserts `$T/calls` exactly):
  - "new report": `issue 90 OWNER '' "$form"` → one line `POST $R/issues/90/labels {"labels":["user/laptop-2","source/user","status/proposed"]}` (the label exists, so no label create).
  - "new device": body names `laptop-3` → first `POST $R/labels` (any args), then `POST $R/issues/91/labels {"labels":["user/laptop-3","source/user","status/proposed"]}`.
  - "reporter changed": `issue 92 OWNER 'user/laptop-3 source/dev status/ready' "$form"` → `DELETE $R/issues/92/labels/user%2Flaptop-3`, `DELETE $R/issues/92/labels/source%2Fdev`, `POST $R/issues/92/labels {"labels":["user/laptop-2","source/user"]}`.
  - "dev issue": `issue 93 OWNER '' 'Plain text'` → `POST $R/issues/93/labels {"labels":["source/dev","status/proposed"]}`.
  - "idempotent": `issue 94 OWNER 'user/laptop-2 source/user status/proposed' "$form"` → no calls.
  - "stranger": `issue 95 NONE '' "$form"` → no calls, exit 0.
  - "create adds source/dev": `gh_create T <file> status/proposed kind/bug` posts labels `["status/proposed","kind/bug","source/dev"]`; with `source/user` given, no `source/dev`.

- [ ] **Step 3:** Run `PATH=$PWD/.dev/bin:$PATH hack/lib/github_test.sh`. Expected: the new checks print `FAIL` (functions undefined), the old ones `ok`.

- [ ] **Step 4: Implement** in `github.sh`: add the two `source/*` entries to `GH_LABELS`; `gh_reporter` (awk: toggle on lines starting with ```, match the heading outside fences, print the first non-blank line after it unless it starts with `### ` or is `_No response_`); `gh_user_slug`; `gh_intake N` (fetch `issues/N` without `--jq`, decide with local `jq`, removals as `DELETE .../labels/<name with / as %2F>`, additions as one `POST .../labels --input -` in the order user, source/user, source/dev, status/proposed, only when non-empty; a missing `user/*` label is created with `GH_USER_COLOR`); `gh_create` appends `source/dev` when no argument starts with `source/`; `intake) gh_intake "$@" ;;` in the dispatch and a usage line in the header.

- [ ] **Step 5:** Run the tests again. Expected: every line `ok`, exit 0.

- [ ] **Step 6: Issue form** `.github/ISSUE_TEMPLATE/user-report.yml`: `name: Report from a device`, `labels: [source/user]`, body fields in order: `input` id `reporter` label `Reported by` (required, description "The device's name, e.g. laptop-2"), `input` id `version` label `theos version`, `textarea` id `happened` label `What happened` (required), `textarea` id `expected` label `What you expected`, `textarea` id `logs` label `Logs` (`render: shell`).

- [ ] **Step 7: Workflow** `.github/workflows/issue-intake.yml`: on `issues: types [opened, edited]`; `permissions: {issues: write, contents: read}`; one job on `ubuntu-latest`: `actions/checkout@v4`, then `run: hack/lib/github.sh intake "$ISSUE"` with env `GH_TOKEN: ${{ github.token }}`, `ISSUE: ${{ github.event.issue.number }}`, `THEOS_GITHUB_REPO: ${{ github.repository }}`. No `${{ }}` inside `run:`.

- [ ] **Step 8: Docs.** README *Report an issue*: the form link (`https://github.com/JairJosafath/theos/issues/new?template=user-report.yml`) and the `gh issue create -R JairJosafath/theos --title … --label source/user --body-file report.md` route with a `report.md` to copy in the form's layout. `handoff/SKILL.md:26`: replace the `source/evaluator` line with "`source/dev` or `source/user` says where an issue came from; `create` adds `source/dev`; `user/<name>` names the reporting device."

- [ ] **Step 9:** `actionlint .github/workflows/issue-intake.yml` if available (else note it), `make check`, PR, merge.

### Task 2: Apply the labels on GitHub (controller, after Task 1 merges)

- [ ] **Step 1:** For every open issue without a `source/*` label: `hack/lib/github.sh label N source/dev`.
- [ ] **Step 2:** Close #80 with a comment: "Product-specific (CEL Lens); see tag archive/product-coupled." (`hack/lib/github.sh close 80 -`).
- [ ] **Step 3:** Delete the ten GitHub default labels: `bug enhancement documentation duplicate 'good first issue' 'help wanted' invalid question wontfix accessibility` (`gh label delete NAME --yes`).
- [ ] **Step 4:** On every closed issue, remove `status/*` and `priority/*` labels (`gh issue edit N --remove-label …`).
- [ ] **Step 5: Live check.** File `gh issue create -R JairJosafath/theos --title 'Intake check' --body $'### Reported by\n\ntest-device\n\n### What happened\n\ncheck'`; within a minute `gh issue view N --json labels` shows `user/test-device`, `source/user`, `status/proposed`. Close it, `gh label delete user/test-device --yes`. Save the output to `docs/screenshots/user-issues-intake.txt` (committed in Task 6).

### Task 3: Remove the evaluator and release backups

**Files:**
- Delete: `.claude/skills/factory-evaluator/`, `hack/evaluate.sh`, `hack/backup-releases.sh`, `backups/`
- Modify: `hack/relay.sh` (header :16-21, `EVALUATIONS_TITLE`/`EVALUATE_EVERY` :56-57, `evaluate()` :199-248 and its call :464-468, `backup()` :303-321 and calls :479, :520), `hack/relay-watch.sh` (:12-14, :103-105: drop the idle start that exists only for the evaluator), `hack/heavy-guard.sh:8`, `Makefile` (`PROJECT ?=` :268, `PUSH ?=` :269, `backup-releases` :271-273), `hack/lib/github.sh` (drop `source/evaluator` from `GH_LABELS`), `.claude/skills/factory-ops/SKILL.md` (:51-52), `AGENTS.md:21`, `docs/dev/testenv.md` (:84-86, :98, :125)

- [ ] **Step 1:** Delete and edit the files above; `hack/relay.sh` keeps picking and working issues exactly as before.
- [ ] **Step 2:** `bash -n hack/relay.sh hack/relay-watch.sh hack/heavy-guard.sh`; `hack/relay.sh --help` (or its dry-run mode, :479) runs without error; `git grep -n 'evaluat\|backup-releases\|backups/'` shows only `docs/plans/` and docs Task 6 rewrites (list them in the PR).
- [ ] **Step 3:** `make check`, PR, merge. Then close #16 ("The evaluator is removed") and `gh label delete source/evaluator --yes`.

### Task 4: Remove the CEL Lens and toolbox products

**Files:**
- Delete: `config/samples/cel-lens/`, `config/samples/toolbox/` (after Step 1), `hack/backlog/`
- Create: `test/e2e/testdata/apps/` from `config/samples/toolbox/{namespace,project,story,factory,kustomization}.yaml` and `tasks/stand-in.yaml`
- Modify: `test/e2e/agents_test.go` (:32-105), `Makefile:80` (e2e prerequisites), `hack/testenv.mk` (:55 seed namespaces, :182 `CLAUDE_NAMESPACE`, :192-202 `cel-lens*`, :204-210 help text, :212-214 `toolbox`), `hack/seed/job.yaml:36`, `hack/seed/rbac.yaml` (:66-121), `hack/toolchain-go/warm.go.txt`, `hack/toolchain-go/Dockerfile` (:2-3, :29-32), `hack/render/main.go:22`, `hack/doctor.sh:123`, `.claude/skills/factory-ops/SKILL.md:34`, `internal/gitprovider/gitea_agents.go:432`, Go tests listed below

- [ ] **Step 1: Fixture.** Move the toolbox files listed above into `test/e2e/testdata/apps/`, rename project and namespace `toolbox` → `apps`, keep one Container (`image-shrink`, on `flutter-app`, `flutter-standard`, `android-release`) and the stand-in Task, and make the story text neutral ("A sample Flutter app for the e2e agent loop"). Delete the other toolbox Task YAMLs.
- [ ] **Step 2: e2e.** In `agents_test.go`: `const apps = "apps"`, the fixture path, and the Container count 6 → 1; every other assertion unchanged. In the Makefile `e2e` rule replace `toolbox` with a new testenv target `e2e-apps` that runs `kubectl apply -k test/e2e/testdata/apps`.
- [ ] **Step 3: Test environment.** `seed` namespaces `webshop apps`; `job.yaml` `PROJECTS` `webshop apps`; `rbac.yaml` toolbox block renamed to `apps`, cel-lens block deleted; `CLAUDE_NAMESPACE ?= apps`; delete `cel-lens`, `cel-lens-backlog`, `toolbox` targets; `claude-secret-copy` help text uses `webshop`/`apps`; `doctor.sh` hint says `make e2e-apps`; `render/main.go` example project `webshop`; drop `make cel-lens-backlog` from `.claude/skills/factory-ops/SKILL.md:34`.
- [ ] **Step 4: toolchain-go.** `warm.go.txt` warms `k8s.io/client-go` and `sigs.k8s.io/controller-runtime` (what the go templates' projects import) instead of the CEL libraries; Dockerfile comments say "the Kubernetes libraries Go projects commonly build on".
- [ ] **Step 5: Go test fixture names.** In `internal/agentrunner/{agentrunner,retry}_test.go`, `internal/controller/task_{issue,sandbox}_unit_test.go`, `internal/gitprovider/gitea_agents_test.go`, `internal/model/{context,task}_test.go`, `internal/scaffold/update_test.go` and the comment at `gitea_agents.go:432`: `toolbox` → `apps`, `cel-lens` → `tools`, consistently within each file. `make test` must pass unchanged otherwise.
- [ ] **Step 6:** `git grep -il 'cel-lens\|cel lens\|toolbox' -- . ':!docs' ':!ui'` prints nothing; `make check`.
- [ ] **Step 7: Cluster.** `free -g` (needs the headroom `docs/dev/testenv.md` states), then `make e2e`. If the device cannot run it, label the issue `needs/cluster`, say so in the PR with the output, and stop before merging.
- [ ] **Step 8:** PR, merge.

### Task 5: The UI demo shows webshop only

**Files:**
- Delete: `ui/src/demo/celLensFixture.ts`
- Modify: `ui/src/demo/factoryFixture.ts` (toolbox :1-4, :50-68, :74-181, :199-204, :330; CEL Lens merges :49, :70), `ui/src/demo/demoSource.ts` (:8, :41, :72), `ui/src/demo/fixture.ts:5-6`, `ui/src/model/story.test.ts` (:2, :339-353), `ui/src/model/factory.test.ts` (:2, :662-702, other literals), `ui/test/health.test.mjs`, `ui/README.md`

- [ ] **Step 1:** Remove the CEL Lens fixture and the toolbox parts; the demo's story comes from `fixture.ts` (webshop) and its factory from the webshop Task (:182-195). If the Crews view would be empty, add one webshop Crew (implementer + reviewer agents) to `factoryFixture.ts`.
- [ ] **Step 2:** Retarget the tests: `story.test.ts` drops "the CEL Lens demo" cases unless they cover a code path no other test does (then point them at the webshop fixture); `factory.test.ts` "the demo" expects the webshop Crews/Tasks; remaining `toolbox`/`cel-lens` literals become `apps`/`tools`.
- [ ] **Step 3:** `make ui-test` and `make ui-build` pass; `git grep -il 'cel-lens\|cel lens\|toolbox' -- ui` prints nothing.
- [ ] **Step 4:** `make check`, PR, merge.

### Task 6: Docs describe a factory without a product

**Files:**
- Delete: `docs/results-v2.md`, `docs/results-v4.md`, `docs/screenshots/v2-*`, `docs/screenshots/v4-*`, `docs/images/design-board.png`, `docs/images/design-board-v2.*`, and the unreferenced screenshots `go-cache-local-timing.txt`, `parallel-agents-heartbeat.md`, `parallel-agents-step4.md`, `relay-loop.md`, `v3-api-stalls.txt`, `v3-factory-demo.png`, `v3-factory-demo-dark.png`, `v3-task-history.txt`
- Move: every `docs/plans/*.md` except `design-team.md`, `user-issues.md`, `user-issues-steps.md`, and all of `docs/superpowers/specs/`, into `docs/plans/done/` (fix links that point at them)
- Modify: `docs/design.md` (title :1, v2 section :176-215, v4 section :244-333, test environment :344-379, MVP plan :380-404, timeline :405-416), `AGENTS.md` (:21 if still there, :92-93, :119-120, :149), `README.md` (Status), `docs/dev/structure.md` (:25-26, :40), `docs/dev/testenv.md` (:17-21, :119-122, :139, :148-149, :160), `DEVELOPMENT.md` (drop the "From the dev container" table), `templates/go-standard/docs/style/go.md:49`, `templates/ts-standard/docs/style/vscode.md:44-60`
- Add: `docs/screenshots/user-issues-intake.txt` (from Task 2)

- [ ] **Step 1: design.md.** Title `# theos: Design`; delete the MVP plan and timeline sections; the test environment section keeps what is still true (Kind, webshop, `apps` fixture); v2 and v4 keep their rules with webshop examples or none (drop design-board-v2 and the Pocket Tools/CEL Lens sentences, and the go-cli/vscode-extension list at :330 becomes "the template catalog in `templates/`"); add *Distribution and issues* (Helm release on devices; the source, user and intake rules from the spec, in four or five sentences).
- [ ] **Step 2: AGENTS.md.** Drop the six-milestones bullet, "Product work stays in Gitea with the Crews." and the `devcontainer up` clause; add under *Working in Sessions*: "Every issue carries `source/dev` (developing the factory) or `source/user` with `user/<name>` (reported from a device running the Helm release)."
- [ ] **Step 3:** README status (what exists today, the Helm release as next), structure.md, testenv.md, DEVELOPMENT.md as listed; the two style guides get a neutral doc-comment example (`// Parse reads a config file and reports its errors.`) and a generic language-client example.
- [ ] **Step 4:** Deletes and moves; `git grep -n 'results-v[24]\|design-board\|superpowers/specs'` shows no dead links; check every relative link in changed `.md` files resolves (a small loop over `git grep -o '](\.\?\.\?/[^)]*)'`).
- [ ] **Step 5: Done-when sweep.** `git grep -il 'cel-lens\|cel lens\|toolbox'` lists only `docs/plans/` and `docs/images/how-it-works.*`, `docs/images/architecture.*`.
- [ ] **Step 6:** `make check`, PR, merge.
- [ ] **Step 7: Follow-ups** (`github.sh create … status/proposed source/dev kind/<kind>`): "Package the operator as a Helm chart" (feature), "Publish images and the chart from a release workflow" (feature, `Depends on` the first), "Redraw the how-it-works and architecture boards without product names" (docs).
- [ ] **Step 8:** Run the spec's *Done when* list; move `user-issues.md` and `user-issues-steps.md` into `docs/plans/done/` in a last small PR.
