# aws-standard (Standard)

How a TypeScript program on AWS in a C4 Factory Container, serverless or a
container service, is written, checked and reviewed: ts-standard's TypeScript rules
(typescript-eslint's strictest configs, Prettier), CDK Nag's AWS Solutions
checks on the CDK app, cfn-lint on what it synthesizes, the Standard's own
assertions on every stack, the agent guide and role guides, offline style and
Well-Architected digests, issue and pull request templates, and the
`make check` contract CI runs. Nothing deploys, bootstraps or calls AWS.

Copied from: ts-standard v1.4.1, the TypeScript rules and `principles.md`. A
drift test in C4 Factory's `internal/scaffold` keeps the shared files equal:
`.editorconfig`, `.prettierrc.json` and `docs/style/typescript.md` render the
same as ts-standard's for `node-cli`, and every line ts-standard's
`eslint.config.mjs` and `docs/style/principles.md` render the same for all its
Frameworks appears in this Standard's, in the same order.

- **Supports:** `function`, `service`; Frameworks: `aws-lambda-ts`,
  `aws-fargate-ts`
- **Per Framework:** `AGENTS.md`, the role guides, the task issue template,
  `eslint.config.mjs`, `docs/style/aws.md`, `principles.md`, `SOURCES.md` and
  `docs/well-architected.md` branch on `.Framework`: `aws-fargate-ts` gets
  the container service's text (the server, the load balancer, the task and
  service, ECS's best practices, `dist/` ignored, `process.exit` refused
  because it drops requests in flight), and any other Framework
  aws-lambda-ts's, unchanged from v1.0.0. The `Makefile`, CI and
  `infra/` don't branch. A Framework added later gets its branches in the
  version that adds it.
- **Writes:** `eslint.config.mjs`, `.prettierrc.json`, `.prettierignore`,
  `.editorconfig`, `Makefile`, `AGENTS.md`, `docs/style/`,
  `docs/well-architected.md`, `docs/agents/implementer.md`,
  `docs/agents/reviewer.md`, `infra/main.ts`, `infra/standard.ts`,
  `infra/standard.test.ts`, `.gitea/workflows/check.yaml`,
  `.gitea/ISSUE_TEMPLATE/task.md`, `.gitea/PULL_REQUEST_TEMPLATE.md`
- **Lints:** ESLint 10 with `@eslint/js` recommended, typescript-eslint
  `strictTypeChecked` and `stylisticTypeChecked` (typed, through the project
  service), plus exhaustive switches, explicit module boundary types,
  `prefer-readonly`, sort compare functions, `no-console` (the function logs
  JSON through the `Log` it is given), `process.exit` refused, `eqeqeq` and
  `curly`; unused disable comments are errors. Prettier formats everything but
  the platform's files, the lockfile and `cdk.out/`, which ESLint ignores too
  (and `dist/`, for aws-fargate-ts, whose `.gitignore` Prettier reads).
- **CDK:** `infra/standard.ts` tags every stack, and every resource in it
  that takes tags, with `c4factory:project`, `c4factory:system` and
  `c4factory:container` (the stack's own tags too, set explicitly, as the
  `@aws-cdk/core:explicitStackTags` flag needs), and adds CDK Nag's
  `AwsSolutionsChecks` to the app as a policy validation plugin
  (`Validations.of(app).addPlugins`, cdk-nag 3), so a finding not
  acknowledged with a reason fails synth. `infra/main.ts` makes the `App`,
  calls the Framework's `defineStacks(app)`, then `applyStandard(app)`, then
  `app.synth()`. `infra/standard.test.ts`, not protected like ts-standard's
  tests, builds the app the same way with `cdk.json`'s context and asserts on
  every stack: synth passes CDK Nag; no error or warning annotation; the three
  tags on the stack and on every resource with a `Tags` list; no IAM action
  `*` or `service:*` in a statement that allows (a `Deny`, such as a bucket's
  TLS-only `s3:*`, may name every action); every DynamoDB table (`SSESpecification`), S3 bucket,
  SQS queue and SNS topic encrypted at rest; every log group with a retention
  and every Lambda function logging to its own; every stateful resource
  (tables, buckets, queues, topics, log groups, databases, file systems, KMS
  keys) with an explicit `DeletionPolicy`. A type with no resource passes.
