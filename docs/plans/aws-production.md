# Plan: AWS Containers fit for production

GitHub issue #1067, filed by the owner (`source/dev`). Agent: otter-1067. This
is the plan #1067 asks for as its first step; it widens the issue from
"Well-Architected defaults and more serverless Frameworks" to what the owner
said on 2026-10-15: the factory's AWS output must be production-grade, not
demo-grade, think in backup and recovery and the serious side of running
software in the cloud, and do each of those only as far as the user
configures it.

## Goal

A Container on an AWS Framework comes out of the factory ready to run in
production: its data is backed up and can be restored by a written procedure,
someone is told when it fails, nothing of value can be deleted by accident,
and every one of those is a choice the Container's spec states and the
repository's checks enforce. A Brief's planner makes the same choices from
the idea. New serverless Frameworks arrive on that footing, not before it.

Done when every step below is merged: `make check` in this repository renders
every AWS Framework with the `operations` block, a scaffolded copy's `make
check` fails when its stack doesn't honour the block, `docs/runbook.md` in
every AWS repository says how to restore and roll back, and the planner picks
the block from an idea.

## Where we stand

What [plans/done/aws.md](done/aws.md) built is a strict foundation: every stack
synthesizes under CDK Nag's `AwsSolutions` pack, and a finding fails `make
check`. That is stricter than any outside source the research in #1067
found. But the Well-Architected digest names the serious topics and then
defers each one "by hand, when the system's needs ask for it", and nothing
asks. On 2026-10-15 the five Frameworks stand like this:

| Topic | aws-lambda-ts, -py, -go | aws-fargate-ts | aws-ec2-ts |
| --- | --- | --- | --- |
| Data | DynamoDB table, `RETAIN`, deletion protection, PITR | the same, plus the load balancer's log bucket | the same |
| Backup beyond PITR | none | none | none |
| Restore procedure | none | none | none |
| Alarms, notifications, dashboard | none | none; `removeDefaultDeploymentAlarm` | none (`AS3` acknowledged) |
| Environments | none | none | none |
| Configurable without editing code | stack name and tags | the same, plus `CertificateArn` | the same |
| Termination protection, stack outputs | neither | protection no, `LoadBalancerDns` yes | neither |
| Network | managed | 2 zones, one NAT gateway, public ALB on TLS | 2 zones, no NAT, internal ALB on plain HTTP |

And the Standard's own test is weaker than its digest claims: the digest
says "every stateful resource states its removal policy; data is `RETAIN`"
(`docs/well-architected.md.tmpl:114`), the test accepts any deletion policy
(`standard.test.ts:179-189`), and nothing checks PITR or deletion protection
beyond CDK Nag's `DDB3`.

Nothing deploys, which is right for this plan: deploying is #1071's, on the
deploy model of #1068, and this plan hands those two what they need (stack
outputs and deploy-time parameters). The owner's observation holds: the
Well-Architected approach was asked for, and the pillars that cost money or
need a decision (reliability's backup and recovery, operational excellence's
alarms and runbooks) were written down and left to the reader.

## Decisions

1. **The tier is where the app runs, and `development` unless something says
   `production`.** The owner decided on 2026-10-15: the default is
   `development`, as long as switching is easy. So the tier is not a property
   of the Container, which is the same code wherever it runs, but of the
   place it is deployed to: a deploy target of #1068 says `production` or
   `development`, and every AWS repository synthesizes its stacks for both
   tiers on every check and release (`cdk.out/production/`,
   `cdk.out/development/`), so a target picks the one it wants and nothing
   is re-scaffolded, re-planned or edited to go to production. A hand deploy
   picks the same way, `cdk deploy -c tier=production`, and is `development`
   when it says nothing. What the Container does say, in a new optional and
   mutable block `Container.spec.operations`, is what the app promises
   whatever tier runs it:

   ```yaml
   spec:
     operations:
       backups:
         retentionDays: 35     # daily copies kept this long by AWS Backup in production; 0 = point-in-time recovery only
         copyToRegion: ""      # a second region the copies go to; empty = none
       recovery:
         rpo: 1h               # the objectives the runbook, the alarms and the checks are written to
         rto: 4h
       alerts:
         email: ""             # who the alarms tell by default; a target overrides it per environment
   ```

   Why `development` is the default: a stack that nobody has yet pointed at
   production costs little and is thrown away freely, and the moment it
   matters is the moment a target is made, which is where the choice is
   asked. Why not a tier on the Container: it would be rendered once, and a
   Container that grows from a trial into production would need a change to
   the code to get there; a target is made for exactly that moment. What
   the tiers mean:

   | Default | `production` | `development` |
   | --- | --- | --- |
   | Removal policy of data (tables, data buckets) | `RETAIN` | `DESTROY` |
   | Deletion protection on tables, termination protection on the stack | on | off |
   | Point-in-time recovery | on | off |
   | AWS Backup plan (daily, `retentionDays`, its own vault and KMS key) | on | off |
   | Cross-region copy | when `copyToRegion` is set | never |
   | Alarms and a dashboard | on | on |
   | Subscription to the alarms | when `email` is set | when `email` is set |
   | X-Ray tracing | on | off |
   | Log retention | 1 year | 1 month |
   | NAT gateways (aws-fargate-ts) | one per zone | one |

   The block renders into `infra/operations.ts` at scaffold, and a change
   to it afterwards reaches the repository the way a Standard bump does: the
   operator renders the file again and opens and merges a pull request with
   it (step 12), reported by a condition `OperationsApplied`. Until that
   step lands, the file is changed by a Task like any other code, and the
   condition says `ChangedAfterScaffold`. A field a client sets today is
   never removed or reinterpreted (the `v1alpha1` promise of
   [plans/before-clients-return.md](before-clients-return.md)). `rpo` and
   `rto` are stated objectives for the production tier: they are written
   into the runbook and the README, the planner derives them from the idea,
   and the Standard's test checks the two things it can see (point-in-time
   recovery when `rpo` is under a day; a copy region in the runbook's
   regional-loss procedure when one is set). The rest of what they promise
   only a restore drill in an account shows, and the runbook says so.

2. **Two layers of knobs, and no third.** The app's layer is
   `operations`: it renders into `infra/operations.ts`, which the stacks
   read at synth, and it is the same for every environment. The
   environment's layer is the target's: the tier, read at synth from the one
   context value `tier` (`development` when absent) so that the resource
   graph differs per tier and nowhere else, and the deploy-time values that
   differ per environment (the alert email, a certificate ARN, a domain),
   which are CloudFormation parameters whose defaults come from
   `operations.ts`, so a deploy target of #1068 overrides them without a
   change to the code. Nothing else reads context or environment variables
   at synth: a synth for a tier is the same on every machine, which is what
   makes `make check` a verdict, and `make check` synthesizes both tiers.
   Environments themselves (staging, production, approval) are #1068's;
   this plan gives each stack what a target needs: a tier to name, outputs
   for its entry point and its alarm topic, and the parameters above.

3. **Backup and recovery, by what holds data.** DynamoDB: point-in-time
   recovery (35 days, seconds of RPO in the region) plus an AWS Backup plan
   (`AWS::Backup::BackupPlan`, `BackupSelection` by the stack's tags, a
   `BackupVault` with its own key) for the retention and the cross-region
   copy PITR can't give. A data bucket (none today; the queue and static-site
   Frameworks below add some) is versioned, with a lifecycle rule; a log
   bucket is not data and keeps its expiry. Instances and tasks hold no
   data: `docs/style/aws.md` makes that a rule, and the EBS root is not backed
   up. No vault lock: it is irreversible and an account-level choice. A
   relational database comes with an AWS Connector, not in this plan (see
   *Not in this plan*). Restore is a procedure, written: `docs/runbook.md`,
   a Standard file with a branch per Framework, says how to restore the
   table to a point in time and from a vault copy, how to roll a deployment
   back, and what each alarm means and what to do first. The implementer
   guide says a change to data handling updates the runbook's
   Framework branch.

4. **Alarms are code, and nobody is told without asking.** Each stack
   builds its alarms with `cdk-monitoring-constructs` (the owner's research
   in #1067): errors, throttles and duration for a function; 5xx, latency
   and unhealthy targets for a load balancer; CPU, memory and running tasks
   for a service; CPU and in-service instances for an Auto Scaling group;
   throttled requests and system errors for a table; the dead-letter queue's
   depth where there is one. They go to one SNS topic per stack, encrypted,
   with an email subscription only when `alerts.email` is set; the topic's
   ARN is a stack output, so a target of #1068 can subscribe its own channel
   later. A dashboard per stack. `aws-fargate-ts` stops removing ECS's
   deployment alarm and `aws-ec2-ts` stops acknowledging `AS3`: the topic is
   what they subscribe to.

5. **The Standard enforces what its digest claims.** `standard.test.ts`
   reads `infra/operations.ts` and asserts, per tier, the removal policies,
   deletion and termination protection, point-in-time recovery, the backup
   plan and vault, the alarms and topic, the subscription, tracing, log
   retention, and that every stack has an output for its entry point and
   its topic. `cfn-lint` runs with `--include-checks I` (I3011 deletion
   policies on stateful resources, I3013 retention periods). The digest's
   "by hand" items that a rule can check stop being by hand. What stays by
   hand is listed in the digest with the reason (a restore drill, a game
   day, right-sizing from measured load).

6. **X-Ray follows the tier.** On in `production`, off in `development`:
   the digest kept it off for cost and for the `IAM5` wildcard it needs, and
   the tier is where that trade-off is made once. The acknowledgement of
   `AwsSolutions-IAM5` for `xray:Put*` lives in the Standard's
   `infra/standard.ts` with its reason, not in every stack.

7. **The `ServerlessChecks` pack runs too**, after the `function` Frameworks
   carry `operations`, with one reasoned acknowledgement per rule #1067
   found wrong or duplicated (`LambdaDLQ` on synchronous handlers,
   `LambdaEventSourceMappingDestination` for SQS sources, the five
   duplicates of `AwsSolutions` rules). The alternative, `cfn-lint-serverless`,
   is a public preview and is left.

8. **Powertools where it exists.** `aws-lambda-ts` and `aws-lambda-py` take
   Powertools for AWS Lambda's Logger, Metrics and Idempotency utilities
   (idempotency backed by a DynamoDB table the stack makes, so a retried
   request changes nothing twice, which today the digest asks for by hand);
   `aws-lambda-go` keeps `slog` JSON logs, as there is no Go Powertools.

9. **Account-level services are the target's, not the Container's.**
   GuardDuty, Security Hub, CloudTrail, AWS Config, Budgets and the cost
   allocation tags' activation are set once per account and never by a
   Container's stack. #1071's plan writes which the factory needs in a
   target account and which it recommends, next to the role it needs; this
   plan's `docs/runbook.md` names them as what the account owner provides.

10. **Hardening comes before new Frameworks.** Every Framework this plan adds
    is built on `operations`, the alarms and the runbook, so steps 2 to 7
    land first. The new Frameworks follow #1067's order, by how common the
    shape is, and each is a `deploysItself` Framework under `aws-standard`
    rather than a Deployment layer, because the AWS gate (synth, CDK Nag,
    the Standard's test) lives in the Standard and a Deployment can't bring
    one. Step Functions is left out: a workflow spans the Containers it
    calls and fits one repository per Container poorly; it gets its own
    issue when a Brief asks for one.

11. **The data stays in the Framework.** Today each AWS Framework makes its
    own table, and the planner plans no DataStore for it. Modelling AWS data
    stores as DataStores with Connectors (DynamoDB, RDS) is the right shape
    and a bigger change than this plan; it is filed as a proposed follow-up
    (#1117), and this plan's backup rules are written per resource so that
    Connector can reuse them.

## Track A, per Framework

What each existing Framework gets, as #1067's *Done when* asks:

| Item | aws-lambda-ts | aws-lambda-py | aws-lambda-go | aws-fargate-ts | aws-ec2-ts |
| --- | --- | --- | --- | --- | --- |
| `infra/operations.ts`, backups, alarms, topic, dashboard, outputs, termination protection, runbook | step 3 | step 4 | step 4 | step 5 | step 6 |
| Powertools (Logger, Metrics, Idempotency) | step 3 | step 4 | no (none for Go) | no (not Lambda) | no |
| `ServerlessChecks` pack | step 7 | step 7 | step 7 | no | no |
| `cfn-lint --include-checks I` | step 3 (the Standard, every Framework) | | | | |
| X-Ray by tier | step 3 | step 4 | step 4 | step 5 (ADOT collector) | no |
| Framework-specific | | | | NAT per zone in production; ECS deployment alarm kept | TLS on the internal ALB (`CertificateArn`); `cfn-signal` so a rolling update stops on a failed health check |

## Steps

Each step is a GitHub issue that names #1067, lands on its own with `make
check`, and leaves main working. A template step bumps the four places
[plans/done/aws.md](done/aws.md) names (the tag, `templates/versions.txt`, the chart's
`catalog.entries`, the tests and samples) and shows a scaffolded copy's
`make check` output in its pull request.

| Step | Issue | Waits on | How we know |
| --- | --- | --- | --- |
| 1. This plan and the design change | #1067 (`Refs`) | | landed in `docs/plans/` and `docs/design.md` |
| 2. `Container.spec.operations` | #1107 | | the field (no tier), its CEL (patterns, ranges) and defaults; mutable, with the condition `OperationsApplied` saying `ChangedAfterScaffold` until step 12; `scaffold.Input.Operations`; `internal/model`'s defaults as a table-tested function; the UI's types and Container page; the planner guide's rule and `plan/model.yaml` check; samples; the `v1alpha1` guard sees only additions; envtest creates a Container with and without the block, and changes it |
| 3. `aws-lambda-ts` v1.1.0 and `aws-standard` v1.6.0 | #1108 | #1107 | `infra/operations.ts` rendered from the block; the tier from the `tier` context, `development` when absent, synthesized for both tiers into `cdk.out/<tier>/` by `make check` and CI; backup plan and vault; alarms, topic, subscription, dashboard; outputs; termination protection; X-Ray by tier; Powertools; `docs/runbook.md`; the digest rewritten; the Standard's test enforces decision 5 for both tiers; `cfn-lint -I` on both; a scaffolded copy's `make check` passes, and fails when `operations.ts` and a stack disagree |
| 4. `aws-lambda-py` and `aws-lambda-go` follow | #1109 | #1108 | the same, Powertools for Python, `slog` for Go; `aws-standard` bump |
| 5. `aws-fargate-ts` follows | #1110 | #1108 | the same, plus NAT per zone in production, the ECS deployment alarm, ADOT tracing by tier; `aws-standard` bump |
| 6. `aws-ec2-ts` follows | #1111 | #1108 | the same, plus TLS on the ALB with `CertificateArn`, `cfn-signal` on the rolling update, the no-data-on-instances rule; `aws-standard` bump |
| 7. `ServerlessChecks` next to `AwsSolutions` | #1112 | #1109 | the pack runs in `infra/standard.ts`; each acknowledgement names its rule and reason; the digest's "by hand" list shrinks to what no rule checks |
| 8. Queue consumer Framework `aws-queue-ts` (type `worker`) | #1113 | #1108 | SQS to Lambda with a DLQ, `reportBatchItemFailures`, Powertools batch; `enforceSSL` and log retention the source pattern lacks; `operations`, alarms (DLQ depth) and runbook; catalog entry, `templateCombos` row, planner rule |
| 9. Static site Framework `aws-static-site-ts` (type `static-site`) | #1114 | #1108 | a Vite app on S3 behind CloudFront with OAC, access logs, a minimum TLS version, a WAF web ACL in production, the bucket versioned; the rest as step 8 |
| 10. Scheduled job Framework `aws-schedule-ts` (type `job`) | #1115 | #1108 | EventBridge Scheduler to Lambda with a DLQ and retry policy, the scheduler's role scoped to the one function; the rest as step 8 |
| 11. Event consumer Framework `aws-event-ts` (type `worker`) | #1116 | #1108 | an EventBridge rule to Lambda with a rule-target DLQ and retries; the rest as step 8 |
| 12. A change to `operations` reaches the repository | #1119 | #1108 | a changed block renders `infra/operations.ts` again and the operator opens and merges the pull request, as a Standard bump does; `OperationsApplied=True` names the merged commit; envtest on a faked git provider |

Steps 3 to 6 each bump `aws-standard`; two in flight at once meet in a merge
of the Standard's version, and the second to merge takes the next minor, as
[plans/done/aws.md](done/aws.md) did.

### Step 3, as built

`aws-standard` v1.5.0 was taken by the CI-system change, so step 3 is
`aws-lambda-ts` v1.1.0 with `aws-standard` v1.6.0. What it settled, which
steps 4 to 6 copy (`docs/screenshots/1108-aws-lambda-ts-check.txt` has the
evidence):

- **The Standard owns the tiers.** `infra/standard.ts` exports `Tier`,
  `TIERS`, `TIER_RULES` (keep data, point-in-time recovery, backups,
  tracing, log retention), the `Operations` type and `tierOf(app)`;
  `applyStandard(app, tier)` sets termination protection and acknowledges
  `AwsSolutions-IAM5[Resource::*]` only on a policy whose `*` statements are
  X-Ray's, and `AwsSolutions-DDB3` only where the tier has no point-in-time
  recovery. `infra/main.ts` calls `defineStacks(app, tier)` through a type
  with both parameters, so an older `stacks.ts` taking the app alone still
  compiles: the other Frameworks pass `make check` on v1.6.0 unchanged.
- **The Standard's test applies the block where there is one.** It imports
  `infra/operations.ts` at run time when the file exists and skips the
  per-tier `describe`s when it doesn't, so a repository scaffolded before
  the block (any Framework at its version before its step) passes a
  Standard update. Its checks are per resource type (every table, function,
  HTTP API), so a Framework that adds `infra/operations.ts` gets them
  without a Standard change; a load balancer's or service's alarms are
  added to `ALARMED_METRICS` by the step that needs them.
- **Names a deploy target reads:** the parameter `AlertEmail` (default
  `alerts.email`; the email subscription has the condition `HasAlertEmail`,
  so an empty one subscribes nobody) and the outputs `EntryPoint` and
  `AlarmTopicArn`, the same in every Framework.
- **The copy region is a second stack.** In production with
  `copyToRegion`, `defineStacks` adds `<name>-backup-copy` in that region,
  holding the vault (`<name>-copies`) and its key, and the main stack
  depends on it; the plan's copy action names that vault's ARN. A stack
  holding only a vault and its key is exempt from the topic, alarm and
  output checks.
- **Framework files to copy:** `infra/alarms.ts` (topic, key, parameter,
  subscription, `MonitoringFacade`, dashboard, `AlarmTopicArn`) and
  `infra/backups.ts` (`Backups`, `BackupCopyStack`); every table is built
  with the tier's rules, the idempotency table included.
- **The docs branch per Framework.** `docs/runbook.md` renders empty, and
  so is left out, for a Framework without the block, and
  `docs/well-architected.md` has an `aws-lambda-ts` branch beside the
  serverless one `aws-lambda-py` and `aws-lambda-go` still use; step 4
  moves them onto it.
- **Synthesizing both tiers doubles the test hooks' work:** a
  `vitest.config.mts` gives `hookTimeout` room (aws-lambda-ts 120 s); step 5
  gives `aws-fargate-ts` one, which it lacks.
- **Every development stack is named `<name>-development`**
  (`stackNameFor`), so a hand `cdk deploy` with no tier never strips a
  production stack of its protections, and the Standard's test checks it.
- **`ts`, a template function,** quotes a string as Prettier does, for
  `infra/operations.ts`'s strings (an email may hold a `'`).
