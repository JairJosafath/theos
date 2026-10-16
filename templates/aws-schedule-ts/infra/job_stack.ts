import { CfnOutput, Duration, RemovalPolicy, Stack, TimeZone, Validations } from 'aws-cdk-lib';
import type { StackProps } from 'aws-cdk-lib';
import { ComparisonOperator, Metric, Stats, TreatMissingData } from 'aws-cdk-lib/aws-cloudwatch';
import { AttributeType, BillingMode, Table, TableEncryption } from 'aws-cdk-lib/aws-dynamodb';
import type { TableProps } from 'aws-cdk-lib/aws-dynamodb';
import { PolicyStatement, Role, ServicePrincipal } from 'aws-cdk-lib/aws-iam';
import { Architecture, LoggingFormat, Runtime, Tracing } from 'aws-cdk-lib/aws-lambda';
import { NodejsFunction } from 'aws-cdk-lib/aws-lambda-nodejs';
import { LogGroup } from 'aws-cdk-lib/aws-logs';
import {
  ContextAttribute,
  Schedule,
  ScheduleExpression,
  ScheduleGroup,
  ScheduleTargetInput,
  TimeWindow,
} from 'aws-cdk-lib/aws-scheduler';
import { LambdaInvoke } from 'aws-cdk-lib/aws-scheduler-targets';
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

/** When the job runs: every day at 03:00 UTC. Change it here. */
export const SCHEDULE = ScheduleExpression.cron({
  minute: '0',
  hour: '3',
  timeZone: TimeZone.ETC_UTC,
});

// The function's time limit for one run, which its duration alarm is a
// share of.
const TIMEOUT = Duration.minutes(5);

// How often the schedule tries again to hand a run to Lambda (Lambda
// throttling the call, or the service failing), and for how long.
export const SCHEDULE_RETRY_ATTEMPTS = 10;
const SCHEDULE_MAX_EVENT_AGE = Duration.hours(1);

// How often Lambda runs a run again that threw: its most, after one and then
// two minutes.
export const RUN_RETRY_ATTEMPTS = 2;
const RUN_MAX_EVENT_AGE = Duration.hours(1);

// The Scheduler's metrics a run that is lost would show: dropped with no
// dead-letter queue to go to, or failed to reach it.
export const LOST_RUN_METRICS = [
  'InvocationDroppedCount',
  'InvocationsFailedToBeSentToDeadLetterCount',
] as const;

/** What the stack is built for. */
export interface JobStackProps extends StackProps {
  /** Where it runs, which decides what it keeps, backs up and traces. */
  readonly tier: Tier;
  /** What the Container promises whatever the tier: `OPERATIONS`. */
  readonly operations: Operations;
  /** The tag, key and value, every resource of the stack carries: what its backups select. */
  readonly backupTag: readonly [string, string];
}

/**
 * The Container's one stack: an EventBridge Scheduler schedule that invokes
 * a Lambda function on `SCHEDULE`, keeping a record of each run in a
 * DynamoDB table. The schedule's role may invoke only that function. A run
 * the schedule can't hand to Lambda, and a run that still fails after
 * Lambda's retries, waits in one dead-letter queue. Every part has alarms, to
 * a topic the stack outputs with the schedule's ARN; production keeps and
 * backs up the data.
 */
export class JobStack extends Stack {
  /** The schedule's ARN. */
  readonly scheduleArn: string;

