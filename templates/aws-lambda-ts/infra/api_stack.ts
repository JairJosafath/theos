import { CfnOutput, Duration, RemovalPolicy, Stack } from 'aws-cdk-lib';
import type { StackProps } from 'aws-cdk-lib';
import { HttpApi, HttpMethod, LogGroupLogDestination } from 'aws-cdk-lib/aws-apigatewayv2';
import { HttpIamAuthorizer } from 'aws-cdk-lib/aws-apigatewayv2-authorizers';
import { HttpLambdaIntegration } from 'aws-cdk-lib/aws-apigatewayv2-integrations';
import { AttributeType, BillingMode, Table, TableEncryption } from 'aws-cdk-lib/aws-dynamodb';
import type { TableProps } from 'aws-cdk-lib/aws-dynamodb';
import { Role, ServicePrincipal } from 'aws-cdk-lib/aws-iam';
import { Architecture, LoggingFormat, Runtime, Tracing } from 'aws-cdk-lib/aws-lambda';
import { NodejsFunction } from 'aws-cdk-lib/aws-lambda-nodejs';
import { LogGroup } from 'aws-cdk-lib/aws-logs';
import type { Construct } from 'constructs';
import path from 'node:path';

import { Alarms } from './alarms';
import { Backups } from './backups';
import { DATA_STORES } from './connectors';
import type { Connect } from './connectors';
import { TIER_RULES } from './standard';
import type { Operations, Tier } from './standard';

// The repository root: the function's code and package-lock.json are there.
const ROOT = path.join(import.meta.dirname, '..');

// The function's time limit, which its duration alarm is a share of.
const TIMEOUT = Duration.seconds(10);

/** What the stack is built for. */
export interface ApiStackProps extends StackProps {
  /** Where it runs, which decides what it keeps, backs up and traces. */
  readonly tier: Tier;
  /** What the Container promises whatever the tier: `OPERATIONS`. */
  readonly operations: Operations;
  /** The tag, key and value, every resource of the stack carries: what its backups select. */
  readonly backupTag: readonly [string, string];
  /** The data stores it connects the function to: `DATA_STORES` unless a test names others. */
  readonly dataStores?: readonly (readonly [string, Connect])[];
}

/**
 * The Container's one stack: an HTTP API whose routes call a Lambda function
 * that keeps items in a DynamoDB table. Callers sign their requests with IAM
 * (SigV4); the API refuses everyone else. Every part has alarms, to a topic
 * the stack outputs with its URL; production keeps and backs up the data.
 */
export class ApiStack extends Stack {
  /** The API's URL, `https://<id>.execute-api.<region>.amazonaws.com/`. */
  readonly url: string;

