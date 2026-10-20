# aws-lambda-py (Framework)

A serverless HTTP API on AWS in Python: a Lambda function using boto3 behind
an API Gateway HTTP API, keeping items in DynamoDB, with its infrastructure
as a CDK app in TypeScript. Nothing deploys: the check synthesizes the
CloudFormation offline. The same API as `aws-lambda-ts`, in Python.

- **Supports:** `function`
- **Layout:** `app/handler.py` (the handler `app.handler.handler`) creates
  boto3's DynamoDB `Table` resource, Powertools' `Logger` and `Metrics` and
  the idempotent PUT at module scope, reads `TABLE_NAME` and
  `IDEMPOTENCY_TABLE_NAME`, and passes each HTTP API event (payload 2.0,
  typed as a `TypedDict`) to `answer` in `app/api.py` with a `Route`: for
  `PUT /items/{id}`, `app/once.py`'s `once_per_key` around `route`, which
  runs it through Powertools' `idempotent_function` (keyed by the
  `Idempotency-Key` header, validated against the path and body; a request
  without the header is just run), raises a refusal out of it so no record
  of it is kept, and answers an in-progress retry 409 and a reused key 422;
  `route` itself for the rest. `route` checks input and answers with status
  codes, raising a refusal as `InvalidInputError` and raising on what the
  store raises; `answer` answers an `InvalidInputError` 400, and logs
  anything else with `Logger` and answers it 500. The handler is decorated with `Logger`'s `inject_lambda_context`
  and `Metrics`' `log_metrics` (the cold start captured, published every
  invocation) and counts `AnsweredRequests` and `FailedRequests`.
  `app/items.py` holds the rules and imports neither boto3 nor Powertools;
  `app/item_store.py` wraps the table behind the `ItemStore` protocol, and
  takes any object with boto3's `get_item` and `put_item`, typed with the
  `types-boto3` stubs. pytest tests in `tests/` use a fake store, a fake
  table and Powertools' records in memory (a `BasePersistenceLayer` over a
  dict); none calls AWS.
- **Operations:** `infra/operations.ts`, rendered from `.Operations` (the
  Container's `spec.operations` with the factory's defaults), exports one
  frozen `OPERATIONS` of the Standard's `Operations` type: `backups`
  (`retentionDays`, `copyToRegion`), `recovery` (`rpo`, `rto`) and `alerts`
  (`email`), each string rendered with `ts`. The factory overwrites it when
  the block changes; it is not a protected path.
