# aws-lambda-ts (Framework)

A serverless HTTP API on AWS in TypeScript: a Lambda function behind an API
Gateway HTTP API, keeping items in DynamoDB through the AWS SDK v3, with its
infrastructure as a CDK app. Nothing deploys: the check synthesizes the
CloudFormation offline.

- **Supports:** `function`
- **Layout:** `src/handler.ts` creates the DynamoDB client, Powertools'
  `Logger` and `Metrics` and the idempotent PUT at module scope, reads
  `TABLE_NAME` and `IDEMPOTENCY_TABLE_NAME`, and passes each
  `APIGatewayProxyEventV2` to `answer` in `src/api.ts` with a `route`:
  for `PUT /items/{id}`, Powertools' `makeIdempotent` around `route` (keyed
  by the `Idempotency-Key` header, validated against the path and body; a
  request without the header is just run), with `src/once.ts` throwing a
  refusal out of it so no record of it is kept and answering an
  in-progress retry 409 and a reused key 422; `route` itself for the rest. `route` checks input and answers with status codes,
  throwing what the store throws; `answer` logs that and answers 500. The
  handler counts `AnsweredRequests` and `FailedRequests` and the cold start
  with `Metrics`. `src/items.ts` holds the rules and imports neither the SDK
  nor the `aws-lambda` types; `src/item_store.ts` wraps DynamoDB behind the
  `ItemStore` interface. Tests next to the code (`*.test.ts`) use a fake
  store and a fake document client; none calls AWS.
- **Operations:** `infra/operations.ts`, rendered from `.Operations` (the
  Container's `spec.operations` with the factory's defaults), exports one
  frozen `OPERATIONS` of the Standard's `Operations` type: `backups`
  (`retentionDays`, `copyToRegion`), `recovery` (`rpo`, `rto`) and `alerts`
  (`email`), each string rendered with `ts`. The factory overwrites it when the block
  changes; it is not a protected path.
- **Infrastructure:** `infra/stacks.ts` exports `defineStacks(app: App, tier:
  Tier): Stack[]`, which adds one stack named after the package (the
  Container, and `<name>-development` in development, the Standard's
  `stackNameFor`), `infra/api_stack.ts`, built for the tier with the
  Standard's `TIER_RULES`, and in production with `copyToRegion` and backups set a
  second, `<name>-backup-copy` in that region (`infra/backups.ts`'s
  `BackupCopyStack`: the vault the backups are copied to, named
  `<name>-copies`, a name over 43 characters cut with a hash, with its own key, both `RETAIN`), which the first depends
  on. The stack has two on-demand DynamoDB tables, the items and
  Powertools' idempotency records (TTL `expiration`), KMS-encrypted, with
  point-in-time recovery, deletion protection and `RETAIN` in production
  and none of them, `DESTROY`, in development; a `NodejsFunction`
  (Node.js 24, arm64, 256 MB, 10 s, JSON logs to its own log group, X-Ray
  `Active` in production only, its own role granted only `GetItem` and
  `PutItem` on the items, `GetItem`, `PutItem`, `UpdateItem` and
  `DeleteItem` on the idempotency records, and writes to its log group); an
  HTTP API with IAM authorization on every route, a throttled `$default`
  stage and an access log; every log group kept one year in production and
  one month in development. `infra/alarms.ts`'s `Alarms` makes the alarm
  topic (encrypted with its own key, which CloudWatch may use, SSL only),
  the `AlertEmail` parameter (default `OPERATIONS.alerts.email`) and an
  email subscription under the condition `HasAlertEmail`, and
  `cdk-monitoring-constructs`' `MonitoringFacade` with a dashboard named
  after the stack: alarms on the API's 5xx, the function's errors,
  throttles and p99 duration (over 8 s of its 10), and each table's read
  and write throttles and system errors, each on one bad five-minute
  period. In production with `retentionDays` over 0, `infra/backups.ts`'s
  `Backups` adds an AWS Backup vault with its own key (both `RETAIN`), a
  daily plan named after the stack keeping `retentionDays` and copying to
  `copyToRegion`'s vault when it is set, and a selection naming the
  `Items` table and each data store's by ARN, never the `Idempotency`
  table, whose records expire, nor by the Container's tag, which a
  `<name>-development` stack in the same account carries too (#1267) (its
  role's AWS managed policy acknowledged, `AwsSolutions-IAM4`). The stack outputs `EntryPoint` (the API's URL) and
  `AlarmTopicArn`. `infra/api_stack.test.ts` asserts it with
  `Template.fromStack` for both tiers.
