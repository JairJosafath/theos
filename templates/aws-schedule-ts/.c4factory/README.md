# aws-schedule-ts (Framework)

A scheduled job on AWS in TypeScript: an EventBridge Scheduler schedule that
invokes a Lambda function, keeping a record of each run in DynamoDB through
the AWS SDK v3, with a dead-letter queue for the runs that keep failing and
its infrastructure as a CDK app. Nothing deploys: the check synthesizes the
CloudFormation offline.

- **Supports:** `job`
- **Layout:** `src/handler.ts` creates the DynamoDB client, Powertools'
  `Logger` and `Metrics` and the idempotent `runJob` at module scope, reads
  `TABLE_NAME` and `IDEMPOTENCY_TABLE_NAME`, and runs each event through
  `reporting(checkingFirst(runOnce))`: `checkingFirst` refuses an event that
  names no run before it is recorded, `runOnce` is `runJob` behind
  Powertools' `makeIdempotent` (keyed by the `scheduledTime`, records kept a day, so a run
  delivered twice runs once, and a failure keeps no record), and
  `reporting` counts `CompletedRuns` and `FailedRuns` with `Metrics` and
  logs a failure, throwing again so Lambda retries the run. `runJob` in
  `src/job.ts` checks the event with `checkEvent` and keeps a record of the
  run, where a Container's own work goes, and throws `Malformed` for an
  event that names no run. `src/runs.ts` holds the rules (a scheduled time
  is `YYYY-MM-DDTHH:MM:SSZ`, an attempt 1 to 186, as a string or a number)
  and imports neither the SDK nor the `aws-lambda` types;
  `src/run_store.ts` wraps DynamoDB behind the `RunStore` interface. Tests
  next to the code (`*.test.ts`) use a fake store and a fake document
  client; none calls AWS.
- **Operations:** `infra/operations.ts`, rendered from `.Operations` (the
  Container's `spec.operations` with the factory's defaults), exports one
  frozen `OPERATIONS` of the Standard's `Operations` type: `backups`
  (`retentionDays`, `copyToRegion`), `recovery` (`rpo`, `rto`) and `alerts`
  (`email`), each string rendered with `ts`. The factory overwrites it when
  the block changes; it is not a protected path.
