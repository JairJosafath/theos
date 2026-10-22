# Plan: deploy AWS apps to an AWS account

GitHub issue #1071, filed by the owner (`source/dev`, priority/4). Agent:
otter-1071. It builds on the deploy model of [deploy.md](done/deploy.md)
(#1068), the tiers, outputs and parameters of
[aws-production.md](aws-production.md) (#1067) and the release images of
[images.md](images.md) (#1069), and is the sibling of
[k8s-deploy.md](k8s-deploy.md) (#1072).

## Goal

A release of an AWS Container runs in the account and region its
DeployTarget names: CI synthesizes the CDK app and attaches the cloud
assembly to the release, and the operator, with the target's credential,
publishes the assets and creates or updates the stack through a
CloudFormation change set, bootstrapping the environment when it can. The
Container's status shows the stack's version, status, entry point URL and
failure reason, and the UI shows the same. The README says which role the
factory needs in the account and which account-level services it needs and
recommends.

Done when every step below is merged: a release of `aws-lambda-ts`,
`aws-lambda-go`, `aws-lambda-py` and `aws-ec2-ts` is shipped through a
`cloudFormation` target against fake CloudFormation, S3 and STS, with the
contract suite and envtest green; `aws-fargate-ts` follows in the last step
with its image. No test uses an AWS account.

## Where we stand

- **Five AWS Frameworks, none deploys.** All `deploysItself` with
  `aws-standard`; its `make check` synthesizes both tiers
  (`cdk synth -c tier=<t> -o cdk.out/<t>`) under cdk-nag and cfn-lint, and
  its Makefile says "Nothing here deploys, bootstraps or calls AWS". No AWS
  template has a release workflow or a `dist` target; the README says
  deploying is the owner's, by hand.
- **Each stack already carries what a target needs** (aws-production.md
  decision 2): the outputs `EntryPoint` and `AlarmTopicArn`, the parameter
  `AlertEmail` (empty subscribes nobody) and, for `aws-ec2-ts` and
  `aws-fargate-ts`, `CertificateArn` with no default. The stack is named
  after the Container in production and `<name>-development` otherwise; a
  production stack with `copyToRegion` has a second stack,
  `<name>-backup-copy`, in another region, which the main one depends on.
- **Every stack uses CDK assets** with the default synthesizer: zipped
  Lambda code, an S3 file asset for the EC2 server, and a Docker image asset
  for Fargate that only `cdk deploy` with Docker builds. All of them need
  the CDK bootstrap (the `CDKToolkit` stack: the assets bucket, the ECR
  repository and the deploy, file-publishing, image-publishing and
  cfn-exec roles) in the account and region.
- **The deploy model is built** (#1161 to #1163): the Deployer port with its
  fake and contract suite, `DeployTarget` with the `github` member, the
  deploy loop in the Container reconciler, `status.deploys[]`, `Deployed`,
  approval and *Needs you*. The `cloudFormation` member is named in
  `design.md`, `architecture.md`'s Ports row and the UI's labels, and
  nowhere else. `Shipment` has `Files` (streamed from the Git host) and
  `Image` (a digest, filled only for `model.BuildsImage` choices, which the
  AWS Frameworks are not).
- **The `gitea-release` Deployment** attaches whatever `make dist
  VERSION=<tag>` writes to `dist/` to the Gitea release of each `v*` tag,
  with the job token, and supports `cli`, `extension` and `operator` only.
- **The Gitea runners** have no Docker and no AWS CLI, and `aws-sdk-go-v2`
  is not in `go.mod`.

## Decisions

1. **CI synthesizes, the operator ships; no OIDC job.** The deploy stays in
   the operator (deploy.md decision 3). An OIDC deploy job was weighed and
   left: Gitea Actions issues no OIDC tokens the way GitHub does, so the job
   would hold a long-lived key anyway; even with short-lived credentials
   the job runs the Makefile the agents wrote, which is the rule the model
   keeps; and the runners would need Docker and the AWS CLI, which the
   operator's path needs neither of. The operator uses the AWS SDK for Go
   v2 and calls only CloudFormation, S3, STS, SSM (the bootstrap version)
   and, for Fargate, ECR.

2. **The release carries the cloud assembly, flat.** `aws-standard` gets a
   `dist` target, and `gitea-release` gains the AWS types (`function`,
   `service`, `static-site`), so an AWS Container ships its release through
   the Deployment it already could have picked; the `.gitea/workflows/
   release.yaml` path stays `gitea-release`'s, so no two layers write it.
   `make dist VERSION=<tag>` synthesizes both tiers and writes to `dist/`:
   - `<tier>.manifest.json`, `<tier>.<stack>.template.json` and
     `<tier>.<stack>.assets.json` for each tier and stack;
   - `asset.<hash>.zip` for each directory asset (zipped as cdk-assets
     would), `asset.<hash>.<ext>` for each file asset, once per hash since
     both tiers share them;
   - no Docker image asset (decision 8 replaces Fargate's).
   Why flat files and not one archive per tier: the adapter streams each
   asset from the Git host straight to S3 with its known size and never
   holds an archive in memory; and a template too large for an inline body
   is already a file asset of its own, so the same path covers it.

3. **The member is `cloudFormation: {region, account}`.** `region` is
   required (the AWS region pattern); `account` is an optional twelve-digit
   guard: `Check` compares it with the credential's account from
   `sts:GetCallerIdentity` and refuses a mismatch (`CredentialRefused`, its
   message naming both), so a key for the wrong account never deploys. The
   Secret's keys are `accessKeyId` and `secretAccessKey`, with an optional
   `sessionToken`. No role ARN field: the roles come from the bootstrap
   (decision 4), and the assembly names them. A credential from the
   operator's own pod identity (EKS) is a follow-up.

4. **The adapter uses the CDK bootstrap roles, so the factory's own role is
   tiny.** The assembly's `manifest.json` and `*.assets.json` name the
   roles `cdk deploy` would assume: the deploy role for CloudFormation, the
   file-publishing role for S3, the image-publishing role for ECR, and the
   cfn-exec role CloudFormation runs the stack as; the adapter assumes them
   with STS, session name `c4factory-<namespace>-<target>`, and resolves
   `${AWS::AccountId}` and `${AWS::Region}` from the credential's identity
   and the target's region, as cdk-assets does. The credential itself needs
   only `sts:AssumeRole` on the `cdk-<qualifier>-*-role-<account>-<region>`
   roles, plus `cloudformation:DescribeStacks` and `ssm:GetParameter` on
   `/cdk-bootstrap/*` to read state and the bootstrap version. The README
   prints that policy. Why not a role of our own: the bootstrap's roles are
   AWS's documented least-privilege split for exactly this, and the stacks
   already expect the cfn-exec role.

5. **Bootstrap included, when the credential can.** At `Ship`, when the
   `CDKToolkit` stack is missing or its `BootstrapVersion` is below the
   `requiresBootstrapStackVersion` of the release's manifest, the adapter
   creates or updates `CDKToolkit` with the credential itself (the roles
   don't exist yet), from the bootstrap template the operator embeds:
   `bootstrap-template.yaml` copied from the aws-cdk release `aws-standard`
   pins (`aws-cdk` in its `package.json.tmpl`), named with its version, with
   `hack/cdk-bootstrap-template.sh` to refresh it and a test that its
   version meets what the templates' synth requires. The default cfn-exec
   policy is `AdministratorAccess`, as `cdk bootstrap`'s is. When the
   credential may not (AccessDenied), `Ship` fails `PrerequisiteMissing`
   with the `cdk bootstrap aws://<account>/<region>` command for the
   account owner, so a production account bootstrapped by its owner, with a
   scoped `--cloudformation-execution-policies`, gives the factory only
   decision 4's policy. The template's own `CheckBootstrapVersion` rule
   stays the last word.

6. **One new port error, `ErrPrerequisiteMissing`.** Something a person sets
   up once at the target is missing: the bootstrap, or a template
   parameter with no default that `parameters` doesn't give
   (`CertificateArn`). It is neither a refused credential nor a failed
   rollout: the controller maps it to reason `PrerequisiteMissing`, ships
   again on the next poll like a refused credential (the Container wakes
   when the DeployTarget changes, so a fixed `parameters` ships at once),
   and *Needs you* lists it with the message. The fake and the contract
   suite learn it (a target `env.Unprepared` fails `Ship` with it and ships
   nothing).

7. **Ship is one change set; State reads the stack back.**
   - **Ship**, for the tier's assembly, in the manifest's dependency order
     (the backup-copy stack first; a stack whose environment names another
     region than the target's is refused with a plain error naming both,
     until cross-region assemblies are supported): publish each asset
     the stack needs that S3 lacks (`HeadObject`, then a streamed
     `PutObject` of the release file), then `CreateChangeSet` (`CREATE` or
     `UPDATE` as the stack exists; `TemplateURL` of the template asset;
     `RoleARN` the cfn-exec role; `CAPABILITY_IAM`, `CAPABILITY_NAMED_IAM`,
     `CAPABILITY_AUTO_EXPAND`; the template's parameters filled from
     `Target.Parameters` by name, the others ignored; tags
     `c4factory.io/app`, `c4factory.io/tier`, `c4factory.io/version` and
     `c4factory.io/stacks`, the assembly's stack count) and `ExecuteChangeSet`
     with a `ClientRequestToken` carrying the version. An empty change set
     (nothing changed) is deleted and counts as deployed. A stack whose
     `c4factory.io/app` tag names another app fails `StackTaken`, as the
     GitHub member's `TagTaken`. Repeating `Ship` for a version the stack is
     deploying or runs starts nothing.
   - **State** is `DescribeStacks` plus the newest stack-level event: the
     event's token gives the version last attempted, so a rolled-back update
     reports that version `Failed` (reason `RolledBack`, message the
     stack's failure reason) rather than the old version running, which
     would make the loop ship it again; `*_IN_PROGRESS` is `Deploying`,
     `CREATE_COMPLETE`/`UPDATE_COMPLETE` is `Deployed`, `ROLLBACK_*`,
     `*_FAILED` and `UPDATE_ROLLBACK_*` are `Failed`, except that a stable
     stack (complete, or `UPDATE_ROLLBACK_COMPLETE`) tagged the version
     `State` is asked for runs it, so wanting the version a rollback
     returned to is `Deployed`; fewer stacks running the version than their
     `c4factory.io/stacks` tag says are `Deploying`. The table is
     `model.CloudFormationDeployState`, pure and table-tested.
   - **Health is the stack status**: `Healthy` on a complete state,
     `Progressing` in progress, `Degraded` on a failed state. Alarms are not
     read (deploy.md decision 6).
   - **URL** is the `EntryPoint` output: as is when it starts with `http`,
     else `https://` before it when the stack has a `CertificateArn`
     parameter, else `http://`.
   - **No stack is ever deleted** by the factory: removing an entry leaves
     what runs there (deploy.md decision 2).

8. **Fargate's image goes through the Registry and ECR.** `aws-fargate-ts`
   becomes an image-building Framework (`model.BuildsImage`), so the
   `image` runner builds and pushes it by digest (#1069) and the Shipment
   carries `Image`. Its stack takes an `ImageUri` parameter and
   `ContainerImage.fromRegistry` instead of `fromAsset`, so its synth needs
   no Docker. The adapter, with the image-publishing role, copies the digest
   from the Registry into the bootstrap ECR repository
   (`cdk-<qualifier>-container-assets-<account>-<region>`) with
   go-containerregistry, as the GHCR copy of #1070 does, and passes
   `ImageUri=<repository>@<digest>`. A shipment without `Image` for a stack
   that asks one is `ErrArtifactMissing`.

9. **The account's baseline services, written down** (aws-production.md
   decision 9). The `aws-standard` README's new "Deploying" section says
   what the factory needs in a target account (the CDK bootstrap at the
   version the Standard requires; the policy of decision 4; the
   certificate behind `CertificateArn` for the service Frameworks) and what
   it recommends and never creates (an account per tier; CloudTrail,
   GuardDuty, Security Hub, AWS Config, Budgets, activating the
   `STANDARD_TAGS` as cost allocation tags), and `docs/runbook.md` points
   at it.

10. **Tests use fakes, in Go.** The adapter's AWS calls go through narrow
    interfaces of the SDK clients it uses; `internal/adapters/cloudformation`
    carries in-memory fakes of CloudFormation (stacks, change sets, events
    that finish on the next read), S3 (objects by key), STS (identities,
    assumable roles) and ECR, and runs `contract.Deployer` on them.
    Fixtures are a trimmed `make dist` of each of the four Frameworks at
    both tiers under `testdata/` (templates and manifests whole, assets
    replaced by small stand-ins), so the Done-when's four Frameworks are
    each shipped in a test. The envtest of the member runs on the fake
    Deployer, as the GitHub member's does.

## Steps

Each step is a GitHub issue that names #1071, lands on its own with `make
check`, and leaves main working. Steps are priority/4.

| Step | Issue | Waits on | How we know |
| --- | --- | --- | --- |
| 1. This plan and the design change | #1071 (`Refs`) | | landed in `docs/plans/` and `docs/design.md` |
| 2. `make dist` in `aws-standard`, AWS types in `gitea-release` | #1607 | | `dist` writes the flat assembly of both tiers (decision 2), checked by the Standard's test on a synth; `gitea-release` supports `function`, `service`, `static-site`; both versions bump with their pins; the README words; scaffold table tests and `make validate-yaml` |
| 3. The CDK assembly model and the CloudFormation adapter | #1608 | | `internal/model/cdk.go` (manifest, assets, parameters, the state table) table-tested on the fixtures; `internal/adapters/cloudformation` on the port with the fakes of decision 10, `contract.Deployer` green, tests for publish, change set, token, rolled-back state, `StackTaken`, the bootstrap (created, outdated, refused) and the four Frameworks' assemblies; `ErrPrerequisiteMissing` on the port, the fake, the contract and the controller's mapping; the embedded bootstrap template and its refresh script; `aws-sdk-go-v2` in `go.mod` |
| 4. The `cloudFormation` member and the loop | #1609 | #1608 | the member on `DeployTargetSpec` with its CEL (one member, the region pattern, the account pattern), the `deployMembers` row (Secret keys; needs a Framework that deploys itself and the `gitea-release` Deployment), `deployTarget`'s conversion, the wiring in `cmd/main.go`, the Ports row; a sample and `make validate-yaml`; envtest on the fake Deployer: `Ready` for each reason, a release shipped, `PrerequisiteMissing` under *Needs you*, `Incompatible` for a Container without the Deployment; the DeployTarget page shows region and account (`make ui-test`); the README's "Deploying" section (decision 9) |
| 5. Fargate's image | #1610 | #1607, #1608, #1609 | `aws-fargate-ts` builds an image, its stack takes `ImageUri`, the adapter copies the digest into the bootstrap ECR repository on a fake and passes the parameter; the Standard's test synthesizes it without Docker |

What only an AWS account would show, and no tier tests: the bootstrap
roles' trust, CloudFormation accepting the published template URL, and the
app answering at `EntryPoint`. The README's runbook says how a person checks
them after the first deploy.

## Out of scope

- A credential from the operator's pod identity; a deploy role of our own.
- Deleting a stack, drift detection, reading alarms, and a rollback by
  CloudFormation's history: a person pins `version` in `spec.deploy`.
- Copying an image to GHCR (#1070); Terraform, SAM and Pulumi.
- The account's baseline services themselves (decision 9 names them, the
  account owner provides them).
