# Agent Factory Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Agents build high-quality Flutter tool apps end to end on theos, fenced to their Container, with CI, an agent reviewer and autonomous merges.

**Architecture:** Two new catalog kinds (Standard, Agent) and one work kind (Task). The operator scaffolds Standards as a layer, protects `main`, reports checks, and drives Tasks through Agent Sandbox pods running `theos-agent` (a Go runner around Claude Code). Gitea Actions runs each repository's `make check`.

**Tech Stack:** Go 1.26, kubebuilder v4, controller-runtime, envtest + Ginkgo, Gitea 1.27 + Actions (act_runner), Agent Sandbox v1.0.4 (`agents.x-k8s.io/v1beta1`), Flutter stable (cirruslabs image), very_good_analysis, Claude Code CLI.

**Spec:** [docs/plans/done/2026-09-30-agent-factory-design.md](2026-09-30-agent-factory-design.md)

## Global Constraints

- Layering from docs/architecture.md holds: `internal/model`, `internal/scaffold`, and the new `internal/agentrunner` import no Kubernetes, controller-runtime or `api/`; `make lint` enforces it (add `agentrunner` to depguard).
- New kinds are created with `kubebuilder create api --group c4 --version v1alpha1 --kind <Kind>`; Standard and Agent with `--controller=false`.
- Never edit generated files (`config/crd/bases`, `config/rbac/role.yaml`, `zz_generated.*`, `PROJECT`).
- Templates are pinned: every new template has a tag in `templates/versions.txt`.
- Tests write with merge patches (`patch(obj, func(){...})`), never `Update`.
- Commits: Conventional Commits, small, as JairJosafath, with the owner's address, ending with the Co-Authored-By line.
- The operator never branches on a Container type (spec decision 7).
- The sandbox pod mounts no ServiceAccount token (`automountServiceAccountToken: false`).
- Logging follows the Kubernetes message style (capital letter, no period, past tense for failures).

## Review Focus

- A Task created before its Container is Ready waits `Pending` and starts once the Container is Ready, never fails.
- Two Tasks on one Container: the second waits until the first is Merged or Failed.
- An agent that makes no change (empty diff) fails the round with a clear message, not an empty PR.
- A sandbox pod that dies without a termination message (OOM, deadline) fails the round with the pod's reason.
- Deleting a Task while its sandbox runs deletes the sandbox (ownerReference) and leaves the branch and PR alone.

---

## Milestone 7: Catalog v2

### Task 7.1: mobile-app type and the no-type-branching rule

**Files:** Modify `api/v1alpha1/shared_types.go`, `internal/model/model.go`; Test `internal/controller/typeneutral_test.go`.

- [ ] Add `mobile-app` to the `ContainerType` enum marker and a `ContainerTypeMobileApp` constant; `TypeMobileApp` in model.
- [ ] Write `TestOperatorIsTypeNeutral`: walk `internal/controller` and `internal/scaffold` non-test `.go` files and fail on any of `ContainerTypeStaticSite|ContainerTypeService|ContainerTypeJob|ContainerTypeWorker|ContainerTypeMobileApp|TypeStaticSite|TypeService|TypeJob|TypeWorker|TypeMobileApp` or the quoted literals `"static-site"`, `"service"`, `"job"`, `"worker"`, `"mobile-app"`.
- [ ] `make manifests generate test`; commit `feat(api): add the mobile-app Container type`.

### Task 7.2: Standard kind and standardRef

**Files:** `kubebuilder create api ... --kind Standard --resource --controller=false`; Modify `api/v1alpha1/standard_types.go`, `container_types.go`, `internal/model/model.go`, `internal/controller/container_resolve.go`, `container_scaffold.go`, `container_controller.go`, `project_controller.go` (adopt Standards like other catalog kinds); Tests `internal/model/model_test.go`, `internal/scaffold/scaffold_test.go`, `internal/controller/catalog_test.go`.

**Interfaces:**
- `StandardSpec{CatalogEntrySpec inline; SupportedFrameworks []string (MinItems=1, listType=set)}`
- `ContainerSpec.StandardRef *LocalRef`
- `model.SideStandard = "Standard"`; `model.CatalogEntry.SupportedFrameworks []string`
- `model.CheckStandard(framework string, standard *CatalogEntry) error` returning `*FitError{Standard, Framework}` with message `"<standard> does not support <framework>"`.
- scaffold `sideOrder`: Framework 0, Standard 1, Compute 2, Deployment 3, Connector 4.
- `scaffold.Input.Standard string`.

- [ ] Table tests: CheckStandard (nil standard ok; listed ok; unlisted error message exact).
- [ ] scaffold test: layer order puts Standard second; a Standard and Framework writing the same path fails with ConflictError naming both.
- [ ] envtest: Container with a Standard that doesn't list its Framework → Failed, ChoicesValid=False, message "strict does not support python-fastapi"; a fitting Standard → scaffoldedFrom has it second.
- [ ] Container reconciler watches Standards (waitingContainers); Project adopts them; RBAC marker adds `standards`.
- [ ] Commit `feat: add the Standard catalog kind`.

### Task 7.3: Agent kind

