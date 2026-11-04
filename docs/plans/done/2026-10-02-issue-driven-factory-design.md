# theos v4: issues, crews and roles

Oct 2, 2026 · asked for by @Jeremy Jo., designed and decided by Claude while working
autonomously. Every decision is in the table below so it can be overruled.

## Goal

Agents build a second product on the factory, one with a real chance of high
quality, and the factory gets better at the things that decide quality:

1. **Clear work.** Work is a backlog of small Gitea issues, each one an agent can
   finish in one Task, with acceptance criteria a reviewer can check.
2. **Agents pick up work.** A Crew per Container takes ready issues in order,
   respecting their dependencies, and turns each into a Task.
3. **The right agent for the role.** Implementer and reviewer are separate Agents
   with their own model; each gets a role guide from the Standard and the C4
   context of its Container, including the repositories it talks to.
4. **Quality shows in Gitea.** Labels say where an issue is, reviews appear as
   pull request reviews, and the reviewer's non-blocking points become proposed
   issues instead of getting lost.

The product: **CEL Lens**, rich CEL language support in Kubernetes YAML.

## What v2 taught us

| v2 finding | v4 answer |
| --- | --- |
| One Task was a whole app ("Build version 1 of Image Shrink"): too much for one run; quality was "ok for testing" | Issues sized for one run, with acceptance criteria; Tasks are made from them |
| Native plugin wiring couldn't be checked: no APK builds, no device | A product whose every behaviour is testable headless: Go tests, LSP protocol tests, grammar tests, a packaged `.vsix` |
| The reviewer is only as good as the Standard; a privacy point went into "non-blocking" and was lost | Role guides in the Standard with a review checklist; non-blocking points become `status/proposed` issues |
| The same agent and prompt implemented and reviewed | Implementer and reviewer are separate Agents with their own models |
| Agents saw only their own repository | Agents can read the repositories their Container has Relationships with |

## Decisions

| # | Topic | Decision |
| --- | --- | --- |
| 1 | Product | CEL Lens: a language server for CEL inside Kubernetes YAML, and a VS Code extension that runs it |
| 2 | Finding CEL | By content: a document's `apiVersion` and `kind` say which fields hold CEL and what is in scope. No file name suffix or marker needed for known kinds. A comment marker for other YAML is a later issue |
| 3 | Fidelity | The server uses Kubernetes' own CEL libraries (`k8s.io/apiserver`, `k8s.io/apiextensions-apiserver`) and Kubernetes' published OpenAPI schemas, embedded, so it reports what the API server would, offline |
| 4 | Containers | Two: `cel-language-server` (Go, type `cli`) and `vscode-extension` (TypeScript, type `extension`), joined by LSP over stdio. Any LSP editor can reuse the server; a third Container has no job |
| 5 | Types | Add `cli` and `extension` to the Container types. The operator still never branches on a type |
| 6 | The work | Gitea issues. A Task takes its goal from an issue (`spec.issue`) or, as before, from `spec.goal` |
| 7 | Ready | An issue is ready when it is labelled `status/ready`, its dependencies are closed, and it has an "Acceptance criteria" checklist. Without the checklist it gets `status/needs-human` and a comment, before any agent runs |
| 8 | Picking up | A new kind, `Crew`, per Container: who implements, who reviews, and whether it picks up ready issues. It starts one Task at a time, lowest issue number first |
| 9 | Issue states | Exclusive scoped labels: `status/proposed`, `status/ready`, `status/in-progress`, `status/needs-human`. Merging closes the issue |
| 10 | Roles | `Agent` gains `model` and `harness`. Crews pair a Sonnet implementer with an Opus reviewer. Fable is never used |
| 11 | Role guides | A Standard ships `docs/agents/implementer.md` and `docs/agents/reviewer.md`; theos-agent reads them from the base branch, so a change under review can't rewrite its own review |
| 12 | C4 context | Agents get the system's needs, the Container, its Relationships with their descriptions, and a read-only clone of every related Container's repository |
| 13 | Reviews in Gitea | Each verdict is posted as a pull request review (approve or request changes) naming the reviewer Agent and model |
| 14 | Follow-ups | The reviewer may name up to three follow-ups; the operator files them as `status/proposed` issues on merge, in the Later milestone, outside any release |
| 15 | Project boards | Gitea 1.27 has no API for projects, so the backlog uses milestones, labels and issue dependencies, which it does have |
| 16 | Toolchain | One Go and Node toolchain image for both Containers' CI, agents and devcontainers; a second runner labelled `go` |
| 17 | Releases | One `gitea-release` Deployment template: on a `v*` tag, CI runs `make dist` and attaches `dist/*` to the Gitea release |

## The product: CEL Lens

### The story

