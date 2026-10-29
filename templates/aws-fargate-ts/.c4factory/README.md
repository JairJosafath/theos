# aws-fargate-ts (Framework)

An HTTP service on AWS in TypeScript: a Node server in a container on Amazon
ECS on Fargate behind an Application Load Balancer, keeping items in DynamoDB
through the AWS SDK v3, with its infrastructure as a CDK app. Nothing deploys:
the check synthesizes the CloudFormation offline, and the image is a
container asset that synth only stages.

- **Supports:** `service`
- **Layout:** `src/main.ts` imports `src/start_tracing.ts` first, which
  calls `startTracing(process.env)` from `src/tracing.ts`: when
  `OTEL_EXPORTER_OTLP_ENDPOINT` is set, an OpenTelemetry `NodeTracerProvider`
  for `OTEL_SERVICE_NAME` with a batch processor and the OTLP over HTTP
  exporter to `<endpoint>/v1/traces`, and the HTTP instrumentation (health
  checks on `/health` ignored), which patches `node:http` and `node:https`
  as they load: they stay outside esbuild's bundle, so the import order is
  what makes it work; unset, nothing starts. Then `src/main.ts` reads `TABLE_NAME`, `PORT` (8080 by default),
  `USER_POOL_ID` and `USER_POOL_CLIENT_ID` (it stops when a pool variable is
  missing), creates the DynamoDB document client and the token verifier once, starts Node's HTTP server with
  keep-alive longer than the load balancer's idle timeout, and closes it on
  `SIGTERM`, shutting tracing down (sending the spans left) once the last request is answered; `src/server.ts` reads each request (at most 64 KiB; 413 above, closing the connection)
  through narrow `Incoming` and `Outgoing` interfaces and hands it to
  `answer` in `src/api.ts`; every route but `GET /health` needs
  `Authorization: Bearer <token>`, an access token of the stack's user pool
  issued to its app client, which `src/auth.ts` verifies with
  `aws-jwt-verify` (signature against the pool's keys, issuer, expiry,
  `token_use`, `client_id`), and is answered 401 without one (500 when
  the pool's keys can't be fetched); `answer` routes `GET /health`, `PUT` and
  `GET /items/{id}`, validates input and answers with status codes;
  `src/items.ts` holds the rules and imports neither the SDK nor Node's HTTP
  types; `src/item_store.ts` wraps DynamoDB behind the `ItemStore` interface
  (`items.ts` and `item_store.ts` are aws-lambda-ts's). Tests next to the code
  (`*.test.ts`) use a fake store, a fake document client and in-memory
  streams; none calls AWS or opens a socket. `src/tracing.test.ts` checks
  that tracing starts only when the endpoint is set.
- **Image:** `Dockerfile` builds on `node:24-alpine`, the build stage on the
  builder's own platform (`--platform=$BUILDPLATFORM`, so an x86 machine
  builds the arm64 image without emulation): `npm ci`, then
  `npm run bundle` (esbuild, one CommonJS file with a source map), and runs
  `node dist/main.cjs` as the `node` user, on a read-only root file system.
  `.dockerignore` lets in only the `Dockerfile`, `package.json`,
  `package-lock.json`, `tsconfig.json` and `src/` without its tests, so synth
  stages a small context and an `infra/` change leaves the image as it was.
- **Operations:** `infra/operations.ts`, rendered from `.Operations` (the
  Container's `spec.operations` with the factory's defaults), exports one
  frozen `OPERATIONS` of the Standard's `Operations` type: `backups`
  (`retentionDays`, `copyToRegion`), `recovery` (`rpo`, `rto`) and `alerts`
  (`email`), each string rendered with `ts`. The factory overwrites it when
  the block changes; it is not a protected path. aws-lambda-ts's file.
