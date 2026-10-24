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
| python-fastapi | Framework | W | yes | yes | yes | yes | yes, #1457 #1072 | yes | yes |
| python-cli | Framework | W | yes | yes | yes | yes | yes | yes | n/a |
| react-vite | Framework | W | yes | yes | yes | yes | yes, #1072 | n/a | n/a |
| node-cli | Framework | W | yes | yes | yes | yes | yes | yes | n/a |
| python-standard | Standard | W | yes | yes | yes | n/a | n/a | yes | n/a |
| ts-standard | Standard | W | yes | yes | yes | n/a | n/a | yes | n/a |
| postgres | Connector | W | yes | yes | yes | n/a | yes | n/a | yes |
| small | Compute | W | yes | yes | yes | n/a | n/a | n/a | n/a |
| k8s-manifests | Deployment | W | yes | yes | yes | yes | yes, #1072 | n/a | n/a |
| gitea-release | Deployment | W | yes | yes | yes | yes | yes | n/a | n/a |
| webshop-domain | Domain | W | yes | yes | n/a | yes | n/a | n/a | n/a |
| go-cli | Framework | | yes | yes | yes | yes | yes | yes | n/a |
| go-kubebuilder | Framework | | yes | yes | yes | yes | yes | yes | yes |
| vscode-extension | Framework | | yes | yes | yes | yes | yes | yes | n/a |
| go-standard | Standard | | yes | yes | yes | n/a | n/a | yes | n/a |
| go-operator-standard | Standard | | yes | yes | yes | n/a | n/a | yes | n/a |
| factory-self | Standard | | yes | yes | yes | n/a | n/a | yes | n/a |
| aws-lambda-ts | Framework | | yes | yes | yes | yes | #1071 | yes, #1420 | yes |
| aws-lambda-go | Framework | | yes | yes | yes | yes | #1071 | yes, #1420 | yes |
| aws-lambda-py | Framework | | yes | yes | yes | yes | #1071 | yes, #1420 | yes |
| aws-ec2-ts | Framework | | yes | yes | yes | yes | #1071 | yes | yes |
| aws-fargate-ts | Framework | | yes | yes | yes | yes | #1071 | yes | yes |
| aws-queue-ts | Framework | | yes | yes | yes | yes | #1071 | yes | n/a |
| aws-schedule-ts | Framework | | yes | yes | yes | yes | #1071 | yes | n/a |
| aws-event-ts | Framework | | yes | yes | yes | yes | #1071 | yes | n/a |
| aws-static-site-ts | Framework | | yes | yes | yes | yes | #1071 | yes | n/a |
| aws-standard | Standard | | yes | yes | yes | n/a | n/a | yes | yes |
| dynamodb | Connector | | yes | yes | yes | n/a | yes, #1393 | n/a | yes |
| rds | Connector | | yes | yes | yes | n/a | yes, #1923 | n/a | yes |
| hello | Framework | | n/a | n/a | n/a | yes | n/a | n/a | n/a |
| hello-standard | Standard | | n/a | yes | n/a | n/a | n/a | n/a | n/a |
| hello-html | Framework | | n/a | n/a | n/a | yes | n/a | n/a | n/a |
| hello-html-standard | Standard | | n/a | n/a | n/a | n/a | n/a | n/a | n/a |

Every template with a CI cell is built and checked as a scaffolded
repository: the operator pair by `make test-templates-operator`, the rest by
`make test-templates` (the webshop's combinations, the go cli, the vscode
extension and every AWS combination), each run by hand when one of its
templates changes.

## The webshop's templates

### python-fastapi (v1.6.0)

A Python service on uv and FastAPI, with `/`, `/healthz` and, with the
`postgres` Connector, `app/db.py` (a psycopg pool) and a `/readyz` that
runs `SELECT 1`.

- CI: `make test-templates`, combo "fastapi service with postgres":
  its `make check`, then `kustomize build deploy/k8s`.
- Tests: `tests/test_main.py.tmpl` (TestClient over every route), run by
  python-standard's `make test`.
- Lint: python-standard's ruff (every rule) and mypy (strict); the
  pyproject's dev group carries both.
- README: `README.md.tmpl`: run (`uv sync`, uvicorn, the devcontainer), check,
  the routes. How to ship is k8s-manifests' `deploy/k8s/README.md` (#1470).
