# aws-lambda-go (Framework)

A serverless HTTP API on AWS in Go: a Lambda function on the AWS SDK for Go
v2 behind an API Gateway HTTP API, keeping items in DynamoDB, with its
infrastructure as a CDK app in TypeScript. Nothing deploys: the check builds
the function with Go and synthesizes the CloudFormation offline.

- **Supports:** `function`
- **Layout:** the function is its own Go module in `src/` (`src/go.mod`,
  module `c4factory.local/<project>/<container>`), apart from `node_modules/`,
  whose stray `.go` files (aws-cdk's `cdk init` templates, flatted's Go port)
  would otherwise be part of it and break `go mod tidy`. `src/main.go` loads
  the AWS configuration and creates the DynamoDB client once, in `main`,
  reads `TABLE_NAME`, logs JSON with `log/slog` to stderr and calls
  `lambda.Start` with a closure that passes each
  `*events.APIGatewayV2HTTPRequest` to `answer` in `src/api.go`. `answer`
  calls `route`, which routes `PUT` and `GET /items/{id}`, validates input
  and answers with status codes, returning what the store returns as an
  error; `answer` logs that as `The request failed.` with the `requestId`,
  `routeKey` and `error`, and answers 500. There is no Powertools for Go:
  the logs are `slog`'s, the function sends no metrics of its own, and there
  is no idempotency utility or table, since a `PUT` replaces the whole item
  and repeating it is harmless. `src/items.go` holds the rules and imports
  neither the SDK nor `aws-lambda-go`; `src/item_store.go` wraps DynamoDB
  behind the `ItemStore` interface, over an interface of the two client
  methods it calls. Tests next to the code (`*_test.go`) use a fake store
  and a fake client; none calls AWS.
- **Operations:** `infra/operations.ts`, rendered from `.Operations` (the
  Container's `spec.operations` with the factory's defaults), exports one
  frozen `OPERATIONS` of the Standard's `Operations` type: `backups`
  (`retentionDays`, `copyToRegion`), `recovery` (`rpo`, `rto`) and `alerts`
  (`email`), each string rendered with `ts`. The factory overwrites it when
  the block changes; it is not a protected path. It is aws-lambda-ts's.