- **Infrastructure:** `infra/stacks.ts` exports `defineStacks(app: App, tier:
  Tier): Stack[]`, which adds one stack named after the package (the
  Container, and `<name>-development` in development, the Standard's
  `stackNameFor`), `infra/api_stack.ts`, built for the tier with the
  Standard's `TIER_RULES`, and in production with `copyToRegion` and
  backups set a second, `<name>-backup-copy` in that region
  (`infra/backups.ts`'s `BackupCopyStack`: the vault the backups are copied
  to, named `<name>-copies`, a name over 43 characters cut with a hash, with
  its own key, both `RETAIN`), which the first depends on. The stack has
  two on-demand DynamoDB tables, the items and Powertools' idempotency
  records (TTL `expiration`), KMS-encrypted, with point-in-time recovery,
  deletion protection and `RETAIN` in production and none of them,
  `DESTROY`, in development; a Lambda `Function` (Python 3.14, arm64,
  256 MB, 10 s, JSON logs to its own log group, X-Ray `Active` in
  production only, its own role granted only `GetItem` and `PutItem` on the
  items, `GetItem`, `PutItem`, `UpdateItem` and `DeleteItem` on the
  idempotency records, and writes to its log group); an HTTP API with IAM
  authorization on every route, a throttled `$default` stage and an access
  log; every log group kept one year in production and one month in
  development. `infra/alarms.ts`'s `Alarms` makes the alarm topic
  (encrypted with its own key, which CloudWatch may use, SSL only), the
  `AlertEmail` parameter (default `OPERATIONS.alerts.email`) and an email
  subscription under the condition `HasAlertEmail`, and
  `cdk-monitoring-constructs`' `MonitoringFacade` with a dashboard named
  after the stack: alarms on the API's 5xx, the function's errors,
  throttles and p99 duration (over 8 s of its 10), and each table's read
  and write throttles and system errors, each on one bad five-minute
  period. In production with `retentionDays` over 0, `infra/backups.ts`'s
  `Backups` adds an AWS Backup vault with its own key (both `RETAIN`), a
  daily plan named after the stack keeping `retentionDays` and copying to
  `copyToRegion`'s vault when it is set, and a selection naming the
  `Items` table by ARN, never the `Idempotency` table, whose records
  expire, nor by the Container's tag, which a `<name>-development` stack in
  the same account carries too (#1267) (its role's AWS managed policy
  acknowledged, `AwsSolutions-IAM4`). The stack outputs `EntryPoint` (the API's URL) and
  `AlarmTopicArn`. `infra/api_stack.test.ts` asserts it with
  `Template.fromStack` for both tiers. `infra/stacks.ts`, `infra/alarms.ts`,
  `infra/backups.ts` and `infra/operations.ts` are aws-lambda-ts's.
- **Writes:** `pyproject.toml`, `uv.lock`, `.python-version`, `app/`,
  `tests/`, `package.json`, `package-lock.json`, `tsconfig.json`,
  `vitest.config.mts`, `cdk.json`, `infra/stacks.ts`, `infra/api_stack.ts`,
  `infra/api_stack.test.ts`, `infra/alarms.ts`, `infra/backups.ts`,
  `infra/operations.ts`, `README.md`, `CHANGELOG.md`, `.gitignore`
- **Python project:** `name` is the Container, version `0.0.1`,
  `requires-python >=3.14` (`.python-version` 3.14, which uv installs on
  first use), `[tool.uv] package = false`: nothing builds a wheel.
  `aws-lambda-powertools` 3.36.0 and boto3 1.43.112 are the
  `dependencies`; the `dev` group pins ruff 0.16.10, mypy 2.4.0 and pytest
  9.1.1 (python-standard's) and `types-boto3[dynamodb]` 1.43.112. pytest
  reads `tests/` with the repository root on the path.
- **Build:** the stack's code is `Code.fromAsset('app')` with local
  bundling: `uv export --frozen --no-dev` lists what `uv.lock` pins, and
  `uv pip install --target --no-deps --only-binary :all:` installs those
  wheels for `aarch64-manylinux_2_28` and Python 3.14 next to a copy of
  `app/`. boto3 and Powertools ship in the bundle, not the runtime's copy
  or a layer. The local bundler returns true or throws, so CDK never falls
  back to Docker. uv reads its cache, which `uv sync` filled, or PyPI for a
  wheel the cache lacks. The asset's hash is its output's, so a change to
  `uv.lock` is a new asset. `cdk.json`'s `app` runs `infra/main.ts` with
  tsx, and its context holds the feature flags `cdk init` of aws-cdk
  2.1145.0 recommends.
- **Tests:** pytest for the function; vitest runs `infra/**/*.test.ts` in
  Node, with a 60-second timeout for the tests that synthesize a stack (and
  bundle the function) and 180 seconds for a `beforeAll` that synthesizes
  both tiers.
- **Type check:** `tsconfig.json` is aws-lambda-ts's, including `infra` and
  `vitest.config.mts`; the Python is type-checked by the Standard's mypy.
- **Lockfiles:** `uv.lock` was generated by uv 0.12.23 and
  `package-lock.json` by npm 11 on Node 24; only the project's `name` is
  templated in each, so `uv sync --locked` and `npm ci` install exactly the
  pinned versions. `allowScripts` lets esbuild's install script run (tsx
  needs it).
- **Standard:** made for `aws-standard` v1.17.0 or later (since
  this Framework's v1.2.0; the first whose test takes a table left out of the backups), which owns
  `infra/main.ts`, `infra/standard.ts` (the tier, `TIER_RULES` and the
  `Operations` type this Framework imports), `infra/standard.test.ts`,
  `ruff.toml` and `mypy.ini`. `package.json` lists its TypeScript tools
  with exact versions as aws-lambda-ts does, and `cdk-monitoring-constructs`
  10.3.2, without esbuild, `@types/aws-lambda`, the AWS SDK or Powertools
  for TypeScript.
- **History:** v1.2.0 (with `aws-standard` v1.17.0) backs up the stack's
  data tables by ARN instead of everything carrying the Container's tag,
  which reached a `<name>-development` stack's tables too (#1267); the `Idempotency` table, whose records expire, is left
  out.
  v1.2.1's README deploys production with `--all`, as a `copyToRegion`
  makes a second stack, `<name>-backup-copy` (#1465).
- **Devcontainer:** `.c4factory/devcontainer.json` uses `c4factory/go:1.26`,
  the Go, Node and uv toolchain CI and agents use, with ruff, Python, ESLint
  and Prettier set up.

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
the one TypeScript file rendered, and no Python file is: the CDK app reads
the stack's name and description from `package.json`.

Each path belongs to exactly one layer; `.devcontainer/**` and `.c4factory.yaml`
belong to the operator. Release a change by tagging a new version and bumping it in
`templates/versions.txt` of C4 Factory; a tag is never moved.
