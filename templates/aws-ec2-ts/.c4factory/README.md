# aws-ec2-ts (Framework)

An HTTP service on AWS infrastructure in TypeScript: a Node.js server on EC2
instances in an Auto Scaling group behind an internal Application Load
Balancer, keeping items in DynamoDB through the AWS SDK v3, with its
infrastructure as a CDK app. Nothing deploys: the check synthesizes the
CloudFormation offline.

- **Supports:** `service`
- **Layout:** `src/main.ts` reads `TABLE_NAME` and `PORT`, creates the
  DynamoDB document client once and starts the server from `src/server.ts`,
  which reads each request (body capped at 64 KiB, request id from the load
  balancer's `X-Amzn-Trace-Id`), hands it to `answer` in `src/api.ts` and
  logs one JSON line per request; `answer` routes `PUT` and `GET
  /items/{id}` and `GET /health`, validates input and answers with status
  codes. `src/items.ts` holds the rules and imports neither the SDK nor
  `node:http`; `src/item_store.ts` wraps DynamoDB behind the `ItemStore`
  interface. Tests next to the code (`*.test.ts`) use a fake store
  (`src/memory_store.fake.ts`) and a fake document client, and test the
  server over HTTP on a loopback port; none calls AWS. On `SIGTERM` the
  server finishes its requests and the process ends.
- **Operations:** `infra/operations.ts`, rendered from `.Operations` (the
  Container's `spec.operations` with the factory's defaults), exports one
  frozen `OPERATIONS` of the Standard's `Operations` type: `backups`
  (`retentionDays`, `copyToRegion`), `recovery` (`rpo`, `rto`) and `alerts`
  (`email`), each string rendered with `ts`. The factory overwrites it when
  the block changes; it is not a protected path.
- **Infrastructure:** `infra/stacks.ts` exports `defineStacks(app: App, tier:
  Tier): Stack[]`, which adds one stack named after the package (the
  Container, and `<name>-development` in development, the Standard's
  `stackNameFor`), `infra/service_stack.ts`, built for the tier with the
  Standard's `TIER_RULES`, and in production with `copyToRegion` and backups
  set a second, `<name>-backup-copy` in that region (`infra/backups.ts`'s
  `BackupCopyStack`, the copy vault and its key). The service stack has an
  on-demand DynamoDB table (KMS-encrypted; in production point-in-time
  recovery, deletion protection and `RETAIN`, in development none and
  `DESTROY`); a VPC (`10.0.0.0/16`, isolated subnets in two zones, no NAT or
  internet gateway, a flow log of rejected traffic) with gateway endpoints
  for S3 and DynamoDB and interface endpoints for CloudWatch Logs and
  CloudFormation; the instances' own role, granted only `GetItem` and
  `PutItem` on the table, writes to the server's log group,
  `s3:GetObject` on the server's asset and `cloudformation:SignalResource`
  on its stack; a launch template (Amazon Linux 2023, `t4g.small`, IMDSv2
  required, an encrypted 8 GiB gp3 root volume deleted with its instance,
  detailed monitoring) whose user data is `INSTALL_COMMAND`, the download
  and `bootCommands`, with an exit trap that runs `cfn-signal` with the
  script's status; an Auto Scaling group of two to four instances with ELB
  health checks, a cooldown, CPU target tracking at 60 %, a creation policy
  waiting for two signals and a rolling update of one instance at a time
  that keeps two in service and waits for each new one's signal (15
  minutes, `SIGNAL_TIMEOUT`), so a failed or missing signal fails the update
  and CloudFormation rolls back, and scaling notifications to the alarm
  topic; and an internal Application Load Balancer serving HTTPS on 443
  only, from the VPC only, with the certificate of the `CertificateArn`
  parameter and the recommended TLS policy (invalid headers dropped, access
  logs to an SSE-S3 bucket expiring after the tier's log retention,
  `RETAIN`), forwarding to port 8080 with a `GET /health` check. Log groups
  keep a year in production, a month in development. `infra/alarms.ts`
  (an encrypted topic whose key CloudWatch and Auto Scaling's
  service-linked role may use, the `AlertEmail` parameter and its
  conditional subscription, `cdk-monitoring-constructs`' `MonitoringFacade`
  and dashboard, the `AlarmTopicArn` output) builds the alarms, the same in
  both tiers: the load balancer's own 5xx, the instances' 5xx, p99 latency
  over a second and unhealthy targets, the group's CPU over 90 % and fewer
  than two instances in service, and the table's read and write throttles
  and system errors. In production with `retentionDays` over 0,
  `infra/backups.ts`'s `Backups` keeps daily AWS Backup backups of the
  stack's table, named by ARN (never by the Container's tag, which the
  instances, their volumes and the development stack's table carry too),
  for `retentionDays` in a vault with its own key, copied to `copyToRegion`.
  No X-Ray in either tier. The stack outputs `EntryPoint` (the load
  balancer's DNS name). It passes CDK Nag's `AwsSolutions` checks with one
  acknowledgement of its own, with its reason: `AwsSolutions-S1` (no server
  access log) on the access log bucket; `infra/backups.ts` acknowledges AWS
  Backup's managed policy (`AwsSolutions-IAM4`). `infra/service_stack.test.ts`
  asserts it for both tiers with `Template.fromStack`.
- **Boot:** the user data installs `nodejs22`, `amazon-cloudwatch-agent`,
  `logrotate` and `aws-cfn-bootstrap` from the Amazon Linux repositories (in
  S3, through the gateway endpoint), downloads the server, runs `node server.mjs` under systemd as the
  `server` user with `NoNewPrivileges` and a read-only system, restarting it
  however often it stops, ships `/var/log/server/server.log` to the log
  group, one stream per instance, and rotates it with logrotate
  (`copytruncate`, daily or past 100 MB, three kept). Its last command waits
  up to five minutes for the server to answer `/health` on the instance, so
  the exit trap signals success only for a healthy server.
- **Server:** a request target that isn't a path (`//x`, `*`) is answered
  400, a client that hangs up mid-body is dropped, and a bug in serving one
  request is logged and closes its connection; none ends the process.
- **Writes:** `package.json`, `package-lock.json`, `tsconfig.json`,
  `vitest.config.mts`, `cdk.json`, `src/`, `infra/stacks.ts`,
  `infra/service_stack.ts`, `infra/service_stack.test.ts`,
  `infra/server_asset.ts`, `infra/alarms.ts`, `infra/backups.ts`,
  `infra/operations.ts`, `README.md`, `CHANGELOG.md`, `.gitignore`
- **Package:** `name` is the Container, version `0.0.1`, private and
  `UNLICENSED`, an ES module, `engines.node` `>=22`. The SDK clients are
  `dependencies`, which esbuild bundles into the server; the CDK, CDK Nag,
  `cdk-monitoring-constructs` 10.3.2, tsx and the Standard's tools are
  `devDependencies`. `npm start` runs the
  server with tsx.
- **Build:** `infra/server_asset.ts` makes an S3 asset whose local bundling
  runs the installed esbuild on `src/main.ts` into one ESM file,
  `server.mjs` (Node 22, with a `require` for the SDK's CommonJS parts, not
  minified so a stack trace reads); never Docker. `cdk.json`'s `app` runs
  `infra/main.ts` with tsx, and its context holds the feature flags `cdk
  init` of aws-cdk 2.1145.0 recommends.
- **Tests:** vitest runs `src/**/*.test.ts` and `infra/**/*.test.ts` in
  Node, with a 60-second timeout for tests and 120 seconds for hooks, since
  the Standard's test synthesizes the app for both tiers and runs CDK Nag in
  its `beforeAll`.
- **Type check:** `tsconfig.json` is `strict` with `noUncheckedIndexedAccess`
  and the other strict flags, Node's types and `resolveJsonModule`;
  `noEmit`. It includes `src`, `infra` and `vitest.config.mts`.
  `exactOptionalPropertyTypes` is off, unlike aws-lambda-ts: aws-cdk-lib's
  `Vpc` and `Bucket` don't satisfy `IVpc` and `IBucket` under it.
- **Lockfile:** `package-lock.json` was generated by npm 11 on Node 24; only its
  two `name` fields are templated, so `npm ci` installs exactly the versions
  in `package.json`. `allowScripts` lets esbuild's install script run.
- **Standard:** made for `aws-standard` (v1.11.0 or later, which has this
  Framework's tiers, the load balancer's and group's alarms in its test, and
  its runbook), which owns `infra/main.ts`, `infra/standard.ts` (the tiers,
  `TIER_RULES`, the `Operations` type this Framework imports) and
  `infra/standard.test.ts`. Lists its tools in `devDependencies` with exact
  versions, the same as aws-lambda-ts: `eslint` 10.11.0, `@eslint/js`
  10.0.1, `typescript-eslint` 8.71.0, `prettier` 3.9.9, `typescript` 6.0.3,
  `vitest` 5.0.3, `esbuild` 0.28.2, `aws-cdk-lib` 2.273.0, `constructs`
  10.8.1, `aws-cdk` 2.1145.0, `cdk-nag` 3.0.2 and `tsx` 4.23.15.
- **Not checked before release:** nothing boots an instance or deploys.
  That the Amazon Linux repositories carry `nodejs22`, the CloudWatch agent
  and `aws-cfn-bootstrap`, that they are reachable through the S3 endpoint,
  that `cfn-signal` reaches CloudFormation through its endpoint and a
  rolling update stops on a failed signal, that Auto Scaling may publish to
  the encrypted topic, that the server answers through the load balancer,
  and that a backup restores can only be seen in an AWS account.
- **History:** v1.1.0 (with `aws-standard` v1.11.0) builds for a tier, renders
  `infra/operations.ts`, keeps and backs up the table in production, alarms
  to a topic with a dashboard, serves HTTPS with `CertificateArn` instead of
  HTTP, waits for `cfn-signal` in a rolling update, and sends the group's
  scaling notifications to the alarm topic instead of acknowledging
  `AwsSolutions-AS3`.
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
`lower`, `dart`, `ts` (a TypeScript string literal, quoted as Prettier quotes
it), plus the `text/template` builtins. `infra/operations.ts.tmpl` is the one
TypeScript file rendered: the CDK app reads the stack's name and description
from `package.json`.

Each path belongs to exactly one layer; `.devcontainer/**` and `.c4factory.yaml`
belong to the operator. Release a change by tagging a new version and bumping it in
`templates/versions.txt` of C4 Factory; a tag is never moved.