```yaml
kind: Person
metadata: {name: policy-author}
spec:
  displayName: Policy author
  description: A platform engineer who writes admission policies, CRD validation rules and Kyverno policies in YAML.
---
kind: SoftwareSystem
metadata: {name: cel-lens}
spec:
  displayName: CEL Lens
  description: Rich language support for CEL inside Kubernetes YAML.
  needs:
  - Finds CEL in Kubernetes YAML by itself; known kinds need no markers or settings
  - Reports the errors the API server would report, at the right line and column
  - Completes and explains variables, fields and functions for the place the expression is in
  - Works offline, without a cluster
  - Answers within 200 ms of a keystroke on a 1,000-line file
  - Never gets in the way of other YAML tooling
  kpis:
  - CEL errors caught before kubectl apply
  - Time to a correct expression
---
kind: Container
metadata: {name: cel-language-server}
spec:
  displayName: CEL language server
  description: Finds CEL in Kubernetes YAML and checks, completes and explains it over LSP; also a CLI that checks files in CI.
  type: cli
  frameworkRef: {name: go-cli}
  standardRef: {name: go-standard}
  deploymentRef: {name: gitea-release}
---
kind: Container
metadata: {name: vscode-extension}
spec:
  displayName: CEL Lens for VS Code
  description: Starts the language server for YAML files and highlights CEL inside YAML.
  type: extension
  frameworkRef: {name: vscode-extension}
  standardRef: {name: ts-standard}
  deploymentRef: {name: gitea-release}
---
kind: Relationship
metadata: {name: vscode-extension-to-cel-language-server}
spec:
  source: {kind: Container, name: vscode-extension}
  target: {kind: Container, name: cel-language-server}
  description: Starts `cel-language-server serve --stdio` and speaks LSP 3.17 with it
```

### Where CEL lives, and what is in scope

| Kind | Fields | Variables |
| --- | --- | --- |
| ValidatingAdmissionPolicy | `matchConditions[].expression`, `variables[].expression`, `validations[].expression` and `.messageExpression`, `auditAnnotations[].valueExpression` | `object`, `oldObject` (typed from `matchConstraints`), `request`, `params` (typed from `paramKind`), `namespaceObject`, `authorizer`, `variables` |
| MutatingAdmissionPolicy | the same, plus `mutations[].applyConfiguration.expression` and `.jsonPatch.expression` | as above |
| Validating/MutatingWebhookConfiguration | `webhooks[].matchConditions[].expression` | `object`, `oldObject`, `request`, `authorizer` |
| CustomResourceDefinition | `x-kubernetes-validations[].rule` and `.messageExpression` anywhere in a version's schema | `self`, `oldSelf`, typed from the schema node the rule sits on |
| Kyverno ValidatingPolicy and friends | as ValidatingAdmissionPolicy, plus Kyverno's libraries | later milestone |

### What the spike proved

Before writing issues, Claude checked the risky parts in a throwaway program:

- `validating.TypeChecker` from `k8s.io/apiserver` type-checks a policy against
  Deployment's schema from a local OpenAPI v3 file and returns the API server's
  messages with line and column inside the expression, such as
  `undefined field 'replica'`.
- Completion works by compiling the receiver (`object.spec.template.spec.containers`)
  in the same environment and listing the fields of its type.
- The environment lists 158 functions and every macro; 56 have documentation
  built in. The Kubernetes library functions (`quantity`, `url`, `isSorted`…) have
  none, so the server needs its own table.
- cel-go moved to `cel.dev/cel-go` in v0.32, while Kubernetes 0.37 still uses
  `github.com/google/cel-go` v0.29: take cel-go through `k8s.io/apiserver`, never
  directly.

These facts go into the issues, so agents start from a known path.

## Factory changes

### Kinds

```yaml
kind: Agent
spec:
  image: theos/agent-go:dev
  harness: claude-code          # new: claude-code (default) or stand-in
  model: claude-sonnet-5-5      # new: passed to the harness; empty means its default
---
kind: Task
spec:
  containerRef: {name: cel-language-server}
  agentRef: {name: claude-implementer}
  reviewerRef: {name: claude-reviewer}   # new, optional: defaults to agentRef
  issue: 3                               # new: exactly one of goal and issue
status:
  issueTitle: Map CEL positions inside YAML scalars   # new
  issueURL: http://gitea.../cel-language-server/issues/3  # new
---
kind: Crew                       # new, owned by its Container
metadata: {name: cel-language-server}
spec:
  containerRef: {name: cel-language-server}
  implementer: {name: claude-implementer}
  reviewer: {name: claude-reviewer}
  paused: false                  # stop picking up new issues
status:
  activeTask: cel-language-server-3
  ready: [4]                     # ready issues, in pick-up order
  waiting: [5, 6]                # ready, but blocked by open dependencies
  conditions: [Ready]
```

### An issue's way through the factory

```
status/proposed ──(a person triages)──► status/ready
                                            │ Crew: dependencies closed, acceptance criteria present
                                            ▼
                                     Task <container>-<n> ──► status/in-progress, comment, bot assigned
                                            │ implement ─► PR "Closes #n" ─► CI ─► review (Gitea review)
                                            ▼
                    merged: issue closed with a comment; follow-ups filed as status/proposed
                            in the Later milestone, outside any release
                    failed: status/needs-human with the reason; relabel ready to try again (3 tries)
                    needs a protected file: the operator files a kind/standard-change issue,
                            status/ready, that blocks this one; the Task is Blocked until it merges,
                            then resumes (see the standard-change design)
```

