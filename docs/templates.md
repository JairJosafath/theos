# Templates: what each gives the application built from it

A scorecard of the templates under `templates/`, at the versions in
`templates/versions.txt`: one row per template, one column per promise. It
shows the gaps between what a template scaffolds and an application someone
can ship; each gap is an issue. Written on 2026-10-20 for step 6 of
[plans/fresh-start.md](plans/fresh-start.md) (#1430).

## How to read it

A cell is one of:

- **yes**: the template keeps the promise; its section below says where the
  evidence is (a test, a script, a file of the template).
- **#N**: the gap, and the issue that closes it. "yes, #N" keeps the promise
  in part; the issue is the rest.
- **n/a**: the promise doesn't apply to that kind of layer; the section says
  why.

The columns:

| Column | The promise |
| --- | --- |
| CI | A factory test renders the template in a combination of layers (a row of `templateCombos` in `internal/scaffold/templates_test.go`) and runs that repository's own `make check` to green. Rendering alone (`TestEveryTemplateRenders`) doesn't count. |
| Tests | Framework and Connector: the scaffold ships tests of its own code that its CI runs. Standard: its `make check` runs the tests. Compute and Deployment: a factory test checks what they render. |
| Lint | Standard: its `make check` runs linters and type checkers. Framework: a Standard that pairs with it lints it. Compute, Deployment and Connector YAML: `make validate-yaml` builds and validates it. |
| README | The scaffold has a README a person can follow: run, test, ship. |
| Deploy | Framework: a Deployment it pairs with ships it. Deployment: what it ships and how. Connector: it provisions what it connects to. |
| Logging | The app logs, to stdout or stderr, in a form an operator can read; a Standard's guides say how to log. |
| Auth | Where the app serves users, it authenticates them. A Connector's credentials come from a Secret or a grant. |

The webshop (`config/samples/webshop`, #1428) uses the rows marked **W**;
their issues are `priority/2` in the milestone *Reference app: the webshop
ships*, the others `priority/4`. One gap shared by many rows is one issue.

## Scorecard

| Template | Kind | W | CI | Tests | Lint | README | Deploy | Logging | Auth |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| python-fastapi | Framework | W | yes | yes | yes | yes | yes, #1457 #1072 | #1473 | #1474 |
| python-cli | Framework | W | yes | yes | yes | yes | yes | yes | n/a |
| react-vite | Framework | W | yes | yes | yes | yes | yes, #1457 #1072 | n/a | n/a |
| node-cli | Framework | W | yes | yes | yes | yes | yes | yes | n/a |
| python-standard | Standard | W | yes | yes | yes | n/a | n/a | yes | n/a |
| ts-standard | Standard | W | yes | yes | yes | n/a | n/a | yes | n/a |
| postgres | Connector | W | yes | #1472 | yes | n/a | #1471 | n/a | #1471 |
| small | Compute | W | yes | yes | yes | n/a | n/a | n/a | n/a |
| k8s-manifests | Deployment | W | yes | yes | yes | #1470 | yes, #1457 #1072 | n/a | n/a |
| gitea-release | Deployment | W | yes | yes | yes | #1475 | yes | n/a | n/a |
| go-cli | Framework | | #1476 | yes | yes | yes | yes | yes | n/a |
| go-kubebuilder | Framework | | yes | yes | yes | #1477 | yes, #1458 | yes | yes |
| vscode-extension | Framework | | #1476 | yes | yes | yes | yes | #1478 | n/a |
| go-standard | Standard | | #1476 | yes | yes | n/a | n/a | yes | n/a |
| go-operator-standard | Standard | | yes | yes | yes | n/a | n/a | yes | n/a |
| aws-lambda-ts | Framework | | #1476 | yes | yes | yes | #1071 | yes, #1420 | yes |
| aws-lambda-go | Framework | | #1476 | yes | yes | yes | #1071 | yes, #1420 | yes |
| aws-lambda-py | Framework | | #1476 | yes | yes | yes | #1071 | yes, #1420 | yes |
| aws-ec2-ts | Framework | | #1476 | yes | yes | yes | #1071 | yes | #1479 |
| aws-fargate-ts | Framework | | #1476 | yes | yes | yes | #1071 | yes | #1479 |
| aws-queue-ts | Framework | | #1476 | yes | yes | yes | #1071 | yes | n/a |
| aws-schedule-ts | Framework | | #1476 | yes | yes | yes | #1071 | yes | n/a |
| aws-event-ts | Framework | | #1476 | yes | yes | yes | #1071 | yes | n/a |
| aws-static-site-ts | Framework | | #1476 | yes | yes | yes | #1071 | yes | n/a |
| aws-standard | Standard | | #1476 | yes | yes | n/a | n/a | yes | yes |
| dynamodb | Connector | | #1476 | yes | yes | n/a | yes, #1393 | n/a | yes |
| hello | Framework | | n/a | n/a | n/a | yes | n/a | n/a | n/a |
| hello-standard | Standard | | n/a | yes | n/a | n/a | n/a | n/a | n/a |
| hello-html | Framework | | n/a | n/a | n/a | yes | n/a | n/a | n/a |
| hello-html-standard | Standard | | n/a | n/a | n/a | n/a | n/a | n/a | n/a |

The CI column is the widest gap: only the operator pair and the webshop's
combinations are built and checked as scaffolded repositories
(`make test-templates-operator` and `make test-templates`, run by hand when
one of their templates changes); #1476 does the same for the rest.

## The webshop's templates

### python-fastapi (v1.3.0)

A Python service on uv and FastAPI, with `/`, `/healthz` and, with the
`postgres` Connector, `/readyz`.

- CI: `make test-templates`, combo "fastapi service with postgres":
  its `make check`, then `kustomize build deploy/k8s`.
- Tests: `tests/test_main.py.tmpl` (TestClient over every route), run by
  python-standard's `make test`.
- Lint: python-standard's ruff (every rule) and mypy (strict); the
  pyproject's dev group carries both.
- README: `README.md.tmpl`: run (`uv sync`, uvicorn, the devcontainer), check,
  the routes. How to ship is k8s-manifests' (#1470).
- Deploy: k8s-manifests in "fastapi service with postgres" writes its
  Dockerfile (uv, uvicorn, port 8000), checked by
  `TestK8sManifestsFollowsTheFramework`. No release builds the image (#1457)
  and nothing applies it to a cluster (#1072).
- Logging: only uvicorn's plain-text access log (#1473).
- Auth: every route is open (#1474).

### python-cli (v1.1.0)

An argparse command-line program on uv that builds a wheel and an sdist; as
a `worker` (#1468), a long-running `python -m app` that does a round of work,
backs off while rounds fail and stops cleanly on SIGTERM.

- CI: `make test-templates`, combo "python cli": its `make check`, then
  `make dist VERSION=v0.1.0` and the wheel and sdist in `dist/`.
- Tests: `tests/test_cli.py.tmpl` and `tests/test_greeting.py.tmpl`, or for a
  worker `tests/test_worker.py.tmpl` (a real SIGTERM included) and
  `tests/test_backoff.py.tmpl`, run by python-standard's `make test`.
- Lint: python-standard's ruff and mypy.
- README: `README.md.tmpl`: use, install the wheel, develop, `make dist`,
  layout.
- Deploy: gitea-release in "python cli" attaches `make dist`'s wheel and sdist
  to the tag's release. Publishing to a package registry is #1229. A worker
  ships with k8s-manifests ("python worker on kubernetes").
- Logging: results to stdout, errors to stderr (`app/cli.py.tmpl`), which is
  what a command-line program owes its user; a worker logs each event to
  stderr with `logging` (`app/worker.py.tmpl`).

### react-vite (v1.0.0)

React 19 and TypeScript, built by Vite into a static site in `dist/`.

- CI: `make test-templates`, combo "react web app on kubernetes": its
  `make check`, then `kustomize build deploy/k8s`.
- Tests: `src/greeting.test.ts`, run by ts-standard's `make check` (vitest).
- Lint: ts-standard's ESLint (typescript-eslint strict-type-checked, React
  hooks), Prettier and `tsc --noEmit`.
- README: `README.md.tmpl`: `npm ci`, `npm run dev`, `npm test`, `make check`,
  `make dist`, layout.
- Deploy: k8s-manifests in "react web app on kubernetes" writes a Dockerfile
  (vite build, unprivileged nginx on 8080), checked by
  `TestK8sManifestsFollowsTheFramework` and `make validate-yaml`'s storefront
  render. Image and rollout: #1457, #1072.
- Logging: n/a: a static page served by nginx; the code has none and ESLint
  refuses `console`.
- Auth: n/a: a public page with no API client; the API it calls
  authenticates (#1474).

### node-cli (v1.0.0)

A TypeScript command-line program for Node 22, bundled by esbuild and packed by
`npm pack`.

- CI: `make test-templates`, combo "node cli": its `make check`, then
  `make dist VERSION=v0.0.1` (package.json's version) and the `.tgz` in
  `dist/`.
- Tests: `src/cli.test.ts` and `src/greeting.test.ts` (vitest), and
  `pack-check` runs the packed program with `--version`.
- Lint: ts-standard's ESLint, Prettier and `tsc`.
- README: `README.md.tmpl`: use, install the tarball, develop, `make dist`,
  layout.
- Deploy: gitea-release in "node cli" attaches `dist/<container>-<version>.tgz`
  to the tag's release. Publishing to a registry is #1229 and #1231.
- Logging: results to stdout, errors to stderr (`src/cli.ts`), exit 2 on a bad
  command line.

### python-standard (v1.4.0)

The Python rulebook and its `make check`.

- CI: its `make check` in `make test-templates`' combos "fastapi service
  with postgres" and "python cli".
- Tests: `Makefile.tmpl`, target `test` (pytest, strict markers and config),
  part of `check`; `.c4factory/ci/giteaActions/.gitea/workflows/check.yaml.tmpl`
  runs `make check`.
- Lint: `Makefile.tmpl`: `fmt-check` (ruff format), `lint` (ruff, `ruff.toml`),
  `typecheck` (mypy, `mypy.ini`), `package-check` (uv build).
- Logging: `docs/style/python.md` (a module logger, lazy arguments,
  `logger.exception`) and `docs/style/principles.md` (only actionable
  messages).
- README, Deploy, Auth: n/a: a Standard writes rules, not the app; its
  `AGENTS.md` and `docs/` are what it adds.

### ts-standard (v1.5.0)

The TypeScript rulebook for react-vite, node-cli and vscode-extension.

- CI: its `make check` in `make test-templates`' combos "react web app on
  kubernetes" and "node cli".
- Tests: `Makefile.tmpl`, target `test` (vitest), part of `check`; its
  `check.yaml.tmpl` runs `make check`.
- Lint: `Makefile.tmpl`: `fmt-check` (Prettier), `lint` (ESLint, no warnings,
  `eslint.config.mjs.tmpl`), `typecheck` (`tsc --noEmit`).
- Logging: `docs/style/principles.md.tmpl` (only actionable messages, never
  user data); `AGENTS.md.tmpl` (an extension logs to a `LogOutputChannel`).
- README, Deploy, Auth: n/a: a Standard.

### postgres (v1.1.0)

A Connector: `DATABASE_URL` in a ConfigMap (`deploy/k8s/connector-postgres.yaml`)
and a Postgres in the devcontainer.

- CI: `make test-templates`, combo "fastapi service with postgres": the
  app's `make check`, then `kustomize build deploy/k8s` with its manifests.
- Tests: the scaffold has no code that connects; python-fastapi's `/readyz`
  only checks the variable is set (#1472).
- Lint: `make validate-yaml` builds the orders-api render with it and
  validates the result with kubectl-validate.
- README: n/a: it writes none; python-fastapi's README names it.
- Deploy: the host `<container>-postgres` its URL names is never created
  (#1471).
- Auth: no password is set; the ConfigMap's comment asks for a Secret
  (#1471).
- Logging: n/a: no code.

### small (v1.1.0)

A Compute: requests and limits for the container `app` as a kustomize patch
(`deploy/k8s/compute.yaml`; for an operator, `config/manager/compute.yaml`).

- CI: `make test-templates` runs `kustomize build deploy/k8s` in the
  combos "fastapi service with postgres" and "react web app on kubernetes".
- Tests and Lint: `make validate-yaml` builds the orders-api, storefront and
  backup-operator renders with it and validates them with kubectl-validate.
- README, Deploy, Logging, Auth: n/a: it sizes a container and ships nothing.

### k8s-manifests (v1.3.0)

A Deployment: a Dockerfile for python-fastapi, python-cli or react-vite, and
`deploy/k8s/` (kustomization, Deployment, and a Service but for a worker).

- CI: `make test-templates` runs `kustomize build deploy/k8s` in the
  combos "fastapi service with postgres" and "react web app on kubernetes".
- Tests: `TestK8sManifestsFollowsTheFramework` (the Dockerfile,
  `.dockerignore`, port and probes per Framework, no Service for a worker);
  `make validate-yaml` builds the orders-api, storefront and payments-worker
  renders.
- Lint: those builds are validated with kubectl-validate.
- README: nothing in the scaffold says how to build the image or apply the
  manifests (#1470).
- Deploy: the manifests, with the image `<container>:dev`, ready for a GitOps
  tool. No release builds the image (#1457); nothing applies it (#1072).
- Logging, Auth: n/a: it packages the app's.

### gitea-release (v1.2.0)

A Deployment for cli, extension and operator Containers:
`.gitea/workflows/release.yaml` runs `make dist VERSION=<tag>` on a `v*` tag
and attaches `dist/` to the tag's release.

- CI: `make test-templates` runs `make dist` in the combos "python cli"
  and "node cli" and checks the files it attaches.
- Tests: `TestTemplatesRenderTheirCIFilesForGiteaActions`
  (`internal/scaffold/ci_test.go`) renders the workflow;
  `make test-templates-operator` runs `make dist` for the operator pair,
  `make test-templates` for the CLIs.
- Lint: `TestEveryTemplateRenders` parses the workflow.
- README: nothing in the scaffold says that a tag releases (#1475).
- Deploy: the Gitea release with the `dist/` files; no image (#1458 for an
  operator's).
- Logging, Auth: n/a: a workflow; it uses the workflow's own token.

## The other templates

### go-cli (v1.0.0)

A Go command-line program.

- CI: `.github/workflows/verify-release.yml`'s "Getting started" step releases
  a go-cli Container on Kind for each minor release; no test runs its
  `make check` before a merge (#1476).
- Tests: `main_test.go.tmpl` (the whole program through `run()`), run by
  go-standard's `make test`.
- Lint: go-standard's golangci-lint, gofumpt, goimports and `go vet`.
- README: `README.md.tmpl`: run, test, `make check`, `make build`, `make dist`.
- Deploy: gitea-release in "go cli" attaches the cross-compiled binaries.
- Logging: errors to stderr (`main.go.tmpl`, `run(os.Stdout, os.Stderr)`).

### go-kubebuilder (v1.0.1)

A kubebuilder v4 operator, regenerated by `hack/template-go-kubebuilder`.

- CI: `make test-templates-operator` (`hack/test-templates-operator.sh`)
  renders "kubebuilder operator", runs `make check`, `kubebuilder create api`,
  `make check` again and `make dist`. Not in `make check`: run when either
  operator template changes.
- Tests: kubebuilder's `test/e2e` scaffold; the unit and envtest suites arrive
  with `kubebuilder create api`, which that script runs.
- Lint: go-operator-standard's golangci-lint.
- README: kubebuilder's boilerplate, `// TODO(user)` included (#1477).
- Deploy: gitea-release attaches the binaries, `install.yaml` and
  `SHA256SUMS`; no release builds the operator's image (#1458).
- Logging: controller-runtime's zap logger (`cmd/main.go.tmpl`), structured
  key-value lines.
- Auth: the metrics endpoint is served over TLS behind an authn/authz role
  (`config/rbac/metrics_auth_role*.yaml`,
  `config/default/cert_metrics_manager_patch.yaml`).

### vscode-extension (v1.0.0)

A VS Code extension in TypeScript, with a stand-in `vscode` module so its tests
run in Node.

- Tests: `src/greeting.test.ts` and `src/extension.test.ts`; `package-check`
  runs `vsce package`.
- Lint: ts-standard's.
- README: `README.md.tmpl`: install the `.vsix`, develop, `make dist`.
- Deploy: gitea-release attaches `dist/<container>-<version>.vsix`.
- Logging: none; ts-standard's guide asks for a `LogOutputChannel` (#1478).
- Auth: n/a: it runs in the user's editor.

### go-standard (v1.3.0) and go-operator-standard (v1.2.0)

The Go rulebook, and its kubebuilder variant (protected generated paths,
envtest).

- CI: go-operator-standard: `make test-templates-operator`, as for
  go-kubebuilder. go-standard: #1476.
- Tests: `Makefile.tmpl`, target `test` (`go test -race`; envtest for the
  operator), part of `check`; their `check.yaml.tmpl` runs `make check`.
- Lint: `.golangci.yml` (golangci-lint), `fmt-check`, `vet`, `tidy-check`; the
  operator's adds `generated-check`.
- Logging: go-standard's `docs/style/principles.md` and `docs/style/go.md`
  (actionable, no secrets, return or log, never both); go-operator-standard's
  `AGENTS.md.tmpl` (`log.FromContext`, key-value pairs).
- README, Deploy, Auth: n/a: Standards.

### The AWS Frameworks

aws-lambda-ts (v1.3.1), aws-lambda-go (v1.2.1) and aws-lambda-py (v1.2.1): an
HTTP API on Lambda with a DynamoDB table. aws-ec2-ts (v1.1.1) and
aws-fargate-ts (v1.2.1): a Node service behind an HTTPS load balancer.
aws-queue-ts, aws-schedule-ts and aws-event-ts (v1.1.1): an SQS, EventBridge
Scheduler or EventBridge consumer on Lambda. aws-static-site-ts (v1.0.0): a
site on S3 behind CloudFront and WAF. Each is a CDK app paired with
aws-standard.

- CI: their combos are rendered and string-checked (`TestEveryTemplateRenders`,
  `internal/scaffold/aws_standard_test.go`), never built (#1476).
- Tests: the handler's tests (`src/*.test.ts`, `src/*_test.go.tmpl`,
  `tests/test_*.py`) and the stack's CDK assertions (`infra/*_stack.test.ts`),
  run by aws-standard's `make test`.
- Lint: aws-standard's `make check`.
- README: `README.md.tmpl` in each: develop, tiers, deploy with `cdk deploy`.
- Deploy: `cdk deploy` with the owner's credentials; the factory deploys
  nothing to AWS (#1071).
- Logging: JSON lines to CloudWatch: Powertools Logger (the TypeScript and
  Python Lambdas, `src/handler.ts`, `app/handler.py`), `slog`'s JSON handler
  (`src/main.go.tmpl`), a JSON line per request (aws-ec2-ts `src/server.ts`,
  aws-fargate-ts `src/main.ts`); load balancer, CloudFront and S3 access logs
  (aws-static-site-ts `infra/site_stack.ts`). The HTTP API's access log isn't
  JSON yet (#1420).
- Auth: the Lambda APIs take SigV4-signed requests (`HttpIamAuthorizer` in
  `infra/api_stack.ts`). aws-ec2-ts and aws-fargate-ts authenticate no one
  (#1479). n/a for the queue, schedule and event consumers (no users) and the
  static site (public by design).

### aws-standard (v1.17.3)

The AWS rulebook: AGENTS.md, the style and Well-Architected digests, a
runbook, `infra/standard.ts` with CDK Nag.

- CI: #1476.
- Tests: `Makefile.tmpl`, target `test` (vitest; pytest or `go test` by
  language), and its own `infra/standard.test.ts` (every log group has a
  retention).
- Lint: `Makefile.tmpl`: ESLint, `tsc`, Prettier; ruff and mypy, or
  golangci-lint and `go vet`, by language; `synth` with CDK Nag (AwsSolutions
  and Serverless); cfn-lint.
- Logging: `docs/well-architected.md.tmpl` (structured JSON logs with the
  request id, access and flow logs).
- Auth: `docs/well-architected.md.tmpl` (authenticate requests; an open route
  is acknowledged with a reason) and CDK Nag's API Gateway and Cognito rules.
- README, Deploy: n/a: a Standard; its Makefile deploys nothing on purpose.

### dynamodb (v1.0.0)

A Connector: a CDK construct (`infra/connector_dynamodb.ts`) making one
on-demand, encrypted table for a function Container.

- CI: #1476.
- Tests: `infra/connector_dynamodb.test.ts`, in the Frameworks' vitest
  include; `TestDynamoDBConnectorConnectsToAWSLambdaTS`
  (`internal/scaffold/aws_connectors_test.go`).
- Lint: aws-standard's `make check` once rendered.
- Deploy: the table is part of the stack, retained and deletion-protected in
  production. Only aws-lambda-ts wires it in (#1393).
- Auth: `table.grant(fn, ...)` grants the item calls on that table only.
- README, Logging: n/a: it writes no README and has no runtime code.

### hello, hello-standard, hello-html, hello-html-standard

The end-to-end suite's fixtures (`test/e2e`): the smallest Container, page and
Standards, kept tiny on purpose so a stand-in agent's Task is fast. Every
promise but a README is n/a for them; `hello-standard`'s `make check` runs
`hello.sh` and expects `hello, world`. They ship nothing a user builds on.

## Keeping it current

A pull request that closes one of these issues fills its cell, with the
evidence, in the same change. A new template adds its row.
