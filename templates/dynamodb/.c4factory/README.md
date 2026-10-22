# dynamodb (Connector)

Connects a Container on an AWS Framework to a DynamoDB table. A Container gets
it through a Relationship to a DataStore backed by this Connector, so the table
is the model's, not the Framework's.

- **Supports:** `function`, `service`, `worker`, `job`
- **Writes:** `infra/connector_dynamodb.ts` and its test. `connectDynamoDB`
  makes one on-demand table keyed by `pk` and `sk` (strings, so one table holds
  every kind of item), encrypted, hands its user its name in
  `DYNAMODB_TABLE_NAME`, and grants it the item calls (`GetItem`, `PutItem`,
  `UpdateItem`, `DeleteItem`, `Query`, `BatchGetItem`, `BatchWriteItem`,
  `ConditionCheckItem`): no `Scan`, no change to the table.
- **Tier and backups:** the table follows the Standard's `TIER_RULES`, as the
  Framework's own tables do: in production it is retained, deletion-protected
  and has point-in-time recovery, and the stack's AWS Backup plan selects it by
  the stack's tag, with the retention and copy region of the Container's
  `spec.operations`; in development it goes with the stack. Restoring it is the
  table branch of `docs/runbook.md`.
- **Who uses the table:** `connectDynamoDB(scope, user, rules)` takes a
  `DataStoreUser`, anything `IGrantable` with an `addEnvironment(key, value)`:
  a Lambda `Function` is one; a Fargate task passes its task role and its
  container definition, an EC2 instance its instance role and the environment
  its unit reads.
- **Frameworks:** a Framework wires it in its `infra/connectors.ts`, rendered with
  `{{ if has .Connectors "dynamodb" }}`, and alarms on the table like its own.
  `aws-lambda-ts`, `aws-lambda-py` and `aws-lambda-go` do, `aws-fargate-ts`
  and `aws-ec2-ts` (`service`, since v1.2.0, #1742), and `aws-queue-ts`,
  `aws-event-ts` and `aws-schedule-ts` (`worker` and `job`, since v1.3.0,
  #1741). A Framework that renders no `infra/connectors.ts` doesn't wire it,
  and `connector_dynamodb.ts.tmpl` refuses it with
  `{{ fail }}`: the Container fails with a `TemplateError` naming the
  Framework and the Connector, rather than getting a file nothing calls.

One table per Container: two DataStores on this Connector are one table. Like
every Connector it only scaffolds; the table is made when the Container's stack
is deployed, not by the factory.

See the python-fastapi template's `.c4factory/README.md` for how templates are rendered.