- **No release job yet:** `make check` and CI synthesize both tiers, but
  `aws-standard` has no release that attaches `cdk.out/`; that is #1071's.

### Step 5, as built

Step 5 is `aws-fargate-ts` v1.1.0 with `aws-standard` v1.7.0 (agent
otter-1110; `docs/screenshots/1110-aws-fargate-ts-check.txt` has the
evidence). It copies step 3: `infra/alarms.ts`, `infra/backups.ts` and
`infra/operations.ts.tmpl` are aws-lambda-ts's, byte for byte, and
`infra/stacks.ts` is its `defineStacks(app, tier)` with the copy-region
stack. What it settled, and what step 6 copies:

- **NAT gateways by tier.** Two in production, one per zone: a NAT gateway
  is zonal, and the VPC guide says to put one in each zone so a zone's
  failure leaves the others' way out. One in development, for half the
  price. It is a constant per tier in `infra/service_stack.ts`, not a
  `TIER_RULES` field: it belongs to a VPC with private subnets, which
  aws-ec2-ts doesn't have. CDK's `Vpc` makes zonal gateways; AWS's newer
  regional NAT gateway is not used.
- **The deployment alarms.** The service names three alarms with
  `enableDeploymentAlarms(..., { behavior: AlarmBehavior.ROLLBACK_ON_ALARM })`,
  beside the circuit breaker: `LoadBalancer-5xx-Critical`,
  `Service-5xx-Critical` and `Service-Unhealthy-Tasks-Critical`. Latency,
  CPU, memory and running tasks don't roll a deployment back: a slow first
  minute or a scale-out is no reason to, and the circuit breaker already
  counts tasks that don't start. The names are written out as
  `<stack>-<name>` and checked against the alarms the stack made, so a
  renamed alarm fails synth; a reference to the alarm would `Ref` it from
  the service while its metric names the service, a dependency cycle.
  `cdk.json` keeps `@aws-cdk/aws-ecs:removeDefaultDeploymentAlarm: true`,
  the `cdk init` flag every AWS Framework carries: it only decides whether
  a service that names no alarm gets an empty, disabled alarm
  configuration, and changes nothing for one that names alarms. The
  runbook says what ECS does with them: it ignores an alarm already firing
  when a deployment starts, and watches for a bake time after it.
