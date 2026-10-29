# rds (Connector)

Connects a Container on an AWS Framework to an Aurora Serverless v2
PostgreSQL cluster. A Container gets it through a Relationship to a DataStore
backed by this Connector, so the cluster is the model's, not the Framework's
([docs/plans/aws-rds.md](../../../docs/plans/aws-rds.md)).

- **Supports:** `function`, `service`, `worker`, `job`
- **Writes:** `infra/connector_rds.ts` and its test. `connectRDS` makes one
  cluster per Container (PostgreSQL 16, Serverless v2, a database named after
  the Container in snake case), encrypted with its own customer managed key,
  its credentials a generated secret in Secrets Manager, IAM database
  authentication on and the Data API enabled. It hands its user
  `RDS_CLUSTER_ARN`, `RDS_SECRET_ARN` and `RDS_DATABASE` and grants it
  `grantDataApiAccess` (the `rds-data` calls on the cluster, and
  `GetSecretValue` on its secret): nothing else, and no change to the cluster
  beyond the ingress rule a user with a `network` gets (below).
- **Tier and backups:** the cluster follows the Standard's `TIER_RULES`. In
  production it scales from 0.5 to 8 ACU with a reader that scales with the
  writer, is deletion-protected, takes a final snapshot on removal, keeps 35
  days of points in time, rotates its secret every 30 days (through a Secrets
  Manager interface endpoint, the rotation function's only way out), and its
  key and secret outlive the stack; the stack's AWS Backup plan selects it by
  ARN with the Framework's tables. In development it scales from 0 to 1 ACU
  and pauses, keeps 1 day, rotates nothing and goes with the stack.
- **Network:** the cluster takes the user's VPC when the `DataStoreUser` has
  a `network` (its VPC and security groups), and then also hands it
  `RDS_HOST` and `RDS_PORT` and an ingress rule, for a driver with a pool.
  Without one (a Lambda outside a VPC) it makes a VPC of its own: two zones
  of isolated subnets, no NAT gateway, no public subnet, a flow log of
  rejected traffic; the function reaches it only through the Data API.
- **Who uses the cluster:** `connectRDS(scope, user, rules)` takes a
  `DataStoreUser`, anything `IGrantable` with an `addEnvironment(key, value)`
  and an optional `network`: a Lambda `Function` is one.
- **How to query:** through the Data API, with `@aws-sdk/client-rds-data`'s
  `ExecuteStatementCommand({ resourceArn, secretArn, database, sql })`, the
  three from the environment. The IAM database user is a migration the
  repository runs; this Connector scaffolds no code (decision 9).
- **Frameworks:** a Framework wires it in its `infra/connectors.ts`, rendered
  with `{{ if has .Connectors "rds" }}`, and alarms on the cluster's CPU and
  connections like its own tables. `aws-lambda-ts`, `aws-lambda-py` and `aws-lambda-go`
  do (since v1.5.0), `aws-queue-ts`, `aws-event-ts` and `aws-schedule-ts`
  (v1.3.0, #1925), and `aws-fargate-ts` (v2.2.0) and `aws-ec2-ts` (v1.4.0)
  with their own VPC and security group, so the cluster joins that VPC
  (#1926). The
  catalog's `wiredBy` names them, so a Container on another Framework is
  refused at `ChoicesValid` (`NotWired`); a Framework that renders no
  `infra/connectors.ts` is refused by `connector_rds.ts.tmpl`'s `{{ fail }}`
  with a `TemplateError` naming the Framework and the Connector.

One cluster per Container: two DataStores on this Connector are one cluster.
Like every Connector it only scaffolds; the cluster is made when the
Container's stack is deployed, not by the factory.

See the python-fastapi template's `.c4factory/README.md` for how templates are rendered.
