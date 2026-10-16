# dynamodb (Connector)

Connects a Container on an AWS Framework to a DynamoDB table. A Container gets
it through a Relationship to a DataStore backed by this Connector, so the table
is the model's, not the Framework's.

- **Supports:** `function`
- **Writes:** `infra/connector_dynamodb.ts` and its test. `connectDynamoDB`
  makes one on-demand table keyed by `pk` and `sk` (strings, so one table holds
  every kind of item), encrypted, hands the function its name in
  `DYNAMODB_TABLE_NAME`, and grants it the item calls (`GetItem`, `PutItem`,
  `UpdateItem`, `DeleteItem`, `Query`, `BatchGetItem`, `BatchWriteItem`,
  `ConditionCheckItem`): no `Scan`, no change to the table.
- **Tier and backups:** the table follows the Standard's `TIER_RULES`, as the
  Framework's own tables do: in production it is retained, deletion-protected
  and has point-in-time recovery, and the stack's AWS Backup plan selects it by
  the stack's tag, with the retention and copy region of the Container's
  `spec.operations`; in development it goes with the stack. Restoring it is the
  table branch of `docs/runbook.md`.
- **Frameworks:** a Framework wires it in its `infra/connectors.ts`, rendered with
  `{{ if has .Connectors "dynamodb" }}`, and alarms on the table like its own.
  `aws-lambda-ts` does; the other AWS Frameworks don't yet, so the file would
  be written and not called.

One table per Container: two DataStores on this Connector are one table. Like
every Connector it only scaffolds; the table is made when the Container's stack
is deployed, not by the factory.

See the python-fastapi template's `.c4factory/README.md` for how templates are rendered.