- **The alarms.** `cdk-monitoring-constructs` 10.3.2 has no monitoring of
  an Application Load Balancer alone, so the load balancer's own 5xx
  (`HTTPCode_ELB_5XX_Count`), the tasks' 5xx (`HTTPCode_Target_5XX_Count`)
  and their p99 latency (`TargetResponseTime` over a second) are
  `monitorCustom` groups whose missing data is not breaching: the load
  balancer reports nothing while no requests come. Unhealthy tasks, CPU and
  memory (over 90 %) come from `monitorFargateApplicationLoadBalancer`,
  unhealthy by the `Minimum` as the load balancer guide advises; running
  tasks under the minimum of two from Container Insights' `RunningTaskCount`,
  as a custom metric, since the library's running-task alarm only fires
  above a maximum.
- **The Standard sees load balancers and services.** `ALARMED_METRICS`
  gains `AWS::ElasticLoadBalancingV2::LoadBalancer` (the two 5xx and
  `TargetResponseTime`), `AWS::ElasticLoadBalancingV2::TargetGroup`
  (`UnHealthyHostCount`) and `AWS::ECS::Service` (`CPUUtilization`,
  `MemoryUtilization`, `RunningTaskCount`). An alarm watched a resource when
  it held `{"Ref": id}`; a load balancer's and a target group's dimensions
  are `Fn::GetAtt` of their full names and a service's of its `Name`, so
  `refersTo` accepts `{"Fn::GetAtt": [id, ...]}` too. A `Ref` matches as
  before, so no existing check got weaker. Step 6's load balancer and
  target group are held to the same three and one; it adds the Auto
  Scaling group's metrics (a `Ref` dimension).
