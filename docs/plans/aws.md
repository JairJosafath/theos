# Plan: Containers that run on AWS

GitHub issue #1028, filed by the owner (`source/dev`). Agent: otter-1028.

## Goal

The factory scaffolds Containers that run on AWS, in the three flavours AWS
comes in: serverless, PaaS and IaaS. The templates carry AWS's own practice:
infrastructure as code, the AWS SDK, and the Well-Architected Framework's
pillars as rules an agent reads and a check enforces. They are built on what
the 19 templates in `templates/` taught us, so the AWS ones don't repeat
those mistakes.

The shape was decided by the maintainer on #1028 (comment of 13 October
2026); this plan writes it down, adds what the retrospective below settles,
and splits the work into steps.

Done when every step below is merged: a Brief or a hand-written model can
pick an AWS Framework for a `function` or `service` Container, the scaffolded
repository's `make check` synthesizes its CloudFormation offline and passes
the Well-Architected rules, and `make check` in this repository proves each
template renders and fits.

## Template retrospective

What adding and changing the existing templates taught us, and the rule each
lesson gives the AWS templates.

1. **Starter code broke its own Standard's rules.** The react-vite and
   node-cli starters failed ts-standard's file naming and its issue-template
   examples, and review caught it (3c20ba0, b24c110). *Rule:* each AWS
   Framework's starter passes `aws-standard`'s `make check` in its own step,
   run on a scaffolded copy, with the output in the pull request.
2. **Per-Framework differences live in the Standard.** ts-standard's
   `Makefile.tmpl` branches on `.Framework`, and its `docs/style/{react,node,
   vscode}.md` render only for their Framework (an all-whitespace render
   leaves the file out). Each new Framework meant a Standard bump (8cec728).
   *Rule:* the same here. The Frameworks carry the app and its CDK stack; the
   Standard carries the Makefile, lint, CI, CDK Nag and the docs, and grows a
   branch per Framework in the step that adds it.
3. **A Container has one Standard, and two layers can't write one path.**
   `standardRef` is a single reference and `CheckStandard` fits one Standard
   to one Framework, so `aws-standard` can't sit on top of `ts-standard`: it
   writes the same Makefile, AGENTS.md and workflow. *Rule:* `aws-standard`
   is a whole TypeScript Standard. It starts as a copy of ts-standard's
   TypeScript rules (eslint, Prettier, `typescript.md`, `principles.md`) at
   the version it copies, says that version in its `SOURCES.md`, and a table
   test in `internal/scaffold` fails when a shared file differs from
   ts-standard's at the pinned version, so the two can't drift silently.
4. **Every change is a new pinned version in four places.** A template
   change means a new tag, a `templates/versions.txt` line, the chart's
   `catalog.entries` ref, and the controller tests and samples that name it
   (8debbc9). The seed only warns when a folder differs from an existing tag.
   *Rule:* each step bumps all four together; `make validate-yaml` already
   fails a chart that disagrees with `versions.txt`.
5. **Lockfiles ship with the Framework.** `npm ci` and `uv sync --locked`
   pass on the first commit only because node-cli, react-vite and
   python-fastapi commit a `package-lock.json.tmpl` or `uv.lock.tmpl` with
   exact versions (catalog-breadth step 4). *Rule:* each AWS Framework
   commits its lock with `aws-cdk-lib`, `constructs`, `aws-cdk`, `cdk-nag`
   and the AWS SDK v3 clients pinned exactly, and `SOURCES.md` names the same
   versions the style digests describe.
6. **Agents have no internet.** Style digests are embedded and dated in
   `SOURCES.md`, self-contained, and name the lint rules they explain.
   *Rule:* the Well-Architected guidance is a digest too:
   `docs/well-architected.md`, one section per pillar (operational
   excellence, security, reliability, performance efficiency, cost
   optimization, sustainability), each naming the CDK Nag rules that enforce
   it and what the agent does by hand where no rule does. Sources are the
   Well-Architected Framework and its Serverless Applications Lens, read on
   a date `SOURCES.md` records.
