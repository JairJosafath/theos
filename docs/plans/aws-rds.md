# AWS relational data: the `rds` Connector

Parent issue: #1394, a follow-up of #1117 and of decision 3 of
[aws-production.md](aws-production.md). Written 2026-10-24.

## Goal

A Container on an AWS Framework with a Relationship to a DataStore on an
`rds` Connector gets a relational database in its CDK stack: the database,
its credentials, its backups and its grants, the way the `dynamodb`
Connector gives it a table. `make check` of the factory and of the
scaffolded repository verify it; nothing is deployed.

## Where it stands

- `dynamodb` v1.3.0 is the one AWS Connector: `connectDynamoDB(scope, user,
  rules)` takes a `DataStoreUser` (`IGrantable` plus `addEnvironment`) and
  returns a `Table`. Every AWS Framework with a Lambda or a task wires it
  through `infra/connectors.ts` (`DATA_STORES`, a list of `[name, Connect]`),
  alarms on each table with `monitorDynamoTable` and passes it to its
  `infra/backups.ts` (`tables`), which selects it by ARN. A Framework that
  renders no `infra/connectors.ts` is refused by the Connector's `fail`
  guard ([aws-data-stores.md](aws-data-stores.md), decision 3).
- `aws-standard` v1.19.1's test (`infra/standard.test.ts`) already lists
  `AWS::RDS::DBCluster` and `AWS::RDS::DBInstance` among the stateful types
  that must say what happens to their data, but backs up, encrypts and
  protects only tables: `BACKED_UP_TYPES` is `['AWS::DynamoDB::Table']` and
  `selectionFaults` reads a selection against the stack's tables.
- The Lambda Frameworks have no VPC. `aws-fargate-ts` makes one with a NAT
  gateway per zone in production, `aws-ec2-ts` one with none and interface
  endpoints instead. A `Function` can't be put in a VPC after it is made.
- `postgres` v1.2.0 is the relational Connector for the cluster: a
  StatefulSet and a ConfigMap the Deployment layer reads, which a CDK app
  never does. [aws-data-stores.md](aws-data-stores.md) left it to this plan.
- The only check between a Connector and a Framework is the Container type.
  The `fail` guard catches a Framework with no `infra/connectors.ts`, but not
  one that renders it and wires only `dynamodb`: that Container would get
  `infra/connector_rds.ts` and nothing would call it. The catalog field that
  plan deferred "until a second Connector family needs it" is needed now.

## Decisions

1. **Aurora Serverless v2, PostgreSQL, one cluster per Container.** A
   `DatabaseCluster` on `AuroraPostgresEngineVersion` 16 (the newest minor
   CDK 2.273.0 names), one serverless writer, in production a serverless
   reader in a second zone (`scaleWithWriter`) for a failover under a
   minute, in development no reader. Capacity is a per-tier constant in the
   Connector's file, the repository's to raise: development `0` to `1` ACU,
   so it pauses after five minutes idle and costs nothing at rest, as an
   on-demand table does; production `0.5` to `8` ACU, never paused. No
   instance class to pick, no MySQL: one engine, the one the `postgres`
   Connector runs in the cluster, so a Container's SQL is the same in
   either place. Two DataStores on `rds` for one Container are one cluster,
   as with `dynamodb`; the database is named after the Container in snake
   case, like `postgres`'s.
2. **The Data API is how every user reaches it; TCP too for a user in its
   VPC.** The cluster has `enableDataApi`; `connectRDS` grants the user
   `grantDataApiAccess` (the five `rds-data` calls on the cluster's ARN and
   `secretsmanager:GetSecretValue` on its secret) and sets
   `RDS_CLUSTER_ARN`, `RDS_SECRET_ARN` and `RDS_DATABASE`. So a Lambda needs
   no VPC, no NAT and no endpoint to query it, and the function's own
   network stays as it is. `DataStoreUser` gains an optional
   `network: { vpc: IVpc; connections: Connections }`; a Framework with a
   VPC (`aws-fargate-ts`, `aws-ec2-ts`) passes its own, and such a user also
   gets `RDS_HOST` and `RDS_PORT` and an ingress rule from its security
   group to the cluster's, for a driver with a pool. The Lambda Frameworks
   pass none. The cluster also turns IAM database authentication on, which
   costs nothing and is what CDK Nag asks (`RDS6`); making the IAM database
   user is a migration the repository runs, not the scaffold.