### Roles

| Role | Agent | Model | Reads |
| --- | --- | --- | --- |
| Implementer | `claude-implementer` | Sonnet 5.5 | the issue, `AGENTS.md`, `docs/agents/implementer.md`, the C4 context, related repositories |
| Reviewer | `claude-reviewer` | Opus 5.5 | the issue's acceptance criteria, `docs/agents/reviewer.md`, the diff, CI's result, the C4 context |
| Planner | Claude, in this session | — | writes the backlog; a planner Agent is a follow-up |

The role guides come from the Standard, at the version the repository was
scaffolded with, read from `main`. The reviewer reports each acceptance criterion
as met or not, and its verdict JSON may carry `followUps`.

### C4 context in every run

The operator builds the context from the model, not from the prompt author:

- the system, its needs and KPIs, and the Persons who use it;
- the Container: type, description, catalog choices;
- each Relationship of the Container, in either direction, with its description;
- for each related Container, its repository, cloned read-only by theos-agent into
  `/workspace/related/<name>` (repositories are public inside Gitea; the bot's
  token can't push there).

## Templates

| Template | Kind | For | Writes |
| --- | --- | --- | --- |
| `go-cli` | Framework | `cli` | `go.mod`, `main.go` with a testable `run`, `internal/`, README, devcontainer base |
| `go-standard` | Standard | `go-cli` | `.golangci.yml` (strict v2 config), `Makefile` (`check`, `build`, `dist`), `AGENTS.md`, `docs/style/`, `docs/agents/`, issue and PR templates, CI |
| `vscode-extension` | Framework | `extension` | `package.json` and lockfile, `src/extension.ts`, strict `tsconfig.json`, vitest config with a `vscode` stand-in, esbuild bundle into `out/`, `.vscodeignore`, README, CHANGELOG |
| `ts-standard` | Standard | `vscode-extension` | `eslint.config.mjs` (typescript-eslint strict), Prettier, `Makefile` (with a packaging check), `AGENTS.md`, `docs/style/`, `docs/agents/`, issue and PR templates, CI |
| `gitea-release` | Deployment | `cli`, `extension` | `.gitea/workflows/release.yaml`: `make dist` on a `v*` tag, assets attached to the release |

Each Standard's `make check` is the whole contract: format, lint, type check,
tests (with `-race` for Go), and for the extension a packaging dry run.

## The backlog

Filed from `config/samples/cel-lens/backlog/` by `make cel-lens-backlog`, with
milestones, labels and dependencies, so a fresh cluster gets the same plan.

- **M1 Diagnostics** (cel-language-server): locate CEL in YAML; positions inside YAML
  scalars; embedded Kubernetes schemas; policy environments; CRD rule environments;
  the `check` command; the LSP server with diagnostics.
- **M2 Completion and hover** (cel-language-server): completion; hover; docs for the
  Kubernetes libraries.
- **M3 VS Code** (vscode-extension): start the server; highlight CEL in YAML;
  settings and status; bundle the server.
- **M4 Release**: `make dist` for the server; a release of each.
- **Later** (proposed, not ready): Kyverno policies; a comment marker for CEL in
  other YAML; MutatingAdmissionPolicy.

## Testing

- Table tests: issue readiness, pick-up order, the C4 context text, prompts with
  role guides, verdicts with follow-ups.
- envtest: a Crew turns a ready issue into a Task on the fake Git host; a Task
  from an issue labels, comments, reviews and closes it there.
- Kind: the real loop on CEL Lens, watched in the Factory view.

## Done when

1. `make check` passes.
2. Both CEL Lens Containers are `Ready` with checks `success`, and their backlogs
   are in Gitea with milestones, labels and dependencies.
3. Crews take issues in order, and only when their dependencies are closed.
4. M1 is merged by agents; as much of M2 to M4 as time allows.
5. A `.vsix` from a Gitea release, installed in VS Code, shows CEL diagnostics in a
   policy, if M3 and M4 get there.
6. `docs/results-v4.md` records what happened and what changed in the factory.

## Risks

- **Usage limits.** Hours of Sonnet and Opus can hit the subscription's limits. A
  limit hit must not cost a round: the Task waits and tries again.
- **Kubernetes dependencies are large.** `k8s.io/apiserver` pulls in a lot; builds
  and the agent's first `go mod download` take time. The toolchain image warms the
  module cache.
- **Cross-repository order.** The extension's bundle step needs a server release,
  which only the operator's user may tag: that step waits for a person.
- **Agents still vary.** Smaller issues, a stronger reviewer and CI bound it; the
  rest is what this round of work is meant to measure.

## Later

A planner Agent that turns a milestone into proposed issues; routing issues to
Agents by label; Kyverno and other CEL hosts; webhooks from Gitea instead of polling.
