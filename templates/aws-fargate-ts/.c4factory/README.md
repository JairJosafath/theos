# aws-fargate-ts (Framework)

An HTTP service on AWS in TypeScript: a Node server in a container on Amazon
ECS on Fargate behind an Application Load Balancer, keeping items in DynamoDB
through the AWS SDK v3, with its infrastructure as a CDK app. Nothing deploys:
the check synthesizes the CloudFormation offline, and the image is a
container asset that synth only stages.

- **Supports:** `service`
- **Layout:** `src/main.ts` reads `TABLE_NAME` and `PORT` (8080 by default),
  creates the DynamoDB document client once, starts Node's HTTP server with
  keep-alive longer than the load balancer's idle timeout, and closes it on
  `SIGTERM`; `src/server.ts` reads each request (at most 64 KiB; 413 above, closing the connection)
  through narrow `Incoming` and `Outgoing` interfaces and hands it to
  `answer` in `src/api.ts`, which routes `GET /health`, `PUT` and
  `GET /items/{id}`, validates input and answers with status codes;
  `src/items.ts` holds the rules and imports neither the SDK nor Node's HTTP
  types; `src/item_store.ts` wraps DynamoDB behind the `ItemStore` interface
  (`items.ts` and `item_store.ts` are aws-lambda-ts's). Tests next to the code
  (`*.test.ts`) use a fake store, a fake document client and in-memory
  streams; none calls AWS or opens a socket.
- **Image:** `Dockerfile` builds on `node:24-alpine`, the build stage on the
  builder's own platform (`--platform=$BUILDPLATFORM`, so an x86 machine
  builds the arm64 image without emulation): `npm ci`, then
  `npm run bundle` (esbuild, one CommonJS file with a source map), and runs
  `node dist/main.cjs` as the `node` user, on a read-only root file system.
  `.dockerignore` lets in only the `Dockerfile`, `package.json`,
  `package-lock.json`, `tsconfig.json` and `src/` without its tests, so synth
  stages a small context and an `infra/` change leaves the image as it was.
- **Infrastructure:** `infra/stacks.ts` exports `defineStacks(app: App):
  Stack[]`, which adds one stack named after the package (the Container):
  `infra/service_stack.ts`, with a `CertificateArn` parameter (the deployer's
  ACM certificate, so synth needs no account); a VPC over two zones with
  public and private subnets, one NAT gateway, a DynamoDB gateway endpoint
  and rejected traffic logged to a log group kept one month (the default
  security group left as it is: restricting it takes a custom-resource
  Lambda); an on-demand DynamoDB table (KMS-encrypted, point-in-time
  recovery, deletion protection, `RETAIN`); a cluster with Container
  Insights; an arm64 Fargate task (0.25 vCPU, 512 MiB) whose container is
  `ContainerImage.fromAsset` for `linux/arm64`, logs to its own log group kept
  one month, and whose task role may only `GetItem` and `PutItem` on the
  table; a service of two tasks in the private subnets, without public IPs,
  with the deployment circuit breaker and rollback, scaling on CPU from two
  to four; and an internet-facing Application Load Balancer dropping invalid
  headers, serving HTTPS on 443 with the `RECOMMENDED_TLS` policy, redirecting
  80 to 443, checking `/health`, and writing access logs to an S3 bucket
  (S3-managed keys, TLS only, objects expire after 30 days, `RETAIN`) through
  the log delivery service principal, not CDK's region-bound
  `logAccessLogs`. It passes CDK Nag's `AwsSolutions` checks with four
  acknowledgements, each with its reason: `ECS2` (the one variable is the
  table's name), `IAM5[Resource::*]` on the execution role
  (`ecr:GetAuthorizationToken`), `S1` on the log bucket and `EC23` on the load
  balancer's security group (a public service). `infra/service_stack.test.ts`
  asserts it with `Template.fromStack`.
- **Writes:** `package.json`, `package-lock.json`, `tsconfig.json`,
  `vitest.config.mts`, `cdk.json`, `Dockerfile`, `.dockerignore`, `src/`,
  `infra/stacks.ts`, `infra/service_stack.ts`, `infra/service_stack.test.ts`,
  `README.md`, `CHANGELOG.md`, `.gitignore`
- **Deployment:** none. The CDK app is the deployment, so a Container on this
  Framework takes no Compute and no Deployment layer; `k8s-manifests` would
  add Kubernetes manifests for an image it doesn't build.
- **Package:** `name` is the Container, version `0.0.1`, private and
  `UNLICENSED`, an ES module, `engines.node` `>=22`. The SDK clients are
  `dependencies`, which esbuild bundles into the image; the CDK, CDK Nag, tsx
  and the Standard's tools are `devDependencies`.
- **Build:** `npm run bundle` writes `dist/main.cjs`, which `.gitignore`
  lists; the image runs its own bundle. `cdk.json`'s `app` runs
  `infra/main.ts` with tsx, and its context holds the feature flags `cdk init`
  of aws-cdk 2.1145.0 recommends (aws-lambda-ts's file).
- **Tests:** vitest runs `src/**/*.test.ts` and `infra/**/*.test.ts` in
  Node, with a 60-second timeout for the tests that synthesize a stack.
- **Type check:** `tsconfig.json` is aws-lambda-ts's (`strict` with
  `noUncheckedIndexedAccess` and the other strict flags) without
  `exactOptionalPropertyTypes`: under it aws-cdk-lib's classes don't satisfy
  their own interfaces (a `Vpc` is no `IVpc`).
- **Lockfile:** `package-lock.json` was generated by npm 11 on Node 24; only its
  two `name` fields are templated, so `npm ci` installs exactly the versions
  in `package.json`, the same as aws-lambda-ts's but for `@types/aws-lambda`.
  `allowScripts` lets esbuild's install script run.
- **Standard:** made for `aws-standard` (v1.2.0 or later), which owns
  `infra/main.ts`, `infra/standard.ts` and `infra/standard.test.ts`. Lists its
  tools in `devDependencies` with exact versions: `eslint` 10.11.0,
  `@eslint/js` 10.0.1, `typescript-eslint` 8.71.0, `prettier` 3.9.9,
  `typescript` 6.0.3, `vitest` 5.0.3, `esbuild` 0.28.2, `aws-cdk-lib`
  2.273.0, `constructs` 10.8.1, `aws-cdk` 2.1145.0, `cdk-nag` 3.0.2 and `tsx`
  4.23.15.
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
and `.Connectors` (sorted names). Functions: `has`, `snake`, `upper`, `lower`, `dart`,
plus the `text/template` builtins. No TypeScript file is rendered: the CDK app
reads the stack's name and description from `package.json`.

Each path belongs to exactly one layer; `.devcontainer/**` and `.c4factory.yaml`
belong to the operator. Release a change by tagging a new version and bumping it in
`templates/versions.txt` of C4 Factory; a tag is never moved.
