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
  `*events.APIGatewayV2HTTPRequest` to `answer` in `src/api.go`, which routes
  `PUT` and `GET /items/{id}`, validates input and answers with status codes;
  `src/items.go` holds the rules and imports neither the SDK nor
  `aws-lambda-go`; `src/item_store.go` wraps DynamoDB behind the `ItemStore`
  interface, over an interface of the two client methods it calls. Tests
  next to the code (`*_test.go`) use a fake store and a fake client; none
  calls AWS.
- **Infrastructure:** `infra/stacks.ts` exports `defineStacks(app: App):
  Stack[]`, which adds one stack named after the package (the Container):
  `infra/api_stack.ts`, the same resources as aws-lambda-ts's (an on-demand,
  KMS-encrypted DynamoDB table with point-in-time recovery, deletion
  protection and `RETAIN`; an HTTP API with IAM authorization on every route,
  a throttled `$default` stage and an access log kept one month), with the
  function a `Function` on `provided.al2023`, handler `bootstrap`, arm64,
  256 MB, 10 s, JSON logs to its own log group kept one month, its own role
  granted only `GetItem` and `PutItem` on the table and writes to its log
  group. It passes CDK Nag's `AwsSolutions` checks with no acknowledgement.
  `infra/api_stack.test.ts` asserts it with `Template.fromStack`, and that
  the asset is one `bootstrap` ELF binary for AArch64.
- **Build:** `Code.fromAsset('src')` with local bundling runs the installed
  Go: `go build -trimpath -buildvcs=false -tags lambda.norpc -ldflags '-s -w'`
  with `GOOS=linux`, `GOARCH=arm64`, `CGO_ENABLED=0`, writing `bootstrap`.
  A failed build throws, so synth fails; Docker is never used (the bundling
  image is required by the type and never pulled). The asset hash is the
  binary's (`AssetHashType.OUTPUT`). `cdk.json` is aws-lambda-ts's.
- **Writes:** `package.json`, `package-lock.json`, `tsconfig.json`,
  `vitest.config.mts`, `cdk.json`, `src/` (with `go.mod` and `go.sum`),
  `infra/stacks.ts`, `infra/api_stack.ts`, `infra/api_stack.test.ts`,
  `README.md`, `CHANGELOG.md`, `.gitignore`
- **Package:** `name` is the Container, version `0.0.1`, private and
  `UNLICENSED`, an ES module, `engines.node` `>=22`; only
  `devDependencies`: the CDK, CDK Nag, tsx and the Standard's tools, at
  aws-lambda-ts's versions. `allowScripts` lets esbuild's install script run
  (tsx needs esbuild).
- **Go modules:** `src/go.mod` requires github.com/aws/aws-lambda-go
  v1.55.1, github.com/aws/aws-sdk-go-v2 v1.47.3, its `config` v1.33.9 and
  `service/dynamodb` v1.70.3, go 1.26.0; `src/go.sum` is committed, so
  `go mod tidy -diff` passes on the first commit.
- **Tests:** `go test` runs the function's; vitest runs `infra/**/*.test.ts`
  in Node, with a 120-second hook timeout, since each test file synthesizes
  its stacks in `beforeAll` and builds the function (a minute with a cold Go
  build cache).
- **Type check:** `tsconfig.json` is aws-lambda-ts's, including `infra` and
  `vitest.config.mts` (no `src`, which is Go).
- **Lockfile:** `package-lock.json` is aws-lambda-ts's with the function's
  packages pruned (`npm install --package-lock-only`); only its two `name`
  fields are templated.
- **Standard:** made for `aws-standard` v1.1.0 or later, whose `aws-lambda-go`
  branch adds the Go checks (`go mod tidy -diff`, `golangci-lint fmt`,
  `go vet`, `golangci-lint run`, `go test -race`, all in `src/`), and which
  owns `infra/main.ts`, `infra/standard.ts` and `infra/standard.test.ts`.
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
and `.Connectors` (sorted names). Functions: `has`, `snake`, `upper`, `lower`, `dart`,
plus the `text/template` builtins. No TypeScript file is rendered: the CDK app
reads the stack's name and description from `package.json`. `src/go.mod` is
rendered for its module path; the Go files are `.go.tmpl` only so C4 Factory's
own Go module skips them, and hold no template actions.

Each path belongs to exactly one layer; `.devcontainer/**` and `.c4factory.yaml`
belong to the operator. Release a change by tagging a new version and bumping it in
`templates/versions.txt` of C4 Factory; a tag is never moved.