- **Tracing through an ADOT collector.** In production the task runs
  `public.ecr.aws/aws-observability/aws-otel-collector:v0.50.0` (2026-08-31;
  its image index has arm64) with its image's `/etc/ecs/ecs-xray.yaml`:
  OTLP in, X-Ray out. Not `ecs-default-config.yaml`, which also exports
  metrics through EMF into a log group the collector makes itself, outside
  the stack's retention and with permissions the task would need. The
  collector is not essential (it loses traces, not requests), runs on a
  read-only root file system (its image is `scratch` with a non-root user)
  and logs to its own log group with the tier's retention. The app
  container starts after it and gets `OTEL_EXPORTER_OTLP_ENDPOINT`
  (`http://localhost:4318`) and `OTEL_SERVICE_NAME` (the stack's name); the
  task role gets `xray:PutTraceSegments` and `xray:PutTelemetryRecords`,
  which step 3's acknowledgement in `infra/standard.ts` already covers.
  X-Ray takes W3C trace IDs from ADOT 0.34.0 on, so the app needs no X-Ray
  ID generator. Development runs none of it.
- **The app's spans.** OpenTelemetry JS v2.12.0 and experimental v0.223.0
  (2026-10-06): `@opentelemetry/api` 1.9.1, `sdk-trace-node` and
  `resources` 2.12.0, `exporter-trace-otlp-http` and `instrumentation-http`
  0.223.0, exact, 22 packages in the lock with their dependencies.
  `@opentelemetry/sdk-node` was left out: it brings every exporter (gRPC,
  Prometheus, Zipkin) for the one this needs. `src/tracing.ts` starts the
  provider only when the endpoint is set; `src/start_tracing.ts` calls it
  and is `src/main.ts`'s first import, so the HTTP instrumentation's hook is
  in place before anything loads `node:http` or `node:https`, which esbuild
  leaves outside its CommonJS bundle. Health checks are not traced, and
  `SIGTERM` sends the spans left once the server has closed. Checked by
  hand on the bundle: run with the endpoint at a local listener, three
  requests and a stop; the listener got one `POST /v1/traces` with the
  requests' spans and none for `/health`.
- **The Standard checks tracing in tasks.** Where the tier traces, every
  `AWS::ECS::TaskDefinition` runs one container on the `aws-otel-collector`
  image and one sending to it through `OTEL_EXPORTER_OTLP_ENDPOINT`;
  elsewhere neither. And, for every Framework, no statement allows X-Ray's
  writes where the tier doesn't trace.
- **The access-log bucket is not data.** It is kept (`RETAIN`) in both
  tiers: CloudFormation can't delete a bucket that holds objects, and
  `autoDeleteObjects` takes a custom-resource Lambda without a log group of
  its own. Its objects expire after the tier's log retention, a year or a
  month, instead of a fixed 30 days. The Standard's per-tier removal checks
  name tables and vaults, so a log bucket passes in either tier; the stack
  test asserts it, and step 9, the first data bucket, adds the bucket rule.
- **`EntryPoint` replaces `LoadBalancerDns`,** with the same value, the
  load balancer's DNS name; nothing read the old output.
- **`$tiered`.** The Standard's templates name the Frameworks with
  operations once per file, `{{- $tiered := or (eq .Framework
  "aws-lambda-ts") (eq .Framework "aws-fargate-ts") -}}`; steps 4 and 6 add
  theirs there. `docs/runbook.md` and `docs/well-architected.md` give
  `aws-fargate-ts` a branch of its own, so step 4's changes to the Lambda
  branch meet this one only at the branch line.
- **`vitest.config.mts`** gives aws-fargate-ts a `hookTimeout` of 120 s.

## Design

- **The block in the API.** `OperationsSpec` in `api/v1alpha1/container_types.go`
  with `Backups{RetentionDays int32 (0..3650), CopyToRegion string (an AWS
  region pattern or empty)}`, `Recovery{RPO, RTO string (`^[0-9]+(m|h|d)$`)}`
  and `Alerts{Email string}`; no tier. Defaults are applied by
  `internal/model`, not by CRD defaulting, so a client that sets one field
  reads back that field and a stored object never changes shape on upgrade.
  The block is mutable; `OperationsApplied` on the Container says whether
  the repository's `infra/operations.ts` matches it. Step 2 chose how: the
  scaffold records the resolved block in `status.operations`, and the
  condition compares the resolved `spec.operations` to it, so writing a
  default out is no change. A Container scaffolded before the record has
  no condition until its block says something, and then
  `ChangedAfterScaffold`, since its repository has none of it. A Standard
  update renders with the recorded block, as it does with the recorded
  checks. Step 12 updates the record when its pull request merges.
- **The planner and existing Containers.** A plan writes `operations` only
  on a Container it makes; `model/containers.yaml` doesn't show an existing
  one's block, and a plan that sets it is refused, so a block a person
  keeps is never undone by a round. A `tier` anywhere in a plan's Container
  refuses the plan with a message saying the tier is the target's.
- **The tier at synth.** `infra/main.ts` (the Standard's) reads
  `app.node.tryGetContext('tier')`, accepts `production` or `development`,
  defaults to `development`, and passes it to every stack; `make synth`
  runs `cdk synth -c tier=<t> -o cdk.out/<t>` for both, `cfn-lint` lints
  both, and the Standard's test synthesizes the app twice, once per tier.
  The release attaches both outputs, so #1071's deployer ships the one its
  target names. A tier is never read from a file in the repository.
- **The rendered file.** `infra/operations.ts` exports one frozen object the
  stacks and the Standard's test import: the resolved defaults and the
  overrides of the block, with a comment that the factory renders it from
  `spec.operations` and overwrites it on a change. The Framework owns it;
  it is not a protected path. `docs/runbook.md` is the Standard's and
  protected.
- **The Standard's test** keeps its shape (synth every stack, assert on the
  templates) and adds one `describe` per decision-5 item and tier, reading
  the block from `infra/operations.ts`. A Framework branch (`{{ if eq
  .Framework ... }}`) adds the resources only that Framework has, as
  `well-architected.md` already does.
- **The operations pull request** (step 12) follows the Standard bump's
  path in `internal/controller`: render the one file, branch, commit, open
  the pull request, merge it when its checks pass, and set the condition;
  the same fake git provider tests it.
- **The planner** sets `operations` from the idea: `email`, `rpo`, `rto`
  and `copyToRegion` when the idea states them; otherwise the block is left
  out and the defaults apply. It never picks a tier: that is the target's.
  A rule in `internal/model/planner.md` and a `plan/model.yaml` check.
- **The digest** (`docs/well-architected.md`) gets, per pillar, what the
  tier does and what a user changes in `infra/operations.ts`; its
  Reliability section names RPO and RTO, the backup plan, the copy and the
  restore procedure; its Cost section says what `production` costs over
  `development` in words (NAT gateways, backups, tracing, retention) so an
  agent picking a tier for a Task knows the trade.
- **Sources** for the new digest sections, read and dated in `SOURCES.md`:
  the Well-Architected Framework's reliability pillar (REL09 backup, REL13
  disaster recovery, REL06 and OPS08 monitoring and alarms), AWS Backup's
  developer guide for DynamoDB, DynamoDB's point-in-time recovery page,
  `cdk-monitoring-constructs`' README at the pinned version, and the
  Serverless Applications Lens.

## Not in this plan

Deploying, CDK bootstrap, environments, approval and status read-back
(#1068, #1071); an account's baseline services (decision 9, documented by
#1071); AWS data stores as DataStores with DynamoDB and RDS Connectors
(#1117, a proposed follow-up); a Step Functions Framework (decision 10); a
WAF on an API Gateway HTTP API, which WAF doesn't support (CloudFront in front
of it is a later Framework choice); Terraform, SAM and Pulumi.
