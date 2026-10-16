# aws-standard (Standard)

How a program on AWS in a C4 Factory Container (a Lambda function, a service
on EC2 or a container service on Fargate) is written, checked and reviewed, its code in TypeScript or Go and its CDK app in TypeScript:
ts-standard's TypeScript rules (typescript-eslint's strictest configs,
Prettier), go-standard's Go rules for a Go function, CDK Nag's AWS Solutions
checks on the CDK app, cfn-lint on what it synthesizes, the Standard's own
assertions on every stack, the agent guide and role guides, offline style and
Well-Architected digests, issue and pull request templates, and the
`make check` contract CI runs. Nothing deploys, bootstraps or calls AWS.

Copied from: ts-standard v1.5.0, the TypeScript rules and `principles.md`. A
drift test in C4 Factory's `internal/scaffold` keeps the shared files equal:
`.editorconfig`, `.prettierrc.json` and `docs/style/typescript.md` render the
same as ts-standard's for `node-cli`, and every line ts-standard's
`eslint.config.mjs` and `docs/style/principles.md` render the same for all its
Frameworks appears in this Standard's, in the same order. For
`aws-lambda-go`, `.golangci.yml` and `docs/style/go.md` are go-standard
v1.3.0's, and the same test keeps them byte for byte equal to go-standard's.
For `aws-lambda-py`, `ruff.toml` and `mypy.ini` render the same as
python-standard v1.3.0's, and `docs/style/python.md` holds its `python.md`'s
general sections, from "Layout and formatting" up to its FastAPI section,
byte for byte.

- **Supports:** `function`, `service`; Frameworks: `aws-lambda-ts`,
  `aws-lambda-go`, `aws-ec2-ts`, `aws-fargate-ts`, `aws-lambda-py`
- **Per Framework:** `aws-lambda-go` (since v1.1.0) branches on `.Framework`
  in the `Makefile` (the Go targets), `AGENTS.md`, the role guides,
  `docs/style/aws.md` and `SOURCES.md` (the Go handler, `slog`, the SDK for
  Go v2, `go build` bundling), `.editorconfig` (tabs for `*.go`),
  `.prettierignore` (`.golangci.yml`) and the workflow (`GOPROXY`,
  `GOPRIVATE`), and alone gets `.golangci.yml` and `docs/style/go.md`, which
  render empty, and so are left out, for any other Framework. `aws-ec2-ts`
  (since v1.2.0) branches in `AGENTS.md`, the role guides, the issue
  template, `eslint.config.mjs`'s comments and message, `docs/style/aws.md`,
  `principles.md`, `SOURCES.md` and `docs/well-architected.md`: an EC2
  digest (the server, the network and load balancer, the instances and the
  Auto Scaling group) and a Well-Architected digest for a service on
  instances. Since v1.2.0 an `aws-lambda-ts` Container's docs no longer say
  "function" where they mean any code, and every Framework's
  `infra/standard.test.ts` reads tags with extra keys (an Auto Scaling
  group's `PropagateAtLaunch`) and lets a `Deny` statement name every action
  (a bucket refusing plain HTTP). `aws-fargate-ts` (since v1.3.0) branches
  in `AGENTS.md`, the role guides, the issue template and
  `eslint.config.mjs` (`dist/` ignored; `process.exit` refused because it
  drops requests in flight), and has its own whole `docs/style/aws.md` (the
  server, the load balancer, the task and service), `SOURCES.md` and
  `docs/well-architected.md` (ECS's best practices instead of the Serverless
  Lens), each one `{{ if eq .Framework "aws-fargate-ts" }}` branch around
  the file. `aws-lambda-py` (since v1.4.0), a Python function with a
  TypeScript CDK app, branches in the `Makefile` (uv, ruff, mypy and pytest
  in its targets), `AGENTS.md`, the role guides, `eslint.config.mjs` (it
  also ignores `.venv/`), `docs/style/aws.md`, `principles.md`, `SOURCES.md`
  and `docs/well-architected.md` (the Python handler, boto3, uv bundling,
  pytest), and alone gets `ruff.toml`, `mypy.ini` and
  `docs/style/python.md`. Since v1.4.1 `docs/well-architected.md` gives
  `aws-lambda-go` cold-start advice for a `go build` binary instead of
  esbuild's. Since v1.6.0 `aws-lambda-ts` (from its v1.1.0) has its own
  `docs/well-architected.md` (the tiers, `infra/operations.ts`, the alarms,
  the backups and what production costs over development), `docs/runbook.md`,
  which renders empty, and so is left out, for every other Framework, and
  branches in `AGENTS.md`, the role guides, `docs/style/aws.md` and
  `SOURCES.md` (the operations sources, read 2026-10-16); and the Go
  digest's cold-start line is wrapped so Prettier keeps it. A Framework
  added later gets its branches in the version that adds it.
- **Writes:** `eslint.config.mjs`, `.prettierrc.json`, `.prettierignore`,
  `.editorconfig`, `Makefile`, `AGENTS.md`, `docs/style/`,
  `docs/well-architected.md`, `docs/agents/implementer.md`,
  `docs/agents/reviewer.md`, `infra/main.ts`, `infra/standard.ts`,
  `infra/standard.test.ts`, `.gitea/workflows/check.yaml`,
  `.gitea/ISSUE_TEMPLATE/task.md`, `.gitea/PULL_REQUEST_TEMPLATE.md`; for
  `aws-lambda-ts` also `docs/runbook.md`; for
  `aws-lambda-go` also `.golangci.yml` and `docs/style/go.md`; for
  `aws-lambda-py` also `ruff.toml`, `mypy.ini` and `docs/style/python.md`
- **Lints:** ESLint 10 with `@eslint/js` recommended, typescript-eslint
  `strictTypeChecked` and `stylisticTypeChecked` (typed, through the project
  service), plus exhaustive switches, explicit module boundary types,
  `prefer-readonly`, sort compare functions, `no-console` (the code logs
  JSON through the `Log` it is given), `process.exit` refused, `eqeqeq` and
  `curly`; unused disable comments are errors. Prettier formats everything but
  the platform's files, the lockfile and `cdk.out/`, which ESLint ignores too.
  For `aws-lambda-go`, golangci-lint with go-standard's `.golangci.yml` (its
  formatters gofumpt and goimports, its linters) lints and formats `src/`,
  and Prettier skips `.golangci.yml`.
