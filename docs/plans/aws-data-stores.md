# AWS data stores: the dynamodb Connector on every AWS Framework

Parent issue: #1393, a follow-up of #1117. Written 2026-10-22.

## Goal

A Container on any AWS Framework with a Lambda or a task reaches a DynamoDB
table through a Relationship to a DataStore on the `dynamodb` Connector, the
way `docs/design.md` models data ("A database or storage, backed by a
Connector"), and the planner plans that DataStore for an AWS Container that
keeps data instead of planning none.

## Where it stands

- `dynamodb` v1.0.0 writes `infra/connector_dynamodb.ts`:
  `connectDynamoDB(scope, fn: LambdaFunction, rules)` makes an on-demand
  table keyed by `pk`/`sk`, follows `TIER_RULES`, grants the item calls and
  sets `DYNAMODB_TABLE_NAME`. Its `supportedTypes` is `[function]`.
- Only `aws-lambda-ts` (v1.2.0 on) wires it, through `infra/connectors.ts`
  rendered from `.Connectors` (a `DATA_STORES` list its stack connects and
  alarms on).
- `aws-lambda-py` and `aws-lambda-go` are `function` Frameworks that don't
  wire it: a Container on them with the DataStore gets the file, never called.
- `aws-queue-ts`, `aws-event-ts` (`worker`), `aws-schedule-ts` (`job`),
  `aws-fargate-ts` and `aws-ec2-ts` (`service`) can't take it at all.
  `aws-static-site-ts` has no Lambda or task and is left out.
- `internal/model/planner.md` said to plan no DataStore for any AWS Container
  (step 5 changes that).
- The only check between a Connector and a Container is the type
  (`CatalogEntry.Supports`). Widening `dynamodb` to `service`, `worker` and
  `job` would let a `python-fastapi` service or a `go-cli` worker take it,
  and get a CDK file that imports an `infra/standard.ts` it doesn't have.

## Decisions

1. **The Framework's own items table stays beside the Connector's.** Each
   AWS Framework makes an `Items` table keyed by `id`, which its starter
   code (`item_store` in TypeScript, Python and Go) reads and writes and its
   tests cover. Moving that table into the Connector would make the starter
   code of seven Frameworks in three languages conditional on a Connector,
   change its key schema to `pk`/`sk`, and leave a Container without a
   DataStore with no table to run against. The Connector's table is the
   model's: what the Container's Relationships say it keeps. The Framework's
   table is the starter's example, and its README says so: new data goes
   in the DataStore's table, named in `DYNAMODB_TABLE_NAME`. The cost is
   one more on-demand table, which costs nothing at rest, and alarms and
   backups that already cover it. This is what `aws-lambda-ts` v1.2.0
   already did (decision 11 of [aws-production.md](aws-production.md)).
   No scaffold breaks: existing Containers are never scaffolded again, and
   new ones without a DataStore get what they get today. Dropping the
   starter table is a later major of each Framework, filed when the planner
   plans a DataStore for every AWS Container that keeps data and the starter
   table is only noise.
2. **The Connector takes a data-store user, not a Lambda.**
   `connectDynamoDB(scope, user, rules)` takes
   `DataStoreUser = IGrantable & { addEnvironment(key: string, value: string): unknown }`,
   exported from the Connector's file. A Lambda `Function` is one already
   (it is `IGrantable` and has `addEnvironment`), so `aws-lambda-ts`'s call
   is unchanged and the Connector is a minor, v1.1.0. Fargate passes its
   task role as the grantee and its container definition's
   `addEnvironment`; EC2 passes its instance role and adds to the
   environment its systemd unit is rendered from. `infra/connectors.ts`'s
   `Connect` type takes a `DataStoreUser` in every Framework that wires it.
3. **A Connector a Framework doesn't wire fails the Container.** The
   Framework is the one that knows whether it wires a Connector
   (design.md, "Shared files between layers"), and a layer tests a file,
   not a name. So `connector_dynamodb.ts.tmpl` starts with
   `{{ if not (has .FrameworkFiles "infra/connectors.ts") }}{{ fail "..." }}{{ end }}`,
   through a new `fail` template function: rendering stops with a
   `TemplateError` whose message says the Framework doesn't wire the
   `dynamodb` Connector, and the Container is `Failed` with it, the way two
   layers writing one path fail it. A Framework renders
   `infra/connectors.ts` only when it wires every AWS Connector there is
   (today one). This is what makes widening `supportedTypes` safe; a
   catalog field naming the Connectors a Framework wires is the bigger
   change, left until a second Connector family needs it. The planner guide
   says the same in words, so a Brief doesn't plan what fails.
4. **`supportedTypes` widens with the Frameworks, not before.** Each step
   that wires a type adds it: `worker` and `job` in step 3, `service` in
   step 4. Between steps a Container of a type no AWS Framework wires yet
   is refused at `ChoicesValid`, as today.
5. **One step per kind of compute.** Two Lambda Frameworks in other
   languages, three event-driven Lambda Frameworks, and two Frameworks with
   a task or an instance are three pieces of work; each wiring is the same
   `infra/connectors.ts` plus its stack's call and alarm.

## Steps

1. **The Connector takes a data-store user and refuses an unwired
   Framework** (#1739). `dynamodb` v1.1.0: `DataStoreUser`, decision 3's
   guard and the `fail` template function in `internal/scaffold`. Done when
   `internal/scaffold` tests show `dynamodb` with `aws-lambda-ts` renders as
   before, and with a Framework without `infra/connectors.ts` fails with a
   `TemplateError` naming the Connector; the Connector's own test covers a
   non-Lambda grantee; the pins in the chart and `versions.txt` are
   v1.1.0.
2. **`aws-lambda-py` and `aws-lambda-go` wire it** (#1740). Each renders
   `infra/connectors.ts` and its stack connects and alarms on each data
   store, as `aws-lambda-ts` does. Done when `aws_connectors_test.go` has a
   case per Framework, `templateCombos` has a row per Framework with
   `dynamodb`, and a `hack/render` copy of each passes its own `make check`
   (output under `docs/screenshots/`).
3. **`aws-queue-ts`, `aws-event-ts` and `aws-schedule-ts` wire it; the
   Connector supports `worker` and `job`** (#1741). Depends on step 1. Done
   as step 2, for these three.
4. **`aws-fargate-ts` and `aws-ec2-ts` wire it; the Connector supports
   `service`** (#1742). Depends on step 1. Fargate passes its task role and
   container, EC2 its instance role and unit environment. Done as step 2,
   for these two, plus a test that `python-fastapi` with `dynamodb` fails
   with step 1's message.
5. **The planner plans the DataStore** (#1743). Depends on steps 2 to 4.
   `internal/model/planner.md` plans a DataStore on `dynamodb` and a
   Relationship to it for an AWS Container that keeps data, one per
   Container, and still none for `aws-static-site-ts`; the Connector's
   description and README and `docs/design.md` drop "aws-lambda-ts wires
   it". Done when the planner guide's tests (`internal/model`) cover an
   AWS Container with the DataStore and a Brief plan with one validates.

## Done when

Every AWS Framework with a Lambda or a task connects a `dynamodb`
DataStore's table, and its scaffolded copy's `make check` passes (steps 2
to 4), and the planner plans the DataStore (step 5). #1393 closes itself
when its last step does.

## Not in this plan

- `postgres` on an AWS Framework: it supports `service`, `worker` and `job`
  and writes Kubernetes files a CDK app never reads. Decision 3's guard is
  the shape of its fix (it renders only when the Framework has no
  `infra/connectors.ts`); it is #1394's (`rds`) to settle.
- Dropping a Framework's starter table (decision 1).
