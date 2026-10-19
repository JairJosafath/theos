import { CfnOutput, Duration, RemovalPolicy, Stack } from 'aws-cdk-lib';
import type { StackProps } from 'aws-cdk-lib';
import { AttributeType, BillingMode, Table, TableEncryption } from 'aws-cdk-lib/aws-dynamodb';
import type { TableProps } from 'aws-cdk-lib/aws-dynamodb';
import { Role, ServicePrincipal } from 'aws-cdk-lib/aws-iam';
import { Architecture, LoggingFormat, Runtime, Tracing } from 'aws-cdk-lib/aws-lambda';
import { SqsEventSource } from 'aws-cdk-lib/aws-lambda-event-sources';
import { NodejsFunction } from 'aws-cdk-lib/aws-lambda-nodejs';
import { LogGroup } from 'aws-cdk-lib/aws-logs';
import { Queue, QueueEncryption } from 'aws-cdk-lib/aws-sqs';
import type { QueueProps } from 'aws-cdk-lib/aws-sqs';
import type { Construct } from 'constructs';
import path from 'node:path';

import { Alarms } from './alarms';
import { Backups } from './backups';
import { TIER_RULES } from './standard';
import type { Operations, Tier } from './standard';

// The repository root: the function's code and package-lock.json are there.
const ROOT = path.join(import.meta.dirname, '..');

// The function's time limit for one batch, which its duration alarm is a
// share of.
const TIMEOUT = Duration.seconds(30);

// How long the event source gathers messages into a batch.
const BATCHING_WINDOW = Duration.seconds(5);

// How often a message is delivered before it goes to the dead-letter queue.
export const MAX_RECEIVE_COUNT = 5;

// The oldest a waiting message may get before its alarm fires: the worker
// is behind, or not consuming at all.
export const MAX_MESSAGE_AGE = Duration.minutes(15);

/** What the stack is built for. */
export interface QueueStackProps extends StackProps {
  /** Where it runs, which decides what it keeps, backs up and traces. */
  readonly tier: Tier;
  /** What the Container promises whatever the tier: `OPERATIONS`. */
  readonly operations: Operations;
}

/**
 * The Container's one stack: an SQS queue whose messages a Lambda function
 * consumes in batches, keeping the items they carry in a DynamoDB table. A
 * message that fails is delivered again alone, and after
 * `MAX_RECEIVE_COUNT` deliveries waits in a dead-letter queue. Producers send
 * to the queue with their own IAM permission (`sqs:SendMessage`), over TLS
 * only. Every part has alarms, to a topic the stack outputs with the queue's
 * URL; production keeps and backs up the data.
 */
export class QueueStack extends Stack {
  /** The queue's URL, `https://sqs.<region>.amazonaws.com/<account>/<name>`. */
  readonly queueUrl: string;

  constructor(scope: Construct, id: string, props: QueueStackProps) {
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
    // Where Powertools' Idempotency records the messages consumed, so a
    // message SQS delivers twice writes nothing twice.
    const idempotency = new Table(this, 'Idempotency', {
      ...tableProps,
      partitionKey: { name: 'id', type: AttributeType.STRING },
      timeToLiveAttribute: 'expiration',
    });

    // Messages not yet consumed are data too: production keeps the queues
    // with the tables. Both are encrypted and refuse requests without TLS.
    const queueProps: QueueProps = {
      encryption: QueueEncryption.SQS_MANAGED,
      enforceSSL: true,
      removalPolicy: rules.keepData ? RemovalPolicy.RETAIN : RemovalPolicy.DESTROY,
    };
    const deadLetters = new Queue(this, 'DeadLetters', {
      ...queueProps,
      // The longest SQS keeps a message: time for a person to look.
      retentionPeriod: Duration.days(14),
    });
    const queue = new Queue(this, 'Messages', {
      ...queueProps,
      retentionPeriod: Duration.days(4),
      // AWS's advice for a Lambda source: six times the function's timeout
      // and the batching window, so a message is never delivered again while
      // a batch still holds it, and a throttled batch has time to retry.
      visibilityTimeout: Duration.seconds(TIMEOUT.toSeconds() * 6 + BATCHING_WINDOW.toSeconds()),
      deadLetterQueue: { queue: deadLetters, maxReceiveCount: MAX_RECEIVE_COUNT },
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
    // Only the calls the handler makes; the event source grants the
    // function receiving and deleting the queue's messages.
    table.grant(handler, 'dynamodb:PutItem');
    idempotency.grant(
      handler,
      'dynamodb:GetItem',
      'dynamodb:PutItem',
      'dynamodb:UpdateItem',
      'dynamodb:DeleteItem',
    );
    handler.addEventSource(
      new SqsEventSource(queue, {
        batchSize: 10,
        maxBatchingWindow: BATCHING_WINDOW,
        // The handler answers the messages that failed, and only those are
        // delivered again; without it one bad message fails its batch.
        reportBatchItemFailures: true,
        // Leaves the account's concurrency for its other functions.
        maxConcurrency: 10,
      }),
    );
    this.queueUrl = queue.queueUrl;

    // Each alarm fires on the first bad five minutes: a failed or throttled
    // call, calls near their time limit, a message waiting too long, or any
    // message in the dead-letter queue. docs/runbook.md says what each means
    // and what to do first.
    // tsconfig's exactOptionalPropertyTypes keeps a Queue or Table from
    // passing as the IQueue or ITable the monitoring takes (an optional
    // property they leave undefined), so it gets a reference by ARN or name.
    const alarms = new Alarms(this, 'Alarms', alerts.email);
    alarms.monitoring
      .monitorSqsQueueWithDlq({
        queue: Queue.fromQueueArn(this, 'MonitoredMessages', queue.queueArn),
        deadLetterQueue: Queue.fromQueueArn(this, 'MonitoredDeadLetters', deadLetters.queueArn),
        alarmFriendlyName: 'Messages',
        addQueueMaxMessageAgeAlarm: {
          Warning: { maxAgeInSeconds: MAX_MESSAGE_AGE.toSeconds() },
        },
        addDeadLetterQueueMaxSizeAlarm: { Critical: { maxMessageCount: 0 } },
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
        // The data tables, by ARN; the idempotency records expire and need no restore.
        tables: [table],
      });
    }

    new CfnOutput(this, 'EntryPoint', {
      value: this.queueUrl,
      description: 'The URL of the queue producers send messages to.',
    });
    new CfnOutput(this, 'QueueArn', {
      value: queue.queueArn,
      description: "The queue's ARN, which a producer's sqs:SendMessage permission names.",
    });
  }
}