- **Check contract:** `make check` = `npm ci`, `prettier --check`,
  `eslint --max-warnings 0`, `tsc --noEmit`, `vitest run`, `cdk synth
  --quiet` (from `node_modules/.bin`, with `--no-notices`,
  `CDK_DISABLE_CLI_TELEMETRY` and `CDK_DISABLE_VERSION_CHECK`, into a fresh
  `cdk.out/`) and `uvx cfn-lint==1.57.2` on `cdk.out/*.template.json`,
  ignoring only W3005 (the `DependsOn` CDK puts on a function's role, which
  cfn-lint calls redundant). Synth needs no Docker, AWS account, credentials
  or network: stacks are environment-agnostic and esbuild bundles locally.
  `.gitea/workflows/check.yaml` runs it on pull requests and on `main` on a
  runner labelled `go` (which has uv), under the status context
  `check / check`, which the protected `main` requires
  (`model.CheckContexts`). npm's and uv's caches go to the runner's
  memory-backed `/tmp`. A change to only `CHANGELOG.md`, `docs/` or images
  (`.png`, `.jpg`, `.gif`, `.svg`) skips the steps after the checkout and
  passes, so the context still reports; any other file, `README.md`
  included, or a first push runs them.
- **Checks before a release** (`checksBeforeRelease: true`): with the
  Container's `checks: BeforeRelease`, `check.yaml` renders a `check` job that
  runs `make check-light` (`npm ci`, `prettier --check`, `eslint
  --max-warnings 0`, `tsc --noEmit`; no tests, synth or cfn-lint) on pull
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
  exporting `defineStacks(app: App): Stack[]`, which adds every stack to
  `app` without an `env` from the environment or a context lookup;
  `cdk.json`, whose `app` is `node_modules/.bin/tsx infra/main.ts` and whose
  context holds the recommended feature flags; `tsconfig.json`, including
  `src`, `infra` and `vitest.config.mts`; and `vitest.config.mts`, running
  `src/**/*.test.ts` and `infra/**/*.test.ts`. aws-lambda-ts and
  aws-fargate-ts do all of this.
- **Role guides:** `docs/agents/implementer.md` and `docs/agents/reviewer.md`,
  which the factory gives its implementer and reviewer Agents, read from `main`.
  They say no test calls AWS (stacks are checked with `Template.fromStack`,
  SDK clients are faked behind interfaces), nothing deploys, permissions come
  from CDK grants, and the reviewer checks every CDK Nag acknowledgement's
  reason.
- **Issues:** `.gitea/ISSUE_TEMPLATE/task.md` has the sections the factory needs;
  an issue without an unchecked item under "Acceptance criteria" is never picked up.
  The pull request template asks for the CDK Nag acknowledgements a change adds.
- **Style digests:** `docs/style/` holds ts-standard's TypeScript digest and
  cross-language principles (adapted per Framework: what the handler or the
  server owns, the layering of pure modules, SDK wrappers and the wiring),
  and `aws.md`, from the Lambda and API Gateway guides, or ECS's and the
  Application Load Balancer's, and the DynamoDB, SDK v3 and CDK guides and
  CDK Nag's rules. Every rule is marked `[lint]`, `[nag]`, `[test]` or `[review]`;
  `docs/style/SOURCES.md` lists the sources with dates and the pinned
  versions.
- **Well-Architected:** `docs/well-architected.md` digests the AWS
  Well-Architected Framework and its Serverless Applications Lens, or ECS's
  best practices for aws-fargate-ts, one
  section per pillar (operational excellence, security, reliability,
  performance efficiency, cost optimization, sustainability), each naming the
  CDK Nag rules that enforce it and what the agent does by hand where none
  does. It is self-contained: agents have no internet.
- **Protect:** list `AGENTS.md`, `Makefile`, `eslint.config.mjs`,
  `.prettierrc.json`, `.prettierignore`, `.editorconfig`, `docs/style/`,
  `docs/agents/`, `docs/well-architected.md`, `infra/main.ts` and
  `infra/standard.ts` in the catalog entry's `protectedPaths`, plus the
  Framework's `tsconfig.json`, `vitest.config.mts` and `cdk.json`, which
  decide what the type check and tests cover and which CDK behaviour the app
  gets; `.gitea/` is always protected.

## How a template works

Everything in this repository is copied into the new Container repository at the
same path, except `.c4factory/`, which is input for the factory. Files ending in
`.tmpl` are rendered with Go `text/template` (missing keys are errors) and lose the
suffix; the workflow writes its `${{ }}` expressions as template strings.
Other files are copied byte for byte. `infra/standard.ts` is the one TypeScript file rendered: it
holds the tag values (`.Project`, `.SoftwareSystem`, `.Container`). A
Markdown template that branches starts with
`{{- $fargate := eq .Framework "aws-fargate-ts" -}}`.