- **Data stores:** `infra/connectors.ts`, rendered from `.Connectors`, exports
  `DATA_STORES`: one entry per Connector of a DataStore the Container's
  Relationships name that this Framework knows (`dynamodb`, whose
  `infra/connector_dynamodb.ts` makes the table, and since v1.5.0 `rds`,
  whose `infra/connector_rds.ts` makes an Aurora Serverless v2 PostgreSQL
  cluster), and none otherwise. A `DataStore` is a `Table` or a
  `DatabaseCluster`. The stack calls each entry with the function and the
  tier's `TIER_RULES`, so the Connector makes its store, grants the function
  its calls and hands it the store's name or ARNs; the stack alarms on a
  table like its own, on a cluster by its CPU (90%) and its connections (nine
  tenths of what its largest capacity allows, `maxConnections`), and the
  backup plan names each by ARN (`Arn`, `DBClusterArn`) with the `Items`
  table.
- **Writes:** `package.json`, `package-lock.json`, `tsconfig.json`,
  `vitest.config.mts`, `cdk.json`, `src/`, `infra/stacks.ts`,
  `infra/api_stack.ts`, `infra/api_stack.test.ts`, `infra/alarms.ts`,
  `infra/backups.ts`, `infra/operations.ts`, `infra/connectors.ts`, `README.md`, `CHANGELOG.md`,
  `.gitignore`
- **Package:** `name` is the Container, version `0.0.1`, private and
  licensed as the repository is scaffolded (`UNLICENSED` with no license, #73), an ES module, `engines.node` `>=22`. The SDK clients and
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
- **Standard:** made for `aws-standard` v1.17.0 or later (since
  this Framework's v1.3.0; the first whose test takes a table left out of the backups), which owns
  `infra/main.ts`, `infra/standard.ts` (the tier, `TIER_RULES` and the
  `Operations` type this Framework imports) and `infra/standard.test.ts`. Lists its tools in
  `devDependencies` with exact versions: `eslint` 10.11.0, `@eslint/js`
  10.0.1, `typescript-eslint` 8.71.0, `prettier` 3.9.9, `typescript` 6.0.3,
  `vitest` 5.0.3, `esbuild` 0.28.2, `aws-cdk-lib` 2.273.0, `constructs`
  10.8.1, `aws-cdk` 2.1145.0, `cdk-nag` 3.0.2 and `tsx` 4.23.15. TypeScript
  is 6.0.3 because typescript-eslint 8.71 supports `<6.1.0`.
- **History:** v1.3.0 (with `aws-standard` v1.17.0) backs up the stack's
  data tables by ARN instead of everything carrying the Container's tag,
  which reached a `<name>-development` stack's tables too (#1267); the `Idempotency` table, whose records expire, is left
  out.
  v1.3.1's README deploys production with `--all`, as a `copyToRegion`
  makes a second stack, `<name>-backup-copy` (#1465).
  v1.4.0 writes the HTTP API stage's access log as a JSON object per
  request (`ACCESS_LOG_FIELDS` in `infra/api_stack.ts`), where it wrote the
  Common Log Format, so Logs Insights queries it by field (#1420).
- **Devcontainer:** `.theos/devcontainer.json` uses `theos/go:1.26`, the
  Go and Node toolchain CI and agents use, with ESLint and Prettier set up.

## How a template works

Everything in this repository is copied into the new Container repository at the
same path, except `.theos/`, which is input for the factory. Files ending in
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

Each path belongs to exactly one layer; `.devcontainer/**` and `.theos.yaml`
belong to the operator. Release a change by tagging a new version and bumping it in
`templates/versions.txt` of theos; a tag is never moved.