**Files:** `kubebuilder create api ... --kind Agent --resource --controller=false`; `api/v1alpha1/agent_types.go`; project adoption.

**Interfaces:** `AgentSpec{DisplayName, Description string; Image string (MinLength 1); CredentialsSecretRef *LocalRef; MaxRounds int32 (default 3, min 1, max 10); Env []corev1.EnvVar (optional, e.g. THEOS_AGENT_FAKE)}`.

- [ ] envtest: Agent is adopted by the Project (ownerReference) like catalog entries.
- [ ] Commit `feat(api): add the Agent catalog kind`.

## Milestone 8: Flutter templates and the Standard

### Task 8.1: flutter-app Framework template
- [ ] Generate with `flutter create --org io.factory.c4 --platforms android --project-name app` in the cirruslabs image; keep `android/`, `lib/`, `test/`, `pubspec.yaml`; templatize the application id (`io.factory.c4.{{snake .Project}}.{{snake .Container}}`), app label (`.DisplayName`), `README.md.tmpl`.
- [ ] Restructure `lib/` into `main.dart`, `src/app.dart`, `src/theme.dart`, `src/home/home_page.dart`; widget test at `test/src/home/home_page_test.dart`.
- [ ] `.theos/devcontainer.json` using the cirruslabs image; `.theos/README.md` per the template convention.
- [ ] Verify: render with a throwaway Go test harness (`scaffold.Build`) into a temp dir together with flutter-standard, then `make check` in the Flutter image passes.

### Task 8.2: flutter-standard Standard template
- [ ] From the style research: `AGENTS.md`, `docs/style/{principles,dart,flutter,mobile-quality}.md`, `analysis_options.yaml`, `.editorconfig`, `Makefile` (`check: fmt-check analyze test`; `fmt`, `build` = `flutter build apk --debug`), `.gitea/workflows/check.yaml` (`runs-on: flutter`, job name `check`, runs `make check` on push and pull_request).
- [ ] flutter-app's `pubspec.yaml` lists `very_good_analysis` in dev_dependencies (the Framework owns pubspec; the Standard's README states the requirement).

### Task 8.3: android-release Deployment template
- [ ] `.gitea/workflows/release.yaml`: on tag `v*`, decode `ANDROID_KEYSTORE_B64` secret, `flutter build apk --release`, create a Gitea release via API with the APK attached (curl, `GITEA_TOKEN` from `secrets.GITHUB_TOKEN`/`gitea.token`).
- [ ] `android/key.properties` wiring lives in flutter-app's `android/app/build.gradle.kts` (reads env when present, falls back to debug signing).

### Task 8.4: seed and versions
- [ ] Add the three templates to `templates/versions.txt` (v1.0.0), seed tags them; `config/samples/toolbox/` with factory.yaml (GitProvider, Framework, Standard, Deployment, Agents) and story.yaml (Project toolbox, SoftwareSystem pocket-tools with needs, Containers photo-editor, unit-converter).
- [ ] envtest suite's fakeGit loads the new templates automatically; add an envtest that scaffolds a mobile-app Container with all three and checks key paths exist.
- [ ] Commit per template.

## Milestone 9: CI and the quality contract

### Task 9.1: Gitea Actions and the runner
- [ ] Enable Actions in `config/gitea` (`GITEA__actions__ENABLED=true`).
- [ ] `hack/runner/Dockerfile`: FROM the Flutter image, add `act_runner` (pinned release binary), entrypoint registers with a token from Secret `gitea/runner-token` (seed creates it with `POST /admin/runners/registration-token`) and runs `daemon` with label `flutter:host`.
- [ ] `config/gitea/runner.yaml` Deployment; `make runner` builds and kind-loads the image.
- [ ] Seed: org-level Actions secret `ANDROID_KEYSTORE_B64` (+ `ANDROID_KEYSTORE_PASSWORD`) generated with keytool once.

### Task 9.2: branch protection and checks status
**Interfaces (gitprovider.Provider additions):**
- `ProtectBranch(ctx, repo Repository, branch string, statusContexts []string) error`
- `CombinedStatus(ctx, repo Repository, ref string) (CheckStatus, error)` with `CheckStatus{Commit, State, URL string}`
- [ ] Gitea impl + httptest-based unit tests; fake provider records protection and serves statuses.
- [ ] Scaffold calls ProtectBranch(main, ["check"]) after the push (idempotent; also on the "found an earlier commit" path).
- [ ] `ContainerStatus.Checks *CheckStatus`; Ready Containers requeue every 60s to refresh it; printer column `Checks`.
- [ ] envtest: Ready Container shows checks from the fake; protection recorded once.

## Milestone 10: The agent loop

### Task 10.1: gitprovider for agents
**Interfaces:**
- `EnsureAgentAccess(ctx, repo Repository, user string) (Credentials, error)` — creates bot user (admin API, random password), adds as write collaborator, returns a token (recreated if missing).
- `OpenPullRequest(ctx, repo, head, base, title, body string) (PullRequest, error)` (returns existing open PR for head)
- `PullRequest{Number int; URL, HeadSHA, State string; Merged bool}`; `GetPullRequest(ctx, repo, number)`
- `CommentPullRequest(ctx, repo, number int, body string) error`
- `MergePullRequest(ctx, repo, number int, message string) error` (merge style `squash`)
- [ ] httptest unit tests for each; seed token gains `write:admin`.