- Deploy: k8s-manifests in "fastapi service with postgres" writes its
  Dockerfile (uv, uvicorn, port 8000), checked by
  `TestK8sManifestsFollowsTheFramework`; each release builds the image for
  both platforms (#1457), and nothing applies it to a cluster (#1072).
- Logging: `app/logs.py`: JSON lines on stdout at `LOG_LEVEL`, one per
  request (method, path, status, `durationMs`, `requestId` from
  `X-Request-Id`, echoed), no query string, header value or body; uvicorn's
  access log off. `tests/test_main.py` and `TestFastAPILogsJSONLines` (#1473).
- Auth: `app/auth.py.tmpl` verifies a bearer JWT (RS256/ES256, `iss`,
  `aud`, `exp`) against the JWKS of `OIDC_ISSUER` on every route but
  `/healthz` and `/readyz`; without `OIDC_ISSUER` the app refuses to start
  unless `AUTH_DISABLED=true` (the devcontainer sets it). The tests sign
  tokens with a key they make (#1474).

### python-cli (v1.2.0)

An argparse command-line program on uv that builds a wheel and an sdist; as
a `worker` (#1468), a long-running `python -m app` that does a round of work,
backs off while rounds fail and stops cleanly on SIGTERM; as a `job`
(#1595), a `python -m app` that does its work once and exits 0, or 1 when
it failed.

- CI: `make test-templates`, combo "python cli": its `make check`, then
  `make dist VERSION=v0.1.0` and the wheel and sdist in `dist/`.
- Tests: `tests/test_cli.py.tmpl` and `tests/test_greeting.py.tmpl`, or for a
  worker `tests/test_worker.py.tmpl` (a real SIGTERM included) and
  `tests/test_backoff.py.tmpl`, or for a job `tests/test_job.py.tmpl`, run
  by python-standard's `make test`.
- Lint: python-standard's ruff and mypy.
- README: `README.md.tmpl`: use, install the wheel, develop, `make dist`,
  layout.
- Deploy: gitea-release in "python cli" attaches `make dist`'s wheel and sdist
  to the tag's release. Publishing to a package registry is #1229. A worker
  ships with k8s-manifests ("python worker on kubernetes"), a job as its
  Job ("python job on kubernetes").
- Logging: results to stdout, errors to stderr (`app/cli.py.tmpl`), which is
  what a command-line program owes its user; a worker logs each event to
  stderr with `logging` (`app/worker.py.tmpl`), and so does a job
  (`app/job.py.tmpl`).

### react-vite (v1.0.0)

React 19 and TypeScript, built by Vite into a static site in `dist/`.

- CI: `make test-templates`, combo "react web app on kubernetes": its
  `make check`, then `kustomize build deploy/k8s`.
- Tests: `src/greeting.test.ts`, run by ts-standard's `make check` (vitest).
- Lint: ts-standard's ESLint (typescript-eslint strict-type-checked, React
  hooks), Prettier and `tsc --noEmit`.
- README: `README.md.tmpl`: `npm ci`, `npm run dev`, `npm test`, `make check`,
  `make dist`, layout; how to ship is k8s-manifests' `deploy/k8s/README.md`.
- Deploy: k8s-manifests in "react web app on kubernetes" writes a Dockerfile
  (vite build, unprivileged nginx on 8080), checked by
  `TestK8sManifestsFollowsTheFramework` and `make validate-yaml`'s storefront
  render. Each release builds the image (#1457); rollout: #1072.
- Logging: n/a: a static page served by nginx; the code has none and ESLint
  refuses `console`.
- Auth: n/a: a public page with no API client; the API it calls
  verifies a bearer JWT (#1474).

### node-cli (v1.1.1)

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
- Deploy: gitea-release in "node cli" attaches
  `dist/<scope sans @>-<container>-<version>.tgz` (`c4factory-<container>-<version>.tgz`
  by default, how `npm pack` names a scoped package) to the tag's release, and
  the Crew publishes the tarball to the owner's npm registry (#1229). Since
  v1.1.0 `package.json` names the package `<scope>/<container>`, the scope
  from the GitProvider's `spec.packages` (`@c4factory` by default), set at
  scaffold time and never again (#1231); since v1.1.1 the README names the
  scoped tarball, as ts-standard's `make dist` help does since v1.6.1 (#1695).
- Logging: results to stdout, errors to stderr (`src/cli.ts`), exit 2 on a bad
  command line.

### python-standard (v1.7.0)

The Python rulebook and its `make check`. Since v1.5.0 the check workflow
writes `~/.npmrc` and `~/.netrc` from the owner's Actions variables and
secrets (`C4_NPM_SCOPE`, `C4_NPM_REGISTRY`, `C4_PYPI_INDEX`, `C4_NPM_TOKEN`,
`C4_PYPI_TOKEN`), falling back to the host's own registries and the
workflow's token, so `uv` fetches factory packages; `AGENTS.md` says how a
package is declared (#1231). Since v1.5.1 `~/.netrc` names the index's host
without its port, which `uv` matches on, and its login is `__token__` when
`C4_PYPI_TOKEN` is set (#1696). Since v1.6.0 it supports a `job` with
python-cli, and `AGENTS.md` has a job's rules: run once, exit non-zero on
failure, safe to redo (#1595).
Since v1.7.0 `AGENTS.md` tells the implementer which Domains the system is
about and to read `docs/agents/domain/<name>/README.md` and its topic files
before planning a change, and the reviewer guide to check the change against
each domain's common mistakes; without Domains both read as before (#1758).

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

### ts-standard (v1.7.0)

The TypeScript rulebook for react-vite, node-cli and vscode-extension. Since
v1.6.0 the check workflow writes `~/.npmrc` and `~/.netrc` from the owner's
Actions variables and secrets (`C4_NPM_SCOPE`, `C4_NPM_REGISTRY`,
`C4_PYPI_INDEX`, `C4_NPM_TOKEN`, `C4_PYPI_TOKEN`), falling back to the host's
own registries and the workflow's token, so `npm ci` fetches factory
packages; `AGENTS.md` says how one is added (#1231). Since v1.6.2 `~/.netrc`
names the index's host without its port, which `uv` matches on, and its
login is `__token__` when `C4_PYPI_TOKEN` is set (#1696).
Since v1.7.0 `AGENTS.md` tells the implementer which Domains the system is
about and to read `docs/agents/domain/<name>/README.md` and its topic files
before planning a change, and the reviewer guide to check the change against
each domain's common mistakes; without Domains both read as before (#1758).

- CI: its `make check` in `make test-templates`' combos "react web app on
  kubernetes", "node cli" and "vscode extension".
- Tests: `Makefile.tmpl`, target `test` (vitest), part of `check`; its
  `check.yaml.tmpl` runs `make check`.
- Lint: `Makefile.tmpl`: `fmt-check` (Prettier), `lint` (ESLint, no warnings,
  `eslint.config.mjs.tmpl`), `typecheck` (`tsc --noEmit`).
- Logging: `docs/style/principles.md.tmpl` (only actionable messages, never
  user data); `AGENTS.md.tmpl` (an extension logs to a `LogOutputChannel`).
- README, Deploy, Auth: n/a: a Standard.

### postgres (v1.2.0)

A Connector: on Kubernetes (`deploy/k8s/connector-postgres.yaml`) a
single-replica Postgres StatefulSet and headless Service `<container>-postgres`
and a ConfigMap with `DATABASE_URL`; a Postgres in the devcontainer.

- CI: `make test-templates`, combo "fastapi service with postgres": the
  app's `make check`, then `kustomize build deploy/k8s` with its manifests.
- Tests: `TestPostgresRunsOnKubernetes` (the pinned image, probe, volume,
  non-root, and no password literal in its manifests or the Deployment).
  python-fastapi's `tests/test_main.py` covers `/readyz` answering 200 and
  503 with the pool faked, and runs it against the devcontainer's Postgres,
  skipped when `DATABASE_URL` reaches no server;
  `TestFastAPIConnectsOnlyWithPostgres` checks `app/db.py` is written only
  with the Connector.
- Lint: `make validate-yaml` builds the orders-api render with it, checks
  the StatefulSet, both Services and both `secretKeyRef`s, and validates
  the result with kubectl-validate.
- README: n/a: it writes none; python-fastapi's README names it.
- Deploy: `postgres:17.11-alpine` by digest, user 70, a 1Gi volume per
  replica, `pg_isready` as the readiness and liveness probe. A human run of
  Kind would show the pod Ready; no agent runs one.
- Auth: the password comes from Secret `<container>-postgres` (key
  `password`), which the user makes: Postgres reads it as
  `POSTGRES_PASSWORD`, the app (k8s-manifests' Deployment) as `PGPASSWORD`.
- Logging: n/a: no code.

### small (v1.1.0)

A Compute: requests and limits for the container `app` as a kustomize patch
(`deploy/k8s/compute.yaml`; for an operator, `config/manager/compute.yaml`).

- CI: `make test-templates` runs `kustomize build deploy/k8s` in the
  combos "fastapi service with postgres" and "react web app on kubernetes".
- Tests and Lint: `make validate-yaml` builds the orders-api, storefront and
  backup-operator renders with it and validates them with kubectl-validate.
- README, Deploy, Logging, Auth: n/a: it sizes a container and ships nothing.

### k8s-manifests (v1.9.1)

A Deployment: a Dockerfile for python-fastapi, python-cli or react-vite, and
`deploy/k8s/` (kustomization, Deployment, and a Service but for a worker; for
a job, a Job instead of the Deployment).

- CI: `make test-templates` runs `kustomize build deploy/k8s` in the
  combos "fastapi service with postgres" and "react web app on kubernetes".
- Tests: `TestK8sManifestsFollowsTheFramework` (the Dockerfile,
  `.dockerignore`, port and probes per Framework, no Service for a worker);
  `make validate-yaml` builds the orders-api, storefront and payments-worker
  renders. With the `postgres` Connector, the Deployment sets `PGPASSWORD`
  from Secret `<container>-postgres` (`TestPostgresRunsOnKubernetes`), and
  for python-fastapi (since v1.7.0, #1528) its readiness probe asks
  `/readyz`, which runs `SELECT 1`: a pod whose database is down leaves the
  Service, and liveness stays on `/healthz`. For python-fastapi (since
  v1.8.0, #1540) `auth.yaml` writes ConfigMap `<container>-auth` with
  placeholder `OIDC_ISSUER` and `OIDC_AUDIENCE`, which the Deployment loads
  through `envFrom`, so the app starts; the README says to set both. For a
  job (since v1.9.0, #1587) `job.yaml` is a `batch/v1` Job, `restartPolicy:
  Never`, annotated `argocd.argoproj.io/sync-options: Force=true,Replace=true`
  (since v1.9.1, #1628: `Replace=true` alone is an update, which the
  immutable template refuses) so Argo CD deletes and recreates it at each
  release, with no
  `ttlSecondsAfterFinished` (the `argoCD` target's `selfHeal` would recreate
  a deleted Job and rerun it), and small's `compute.yaml` patches the Job
  (`TestK8sManifestsRunsAJob`); `make validate-yaml` builds the nightly-sync
  job render and finds the patch's `cpu: 50m` and the annotation on the Job.
- Lint: those builds are validated with kubectl-validate.
- README: `deploy/k8s/README.md.tmpl` (#1470): build the image, set it with
  `kustomize edit set image`, preview, `kubectl apply -k deploy/k8s`, what
  `compute.yaml` and each Connector's file are, the port and probes; checked
  by `TestK8sManifestsWritesItsREADME`.
- Release (since v1.6.0, #1457): `.gitea/workflows/image.yaml`, for Gitea
  Actions, builds the Dockerfile on each `v*` tag on the `image` runner with
  rootless BuildKit, for `linux/amd64` and `linux/arm64` with an SBOM and
  provenance, and attaches `<repository>-<tag>.oci.tar` to the release; the
  Dockerfiles `RUN` only in stages on `$BUILDPLATFORM`
  (`TestDockerfilesRunOnlyOnTheBuildPlatform`,
  `TestK8sManifestsBuildsItsImageOnRelease`).
- Deploy: the manifests, with the image `<container>:dev`; the `argoCD`
  DeployTarget ships them at each release (#1072), and the README says how
  to ship them yourself.
- Logging, Auth: n/a: it packages the app's.

### gitea-release (v1.8.0)

A Deployment for cli, extension and operator Containers, and for the AWS
Frameworks' function, service, static-site, worker and job ones:
`.gitea/workflows/release.yaml` runs `make dist VERSION=<tag>` on a `v*` tag
and attaches `dist/` to the tag's release. For an operator an `image` job
builds the image first and the release names it by digest (#1458), on the
Git host alone, whatever path its `ROOT_URL` has. Since v1.4.1 (#1652) the
release job has a digest guard: it fails before `make dist` unless the
`image` job's digest is `sha256:` plus 64 hex, so the release files never
name an image that is not there (#1581). And a rerun rule: the operator
pushes a tag's image once and never replaces it, so the job asks the
registry for the tag's digest first and names the one it holds; 404 names
the digest just built, any other answer fails the step (#1583). Since
v1.5.0 the release job writes `~/.npmrc` and `~/.netrc` from the owner's
Actions variables and secrets before `make dist`, falling back to the
host's own registries and the workflow's token, so a build that installs
factory packages finds them (#1231). Since v1.5.1 `~/.netrc` names the
index's host without its port, which `uv` matches on, and its login is
`__token__` when `C4_PYPI_TOKEN` is set (#1696). Since v1.5.2 `RELEASING.md`
renders its lists loose, a blank line before the step that watches the run
and before each item of "When a run fails", so ts-standard's Prettier check
passes it (#1707). Since v1.6.0 it supports `function`, `service` and
`static-site` for the AWS Frameworks, whose `make dist` writes the flat
cloud assembly a `cloudFormation` DeployTarget ships (#1607). Since
v1.7.0 an `aws-fargate-ts` service's release has the `image` job too: the
release job names no image, and the deployer copies the digest the
factory pushed into the bootstrap's ECR repository and passes it as the
stack's `ImageUri` parameter (#1610). Since v1.8.0 it supports `worker`
and `job` too, for `aws-queue-ts`, `aws-event-ts` and `aws-schedule-ts`,
whose `make dist` writes the same assembly (#1773).

- CI: `make test-templates` runs `make dist` in the combos "python cli",
  "node cli", "go cli", "vscode extension" and the AWS combos and checks
  the files it attaches.
- Tests: `TestTemplatesRenderTheirCIFilesForGiteaActions`
  (`internal/scaffold/ci_test.go`) renders the workflow;
  `make test-templates-operator` runs `make dist` for the operator pair,
  `make test-templates` for the CLIs;
  `TestGiteaReleaseBuildsAnOperatorsImage` renders the `image` job for an
  operator and not for a CLI, with the digest guard and the registry lookup
  in order; `TestGiteaReleaseNamesTheImageAsTheOperatorPushesIt` runs the
  release step's shell against a stub `make` (#1581);
  `TestGiteaReleasePinHoldsItsReleaseJobFixes` keeps the pin at v1.4.1 or
  later (#1652).
- Lint: `TestEveryTemplateRenders` parses the workflow.
- README: `RELEASING.md` says how to tag a release, check one with
  `make dist` and recover from a failed run
  (`TestGiteaReleaseWritesReleasingGuide`).
- Deploy: the Gitea release with the `dist/` files, and an operator's
  image, pushed by the factory.
- Logging, Auth: n/a: a workflow; it uses the workflow's own token.

### webshop-domain (v1.0.0)

The webshop's Domain (catalog entry `webshop`, #1759; step 4 of
[plans/domains.md](plans/domains.md)): what a webshop is about, for the
agents that build and review one, in plain prose and with no stack. A
`README.md` with a glossary and a line per topic, and a topic file each for
catalog and search, cart, checkout and orders, stock, payments, pricing and
money, and accounts and personal data; every topic file has the four
headings *Practices*, *Common mistakes*, *Controversies* (the choice this
domain makes and why) and *Sources*. Built as the Domain of a Container
(`domainRefs: [webshop]` on the webshop's `webshop-core`), every file lands
under `docs/agents/domain/webshop/`, and the Standard's `AGENTS.md` and
`docs/agents/reviewer.md` point the implementer and the reviewer at it
(#1758). A
Domain is Markdown the agents read, so most promises don't apply.

- CI: rendered in the python-fastapi combination of `templateCombos`
  (`TestEveryTemplateRenders`), whose Container names the Domain; the
  shell combo of the same name in `hack/test-templates.sh` renders it too
  (`hack/render` takes `Domain=webshop-domain`) and runs that repository's
  `make check` to green.
- Tests: `TestDomainTemplatesHaveTheShape` checks the shape of every
  `*-domain` folder (Markdown only, the README names every topic file, the
  four headings) and builds it as a Container's Domain.
- README: the template's own `README.md`, the file an agent reads first.
- Lint, Deploy, Logging, Auth: n/a: prose, with no code, service or user.

## The other templates

### go-cli (v1.0.0)

A Go command-line program.

- CI: `make test-templates`, combo "go cli": its `make check`, then
  `make dist VERSION=v0.1.0`, checking the six binaries and `SHA256SUMS` in
  `dist/`. `.github/workflows/verify-release.yml`'s "Getting started" step
  also releases a go-cli Container on Kind for each minor release.
- Tests: `main_test.go.tmpl` (the whole program through `run()`), run by
  go-standard's `make test`.
- Lint: go-standard's golangci-lint, gofumpt, goimports and `go vet`.
- README: `README.md.tmpl`: run, test, `make check`, `make build`, `make dist`.
- Deploy: gitea-release in "go cli" attaches the cross-compiled binaries.
- Logging: errors to stderr (`main.go.tmpl`, `run(os.Stdout, os.Stderr)`).

### go-kubebuilder (v1.2.0)

A kubebuilder v4 operator, regenerated by `hack/template-go-kubebuilder`.

- CI: `make test-templates-operator` (`hack/test-templates-operator.sh`)
  renders "kubebuilder operator", runs `make check`, `kubebuilder create api`,
  `make check` again and `make dist`. Not in `make check`: run when either
  operator template changes.
- Tests: kubebuilder's `test/e2e` scaffold; the unit and envtest suites arrive
  with `kubebuilder create api`, which that script runs.
- Lint: go-operator-standard's golangci-lint.
- README: the factory's own, written by `hack/template-go-kubebuilder`
  (#1477): the Container's display name and description, `kubebuilder create
  api` first, `make check`, `make install run`, `make dist` and what
  gitea-release attaches; the Go version is `go.mod`'s.
- Deploy: gitea-release builds the image (the builder stage runs on
  `$BUILDPLATFORM`) and attaches it, the binaries, `install.yaml` naming the
  image by digest, and `SHA256SUMS`.
- Logging: controller-runtime's zap logger (`cmd/main.go.tmpl`), structured
  key-value lines.
- Auth: the metrics endpoint is served over TLS behind an authn/authz role
  (`config/rbac/metrics_auth_role*.yaml`,
  `config/default/cert_metrics_manager_patch.yaml`).

### vscode-extension (v1.1.0)

A VS Code extension in TypeScript, with a stand-in `vscode` module so its tests
run in Node.

- CI: `make test-templates`, combo "vscode extension": its `make check`, then
  `make dist` at its version, checking `dist/<container>-<version>.vsix`.
- Tests: `src/greeting.test.ts` and `src/extension.test.ts`; `package-check`
  runs `vsce package`.
- Lint: ts-standard's.
- README: `README.md.tmpl`: install the `.vsix`, develop, `make dist`.
- Deploy: gitea-release attaches `dist/<container>-<version>.vsix`.
- Logging: a `LogOutputChannel` named after the extension (`src/extension.ts.tmpl`);
  the command logs at `info` when it runs and at `error` when it fails.
- Auth: n/a: it runs in the user's editor.

### go-standard (v1.4.0) and go-operator-standard (v1.4.0)

The Go rulebook, and its kubebuilder variant (protected generated paths,
envtest).
Since v1.4.0 `AGENTS.md` tells the implementer which Domains the system is
about and to read `docs/agents/domain/<name>/README.md` and its topic files
before planning a change, and the reviewer guide to check the change against
each domain's common mistakes; without Domains both read as before (#1758).

- CI: go-operator-standard: `make test-templates-operator`, as for
  go-kubebuilder. go-standard: its `make check` in `make test-templates`'
  combo "go cli".
- Tests: `Makefile.tmpl`, target `test` (`go test -race`; envtest for the
  operator), part of `check`; their `check.yaml.tmpl` runs `make check`.
- Lint: `.golangci.yml` (golangci-lint), `fmt-check`, `vet`, `tidy-check`; the
  operator's adds `generated-check`.
- Logging: go-standard's `docs/style/principles.md` and `docs/style/go.md`
  (actionable, no secrets, return or log, never both); go-operator-standard's
  `AGENTS.md.tmpl` (`log.FromContext`, key-value pairs).
- README, Deploy, Auth: n/a: Standards.

### factory-self (v1.0.0)

The Standard of the factory's own repository, adopted by Container
`c4factory` ([self-hosting.md](plans/self-hosting.md), decision 5). Its only
file is a plain `.gitea/workflows/check.yaml`, the same bytes in the
repository; its `protectedPaths` are the fence agents may not change.

- CI: its job `check` runs `make check` on runner `go`;
  `TestFactorySelfWritesItsCheckAsIs` checks the file and its bytes.
- Tests, Lint, Logging: the factory's own `make check`.
- README, Deploy, Auth: n/a: a Standard.

### The AWS Frameworks

aws-lambda-ts (v1.5.0), aws-lambda-go (v1.4.0) and aws-lambda-py (v1.4.0): an
HTTP API on Lambda with a DynamoDB table. aws-ec2-ts (v1.3.0) and
aws-fargate-ts (v2.1.0): a Node service behind an HTTPS load balancer.
aws-queue-ts, aws-schedule-ts and aws-event-ts (v1.2.0): an SQS, EventBridge
Scheduler or EventBridge consumer on Lambda. aws-static-site-ts (v1.0.0): a
site on S3 behind CloudFront and WAF. Each is a CDK app paired with
aws-standard.

- CI: `make test-templates` runs each one's combo's `make check` (combos
  "aws lambda function", "aws lambda function in go", "aws python lambda
  function", "aws ec2 service", "aws fargate service", "aws queue consumer",
  "aws scheduled job", "aws event consumer", "aws static site"): its tests,
  a synth with CDK Nag and cfn-lint; nothing calls AWS.
- Tests: the handler's tests (`src/*.test.ts`, `src/*_test.go.tmpl`,
  `tests/test_*.py`) and the stack's CDK assertions (`infra/*_stack.test.ts`),
  run by aws-standard's `make test`.
- Lint: aws-standard's `make check`.
- README: `README.md.tmpl` in each: develop, tiers, deploy with `cdk deploy`.
- Deploy: gitea-release attaches aws-standard's `make dist` assembly to
  each release, for the operator's `cloudFormation` DeployTarget to ship
  (#1071); or `cdk deploy` with the owner's credentials.
- Logging: JSON lines to CloudWatch: Powertools Logger (the TypeScript and
  Python Lambdas, `src/handler.ts`, `app/handler.py`), `slog`'s JSON handler
  (`src/main.go.tmpl`), a JSON line per request (aws-ec2-ts `src/server.ts`,
  aws-fargate-ts `src/main.ts`); load balancer, CloudFront and S3 access logs
  (aws-static-site-ts `infra/site_stack.ts`); the HTTP API's access log, a
  JSON object per request (the Lambda Frameworks' `infra/api_stack.ts`,
  since aws-lambda-ts v1.4.0 and aws-lambda-go and -py v1.3.0, #1420).
- Auth: the Lambda APIs take SigV4-signed requests (`HttpIamAuthorizer` in
  `infra/api_stack.ts`). aws-ec2-ts (since v1.2.0) and aws-fargate-ts
  (since v1.3.0) verify a Cognito access token of the stack's own user pool
  and app client on every route but `/health` (`src/auth.ts`, `aws-jwt-verify`;
  `infra/user_pool.ts`, #1479). n/a for the queue, schedule and event consumers (no users) and the
  static site (public by design).

### aws-standard (v1.21.0)

The AWS rulebook: AGENTS.md, the style and Well-Architected digests, a
runbook, `infra/standard.ts` with CDK Nag. Since v1.18.0 the check workflow
writes `~/.npmrc` and `~/.netrc` as ts-standard's does, so `npm ci` and `uv`
fetch factory packages (#1231); since v1.18.1 `~/.netrc` names the index's
host without its port, as ts-standard v1.6.2's does (#1696). Since v1.19.0
`make dist VERSION=<tag>` writes both tiers' cloud assemblies, flat, into
`dist/` (`infra/dist.ts`, tested by `infra/dist.test.ts` on a synth) for
gitea-release to attach (#1607); v1.19.1's runbook passes Prettier
(#1610).
Since v1.20.0 `AGENTS.md` tells the implementer which Domains the system is
about and to read `docs/agents/domain/<name>/README.md` and its topic files
before planning a change, and the reviewer guide to check the change against
each domain's common mistakes; without Domains both read as before (#1758).
Since v1.21.0 the Standard knows an Aurora cluster: `BACKED_UP_TYPES`
gains `AWS::RDS::DBCluster`, the backup selection names clusters by
`DBClusterArn` as it names tables by `Arn`, and the encryption (a customer
managed key), removal (`Snapshot` where the tier keeps data, else
`Delete`), deletion protection and recovery (35 days of backups) rules
check every cluster, the selection by the `CfnDBCluster`'s
`attrDbClusterArn` (the L2's `clusterArn` is a `formatArn` over a `Ref`, an
ARN written out); where the tier has no point-in-time recovery it
acknowledges `AwsSolutions-SMG4` on every secret, and where it keeps no
data `AwsSolutions-RDS10` on every cluster (#1921).

- CI: its `make check` in every AWS combo of `make test-templates`.
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
- README, Deploy: n/a: a Standard; its Makefile deploys nothing on purpose,
  and `make dist` only writes the assembly the `cloudFormation` DeployTarget
  ships.

### dynamodb (v1.3.0)

A Connector: a CDK construct (`infra/connector_dynamodb.ts`) making one
on-demand, encrypted table for a function Container. Since v1.1.0
`connectDynamoDB` takes a `DataStoreUser` (anything `IGrantable` with an
`addEnvironment`: a Lambda `Function`, a task role with its container, an
instance role with its unit's environment) in place of a Lambda, and its
file refuses a Framework that renders no `infra/connectors.ts` with
`{{ fail }}`, so the Container fails with a `TemplateError` naming the
Framework and the Connector instead of getting a file nothing calls (#1739,
[docs/plans/aws-data-stores.md](plans/aws-data-stores.md)). Since v1.2.0 it
supports `service` too, for aws-fargate-ts (the task role and the App
container's environment) and aws-ec2-ts (the instance role and the unit's
environment) (#1742), and since v1.3.0 `worker` and `job`, for aws-queue-ts,
aws-event-ts and aws-schedule-ts (#1741).

- CI: `make test-templates`, combos "aws lambda function with dynamodb",
  "aws lambda function in go with dynamodb", "aws python lambda function
  with dynamodb", "aws fargate service with dynamodb", "aws ec2 service
  with dynamodb", "aws queue consumer with dynamodb", "aws event consumer
  with dynamodb" and "aws scheduled job with dynamodb": their `make check`.
- Tests: `infra/connector_dynamodb.test.ts`, in the Frameworks' vitest
  include, a non-Lambda grantee included;
  `TestDynamoDBConnectorConnectsToAWSLambdaTS`,
  `TestDynamoDBConnectorConnectsToOtherAWSLambdas`,
  `TestDynamoDBConnectorConnectsToAWSServices`,
  `TestDynamoDBConnectorConnectsToAWSWorkersAndJobs`,
  `TestDynamoDBConnectorFailsAFrameworkThatDoesNotWireIt`,
  `TestDynamoDBConnectorFailsAServiceFrameworkThatDoesNotWireIt` and
  `TestDynamoDBConnectorFailsAWorkerFrameworkThatDoesNotWireIt`
  (`internal/scaffold/aws_connectors_test.go`).
- Lint: aws-standard's `make check` once rendered.
- Deploy: the table is part of the stack, retained and deletion-protected in
  production. aws-lambda-ts, aws-lambda-py and aws-lambda-go wire it in
  (#1393, #1740), aws-fargate-ts and aws-ec2-ts (#1742), and aws-queue-ts,
  aws-event-ts and aws-schedule-ts (#1741); the other AWS Frameworks fail
  the Container until their step of the plan.
- Auth: `table.grant(fn, ...)` grants the item calls on that table only.
- README, Logging: n/a: it writes no README and has no runtime code.

### rds (v1.0.0)

A Connector: a CDK construct (`infra/connector_rds.ts`) making one Aurora
Serverless v2 PostgreSQL 16 cluster for a function Container, with a
database named after the Container, its own customer managed key, a
generated secret in Secrets Manager, the Data API and IAM database
authentication on (#1923, [docs/plans/aws-rds.md](plans/aws-rds.md)).
`connectRDS` takes a `DataStoreUser` as `connectDynamoDB` does, with an
optional `network` (a VPC and security groups): with one the cluster joins
the user's VPC and hands it `RDS_HOST` and `RDS_PORT` too, without one it
makes an isolated two-zone VPC of its own and the user reaches it through
the Data API. Production scales 0.5 to 8 ACU with a reader, keeps 35 days,
rotates the secret every 30 days, is deletion-protected and snapshotted on
removal; development scales 0 to 1 ACU, pauses, keeps 1 day and goes with
the stack. Its file refuses a Framework that renders no
`infra/connectors.ts` with `{{ fail }}`, and the catalog's `wiredBy` refuses
a Container on a Framework that doesn't wire it (`NotWired`).

- CI: `make test-templates`, combos "aws lambda function with rds" and "aws
  lambda function with dynamodb and rds": their `make check`
  ([docs/screenshots/1923-aws-lambda-ts-rds-check.txt](screenshots/1923-aws-lambda-ts-rds-check.txt)).
- Tests: `infra/connector_rds.test.ts`, in the Framework's vitest include,
  both tiers and a user with a network;
  `TestRDSConnectorConnectsToAWSLambdaTS` and
  `TestRDSConnectorFailsAFrameworkThatDoesNotWireIt`
  (`internal/scaffold/aws_connectors_test.go`).
- Lint: aws-standard's `make check` once rendered, CDK Nag and cfn-lint
  included.
- Deploy: the cluster is part of the stack; aws-lambda-ts (v1.5.0) wires it,
  alarms on its CPU and connections and backs it up by `DBClusterArn`. No
  other Framework does yet.
- Auth: `grantDataApiAccess` grants the `rds-data` calls on that cluster and
  `GetSecretValue` on its secret only; an ingress rule from the user's
  security groups where it has a network.
- README, Logging: n/a: it writes no README and has no runtime code; the
  own VPC has a flow log of rejected traffic.

### hello, hello-standard, hello-html, hello-html-standard

The end-to-end suite's fixtures (`test/e2e`): the smallest Container, page and
Standards, kept tiny on purpose so a stand-in agent's Task is fast. Every
promise but a README is n/a for them; `hello-standard`'s `make check` runs
`hello.sh` and expects `hello, world`. They ship nothing a user builds on.

## Keeping it current

A pull request that closes one of these issues fills its cell, with the
evidence, in the same change. A new template adds its row.