- **CDK:** `infra/standard.ts` names the tiers (`Tier`, `TIERS`:
  `production` and `development`), what each means for every stack
  (`TIER_RULES`: keeping and protecting data, point-in-time recovery, AWS
  Backup, X-Ray, log retention one year or one month), the `Operations` type
  a Framework's `infra/operations.ts` has, `tierOf(app)`, the app's
  `tier` context value, `development` when it has none, refusing any other,
  and `stackNameFor(name, tier)`, which adds `-development` to a
  development stack's name.
  `applyStandard(app, tier)` tags every stack, and every resource in it that
  takes tags, with `c4factory:project`, `c4factory:system` and
  `c4factory:container` (the stack's own tags too, set explicitly, as the
  `@aws-cdk/core:explicitStackTags` flag needs), sets termination protection
  where the tier keeps data, acknowledges `AwsSolutions-IAM5[Resource::*]` on
  a policy whose only statements on the resource `*` are X-Ray's
  `PutTraceSegments` and `PutTelemetryRecords`, and `AwsSolutions-DDB3` on
  every table where the tier has no point-in-time recovery, and adds CDK
  Nag's `AwsSolutions` to the app as a policy validation plugin
  (`Validations.of(app).addPlugins`, cdk-nag 3), so a finding not
  acknowledged with a reason fails synth. `infra/main.ts` makes the `App`,
  reads the tier, calls the Framework's `defineStacks(app, tier)` (through a
  type with both parameters, so a `stacks.ts` written before tiers, taking
  the app alone, still fits), then `applyStandard(app, tier)`, then
  `app.synth()`. `infra/standard.test.ts`, not protected like ts-standard's
  tests, builds the app the same way with `cdk.json`'s context, once per
  tier, and asserts on every stack of both: `tierOf` defaults and refuses;
  synth passes CDK Nag; no error or warning annotation; the three tags on
  the stack and on every resource with a `Tags` list; no IAM action `*` or
  `service:*` in a statement that allows, but a key's default policy (`kms:*`
  to the account's root); every DynamoDB table (`SSESpecification`), S3
  bucket, SQS queue and SNS topic encrypted at rest; every log group with a
  retention and every Lambda function logging to its own; every stateful
  resource (tables, buckets, queues, topics, log groups, databases, file
  systems, KMS keys, backup vaults) with an explicit `DeletionPolicy`;
  termination protection by tier. When the repository has
  `infra/operations.ts`, it imports its `OPERATIONS` at run time, checks its
  shape, and adds a `describe` per tier and item of what it promises: the
  stack names (`stackNameFor`), the removal policies (tables `Retain` or
  `Delete` by tier, every backup vault and its key `Retain`), deletion and
  termination protection, point-in-time recovery (and on in production
  when `rpo` is under a day), the backup plan and vault (in production with `retentionDays`
  over 0: one plan whose rules are daily, keep `retentionDays` and copy to
  `copyToRegion`'s vault when set, one vault with its own key, one selection
  by the `c4factory:container` tag, and a copy stack in that region; else
  none), the runbook (naming the copy region when there is one), the alarms and topic (one topic, a dashboard, every alarm notifying
  the topic, alarms on every function's `Errors`, `Throttles` and
  `Duration`, every table's `ReadThrottleEvents`, `WriteThrottleEvents` and
  `SystemErrors`, every HTTP API's `5xx`), the subscription (the
  `AlertEmail` parameter defaulting to `alerts.email`, one email
  subscription to it under a condition that it is not empty), tracing
  (`Active` by tier), log retention (the tier's) and the two outputs
  (`AlarmTopicArn`, the topic, and `EntryPoint`). A stack holding only a
  backup vault and its key, the copy's, serves nothing and has no topic,
  alarms or outputs. A repository with no `infra/operations.ts`, scaffolded
  before it, skips those. A type with no resource passes.
- **Check contract:** `make check` = `npm ci`, `prettier --check`,
  `eslint --max-warnings 0`, `tsc --noEmit`, `vitest run`, `cdk synth
  --quiet -c tier=<tier> -o cdk.out/<tier>` for `production` and
  `development` (from `node_modules/.bin`, with `--no-notices`,
  `CDK_DISABLE_CLI_TELEMETRY` and `CDK_DISABLE_VERSION_CHECK`, into a fresh
  `cdk.out/`) and `uvx cfn-lint==1.57.2 --include-checks I` on both
  folders' `*.template.json` (I3011, a stateful resource without a deletion
  policy; I3013, expiring content without a set retention), ignoring only
  W3005 (the `DependsOn` CDK puts on a function's role, which cfn-lint calls
  redundant). Synth needs no Docker, AWS account, credentials
  or network: stacks are environment-agnostic and esbuild bundles locally.
  For `aws-lambda-go`, `make check` = `npm ci`, `go mod download`,
  `go mod tidy -diff`, `prettier --check`, `golangci-lint fmt --diff`,
  `eslint`, `go vet`, `golangci-lint run`, `tsc --noEmit`,
  `go test -race -count=1`, `vitest run` (the stacks' tests, which build the
  function), `cdk synth` and cfn-lint as above; every Go command runs in
  `src/`, the function's own module. `make fmt` also runs
  `golangci-lint fmt`. Synth builds the function with the runner's Go.
  For `aws-lambda-py`, `deps` also runs `uv sync --locked`, `fmt-check` adds
  `ruff format --diff`, `lint` `ruff check --no-fix`, `typecheck`
  `mypy app tests` (not `.`: `cdk.out/` holds a bundled copy of boto3), and
  `test` runs `pytest --strict-markers --strict-config` before vitest. Its
  stack bundles the function with uv from uv's cache, which `uv sync` filled,
  or PyPI when the cache lacks a wheel, as `uvx cfn-lint` already needs.
  `.gitea/workflows/check.yaml` runs it on pull requests and on `main` on a
  runner labelled `go` (which has uv), under the status context
  `check / check`, which the protected `main` requires
  (Gitea Actions' check contexts, `model.CI` in `internal/model/ci.go`). npm's and uv's caches go to the runner's
  memory-backed `/tmp`. A change to only `CHANGELOG.md`, `docs/` or images
  (`.png`, `.jpg`, `.gif`, `.svg`) skips the steps after the checkout and
  passes, so the context still reports; any other file, `README.md`
  included, or a first push runs them.
- **Checks before a release** (`checksBeforeRelease: true`): with the
  Container's `checks: BeforeRelease`, `check.yaml` renders a `check` job that
  runs `make check-light` (`npm ci`, `prettier --check`, `eslint
  --max-warnings 0`, `tsc --noEmit`, and for `aws-lambda-go` the Go module,
  tidy, format, vet and lint checks; no tests, synth or cfn-lint) on pull
  requests and on `main`, with the same skip, and a `full` job that runs
  `make check` on a push to a `release-check/**` branch, whatever the push
  changes (status context `check / full (push)`). With `EveryChange`, the
  default, it renders as before.
- **Releases:** none. The CDK app is the deployment, and deploying it is for
  the owner with their credentials; there is no `dist` or `deploy` target.
  `make clean` removes `cdk.out/`.
- **Framework contract:** the Framework owns `package.json`, which must list
  this Standard's tools with exact versions: `eslint`, `@eslint/js`,
  `typescript-eslint`, `prettier`, `typescript`, `vitest`, `aws-cdk-lib`,
  `constructs`, `aws-cdk`, `cdk-nag` and `tsx` (which runs `infra/main.ts`),
  and `esbuild` when it bundles a Node function. It owns `infra/stacks.ts`,
  exporting `defineStacks(app: App, tier: Tier): Stack[]` (one written
  before tiers takes the app alone), which adds every stack for the tier to
  `app` without an `env` from the environment or a context lookup; and,
  from `aws-lambda-ts` v1.1.0 on, `infra/operations.ts`, rendered from
  `.Operations`, exporting `OPERATIONS` of the `Operations` type, which its
  stacks read;
  `cdk.json`, whose `app` is `node_modules/.bin/tsx infra/main.ts` and whose
  context holds the recommended feature flags; `tsconfig.json`, including
  `src`, `infra` and `vitest.config.mts`; and `vitest.config.mts`, running
  `src/**/*.test.ts` and `infra/**/*.test.ts`. aws-lambda-ts, aws-ec2-ts
  and aws-fargate-ts (which bundles in its image's build, not in synth) do
  all of this. `aws-lambda-go` does the same with no `esbuild` of its own,
  `tsconfig.json` and vitest without `src`, and the function in `src/` as a
  Go module with `go.mod` and `go.sum`, built by its stack. `aws-lambda-py`
  does the same without `src/` and esbuild, and also owns `pyproject.toml`
  (the function's packages in `dependencies`, `ruff`, `mypy`, `pytest` and
  `types-boto3` exact in the `dev` group, pytest reading `tests/`,
  `[tool.uv] package = false`), `uv.lock`, the package `app/` and `tests/`.
- **Role guides:** `docs/agents/implementer.md` and `docs/agents/reviewer.md`,
  which the factory gives its implementer and reviewer Agents, read from `main`.
  They say no test calls AWS (stacks are checked with `Template.fromStack`,
  SDK clients are faked behind interfaces), nothing deploys, permissions come
  from CDK grants, and the reviewer checks every CDK Nag acknowledgement's
  reason.
- **Issues:** `.gitea/ISSUE_TEMPLATE/task.md` has the sections the factory needs;
  an issue without an unchecked item under "Acceptance criteria" is never picked up.
  The pull request template asks for the CDK Nag acknowledgements a change adds.
- **Style digests:** `docs/style/` holds ts-standard's TypeScript digest (and
  go-standard's `go.md` for `aws-lambda-go`) and cross-language principles
  (adapted per Framework: what the handler or the server owns, the layering
  of pure modules, SDK wrappers and the entry point), and `aws.md`, from the
  Lambda, API Gateway, DynamoDB, SDK and CDK guides and CDK Nag's rules, or
  for `aws-ec2-ts` the EC2 Auto Scaling, Application Load Balancer, VPC,
  DynamoDB, SDK v3 and CDK guides, or for `aws-fargate-ts` ECS's best
  practices and the Application Load Balancer's guides. Every rule is marked `[lint]`, `[nag]`,
  `[test]` or `[review]`;
  `docs/style/SOURCES.md` lists the sources with dates and the pinned
  versions.
- **Well-Architected:** `docs/well-architected.md` digests the AWS
  Well-Architected Framework (with its Serverless Applications Lens, but for
  `aws-ec2-ts`, and with ECS's best practices for `aws-fargate-ts`), one section per pillar (operational excellence, security, reliability,
  performance efficiency, cost optimization, sustainability), each naming the
  CDK Nag rules that enforce it and what the agent does by hand where none
  does. It is self-contained: agents have no internet. For `aws-lambda-ts`
  it also says what each tier does per pillar and what `infra/operations.ts`
  changes, its Reliability section names the RPO and RTO, the backup plan,
  the copy and the restore procedure, its Cost section says in words what
  production costs over development, and what stays by hand (a restore
  drill, a game day, right-sizing from measured load) is listed with why.
- **Runbook:** `docs/runbook.md`, for `aws-lambda-ts`, renders the
  Container's operations block and says how to restore the items to a point
  in time, from the vault and from the copy, how to bring back a deleted
  table, how to roll a deployment back, what each alarm means and what to do
  first, which account-level services the account's owner provides
  (GuardDuty, Security Hub, CloudTrail, AWS Config, Budgets, the cost
  allocation tags' activation), and how to drill a restore.
- **Protect:** list `AGENTS.md`, `Makefile`, `eslint.config.mjs`, `.golangci.yml`,
  `ruff.toml`, `mypy.ini`,
  `.prettierrc.json`, `.prettierignore`, `.editorconfig`, `docs/style/`,
  `docs/agents/`, `docs/well-architected.md`, `docs/runbook.md`,
  `infra/main.ts` and `infra/standard.ts` in the catalog entry's
  `protectedPaths`, plus the
  Framework's `tsconfig.json`, `vitest.config.mts` and `cdk.json`, which
  decide what the type check and tests cover and which CDK behaviour the app
  gets; `.gitea/` is always protected.

## How a template works

Everything in this repository is copied into the new Container repository at the
same path, except `.c4factory/`, which is input for the factory. Files ending in
`.tmpl` are rendered with Go `text/template` (missing keys are errors) and lose the
suffix; the workflow writes its `${{ }}` expressions as template strings.
Other files are copied byte for byte. `infra/standard.ts` is the one TypeScript file rendered: it
holds the tag values (`.Project`, `.SoftwareSystem`, `.Container`).