- **Infrastructure:** `infra/stacks.ts` exports `defineStacks(app: App, tier:
  Tier): Stack[]`, which adds one stack named after the package (the
  Container, and `<name>-development` in development, the Standard's
  `stackNameFor`), `infra/service_stack.ts`, built for the tier with the
  Standard's `TIER_RULES`, and in production with `copyToRegion` and backups
  set a second, `<name>-backup-copy` in that region (`infra/backups.ts`'s
  `BackupCopyStack`), which the first depends on, as aws-lambda-ts does.
  The stack has a `CertificateArn` parameter (the deployer's ACM
  certificate, so synth needs no account); a VPC over two zones with public
  and private subnets, a NAT gateway in each zone in production and one in
  development, a DynamoDB gateway endpoint and rejected traffic logged to a
  log group (the default security group left as it is: restricting it takes
  a custom-resource Lambda); an on-demand DynamoDB table (KMS-encrypted;
  point-in-time recovery, deletion protection and `RETAIN` in production,
  none of them, `DESTROY`, in development); a cluster with Container
  Insights; an arm64 Fargate task whose app container (256 CPU units and a
  512 MiB reservation, the task's whole size in development) is
  `ContainerImage.fromAsset` for `linux/arm64`, logs to its own log group,
  and whose task role may only `GetItem` and `PutItem` on the table; in
  production the task also runs the AWS Distro for OpenTelemetry collector
  (`public.ecr.aws/aws-observability/aws-otel-collector:v0.50.0`, its
  image's `/etc/ecs/ecs-xray.yaml`: OTLP in, X-Ray out), with 256 CPU units
  and a hard limit of 512 MiB of its own added to the task (0.5 vCPU, 1 GB),
  so the app has the room it has in development, not essential, on a
  read-only root file system, logging to its own log group; the app
  container starts after it and gets `OTEL_EXPORTER_OTLP_ENDPOINT`
  (`http://localhost:4318`) and `OTEL_SERVICE_NAME` (the stack's name), and
  the task role `xray:PutTraceSegments` and `xray:PutTelemetryRecords` (the
  Standard acknowledges their `*`). A service of two tasks in the private
  subnets, without public IPs, with the deployment circuit breaker and
  rollback and deployment alarms that roll back (`ROLLBACK_ON_ALARM`) on the
  load balancer's and the tasks' 5xx over 5 % of the requests for three
  one-minute periods in a row and on an unhealthy task (the Maximum of
  `UnHealthyHostCount` in two one-minute periods of three), named by their
  literal names (a reference would make the alarm and the service depend on
  each other), scaling on CPU from two to four. `cdk.json` keeps
  `@aws-cdk/aws-ecs:removeDefaultDeploymentAlarm`: it only governs a
  service that names no alarm. An internet-facing Application Load Balancer
  dropping invalid headers, serving HTTPS on 443 with the `RECOMMENDED_TLS`
  policy, redirecting 80 to 443, checking `/health`, and writing access
  logs to an S3 bucket (S3-managed keys, TLS only, objects expire after the
  tier's log retention, `RETAIN` in both tiers: logs, not data, and a bucket
  holding objects can't be deleted) through the log delivery service
  principal, not CDK's region-bound `logAccessLogs`. Every log group keeps
  one year in production and one month in development. `infra/alarms.ts`'s
  `Alarms` (aws-lambda-ts's) makes the alarm topic, the `AlertEmail`
  parameter and its subscription under `HasAlertEmail`, and
  `cdk-monitoring-constructs`' `MonitoringFacade` with a dashboard: alarms on
  any of the load balancer's 5xx (`HTTPCode_ELB_5XX_Count`) and the tasks'
  5xx (`HTTPCode_Target_5XX_Count`), as warnings, and on their share of
  `RequestCount` (metric math, the deployment alarms above), p99 latency
  (`TargetResponseTime` over a second), unhealthy tasks, the service's CPU
  and memory (over 90 %) and running tasks (Container Insights'
  `RunningTaskCount` under two, no data counting as too few), and the table's
  read and write throttles and system errors, each on one bad five-minute
  period but the deployment alarms. In production with `retentionDays` over
  0, `infra/backups.ts`'s `Backups` adds the AWS Backup vault, plan and
  selection: aws-lambda-ts's, naming the `Items` table by ARN, so the
  access-log bucket, which is logs, is not in it, nor a
  `<name>-development` stack's table, which carries the same tag (#1267).
  `infra/user_pool.ts`'s `Users` makes the Cognito user pool (no self
  sign-up, email sign-in, a 12-character password policy, optional TOTP MFA;
  in production deletion protection and `RETAIN`) and a public app client
  (password and SRP sign-in, no secret, no OAuth, one-hour access tokens),
  outputs `UserPoolId`, `UserPoolClientId` and `Issuer`, and hands the
  container `USER_POOL_ID` and `USER_POOL_CLIENT_ID`. The stack outputs
  `EntryPoint` (the load balancer's DNS name) and `AlarmTopicArn`. It passes
  CDK Nag's `AwsSolutions` checks with four acknowledgements of its own,
  each with its reason: `ECS2` (the variables are the table's name and the
  collector's address and service name, and the user pool's and client's
  ids), `IAM5[Resource::*]` on the
  execution role (`ecr:GetAuthorizationToken`), `S1` on the log bucket and
  `EC23` on the load balancer's security group (a public service), and
  `IAM4` on AWS Backup's role in `infra/backups.ts`, and `COG2` (MFA
  optional) and `COG8` (no Plus tier) on the user pool in
  `infra/user_pool.ts`.
  `infra/service_stack.test.ts` asserts it with `Template.fromStack` for
  both tiers.
- **Data stores:** `infra/connectors.ts`, rendered from `.Connectors`, exports
  `DATA_STORES`: one entry per Connector of a DataStore the Container's
  Relationships name that this Framework knows (`dynamodb`, whose
  `infra/connector_dynamodb.ts` makes the table, and since v2.2.0 `rds`,
  whose `infra/connector_rds.ts` makes an Aurora Serverless v2 PostgreSQL
  cluster), and none otherwise. A `DataStore` is a `Table` or a
  `DatabaseCluster`. The
  stack calls each entry with a `DataStoreUser` of the task role and the App
  container's `addEnvironment`, and the tier's `TIER_RULES`, so the Connector
  makes its store, grants the role its calls and hands the container the
  store's name or ARNs. The user's `network` is the stack's VPC and the
  service's security group, so a cluster goes in the VPC's private subnets,
  lets the tasks in and hands them `RDS_HOST` and `RDS_PORT` too. The stack
  alarms on a table like its own, on a cluster by its CPU (90%) and its
  connections (nine tenths of what its largest capacity allows,
  `maxConnections`, the alarm a deployment rolls back on), and the backup
  plan names each by ARN (`Arn`, `DBClusterArn`). The `Items` table stays beside it as the
  starter's example; the README says new data goes in the DataStore's table.
  `infra/connectors.ts` is aws-lambda-ts's.
- **Writes:** `package.json`, `package-lock.json`, `tsconfig.json`,
  `vitest.config.mts`, `cdk.json`, `Dockerfile`, `.dockerignore`, `src/`,
  `infra/stacks.ts`, `infra/service_stack.ts`, `infra/service_stack.test.ts`,
  `infra/alarms.ts`, `infra/backups.ts`, `infra/operations.ts`,
  `infra/connectors.ts`, `infra/user_pool.ts`, `README.md`, `CHANGELOG.md`,
  `.gitignore`
- **Deployment:** none. The CDK app is the deployment, so a Container on this
  Framework takes no Compute and no Deployment layer; `k8s-manifests` would
  add Kubernetes manifests for an image it doesn't build.
- **Package:** `name` is the Container, version `0.0.1`, private and
  `UNLICENSED`, an ES module, `engines.node` `>=22`. The SDK clients and
  OpenTelemetry's `api` 1.9.1, `sdk-trace-node` 2.12.0, `resources` 2.12.0,
  `exporter-trace-otlp-http` 0.223.0 and `instrumentation-http` 0.223.0 (one
  release of opentelemetry-js) are `dependencies`, which esbuild bundles
  into the image; the CDK, CDK Nag, `cdk-monitoring-constructs` 10.3.2, tsx
  and the Standard's tools are `devDependencies`.
- **Build:** `npm run bundle` writes `dist/main.cjs`, which `.gitignore`
  lists; the image runs its own bundle. `cdk.json`'s `app` runs
  `infra/main.ts` with tsx, and its context holds the feature flags `cdk init`
  of aws-cdk 2.1145.0 recommends (aws-lambda-ts's file).
- **Tests:** vitest runs `src/**/*.test.ts` and `infra/**/*.test.ts` in
  Node, with a 60-second timeout for the tests that synthesize a stack and
  120 seconds for a `beforeAll` that synthesizes both tiers.
- **Type check:** `tsconfig.json` is aws-lambda-ts's (`strict` with
  `noUncheckedIndexedAccess` and the other strict flags) without
  `exactOptionalPropertyTypes`: under it aws-cdk-lib's classes don't satisfy
  their own interfaces (a `Vpc` is no `IVpc`).
- **Lockfile:** `package-lock.json` was generated by npm 11 on Node 24; only its
  two `name` fields are templated, so `npm ci` installs exactly the versions
  in `package.json`. `allowScripts` lets esbuild's install script run.
- **Standard:** made for `aws-standard` v1.11.0 or later (since
  this Framework's v1.2.0; the first whose test takes a selection by ARN), which owns
  `infra/main.ts`, `infra/standard.ts` (the tier, `TIER_RULES` and the
  `Operations` type this Framework imports) and `infra/standard.test.ts`.
  Lists its tools in `devDependencies` with exact versions: `eslint` 10.11.0,
  `@eslint/js` 10.0.1, `typescript-eslint` 8.71.0, `prettier` 3.9.9,
  `typescript` 6.0.3, `vitest` 5.0.3, `esbuild` 0.28.2, `aws-cdk-lib`
  2.273.0, `constructs` 10.8.1, `aws-cdk` 2.1145.0, `cdk-nag` 3.0.2 and `tsx`
  4.23.15.
- **History:** v1.2.0 (with `aws-standard` v1.17.0, which refuses a tag) backs up the stack's
  data tables by ARN instead of everything carrying the Container's tag,
  which reached a `<name>-development` stack's tables too (#1267). v1.2.1's
  README deploys with `--all` and names the service stack in
  `--parameters <name>:CertificateArn=<arn>`, as a `copyToRegion` makes a
  second stack, which takes no certificate (#1305). v1.3.0 verifies a
  Cognito bearer token on every route but `/health`, with the stack's own
  user pool and app client (#1479). v2.0.0's stack takes its image as the
  `ImageUri` parameter instead of building a Docker asset, so `cdk synth`
  and `make dist` need no Docker; gitea-release v1.7.0 builds and pushes
  the image, and the deployer copies it to ECR by digest (#1610).
  v2.1.0 renders `infra/connectors.ts` and connects the service to each
  `dynamodb` DataStore's table through its task role and the App container's
  environment, alarming on it and backing it up (#1742). v2.2.0 connects
  the service to an `rds` DataStore's cluster in its own VPC (#1926).
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