### Task 10.2: runner (internal/agentrunner + cmd/theos-agent)
**Interfaces:**
- `agentrunner.Job{Role string ("implement"|"review"); RepoURL, Branch, Base string; Goal, Feedback, Container, Project string; Username, Token string; Round int}` read by `JobFromEnv(getenv func(string) string) (Job, error)` from `THEOS_ROLE, THEOS_REPO_URL, THEOS_BRANCH, THEOS_BASE, THEOS_GOAL, THEOS_FEEDBACK, THEOS_CONTAINER, THEOS_PROJECT, THEOS_GIT_USERNAME, THEOS_GIT_TOKEN, THEOS_ROUND`.
- `agentrunner.Agent interface{ Run(ctx, dir, prompt string) (string, error) }`; `ClaudeAgent{Command []string}`; `FakeAgent` (implement: appends a line to `CHANGELOG.md`, review: approves).
- `agentrunner.Result{Role, Commit, Summary, Verdict string; Error string}` JSON ≤ 3.5 KB (summary truncated).
- `agentrunner.Run(ctx, job, agent, git GitOps) Result`; `ImplementPrompt(job) string`; `ReviewPrompt(job, diff string) string`; `ParseVerdict(output string) (verdict, summary string, err error)` (looks for a JSON object with `verdict` approve|changes).
- [ ] Table tests: prompts contain goal/feedback/rules pointer; ParseVerdict variants; Run against a local bare repo with FakeAgent (branch pushed, commit returned; empty diff → Error "the agent made no changes").
- [ ] `cmd/theos-agent`: writes `Result` to `/dev/termination-log` (path flag), exits 0 even on job errors (the result carries them).
- [ ] `hack/agent/Dockerfile`: FROM Flutter image + Node + `@anthropic-ai/claude-code` + theos-agent; `make agent-image`.

### Task 10.3: Task kind and reconciler
`kubebuilder create api --group c4 --version v1alpha1 --kind Task --resource --controller`.

**Interfaces:**
- `TaskSpec{ContainerRef LocalRef (immutable); AgentRef LocalRef (immutable); Goal string (1..4000)}`
- `TaskStatus{Phase TaskPhase; Round int32; Branch, PullRequestURL string; PullRequest int32; Checks *CheckStatus; Review *TaskReview{Verdict, Summary}; Sandbox string; Conditions}`; phases `Pending, Implementing, Checking, Reviewing, Merged, Failed`.
- `model.NextStep(TaskFacts) Step` — pure state machine. `TaskFacts{Phase; Round, MaxRounds int; ContainerReady, OtherTaskActive bool; SandboxDone bool; SandboxResult *RunResult; ChecksState string; ReviewVerdict string; PRMerged bool}`; `Step{Action (Wait|StartImplement|StartReview|OpenPR|Merge|Retry|Fail); Phase; Feedback, Message string}`.
- [ ] Table tests for NextStep covering every Review Focus line.
- [ ] Reconciler: adopt under Container; ensure agent access Secret `<container>-agent-git` (owned by Container); create `agents.x-k8s.io/v1beta1 Sandbox` (unstructured) named `<task>-r<round>-<role>`, owned by the Task, pod: image, env from job + Secret refs, `restartPolicy: Never`, `automountServiceAccountToken: false`, label `theos.factory.io/sandbox=true`, `shutdownTime` now+45m; read the pod's termination message (list pods by the Sandbox's `status.selector`); open PR; poll checks via CombinedStatus(head); start reviewer; comment review on the PR; merge.
- [ ] envtest installs Agent Sandbox CRDs from `test/crds/agent-sandbox/` (copied from release v1.0.4 `sandbox.yaml`, CRDs only); the test plays the pod (creates a Pod matching the selector with a terminated container status + message).
- [ ] NetworkPolicy `c4-agent-sandbox` applied per project namespace by the Project reconciler (egress: DNS, gitea namespace, 0.0.0.0/0:443 except cluster CIDRs 10.0.0.0/8).

### Task 10.4: test environment
- [ ] `make agent-sandbox` installs Agent Sandbox v1.0.4 into Kind; `make claude-secret CLAUDE_TOKEN=...` creates Secret `claude-credentials` in the project namespace (token from the variable, never echoed).
- [ ] operator RBAC for sandboxes, pods (get/list/watch), networkpolicies.

## Milestone 11: Prove it

- [ ] e2e: toolbox sample scaffolds Flutter Containers, CI green on main, Task with fake Agent merges.
- [ ] Real run: Tasks with the Claude Agent build photo-editor and unit-converter; tag v0.1.0 → APK in the Gitea release. Record findings in `docs/results-v2.md` and fix the platform where it hurt.

## Milestone 12: Docs

- [ ] docs/design.md and docs/architecture.md describe v2; new design board `docs/images/design-board-v2.png` (HTML rendered headless); screenshots in `docs/screenshots/v2-*`.