  constructor(scope: Construct, id: string, props: JobStackProps) {
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
    const table = new Table(this, 'Runs', {
      ...tableProps,
      partitionKey: { name: 'id', type: AttributeType.STRING },
    });
    // Where Powertools' Idempotency records the runs done, so a run
    // delivered twice does nothing twice.
    const idempotency = new Table(this, 'Idempotency', {
      ...tableProps,
      partitionKey: { name: 'id', type: AttributeType.STRING },
      timeToLiveAttribute: 'expiration',
    });

    // Runs that failed are data too: production keeps the queue with the
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
        'The queue is the dead-letter queue of the schedule’s target and of the function’s asynchronous invocations, which name it in their own DeadLetterConfig, not in a queue’s redrive policy.',
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
      // The schedule invokes the function asynchronously: a run that throws
      // is run again by Lambda, and then sent to the dead-letter queue.
      retryAttempts: RUN_RETRY_ATTEMPTS,
      maxEventAge: RUN_MAX_EVENT_AGE,
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

    // The Scheduler's metrics are per schedule group: the stack's own group
    // holds its schedule, so its alarms see only this job.
    const group = new ScheduleGroup(this, 'Schedules', { removalPolicy: RemovalPolicy.DESTROY });
    // The schedule's role, which only EventBridge Scheduler may assume, for
    // this group in this account, and which may invoke only this function
    // and send to the dead-letter queue. The target would make one that
    // invokes every version and alias too (`<arn>:*`); the schedule invokes
    // the function itself, so the target gets this role, closed to its
    // grants.
    const scheduleRole = new Role(this, 'ScheduleRole', {
      assumedBy: new ServicePrincipal('scheduler.amazonaws.com', {
        conditions: {
          StringEquals: {
            'aws:SourceAccount': this.account,
            'aws:SourceArn': group.scheduleGroupArn,
          },
        },
      }),
    });
    scheduleRole.addToPolicy(
      new PolicyStatement({ actions: ['lambda:InvokeFunction'], resources: [handler.functionArn] }),
    );
    scheduleRole.addToPolicy(
      new PolicyStatement({ actions: ['sqs:SendMessage'], resources: [deadLetters.queueArn] }),
    );
    const schedule = new Schedule(this, 'Schedule', {
      description: `Runs ${id}.`,
      schedule: SCHEDULE,
      scheduleGroup: group,
      timeWindow: TimeWindow.off(),
      target: new LambdaInvoke(handler, {
        role: scheduleRole.withoutPolicyUpdates(),
        // The run's key, and which delivery of it this is.
        input: ScheduleTargetInput.fromObject({
          scheduledTime: ContextAttribute.scheduledTime,
          attemptNumber: ContextAttribute.attemptNumber,
        }),
        retryAttempts: SCHEDULE_RETRY_ATTEMPTS,
        maxEventAge: SCHEDULE_MAX_EVENT_AGE,
        deadLetterQueue: deadLetters,
      }),
    });
    this.scheduleArn = schedule.scheduleArn;

    // Each alarm fires on the first bad five minutes: a failed or throttled
    // run, a run near its time limit, a run lost by the Scheduler, or any
    // message in the dead-letter queue. docs/runbook.md says what each means
    // and what to do first.
    // tsconfig's exactOptionalPropertyTypes keeps a Queue or Table from
    // passing as the IQueue or ITable the monitoring takes (an optional
    // property they leave undefined), so it gets a reference by ARN or name.
    const above = (threshold: number): CustomThreshold => ({
      threshold,
      comparisonOperator: ComparisonOperator.GREATER_THAN_THRESHOLD,
      // The Scheduler reports nothing while nothing went wrong.
      treatMissingDataOverride: TreatMissingData.NOT_BREACHING,
    });
    const alarms = new Alarms(this, 'Alarms', alerts.email);
    alarms.monitoring
      .monitorCustom({
        alarmFriendlyName: 'Schedule',
        humanReadableName: 'Schedule',
        metricGroups: [
          {
            title: 'Runs lost by the Scheduler',
            metrics: LOST_RUN_METRICS.map((metricName) => ({
              metric: new Metric({
                namespace: 'AWS/Scheduler',
                metricName,
                dimensionsMap: { ScheduleGroup: group.scheduleGroupName },
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
      ['Runs', table],
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
      value: this.scheduleArn,
      description: 'The ARN of the schedule that runs the job.',
    });
    new CfnOutput(this, 'FunctionName', {
      value: handler.functionName,
      description: 'The function the schedule invokes, which a person may invoke by hand.',
    });
  }
}