3. **The cluster's VPC is the Framework's, else the Connector's own.** With
   a `network` the cluster goes in its VPC's private or isolated subnets.
   Without one `connectRDS` makes a `Vpc` of two zones with isolated
   subnets only (no NAT gateway, no public subnet: free), a flow log to a
   log group as the Frameworks' VPCs have, and nothing else. The Data API
   and the rotation function are the only things that need to reach in.
4. **Credentials live in Secrets Manager and rotate in production.**
   `Credentials.fromGeneratedSecret('app')`: a `DatabaseSecret` nobody
   reads at scaffold or deploy, which the Data API resolves by ARN and a
   TCP user fetches with `GetSecretValue` on each new connection, never at
   build time and never into an environment variable (the Fargate stack's
   Nag note already promises none is a secret). In production
   `addRotationSingleUser` every 30 days, through a Secrets Manager
   interface endpoint the Connector adds to the cluster's subnets (the
   rotation function runs in the VPC and has no other way out); in
   development no rotation, and the Standard acknowledges `AwsSolutions-SMG4`
   on that tier with the reason it gives tables without recovery. The
   secret and the key follow `keepData`. The `secrets` skill applies to the
   step that writes this.
5. **Backups are Aurora's continuous backups plus the stack's AWS Backup
   plan, by ARN.** The cluster's `backup.retention` is 35 days where the
   tier recovers data and 1 day (Aurora's least) elsewhere: a point in time
   to the second, the table's PITR equivalent. Where the tier keeps data the
   cluster has `deletionProtection` and `RemovalPolicy.SNAPSHOT` (a final
   snapshot, which the Standard's stateful-resource rule already accepts);
   elsewhere `DESTROY`. The cluster is encrypted with its own customer
   managed `Key`, retained with the data: AWS Backup can't copy a cluster
   encrypted with the AWS-managed key to another region, and the plan's
   `copyToRegion` must work for it as for a table. Each Framework's
   `infra/backups.ts` takes `clusters` beside `tables` and selects each with
   `BackupResource.fromArn(cluster.clusterArn)`; the Standard's
   `selectionFaults` reads clusters (`Fn::GetAtt [id, DBClusterArn]`) as it
   reads tables, `BACKED_UP_TYPES` gains `AWS::RDS::DBCluster`, and the
   rules for encryption, deletion protection, removal and recovery get a
   cluster branch. Restore is a runbook section per Framework, as the
   table's.
6. **A `Connect` returns a `DataStore`, a `Table` or a `DatabaseCluster`.**
   `infra/connectors.ts` in every Framework: `export type DataStore = Table |
   DatabaseCluster` and `Connect = (scope, user, rules) => DataStore`. The
   stack branches with `instanceof Table`: a table is alarmed with
   `monitorDynamoTable` and goes to `tables`, a cluster with
   `monitorAuroraCluster` (CPU and connections, `cdk-monitoring-constructs`'
   defaults, the connections alarm among the ones a deployment rolls back
   on) and goes to `clusters`. `connectDynamoDB` is unchanged: a `Table` is
   a `DataStore`.
7. **A Connector entry names the Frameworks that wire it.** `ConnectorSpec`
   gains `wiredBy`, a list of Framework names; when it is set, `ChoicesValid`
   refuses a DataStore on that Connector for a Container whose Framework is
   not in it, naming both. The cluster Connectors (`postgres`) leave it
   unset: their files need no wiring. `dynamodb`'s entry lists its eight
   Frameworks; `rds`'s grows with the steps, so between steps a Container
   on a Framework that doesn't wire `rds` yet is refused at `ChoicesValid`
   instead of getting a file nothing calls. The `fail` guard stays as the
   file-level check. The entry lives in the chart's catalog, so the list is
   a chart bump, not a template release.
8. **`postgres` refuses an AWS Framework.** Its first rendered file starts
   with the inverse of the `dynamodb` guard: `{{ if has .FrameworkFiles
   "infra/connectors.ts" }}{{ fail "... use the rds Connector" }}{{ end }}`.
   The planner guide says the same in words: `rds` on the `aws-*`
   Frameworks, `postgres` in the cluster, and a Container that keeps
   relational data gets one DataStore on the one its Framework takes.
9. **No starter code.** As `dynamodb`, the Connector scaffolds the database,
   its grants and its environment; the Framework's own items table stays
   (decision 1 of [aws-data-stores.md](aws-data-stores.md)). The Framework's
   README says what `RDS_*` names and that a query goes through the Data
   API (the SDK's `rds-data` client) or, in a VPC, a driver with the secret.
   The implementer guide's data rule says the same.

## Steps

1. **A Connector entry names the Frameworks that wire it** (#S1).
   `ConnectorSpec.wiredBy`, `make manifests`, the `ChoicesValid` rule in
   `internal/model` and its envtest, `dynamodb`'s entry in the chart. Done
   when a table test in `internal/model` and an envtest show a DataStore on
   a Connector naming `wiredBy` is refused for a Framework outside it, with
   the reason naming the Connector and the Framework, and accepted for one
   inside it or when `wiredBy` is unset; `make validate-yaml` passes.
2. **The Standard knows a relational cluster** (#S2). `aws-standard` minor:
   `BACKED_UP_TYPES`, `selectionFaults` over clusters, the encryption,
   protection, removal and recovery rules' cluster branch, the `SMG4`
   acknowledgement in development, and the README's list of what it checks.
   Done when `standard.test.ts` has a case per rule on a stack with a
   cluster in either tier, and a selection that leaves the cluster out, or
   names it by an ARN written out, has a fault.
3. **The `rds` Connector v1.0.0, wired by `aws-lambda-ts`** (#S3). Depends on
   steps 1 and 2. `infra/connector_rds.ts.tmpl` (the guard, `connectRDS`,
   `DataStoreUser` with `network`, the constants) and its test; the catalog
   entry with `wiredBy: [aws-lambda-ts]`, `supportedTypes: [function]`, and
   `versions.txt`; `aws-lambda-ts` minor with decision 6's `DataStore`,
   the stack's cluster branch and `backups.ts`'s `clusters`. Done when
   `aws_connectors_test.go` renders `aws-lambda-ts` with `rds`, with both
   Connectors, and fails a Framework without `infra/connectors.ts`;
   `templateCombos` has the rows; and a `hack/render` copy with `rds` passes
   its own `make check` in both tiers under CDK Nag and cfn-lint (output
   under `docs/screenshots/`).
4. **`aws-lambda-py` and `aws-lambda-go` wire it** (#S4). Depends on step 3.
   Done as step 3, for these two; `wiredBy` grows.
5. **`aws-queue-ts`, `aws-event-ts` and `aws-schedule-ts` wire it; the
   Connector supports `worker` and `job`** (#S5). Depends on step 3. Done as
   step 3, for these three.
6. **`aws-fargate-ts` and `aws-ec2-ts` wire it with their network; the
   Connector supports `service`** (#S6). Depends on step 3. Each passes
   `network` and its rendered copy's test shows the ingress rule, `RDS_HOST`
   and `RDS_PORT`, and no new VPC. Done as step 3, for these two.
7. **`postgres` refuses AWS, the planner plans the store, the docs say so**
   (#S7). Depends on steps 4 to 6. `postgres` minor with decision 8's guard
   and a scaffold test; `internal/model/planner.md` and its tests; the
   runbook's restore sections per Framework, the style guide's relational
   rule, the implementer guide, `docs/design.md`'s AWS line. Done when a
   Brief plan with a relational DataStore on an AWS Container validates and
   the guide's tests cover it, and `python-fastapi` with `rds` and
   `aws-lambda-ts` with `postgres` both fail with the Connector's message.

## Done when

A Container on any AWS Framework with a Lambda or a task, with a
Relationship to a DataStore on `rds`, scaffolds the cluster, its secret, its
backups and its grants, and its scaffolded copy's `make check` passes (steps
3 to 6); the planner plans it (step 7). #1394 closes itself when its last
step does.

## Not in this plan

- Starter code that queries the database, and the migration that makes the
  IAM database user (decision 9).
- Multi-user rotation, Aurora global databases, RDS Proxy, MySQL.
- Dropping a Framework's starter table (aws-data-stores.md, decision 1).
