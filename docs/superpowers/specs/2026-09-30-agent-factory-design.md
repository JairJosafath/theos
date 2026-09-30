# C4 Factory v2: the Agent Factory

Sep 30, 2026 · design agreed step by step with @Jeremy Jo., written up by Claude.

## Goal

Agents build complete, high-quality apps on C4 Factory when asked, and stay inside
their workspace while they do. The first proving ground is small mobile tool apps
that need no internet; the platform must stay open to other shapes, such as AWS
serverless functions, without a redesign.

Quality does not come from trusting the agent. It comes from what the platform
enforces: coding rules as lint configuration, a `make check` contract, CI that runs
it, a protected `main`, and an agent reviewer. Only green, reviewed work lands.

## Decisions

Settled in the design discussion on 30 September 2026.

| # | Topic | Decision |
| --- | --- | --- |
| 1 | Mobile stack | Flutter. One codebase, Android now, iOS open later. Strict format, analyzer and headless widget tests. |
| 2 | Done | CI builds a signed APK and attaches it to a Gitea release. No store publishing yet. |
| 3 | Coding rules | A new catalog kind, `Standard`: a layer next to Framework that owns lint and format configs, `AGENTS.md`, style digests, the `Makefile` check contract and CI. |
| 4 | Style guides | Studied and distilled. Rules a tool can enforce go into lint config; the rest into `AGENTS.md`. `AGENTS.md` links the full guides, and `docs/style/` holds offline digests. |
| 5 | Checks | Gitea Actions with a runner in the cluster. `main` is protected: work lands only through a PR with passing checks. The Container shows the latest check on `main`. |
| 6 | App values | "Offline, no ads, no login" is not a platform rule. It is a `need` on the sample's SoftwareSystem. The platform stays neutral. |
| 7 | Types | Add `mobile-app`. The operator never branches on a Container type; everything type-specific lives in the catalog. A test enforces it. `function` (serverless) can follow as one enum value plus templates. |
| 8 | Agent runtime | [Kubernetes Agent Sandbox](https://github.com/kubernetes-sigs/agent-sandbox) (OSS, SIG Apps, v1.0.x) runs agents. AX and Agent Substrate were spiked and are too young; see "Spike" below. |
| 9 | Work | A new kind, `Task`: a goal for a Container. The operator runs an agent in a sandbox; the agent works on a branch; a PR is opened. |
| 10 | Merging | Fully autonomous: an agent reviewer reviews, and the operator merges when checks pass and the reviewer approves. |

## The loop

```
Task (goal) ──► operator ──► Sandbox: implementer agent ──► branch pushed
                   │                                           │
                   │◄──────────── result (termination message) ┘
                   ├──► opens PR (Gitea) ──► CI runs make check
                   ├──► Sandbox: reviewer agent ──► verdict
                   │
                   ├── checks green + approve ──► merge ──► Task Succeeded
                   └── checks red or changes asked ──► next round (max rounds) ──► else Failed
```

## New and changed kinds

### Container: new type and Standard

```yaml
kind: Container
spec:
  type: mobile-app                 # new enum value
  frameworkRef: {name: flutter-app}
  standardRef: {name: flutter-standard}   # new, optional
  deploymentRef: {name: android-release}
status:
  checks:                          # new: the latest check on the default branch
    commit: 3f2c9ab
    state: success                 # pending | success | failure | error | ""
    url: http://gitea.../actions/runs/12
```

Layers render in the order Framework, **Standard**, Compute, Deployment, Connectors.
`scaffoldedFrom` includes the Standard.

### Standard (new catalog kind)

Same shape as Framework, plus the Frameworks it fits:

```yaml
kind: Standard
metadata: {name: flutter-standard}
spec:
  displayName: Flutter quality standard
  supportedTypes: [mobile-app]
  supportedFrameworks: [flutter-app]
  template: {url: http://gitea.../templates/flutter-standard, ref: v1.0.0}
```

A Container whose Standard does not list its Framework fails with
`ChoicesValid=False`, for example "flutter-standard does not support python-fastapi".

### Agent (new catalog kind)

Which agent does the work, and the image it runs in:

```yaml
kind: Agent
metadata: {name: claude-flutter}
spec:
  displayName: Claude Code with the Flutter toolchain
  image: c4factory/agent-flutter:dev
  credentialsSecretRef: {name: claude-credentials}   # key CLAUDE_CODE_OAUTH_TOKEN
  maxRounds: 3                                       # default 3
```

The image holds the stack's toolchain, the agent CLI and `c4agent`, the runner
(below). A stand-in Agent (`image` with `C4AGENT_FAKE=1`) makes a scripted change,
so envtest and e2e can prove the loop without spending tokens.

### Task (new)

```yaml
kind: Task
metadata: {name: add-crop}
spec:
  containerRef: {name: photo-editor}    # owner; immutable
  agentRef: {name: claude-flutter}      # immutable
  goal: Add crop and rotate to the editor
status:
  phase: Implementing                   # Pending, Implementing, Checking, Reviewing, Merged, Failed
  round: 1
  branch: task/add-crop
  pullRequestURL: http://gitea.../pulls/3
  checks: {commit: ..., state: pending}
  review: {verdict: approve, summary: "..."}
  conditions: [...]
```

A Task waits (`Pending`) until its Container is `Ready`. One Task per Container runs at
a time; others wait in creation order.

## The runner: c4agent

`cmd/c4agent` is the sandbox's entrypoint, a small Go CLI with its logic in
`internal/agentrunner`. It has no Kubernetes credentials (the sandbox pod does not
mount a ServiceAccount token). It reads its job from environment variables, does the
work, and reports one JSON result in the pod's termination message, which the
operator reads from the pod status.

- **implement:** clone the repository, check out or create the Task's branch, write
  the prompt (goal, container context, review feedback and failed-check output of the
  previous round), run the agent, run `make check` and let the agent fix failures
  until green or out of turns, commit, push the branch. Result: `{commit, summary}`.
- **review:** clone, check out the branch, diff against `main`, run the agent with the
  review prompt, which asks for a JSON verdict. Result: `{verdict, summary}`.

The agent command for Claude Code is `claude -p <prompt> --dangerously-skip-permissions
--output-format json`: the sandbox is the fence, not the permission prompt.

## The fence

What an agent can reach, and what it can't:

| Boundary | How |
| --- | --- |
| Kubernetes | No ServiceAccount token in the sandbox. Results go out through the termination message only. |
| Git | A per-Container bot user in Gitea, a collaborator on that one repository only. Its token lives in a Secret owned by the Container. |
| `main` | Branch protection: no direct pushes, required status check `check`. Only the operator merges. |
| Network | A NetworkPolicy on sandbox pods allows DNS, Gitea, and HTTPS to addresses outside the cluster. The Kubernetes API and other in-cluster services are unreachable. |
| Claude token | A Secret the user creates (`make claude-secret CLAUDE_TOKEN=...`). The agent can read it inside its sandbox; later, an egress proxy that injects it (Agent Substrate's approach) can close that. |
| Time | Each sandbox has a `shutdownTime` (default 45 minutes). |

## CI and the quality contract

- Gitea Actions is enabled. An `act_runner` Deployment in the cluster runs jobs in
  host mode inside a toolchain image (label `flutter`).
- Every Standard's `Makefile` provides `check` (format, analyze, test) and `build`.
  Its workflow `.gitea/workflows/check.yaml` runs `make check` on pushes and PRs
  under the status context `check`.
- The android-release Deployment's workflow builds a signed APK on a `v*` tag and
  attaches it to a Gitea release. The keystore is an org-level Actions secret that
  the seed job creates.
- At scaffold, the operator protects `main`. Scaffolding still happens once;
  protection is part of it.

## Flutter templates

- **flutter-app (Framework):** a Flutter app with a clean layout (`lib/src/` by
  feature, a widget test, an app theme), Android embedding, application id from the
  project and container names.
- **flutter-standard (Standard):** `analysis_options.yaml` on top of
  `very_good_analysis` (OSS, strict), `.editorconfig`, `Makefile`, `AGENTS.md`,
  `docs/style/` digests, `.gitea/workflows/check.yaml`.
- **android-release (Deployment):** the release workflow and a README.

The style research covers the guides the user named (LLVM, Chromium, Google, Mozilla,
Linux kernel, C++ Core Guidelines, Rust API naming, GNU) for cross-language
principles, plus the ones that fit the stack: Effective Dart, Flutter's own style
guide, Android core app quality and Material accessibility.

## The sample story

`config/samples/toolbox/`: Project `toolbox` with a SoftwareSystem "Pocket Tools"
(needs: works fully offline, no account, no ads) and Containers such as
`photo-editor` and `unit-converter`, type `mobile-app`. Tasks ask agents to build
them.

## Serverless later

A `function` type fits the same shape: a TypeScript Lambda Framework, a TypeScript
Standard, and a SAM or CDK Deployment. Nothing in the operator changes but the enum.

## Architecture

| Package | Role |
| --- | --- |
| `internal/model` | Standard fit, Task state machine (next phase from observed facts), prompt text. Pure. |
| `internal/scaffold` | Standard as a layer. Pure. |
| `internal/gitprovider` | New: protect branch, bot user and collaborator, PR open/merge/comment, combined commit status. |
| `internal/agentrunner` | The runner's logic: git, prompt, agent command, result. No Kubernetes. |
| `internal/controller` | Task reconciler: sandboxes, results, PRs, merge. Container reconciler: Standard, protection, bot user, checks status. |
| `cmd/c4agent` | The runner entrypoint. |

Agent Sandbox's `Sandbox` type is used through `unstructured` to keep the dependency
light; its CRD is installed in envtest from a pinned copy under `test/crds/`.

## Testing

- Table tests for Standard fit, the Task state machine, prompts, the runner (against
  a local bare Git repository and a fake agent).
- envtest: a Task drives a Sandbox object; the test plays the sandbox (sets the pod's
  termination message) and the fake Git host plays Gitea (PRs, statuses, merges).
- e2e in Kind: a real Container scaffolded from the Flutter templates, CI green on
  `main`, a Task run with the stand-in agent merged autonomously, and a Task with the
  real Claude agent when the Secret exists.

## Done when

1. `make check` passes, including the new tests.
2. In Kind: `kubectl get containers -n toolbox` shows the Flutter Containers `Ready`
   with checks `success`.
3. A Task with the stand-in agent goes Implementing → Checking → Reviewing → Merged.
4. With the Claude Secret: agents build at least one tool app end to end, and its
   release APK exists in Gitea.
5. docs/design.md and the design board image describe v2.

## Spike: AX and Agent Substrate

- AX v0.3 (10 days old) runs arbitrary images but only passes plain env values, and
  injects only Gemini keys. Not safe for a Claude token yet.
- Agent Substrate installs on Kind and has an egress gateway that injects credentials
  so the agent never holds them, but that is experimental and AX can't turn it on.
- Conclusion: Agent Sandbox now; Substrate's credential injection is the later fix for
  the Claude token row in "The fence".

## Risks

- **Heavy toolchain.** Flutter plus the Android SDK is several GB, and Gradle builds
  need memory. The runner and agent images share one base to limit the cost.
- **Agent quality varies.** Max rounds, the reviewer and CI bound the damage; a
  Failed Task says why.
- **The Claude token is readable inside the sandbox.** Accepted for the prototype;
  see "The fence".