7. **A check needs a toolchain the runner has.** The Flutter templates
   needed their own Flutter and Android runner and agent images, and were
   removed with them (c1388cc, 93e5133): a template that needs a new
   toolchain image costs far more than the template. The runners are `base` (Node, git, make) and `go` (adds Go, uv,
   golangci-lint); neither has Docker or the AWS CLI. *Rule:* nothing in an
   AWS template's `make check` needs Docker, an AWS account or credentials.
   `cdk synth` is offline with Node alone; Lambda bundling uses the locally
   installed esbuild (never the Docker fallback); a Fargate image is a
   container asset that synth only stages; `cfn-lint` runs through the
   runner's pinned uv as `uvx cfn-lint==<pinned>`.
8. **Caches and artifacts must stay small.** CI keeps the npm cache in
   `/tmp` (the runner's /tmp is in memory; an on-disk cache stalled the
   node), and a Dockerfile must ignore CI artifacts (8debbc9). *Rule:* the
   same `npm_config_cache`, and `cdk.out/` in `.gitignore`,
   `.prettierignore`, eslint's ignores and any `.dockerignore`.
9. **The agents' rules never change, and protected files say so.** Standard
   docs once told the implementer the reverse of the spec (f31a612); tests
   once started a browser (3b83fa1) or held wall-clock bounds (e95c8e0).
   *Rule:* `aws-standard` copies ts-standard's agent guides and protected
   paths, adds `cdk.json` and `docs/well-architected.md` to them, and its
   guides say no test calls AWS: tests use CDK's `Template.fromStack`
   assertions and stubbed SDK clients.
10. **A layer that isn't in the chart doesn't exist for a Brief** (catalog
    C8). *Rule:* each step adds its catalog entry, a `templateCombos` row in
    `internal/scaffold/templates_test.go`, `.c4factory/README.md` and a line
    in `docs/getting-started.md`, and teaches the planner guide when to pick
    it.

## Decisions

1. **AWS CDK in TypeScript is the one IaC layer** (maintainer). It compiles
   to CloudFormation and `cdk synth` runs offline, so the whole check runs in
   `make check` and in Gitea CI. Terraform, SAM and Pulumi are not in this
   plan.
2. **Flavours are Frameworks; AWS rules are a Standard** (maintainer). The
   Frameworks and their order: `aws-lambda-ts` (serverless: Lambda, API
   Gateway HTTP API, DynamoDB), `aws-fargate-ts` (PaaS: ECS on Fargate behind
   an Application Load Balancer), `aws-ec2-ts` (IaaS: EC2 in an Auto Scaling
   group behind an ALB). Each Framework holds its app code in `src/` and its
   CDK app in `infra/`, one stack per Container.
3. **A `function` Container type** for serverless, the one the design
   foresaw ("one enum value plus templates"); `aws-fargate-ts` and
   `aws-ec2-ts` are type `service`. The type lands first, on its own, with
   no catalog entry supporting it, so nothing can pick it until step 2.
4. **No Deployment layer with the AWS Frameworks.** The CDK app is the
   deployment. A Deployment can't be limited to Frameworks, so
   `k8s-manifests` picked with `aws-fargate-ts` fails the Container on the
   shared `Dockerfile` path, as the one-owner rule intends; the planner
   guide says AWS Frameworks take no Deployment and no Compute.
5. **The check:** `make check` is ts-standard's (deps, fmt-check, lint,
   typecheck, vitest) plus `synth` (`cdk synth --quiet` with CDK Nag's
   `AwsSolutions` pack applied as an Aspect, so an unsuppressed finding fails
   synth) and `cfn-lint` on `cdk.out/*.template.json`. A suppression needs a
   written reason, which CDK Nag already requires; the reviewer guide says to
   check each one. (Step 2: CDK Nag 3 has no Aspect; the pack is a
   validation plugin, `Validations.of(app).addPlugins(new
   AwsSolutionsChecks(app))`, and a finding still fails synth. A suppression
   is `Validations.of(construct).acknowledge({ id, reason })`. cfn-lint
   ignores only W3005, which CDK's own `DependsOn` on the function's role
   trips.)
6. **AWS defaults the Standard requires:** every stack tagged with project,
   system and container names; encryption at rest on; least-privilege IAM
   through CDK grants, never `*` actions; log retention set; removal policy
   explicit. Most are `AwsSolutions` rules already; the rest are in the
   Standard's own vitest assertions shipped as an unprotected test it
   carries, like ts-standard's dist-name tests (2de7d04).