  constructor(scope: Construct, id: string, props: ApiStackProps) {
    super(scope, id, props);
    const rules = TIER_RULES[props.tier];
    const { backups, alerts } = props.operations;

    // Every table holds data: kept, protected and recoverable where the tier
    // keeps data, thrown away with the stack where it doesn't.
    const tableProps: Omit<TableProps, 'partitionKey'> = {
      billingMode: BillingMode.PAY_PER_REQUEST,
      encryption: TableEncryption.AWS_MANAGED,
      pointInTimeRecoverySpecification: {
        pointInTimeRecoveryEnabled: rules.pointInTimeRecovery,
      },
      deletionProtection: rules.keepData,
      removalPolicy: rules.keepData ? RemovalPolicy.RETAIN : RemovalPolicy.DESTROY,
    };
    const table = new Table(this, 'Items', {
      ...tableProps,
      partitionKey: { name: 'id', type: AttributeType.STRING },
    });
    // Where Powertools' Idempotency records the requests it has answered, so
    // a request sent again with the same Idempotency-Key writes nothing twice.
    const idempotency = new Table(this, 'Idempotency', {
      ...tableProps,
      partitionKey: { name: 'id', type: AttributeType.STRING },
      timeToLiveAttribute: 'expiration',
    });

    const functionLogs = new LogGroup(this, 'HandlerLogs', {
      retention: rules.logRetention,
      removalPolicy: RemovalPolicy.DESTROY,
    });
    // The function's own role, allowed only to write its log group: the
    // default role would carry the AWS managed AWSLambdaBasicExecutionRole,
    // which may write to every log group in the account.
    const role = new Role(this, 'HandlerRole', {
      assumedBy: new ServicePrincipal('lambda.amazonaws.com'),
    });
    functionLogs.grantWrite(role);
    const handler = new NodejsFunction(this, 'Handler', {
      role,
      entry: path.join(ROOT, 'src', 'handler.ts'),
      handler: 'handler',
      projectRoot: ROOT,
      depsLockFilePath: path.join(ROOT, 'package-lock.json'),
      runtime: Runtime.NODEJS_24_X,
      architecture: Architecture.ARM_64,
      memorySize: 256,
      timeout: TIMEOUT,
      logGroup: functionLogs,
      loggingFormat: LoggingFormat.JSON,
      tracing: rules.tracing ? Tracing.ACTIVE : Tracing.DISABLED,
      environment: {
        TABLE_NAME: table.tableName,
        IDEMPOTENCY_TABLE_NAME: idempotency.tableName,
        // Powertools' Logger and Metrics name the service, and Metrics
        // writes its metrics under this namespace.
        POWERTOOLS_SERVICE_NAME: id,
        POWERTOOLS_METRICS_NAMESPACE: id,
      },
      bundling: {
        // The installed esbuild bundles; never Docker, which CI doesn't have.
        forceDockerBundling: false,
        // Bundle the SDK pinned in package-lock.json, not the runtime's copy.
        externalModules: [],
        minify: true,
        sourceMap: true,
      },
    });
    // Only the calls the handler makes.
    table.grant(handler, 'dynamodb:GetItem', 'dynamodb:PutItem');
    idempotency.grant(
      handler,
      'dynamodb:GetItem',
      'dynamodb:PutItem',
      'dynamodb:UpdateItem',
      'dynamodb:DeleteItem',
    );
    // The DataStores the Container's Relationships name, from their Connectors.
    const stores = (props.dataStores ?? DATA_STORES).map(
      ([name, connect]) => [name, connect(this, handler, rules)] as const,
    );

    const accessLogs = new LogGroup(this, 'ApiAccessLogs', {
      retention: rules.logRetention,
      removalPolicy: RemovalPolicy.DESTROY,
    });
    const api = new HttpApi(this, 'Api', {
      defaultAuthorizer: new HttpIamAuthorizer(),
      createDefaultStage: false,
    });
    api.addStage('DefaultStage', {
      stageName: '$default',
      autoDeploy: true,
      throttle: { rateLimit: 50, burstLimit: 100 },
      accessLogSettings: { destination: new LogGroupLogDestination(accessLogs) },
    });
    api.addRoutes({
      path: '/items/{id}',
      methods: [HttpMethod.GET, HttpMethod.PUT],
      integration: new HttpLambdaIntegration('Items', handler),
    });
    this.url = api.apiEndpoint;

    // Each alarm fires on the first bad five minutes: a server error, a
    // failed or throttled call, or calls near their time limit.
    // docs/runbook.md says what each means and what to do first.
    // tsconfig's exactOptionalPropertyTypes keeps a HttpApi or Table from
    // passing as the IHttpApi or ITable the monitoring takes (an optional
    // property they leave undefined), so it gets a reference by id or name.
    const alarms = new Alarms(this, 'Alarms', alerts.email);
    alarms.monitoring
      .monitorApiGatewayV2HttpApi({
        api: HttpApi.fromHttpApiAttributes(this, 'MonitoredApi', { httpApiId: api.apiId }),
        alarmFriendlyName: 'Api',
        add5xxCountAlarm: { Critical: { maxErrorCount: 0 } },
      })
      .monitorLambdaFunction({
        lambdaFunction: handler,
        alarmFriendlyName: 'Handler',
        addFaultCountAlarm: { Critical: { maxErrorCount: 0 } },
        addThrottlesCountAlarm: { Critical: { maxErrorCount: 0 } },
        addLatencyP99Alarm: {
          Warning: { maxLatency: Duration.millis(TIMEOUT.toMilliseconds() * 0.8) },
        },
      });
    for (const [name, monitored] of [
      ['Items', table],
      ['Idempotency', idempotency],
      ...stores,
    ] as const) {
      alarms.monitoring.monitorDynamoTable({
        table: Table.fromTableName(this, `Monitored${name}`, monitored.tableName),
        alarmFriendlyName: name,
        billingMode: BillingMode.PAY_PER_REQUEST,
        addSystemErrorCountAlarm: { Critical: { maxErrorCount: 0 } },
        addReadThrottledEventsCountAlarm: { Critical: { maxThrottledEventsThreshold: 0 } },
        addWriteThrottledEventsCountAlarm: { Critical: { maxThrottledEventsThreshold: 0 } },
      });
    }

    if (rules.backups && backups.retentionDays > 0) {
      new Backups(this, 'Backups', {
        retentionDays: backups.retentionDays,
        copyToRegion: backups.copyToRegion,
        tag: props.backupTag,
      });
    }

    new CfnOutput(this, 'EntryPoint', {
      value: this.url,
      description: 'The URL of the HTTP API.',
    });
  }
}
