import { CfnOutput, Duration, RemovalPolicy, Stack, Validations } from 'aws-cdk-lib';
import type { StackProps } from 'aws-cdk-lib';
import { ComparisonOperator, Metric, Stats, TreatMissingData } from 'aws-cdk-lib/aws-cloudwatch';
import { AttributeType, BillingMode, Table, TableEncryption } from 'aws-cdk-lib/aws-dynamodb';
import type { TableProps } from 'aws-cdk-lib/aws-dynamodb';
import { Role, ServicePrincipal } from 'aws-cdk-lib/aws-iam';
import { Architecture, LoggingFormat, Runtime, Tracing } from 'aws-cdk-lib/aws-lambda';
import { NodejsFunction } from 'aws-cdk-lib/aws-lambda-nodejs';
import { LogGroup } from 'aws-cdk-lib/aws-logs';
import { Rule } from 'aws-cdk-lib/aws-events';
import type { EventPattern } from 'aws-cdk-lib/aws-events';
import { LambdaFunction } from 'aws-cdk-lib/aws-events-targets';
import { Queue, QueueEncryption } from 'aws-cdk-lib/aws-sqs';
import type { CustomThreshold } from 'cdk-monitoring-constructs';
import type { Construct } from 'constructs';
import path from 'node:path';

import { Alarms } from './alarms';
import { Backups } from './backups';
import { TIER_RULES } from './standard';
import type { Operations, Tier } from './standard';

// The repository root: the function's code and package-lock.json are there.
const ROOT = path.join(import.meta.dirname, '..');

/** The kind of event the rule sends to the function. */
export const DETAIL_TYPE = 'ItemSubmitted';

/**
 * The events the rule sends to the function: an `ItemSubmitted` whose
 * source is `source`, the Container's name, on the account's default event
 * bus. Change it here.
 */
export function eventPatternFor(source: string): EventPattern {
  return { source: [source], detailType: [DETAIL_TYPE] };
}

// The function's time limit for one event, which its duration alarm is a
// share of.
const TIMEOUT = Duration.seconds(30);

// How often the rule tries again to hand an event to Lambda (Lambda
// throttling the call, or the service failing), and for how long.
export const RULE_RETRY_ATTEMPTS = 10;
const RULE_MAX_EVENT_AGE = Duration.hours(1);

// How often Lambda runs an event again that threw: its most, after one and
// then two minutes.
export const EVENT_RETRY_ATTEMPTS = 2;
const EVENT_MAX_EVENT_AGE = Duration.hours(1);

// The rule's metrics an event that is lost would show: failed for good, or
// failed to reach the dead-letter queue.
export const LOST_EVENT_METRICS = ['FailedInvocations', 'InvocationsFailedToBeSentToDlq'] as const;

/** What the stack is built for. */
export interface EventStackProps extends StackProps {
  /** Who sends the events the rule matches: the Container's name. */
  readonly source: string;
  /** Where it runs, which decides what it keeps, backs up and traces. */
  readonly tier: Tier;
  /** What the Container promises whatever the tier: `OPERATIONS`. */
  readonly operations: Operations;
  /** The tag, key and value, every resource of the stack carries: what its backups select. */
  readonly backupTag: readonly [string, string];
}

/**
 * The Container's one stack: an EventBridge rule on the default event bus
 * that sends the events matching `eventPatternFor` to a Lambda function,
 * which stores the item each carries in a DynamoDB table. Only that rule may
 * invoke the function. An event the rule can't hand to Lambda, and an event
 * that still fails after Lambda's retries, waits in one dead-letter queue.
 * Every part has alarms, to a topic the stack outputs with the rule's ARN;
 * production keeps and backs up the data.
 */
export class EventStack extends Stack {
  /** The rule's ARN. */
  readonly ruleArn: string;