- **Infrastructure:** `infra/stacks.ts` exports `defineStacks(app: App, tier:
  Tier): Stack[]`, which adds one stack named after the package (the
  Container, and `<name>-development` in development, the Standard's
  `stackNameFor`), `infra/job_stack.ts`, built for the tier with the
  Standard's `TIER_RULES`, and in production with `copyToRegion` and backups
  set a second, `<name>-backup-copy` in that region (`infra/backups.ts`'s
  `BackupCopyStack`, as `aws-lambda-ts` has it), which the first depends on.
  The stack has an EventBridge Scheduler schedule (`SCHEDULE`, `cron(0 3 * *
  ? *)` in `Etc/UTC`, no flexible window) in a schedule group of its own,
  since the Scheduler's metrics are per group; its target is the function,
  with the input `{"scheduledTime": <aws.scheduler.scheduled-time>,
  "attemptNumber": <aws.scheduler.attempt-number>}`, a retry policy (10
  attempts within an hour) and the dead-letter queue, through a role of the
  stack's own that only `scheduler.amazonaws.com` may assume, for this
  account (`aws:SourceAccount`) and group (`aws:SourceArn`), allowed only
  `lambda:InvokeFunction` on the function's ARN (not `<arn>:*`, which the
  target's own role would add) and `sqs:SendMessage` on the dead-letter
  queue; the target gets the role `withoutPolicyUpdates()`. One SQS queue,
  the dead-letter queue (kept 14 days, SQS-managed encryption, a queue
  policy refusing requests without TLS, `RETAIN` in production, `DESTROY` in
  development; `AwsSolutions-SQS3` acknowledged, since no redrive policy
  names it). Two on-demand DynamoDB tables, the runs and Powertools'
  idempotency records (TTL `expiration`), KMS-encrypted, with point-in-time
  recovery, deletion protection and `RETAIN` in production and none of
  them, `DESTROY`, in development. A `NodejsFunction` (Node.js 24, arm64,
  256 MB, 5 minutes, JSON logs to its own log group, X-Ray `Active` in
  production only, its own role granted only `PutItem` on the runs,
  `GetItem`, `PutItem`, `UpdateItem` and `DeleteItem` on the idempotency
  records, `sqs:SendMessage` on the dead-letter queue and writing its log
  group), invoked asynchronously: its `EventInvokeConfig` retries a run
  that threw twice within an hour, and its `DeadLetterConfig` names the
  dead-letter queue. `infra/alarms.ts`'s `Alarms` makes the alarm topic,
  the `AlertEmail` parameter, the subscription under `HasAlertEmail` and
  the `MonitoringFacade` with a dashboard named after the stack, as in
  `aws-lambda-ts`: alarms on the runs the Scheduler lost
  (`InvocationDroppedCount` and `InvocationsFailedToBeSentToDeadLetterCount`
  of the group, any, missing data not breaching), the dead-letter queue's
  depth (any message), the function's errors, throttles and p99 duration
  (over 4 minutes of its 5), and each table's read and write throttles and
  system errors, each on one bad five-minute period. In production with
  `retentionDays` over 0, `infra/backups.ts`'s `Backups` adds the vault, the
  daily plan and the selection by the `c4factory:container` tag, as in
  `aws-lambda-ts`. The stack outputs `EntryPoint` (the schedule's ARN),
  `FunctionName` (what a person invokes to run the job by hand) and
  `AlarmTopicArn`. `infra/job_stack.test.ts` asserts it with
  `Template.fromStack` for both tiers.
- **Writes:** `package.json`, `package-lock.json`, `tsconfig.json`,
  `vitest.config.mts`, `cdk.json`, `src/`, `infra/stacks.ts`,
  `infra/job_stack.ts`, `infra/job_stack.test.ts`, `infra/alarms.ts`,
  `infra/backups.ts`, `infra/operations.ts`, `README.md`, `CHANGELOG.md`,
  `.gitignore`
- **Package:** `name` is the Container, version `0.0.1`, private and
  `UNLICENSED`, an ES module, `engines.node` `>=22`. The SDK clients and
  Powertools' `logger`, `metrics` and `idempotency` 2.36.0 are
  `dependencies`, which esbuild bundles into the function (not the
  runtime's copy); the CDK, CDK Nag, `cdk-monitoring-constructs` 10.3.2,
  tsx and the Standard's tools are `devDependencies`.
- **Build:** `NodejsFunction` bundles `src/handler.ts` with the installed
  esbuild (`forceDockerBundling: false`, minified, with a source map); never
  Docker. `cdk.json`'s `app` runs `infra/main.ts` with tsx, and its context
  holds the feature flags `cdk init` of aws-cdk 2.1145.0 recommends.
- **Tests:** vitest runs `src/**/*.test.ts` and `infra/**/*.test.ts` in
  Node, with a 60-second timeout for the tests that synthesize a stack and
  120 seconds for a `beforeAll` that synthesizes both tiers.
- **Type check:** `tsconfig.json` is `strict` with `noUncheckedIndexedAccess`,
  `exactOptionalPropertyTypes` and the other strict flags, Node's types and
  `resolveJsonModule`; `noEmit`. It includes `src`, `infra` and
  `vitest.config.mts`.
- **Lockfile:** `package-lock.json` was generated by npm 11 on Node 24; only its
  two `name` fields are templated, so `npm ci` installs exactly the versions
  in `package.json`. `allowScripts` lets esbuild's install script run.
- **Standard:** made for `aws-standard` v1.10.0 or later, the first that supports `job` and this Framework, which owns
  `infra/main.ts`, `infra/standard.ts` (the tier, `TIER_RULES` and the
  `Operations` type this Framework imports) and `infra/standard.test.ts`. Lists its tools in
  `devDependencies` with exact versions: `eslint` 10.11.0, `@eslint/js`
  10.0.1, `typescript-eslint` 8.71.0, `prettier` 3.9.9, `typescript` 6.0.3,
  `vitest` 5.0.3, `esbuild` 0.28.2, `aws-cdk-lib` 2.273.0, `constructs`
  10.8.1, `aws-cdk` 2.1145.0, `cdk-nag` 3.0.2 and `tsx` 4.23.15. TypeScript
  is 6.0.3 because typescript-eslint 8.71 supports `<6.1.0`.
- **Devcontainer:** `.c4factory/devcontainer.json` uses `c4factory/go:1.26`, the
  Go and Node toolchain CI and agents use, with ESLint and Prettier set up.

## How a template works

Everything in this repository is copied into the new Container repository at the
same path, except `.c4factory/`, which is input for the factory. Files ending in
`.tmpl` are rendered with Go `text/template` (missing keys are errors) and lose the
suffix; a `.tmpl` that renders to only whitespace is left out. Other files are copied
byte for byte. Paths are never rendered, only contents.

Data: `.Project`, `.SoftwareSystem`, `.Container`, `.DisplayName`, `.Type`, the chosen
`.Framework`, `.Standard`, `.Compute` and `.Deployment` (empty when none is chosen),
`.Connectors` (sorted names) and `.Operations` (the Container's
`spec.operations`, defaults filled in). Functions: `has`, `snake`, `upper`,
`lower`, `dart`, `ts` (a TypeScript string literal, quoted as Prettier
quotes it), plus the `text/template` builtins. `infra/operations.ts.tmpl` is
the one TypeScript file rendered: the CDK app reads the stack's name and
description from `package.json`.

Each path belongs to exactly one layer; `.devcontainer/**` and `.c4factory.yaml`
belong to the operator. Release a change by tagging a new version and bumping it in
`templates/versions.txt` of C4 Factory; a tag is never moved.