- **Infrastructure:** `infra/stacks.ts`, `infra/alarms.ts` and
  `infra/backups.ts` are aws-lambda-ts's. `infra/stacks.ts` exports
  `defineStacks(app: App, tier: Tier): Stack[]`, which adds one stack named
  after the package (the Container, and `<name>-development` in
  development, the Standard's `stackNameFor`), `infra/api_stack.ts`, built
  for the tier with the Standard's `TIER_RULES`, and in production with
  `copyToRegion` and backups set a second, `<name>-backup-copy` in that
  region (`infra/backups.ts`'s `BackupCopyStack`: the vault the backups are
  copied to, named `<name>-copies`, a name over 43 characters cut with a
  hash, with its own key, both `RETAIN`), which the first depends on. The
  stack has one on-demand DynamoDB table, the items, KMS-encrypted, with
  point-in-time recovery, deletion protection and `RETAIN` in production
  and none of them, `DESTROY`, in development; a `Function` on
  `provided.al2023`, handler `bootstrap`, arm64, 256 MB, 10 s, JSON logs to
  its own log group, X-Ray `Active` in production only (Lambda's own
  segments; the function adds no subsegments and needs no SDK for it), its
  own role granted only `GetItem` and `PutItem` on the table and writes to
  its log group; an HTTP API with IAM authorization on every route, a
  throttled `$default` stage and an access log; every log group kept one
  year in production and one month in development. `infra/alarms.ts`'s
  `Alarms` makes the alarm topic (encrypted with its own key, which
  CloudWatch may use, SSL only), the `AlertEmail` parameter (default
  `OPERATIONS.alerts.email`) and an email subscription under the condition
  `HasAlertEmail`, and `cdk-monitoring-constructs`' `MonitoringFacade` with
  a dashboard named after the stack: alarms on the API's 5xx, the
  function's errors, throttles and p99 duration (over 8 s of its 10), and
  the table's read and write throttles and system errors, each on one bad
  five-minute period. In production with `retentionDays` over 0,
  `infra/backups.ts`'s `Backups` adds an AWS Backup vault with its own key
  (both `RETAIN`), a daily plan named after the stack keeping
  `retentionDays` and copying to `copyToRegion`'s vault when it is set, and
  a selection naming the `Items` table by ARN, never by the Container's
  tag, which a `<name>-development` stack in the same account carries too
  (#1267) (its role's AWS managed policy acknowledged,
  `AwsSolutions-IAM4`). The stack outputs `EntryPoint`
  (the API's URL) and `AlarmTopicArn`. `infra/api_stack.test.ts` asserts it
  with `Template.fromStack` for both tiers, and that the asset is one
  `bootstrap` ELF binary for AArch64.
- **Build:** `Code.fromAsset('src')` with local bundling runs the installed
  Go: `go build -trimpath -buildvcs=false -tags lambda.norpc -ldflags '-s -w'`
  with `GOOS=linux`, `GOARCH=arm64`, `CGO_ENABLED=0`, writing `bootstrap`.
  A failed build throws, so synth fails; Docker is never used (the bundling
  image is required by the type and never pulled). The asset hash is the
  binary's (`AssetHashType.OUTPUT`). `cdk.json` is aws-lambda-ts's.
- **Data stores:** `infra/connectors.ts`, rendered from `.Connectors`, exports
  `DATA_STORES`: one entry per Connector of a DataStore the Container's
  Relationships name that this Framework knows (`dynamodb`, whose
  `infra/connector_dynamodb.ts` makes the table), and none otherwise. The
  stack calls each entry with the function and the tier's `TIER_RULES`, so the
  Connector makes its table, grants the function its calls and hands it the
  table's name; the stack alarms on the table like its own and backs it up
  by ARN. The `Items` table stays beside it as the starter's example; the
  README says new data goes in the DataStore's table. `infra/connectors.ts`
  is aws-lambda-ts's.
- **Writes:** `package.json`, `package-lock.json`, `tsconfig.json`,
  `vitest.config.mts`, `cdk.json`, `src/` (with `go.mod` and `go.sum`),
  `infra/stacks.ts`, `infra/api_stack.ts`, `infra/api_stack.test.ts`,
  `infra/alarms.ts`, `infra/backups.ts`, `infra/operations.ts`,
  `infra/connectors.ts`, `README.md`,
  `CHANGELOG.md`, `.gitignore`
- **Package:** `name` is the Container, version `0.0.1`, private and
  `UNLICENSED`, an ES module, `engines.node` `>=22`; only
  `devDependencies`: the CDK, CDK Nag, `cdk-monitoring-constructs` 10.3.2,
  tsx and the Standard's tools, at aws-lambda-ts's versions. `allowScripts`
  lets esbuild's install script run (tsx needs esbuild).
- **Go modules:** `src/go.mod` requires github.com/aws/aws-lambda-go
  v1.55.1, github.com/aws/aws-sdk-go-v2 v1.47.3, its `config` v1.33.9 and
  `service/dynamodb` v1.70.3, go 1.26.0; `src/go.sum` is committed, so
  `go mod tidy -diff` passes on the first commit.
- **Tests:** `go test` runs the function's; vitest runs `infra/**/*.test.ts`
  in Node, with a 60-second test timeout and a 180-second hook timeout,
  since each test file synthesizes its stacks for both tiers in `beforeAll`
  and builds the function (20 s with a cold Go build cache on a busy
  12-core machine, a minute or more on a small runner).
- **Type check:** `tsconfig.json` is aws-lambda-ts's, including `infra` and
  `vitest.config.mts` (no `src`, which is Go).
- **Lockfile:** `package-lock.json` is aws-lambda-ts's with the function's
  packages pruned (the SDK and Powertools), kept current with `npm install`
  in a rendered copy; only its two `name` fields are templated.
- **Standard:** made for `aws-standard` v1.11.0 or later (since
  this Framework's v1.2.0; the first whose test takes a selection by ARN), whose `aws-lambda-go`
  branch adds the Go checks (`go mod tidy -diff`, `golangci-lint fmt`,
  `go vet`, `golangci-lint run`, `go test -race`, all in `src/`), and which
  owns `infra/main.ts`, `infra/standard.ts` (the tier, `TIER_RULES` and the
  `Operations` type this Framework imports) and `infra/standard.test.ts`.
- **History:** v1.2.0 (with `aws-standard` v1.17.0, which refuses a tag) backs up the stack's
  data tables by ARN instead of everything carrying the Container's tag,
  which reached a `<name>-development` stack's tables too (#1267).
  v1.2.1's README deploys production with `--all`, as a `copyToRegion`
  makes a second stack, `<name>-backup-copy` (#1465).
  v1.3.0 writes the HTTP API stage's access log as a JSON object per
  request (`ACCESS_LOG_FIELDS` in `infra/api_stack.ts`), where it wrote the
  Common Log Format, so Logs Insights queries it by field (#1420).
  v1.4.0 renders `infra/connectors.ts` and connects the function to each
  `dynamodb` DataStore's table, alarming on it and backing it up (#1740).
- **Devcontainer:** `.c4factory/devcontainer.json` uses `c4factory/go:1.26`, the
  Go and Node toolchain CI and agents use, with the Go extension (gofumpt,
  golangci-lint), ESLint and Prettier set up.

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
description from `package.json`. `src/go.mod` is rendered for its module
path; the Go files are `.go.tmpl` only so C4 Factory's own Go module skips
them, and hold no template actions.

Each path belongs to exactly one layer; `.devcontainer/**` and `.c4factory.yaml`
belong to the operator. Release a change by tagging a new version and bumping it in
`templates/versions.txt` of C4 Factory; a tag is never moved.