7. **SDKs follow the language** (maintainer). TypeScript and AWS SDK v3
   first. A Python (boto3) Lambda and a Go (SDK v2) Lambda are later steps,
   after the first Framework ships; their CDK app stays TypeScript, so
   `aws-standard` covers them with a Framework branch that adds the
   language's own check.
8. **No AWS account, no deploy** (maintainer). No step deploys, bootstraps
   or calls AWS. Deploying from the factory needs the owner's credentials and
   is its own item later, not in this plan.

## Steps

Each step is a GitHub issue that names #1028, lands on its own and leaves
main working.

1. **The `function` Container type** (#1029). Enum value in
   `api/v1alpha1/shared_types.go`, `make manifests`, `internal/model`'s
   type constants, the UI's copies (`ui/src/k8s/types.ts`,
   `ui/src/model/describe.ts`), the planner guide's type list, and the
   design's type list. *Done when:* `make check` passes with an envtest
   spec creating a `function` Container and a model test that no catalog
   entry in the chart supports it yet.
2. **`aws-standard` and the serverless Framework `aws-lambda-ts`** (#1030). Both
   v1.0.0, in one step because neither renders or checks without the other.
   *Done when:* `make check` passes with the combo row rendering,
   the drift test against ts-standard, and the chart entry; a scaffolded copy's
   `make check` (synth, CDK Nag, cfn-lint, tests) passes on this device
   with its output in the pull request.
3. **The PaaS Framework `aws-fargate-ts`** (#1031), and `aws-standard` v1.1.0 with
   its branch. *Done when:* the same as step 2, for this Framework.
4. **The IaaS Framework `aws-ec2-ts`** (#1032), and `aws-standard` v1.2.0 with its
   branch. *Done when:* the same as step 2, for this Framework. (Step 4,
   landed as aws-standard v1.2.0, after #1034 took v1.1.0: the
   instances sit in isolated subnets with no NAT gateway and reach S3,
   DynamoDB and CloudWatch Logs through endpoints; the load balancer is
   internal, so no route needs an authorizer and no rule is open to the
   world. The server ships as an S3 asset that synth bundles with esbuild,
   and user data runs it under systemd. ALB access logs use the log
   delivery service principal, since `logAccessLogs` needs a region the
   environment-agnostic stack doesn't have. aws-ec2-ts turns
   `exactOptionalPropertyTypes` off: aws-cdk-lib's `Vpc` is not an `IVpc`
   under it. The Standard's test now lets a `Deny` name every action and
   reads tags with extra keys. A Deployment or Compute picked with
   aws-ec2-ts writes no path the Framework does, so only the planner guide
   keeps them off.)
5. **A Python (boto3) Lambda Framework `aws-lambda-py`** (#1033), with
   `aws-standard` adding ruff and pytest for its branch. Waits on step 2.
   (Landed as aws-lambda-py v1.0.0 and aws-standard v1.3.0. The branch adds
   mypy `--strict` too, and copies python-standard's `ruff.toml`, `mypy.ini`
   and `python.md`'s general sections, which a drift test keeps equal, as it
   does ts-standard's files. The function is the package `app/`; the stack
   bundles it with uv's local bundling, the wheels `uv.lock` pins for arm64
   Linux, never Docker; uv reads its cache or PyPI, as `uvx cfn-lint`
   already does.)
6. **A Go (SDK v2) Lambda Framework `aws-lambda-go`** (#1034), with `aws-standard`
   adding the Go checks for its branch. Waits on step 2.
   (Step 6: `aws-standard` v1.1.0. The function is its own Go module in
   `src/`, not at the root: `node_modules/` holds stray `.go` files, aws-cdk's
   `cdk init` templates among them, that would join a root module and break
   `go mod tidy`. The stack builds it with `Code.fromAsset` and local
   bundling that runs `go build` for arm64 into `bootstrap`; no
   `@aws-cdk/aws-lambda-go-alpha`, an alpha module. `.golangci.yml` and
   `docs/style/go.md` are go-standard's, byte for byte, kept so by the drift
   test.)

Steps 3 to 6 each wait on step 2; step 2 waits on step 1. Each bumps
`aws-standard`, so two of them in flight at once meet in a merge of the
Standard's version; the second to merge takes the next minor version.

## Not in this plan

Deploying to an AWS account, CDK bootstrap, a Gitea Actions deploy job, and
credentials for any of them; Terraform and SAM; Compute entries for AWS
sizing.