  constructor(scope: Construct, id: string, props: EventStackProps) {
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
    // Where Powertools' Idempotency records the events consumed, so an
    // event delivered twice is stored once.
    const idempotency = new Table(this, 'Idempotency', {
      ...tableProps,
      partitionKey: { name: 'id', type: AttributeType.STRING },
      timeToLiveAttribute: 'expiration',
    });

    // Events that failed are data too: production keeps the queue with the
    // tables. It is encrypted and refuses requests without TLS.
    const deadLetters = new Queue(this, 'DeadLetters', {
      encryption: QueueEncryption.SQS_MANAGED,
      enforceSSL: true,
      // The longest SQS keeps a message: time for a person to look.
      retentionPeriod: Duration.days(14),
      removalPolicy: rules.keepData ? RemovalPolicy.RETAIN : RemovalPolicy.DESTROY,
    });
    Validations.of(deadLetters).acknowledge({
      id: 'AwsSolutions-SQS3',
      reason:
        'The queue is the dead-letter queue of the rule’s target and of the function’s asynchronous invocations, which name it in their own DeadLetterConfig, not in a queue’s redrive policy.',
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
      // The rule invokes the function asynchronously: an event that throws
      // is run again by Lambda, and then sent to the dead-letter queue.
      retryAttempts: EVENT_RETRY_ATTEMPTS,
      maxEventAge: EVENT_MAX_EVENT_AGE,
      deadLetterQueue: deadLetters,
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
    table.grant(handler, 'dynamodb:PutItem');
    idempotency.grant(
      handler,
      'dynamodb:GetItem',
      'dynamodb:PutItem',
      'dynamodb:UpdateItem',
      'dynamodb:DeleteItem',
    );

    // On the default bus, where the account's own services and producers
    // put their events. A rule there is named by its name alone, which is
    // the dimension of its metrics; on a custom bus it would be `bus|rule`.
    const rule = new Rule(this, 'Events', {
      description: `Sends the ${DETAIL_TYPE} events of ${props.source} to ${id}.`,
      eventPattern: eventPatternFor(props.source),
    });
    // The target invokes the function through a resource policy only this
    // rule may use, and may send to the dead-letter queue by the queue's
    // policy, for this rule only.
    rule.addTarget(
      new LambdaFunction(handler, {
        retryAttempts: RULE_RETRY_ATTEMPTS,
        maxEventAge: RULE_MAX_EVENT_AGE,
        deadLetterQueue: deadLetters,
      }),
    );
    this.ruleArn = rule.ruleArn;

    // Each alarm fires on the first bad five minutes: a failed or throttled
    // event, an event near its time limit, an event lost by the rule, or any
    // message in the dead-letter queue. docs/runbook.md says what each means
    // and what to do first.
    // tsconfig's exactOptionalPropertyTypes keeps a Queue or Table from
    // passing as the IQueue or ITable the monitoring takes (an optional
    // property they leave undefined), so it gets a reference by ARN or name.
    const above = (threshold: number): CustomThreshold => ({
      threshold,
      comparisonOperator: ComparisonOperator.GREATER_THAN_THRESHOLD,
      // The rule reports nothing while nothing went wrong.
      treatMissingDataOverride: TreatMissingData.NOT_BREACHING,
    });
    const alarms = new Alarms(this, 'Alarms', alerts.email);
    alarms.monitoring
      .monitorCustom({
        alarmFriendlyName: 'Rule',
        humanReadableName: 'Rule',
        metricGroups: [
          {
            title: 'Events lost by the rule',
            metrics: LOST_EVENT_METRICS.map((metricName) => ({
              metric: new Metric({
                namespace: 'AWS/Events',
                metricName,
                dimensionsMap: { RuleName: rule.ruleName },
                statistic: Stats.SUM,
                period: Duration.minutes(5),
              }),
              alarmFriendlyName: metricName,
              addAlarm: { Critical: above(0) },
            })),
          },
        ],
      })
      .monitorSqsQueue({
        queue: Queue.fromQueueArn(this, 'MonitoredDeadLetters', deadLetters.queueArn),
        alarmFriendlyName: 'DeadLetters',
        addQueueMaxSizeAlarm: { Critical: { maxMessageCount: 0 } },
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
        tag: props.backupTag,
      });
    }

    new CfnOutput(this, 'EntryPoint', {
      value: this.ruleArn,
      description: 'The ARN of the rule that sends the events to the function.',
    });
    // What a producer's events must match, in EventBridge's own form.
    const { source, detailType } = eventPatternFor(props.source);
    new CfnOutput(this, 'EventPattern', {
      value: JSON.stringify({ source, 'detail-type': detailType }),
      description: 'The pattern a producer’s events on the default event bus must match.',
    });
    new CfnOutput(this, 'FunctionName', {
      value: handler.functionName,
      description:
        'The function the rule invokes, which a person invokes to run a dead-lettered event again.',
    });
  }
}
