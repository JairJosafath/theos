import { App } from 'aws-cdk-lib';
import { Match, Template } from 'aws-cdk-lib/assertions';
import { AttributeType, Table } from 'aws-cdk-lib/aws-dynamodb';
import { beforeAll, describe, expect, it } from 'vitest';

import { copyVaultName } from './backups';
import { DATA_STORES } from './connectors';
import type { Connect } from './connectors';
import {
  JobStack,
  LOST_RUN_METRICS,
  RUN_RETRY_ATTEMPTS,
  SCHEDULE_RETRY_ATTEMPTS,
} from './job_stack';
import type { Operations, Tier } from './standard';

const OPERATIONS: Operations = {
  backups: { retentionDays: 35, copyToRegion: '' },
  recovery: { rpo: '1h', rto: '4h' },
  alerts: { email: '' },
};

/** The stack's own tables, without a data store: its runs and its idempotency records. */
const TABLES = 2;

/**
 * The stack's template for `tier`, with `operations` changed as given, and
 * `dataStores` in place of the Connectors' (none, unless a test names them).
 */
function synth(
  tier: Tier,
  backups: Partial<Operations['backups']> = {},
  alerts: Partial<Operations['alerts']> = {},
  dataStores: readonly (readonly [string, Connect])[] = [],
): Template {
  const operations: Operations = {
    ...OPERATIONS,
    backups: { ...OPERATIONS.backups, ...backups },
    alerts: { ...OPERATIONS.alerts, ...alerts },
  };
  return Template.fromStack(
    new JobStack(new App(), 'test', {
      tier,
      operations,
      dataStores,
    }),
  );
}

/** The IAM actions the policies of the role `role` assumes allow, sorted. */
function allowedActions(template: Template, role: RegExp): string[] {
  return Object.values(template.findResources('AWS::IAM::Policy'))
    .filter((policy: unknown) =>
      role.test(/"Roles":\[\{"Ref":"([^"]+)"/.exec(JSON.stringify(policy))?.[1] ?? ''),
    )
    .flatMap((policy: unknown) => JSON.stringify(policy).match(/"[a-z0-9]+:[A-Za-z]+"/g) ?? [])
    .sort();
}

let production: Template;
let development: Template;

beforeAll(() => {
  production = synth('production');
  development = synth('development');
});

describe('JobStack', () => {
  it('keeps runs and idempotency records in on-demand tables, kept and recoverable in production', () => {
    production.resourceCountIs('AWS::DynamoDB::Table', 2);
    production.hasResource('AWS::DynamoDB::Table', {
      DeletionPolicy: 'Retain',
      UpdateReplacePolicy: 'Retain',
      Properties: {
        KeySchema: [{ AttributeName: 'id', KeyType: 'HASH' }],
        BillingMode: 'PAY_PER_REQUEST',
        PointInTimeRecoverySpecification: { PointInTimeRecoveryEnabled: true },
        DeletionProtectionEnabled: true,
      },
    });
    production.hasResourceProperties('AWS::DynamoDB::Table', {
      TimeToLiveSpecification: { AttributeName: 'expiration', Enabled: true },
    });
  });

  it('throws the tables away with the stack in development', () => {
    development.allResources('AWS::DynamoDB::Table', {
      DeletionPolicy: 'Delete',
      Properties: {
        PointInTimeRecoverySpecification: { PointInTimeRecoveryEnabled: false },
        DeletionProtectionEnabled: false,
      },
    });
  });

  it('runs the handler on arm64 and the newest Node, with its limits and Powertools set', () => {
    production.hasResourceProperties('AWS::Lambda::Function', {
      Handler: 'index.handler',
      Runtime: 'nodejs24.x',
      Architectures: ['arm64'],
      MemorySize: 256,
      Timeout: 300,
      Environment: {
        Variables: {
          TABLE_NAME: Match.anyValue(),
          IDEMPOTENCY_TABLE_NAME: Match.anyValue(),
          POWERTOOLS_SERVICE_NAME: 'test',
          POWERTOOLS_METRICS_NAMESPACE: 'test',
        },
      },
    });
  });

  it('traces with X-Ray in production only', () => {
    production.hasResourceProperties('AWS::Lambda::Function', {
      TracingConfig: { Mode: 'Active' },
    });
    development.hasResourceProperties('AWS::Lambda::Function', {
      TracingConfig: Match.absent(),
    });
  });

  it('lets the handler only put runs, keep idempotency records, dead-letter a run and write its logs', () => {
    const own = [
      '"dynamodb:DeleteItem"',
      '"dynamodb:GetItem"',
      '"dynamodb:PutItem"',
      '"dynamodb:PutItem"',
      '"dynamodb:UpdateItem"',
      '"logs:CreateLogStream"',
      '"logs:PutLogEvents"',
      '"sqs:SendMessage"',
    ];
    expect(allowedActions(development, /^HandlerRole/)).toEqual(own);
    expect(allowedActions(production, /^HandlerRole/)).toEqual(
      [...own, '"xray:PutTelemetryRecords"', '"xray:PutTraceSegments"'].sort(),
    );
    // The handler's role carries no managed policy.
    production.resourcePropertiesCountIs(
      'AWS::IAM::Role',
      {
        AssumeRolePolicyDocument: Match.objectLike({
          Statement: [Match.objectLike({ Principal: { Service: 'lambda.amazonaws.com' } })],
        }),
        ManagedPolicyArns: Match.anyValue(),
      },
      0,
    );
  });

  it('runs the job on its schedule, in the stack’s own group, at the time it names', () => {
    for (const template of [production, development]) {
      template.resourceCountIs('AWS::Scheduler::Schedule', 1);
      template.resourceCountIs('AWS::Scheduler::ScheduleGroup', 1);
      template.hasResourceProperties('AWS::Scheduler::Schedule', {
        // A name CDK makes at synth: the alarms' dimension names it too.
        GroupName: Match.stringLikeRegexp('Schedules'),
        ScheduleExpression: 'cron(0 3 * * ? *)',
        ScheduleExpressionTimezone: 'Etc/UTC',
        FlexibleTimeWindow: { Mode: 'OFF' },
        State: 'ENABLED',
        Target: {
          Arn: { 'Fn::GetAtt': [Match.stringLikeRegexp('^Handler'), 'Arn'] },
          // The run's key, which the idempotency records go by.
          Input: JSON.stringify({
            scheduledTime: '<aws.scheduler.scheduled-time>',
            attemptNumber: '<aws.scheduler.attempt-number>',
          }),
          RetryPolicy: {
            MaximumRetryAttempts: SCHEDULE_RETRY_ATTEMPTS,
            MaximumEventAgeInSeconds: 3600,
          },
          DeadLetterConfig: {
            Arn: { 'Fn::GetAtt': [Match.stringLikeRegexp('^DeadLetters'), 'Arn'] },
          },
          RoleArn: { 'Fn::GetAtt': [Match.stringLikeRegexp('^ScheduleRole'), 'Arn'] },
        },
      });
    }
  });

  it('lets the schedule only invoke the one function and dead-letter a run, and only the Scheduler of its group assume its role', () => {
    production.hasResourceProperties('AWS::IAM::Policy', {
      Roles: [{ Ref: Match.stringLikeRegexp('^ScheduleRole') }],
      PolicyDocument: {
        Statement: [
          {
            Effect: 'Allow',
            Action: 'lambda:InvokeFunction',
            Resource: { 'Fn::GetAtt': [Match.stringLikeRegexp('^Handler'), 'Arn'] },
          },
          {
            Effect: 'Allow',
            Action: 'sqs:SendMessage',
            Resource: { 'Fn::GetAtt': [Match.stringLikeRegexp('^DeadLetters'), 'Arn'] },
          },
        ],
      },
    });
    production.hasResourceProperties('AWS::IAM::Role', {
      AssumeRolePolicyDocument: {
        Statement: [
          {
            Action: 'sts:AssumeRole',
            Effect: 'Allow',
            Principal: { Service: 'scheduler.amazonaws.com' },
            Condition: {
              StringEquals: {
                'aws:SourceAccount': { Ref: 'AWS::AccountId' },
                'aws:SourceArn': { 'Fn::GetAtt': [Match.stringLikeRegexp('^Schedules'), 'Arn'] },
              },
            },
          },
        ],
      },
    });
    // The schedule invokes through its role, never through a resource policy.
    production.resourceCountIs('AWS::Lambda::Permission', 0);
  });

  it('retries a run that threw, then dead-letters it', () => {
    for (const template of [production, development]) {
      template.hasResourceProperties('AWS::Lambda::EventInvokeConfig', {
        MaximumRetryAttempts: RUN_RETRY_ATTEMPTS,
        MaximumEventAgeInSeconds: 3600,
      });
      template.hasResourceProperties('AWS::Lambda::Function', {
        DeadLetterConfig: {
          TargetArn: { 'Fn::GetAtt': [Match.stringLikeRegexp('^DeadLetters'), 'Arn'] },
        },
      });
    }
  });

  it('encrypts the dead-letter queue and refuses requests to it without TLS', () => {
    for (const template of [production, development]) {
      template.resourceCountIs('AWS::SQS::Queue', 1);
      template.hasResourceProperties('AWS::SQS::Queue', {
        SqsManagedSseEnabled: true,
        MessageRetentionPeriod: 14 * 24 * 3600,
      });
      template.hasResourceProperties('AWS::SQS::QueuePolicy', {
        PolicyDocument: {
          Statement: Match.arrayWith([
            Match.objectLike({
              Effect: 'Deny',
              Action: 'sqs:*',
              Condition: { Bool: { 'aws:SecureTransport': 'false' } },
            }),
          ]),
        },
      });
    }
  });

  it('keeps the dead-letter queue with the stack in production, and throws it away in development', () => {
    production.allResources('AWS::SQS::Queue', { DeletionPolicy: 'Retain' });
    development.allResources('AWS::SQS::Queue', { DeletionPolicy: 'Delete' });
  });

  it('alarms on the schedule, the dead-letter queue, the function and both tables, in either tier', () => {
    for (const template of [production, development]) {
      // Runs lost by the Scheduler; the dead-letter queue's depth; errors,
      // throttles and duration; per table read and write throttles and
      // system errors.
      template.resourceCountIs('AWS::CloudWatch::Alarm', LOST_RUN_METRICS.length + 1 + 3 + 2 * 3);
      for (const metric of LOST_RUN_METRICS) {
        template.hasResourceProperties('AWS::CloudWatch::Alarm', {
          Namespace: 'AWS/Scheduler',
          MetricName: metric,
          Dimensions: [{ Name: 'ScheduleGroup', Value: Match.stringLikeRegexp('Schedules') }],
          Threshold: 0,
          ComparisonOperator: 'GreaterThanThreshold',
          TreatMissingData: 'notBreaching',
        });
      }
      // Any message in the dead-letter queue: a person looks.
      const [deadLetters] = Object.keys(template.findResources('AWS::SQS::Queue'));
      const depth = Object.values(template.findResources('AWS::CloudWatch::Alarm')).filter(
        (alarm: unknown) => {
          const json = JSON.stringify(alarm);
          return (
            json.includes('"MetricName":"ApproximateNumberOfMessagesVisible"') &&
            json.includes(`"Fn::GetAtt":["${String(deadLetters)}","Arn"]`) &&
            json.includes('"Threshold":0')
          );
        },
      );
      expect(depth).toHaveLength(1);
      template.hasOutput('AlarmTopicArn', { Value: { Ref: Match.anyValue() } });
      template.hasOutput('EntryPoint', {
        Value: { 'Fn::GetAtt': [Match.stringLikeRegexp('^Schedule[0-9A-F]'), 'Arn'] },
      });
      template.hasOutput('FunctionName', { Value: { Ref: Match.stringLikeRegexp('^Handler') } });
    }
  });

  it('defaults the alert email to alerts.email', () => {
    synth('development', {}, { email: 'ops@example.com' }).hasParameter('AlertEmail', {
      Default: 'ops@example.com',
    });
  });

  it('backs up daily for retentionDays in production, and not at all in development', () => {
    production.hasResourceProperties('AWS::Backup::BackupPlan', {
      BackupPlan: {
        BackupPlanRule: [Match.objectLike({ Lifecycle: { DeleteAfterDays: 35 }, CopyActions: [] })],
      },
    });
    // The data table by ARN, never the Container's tag, which a development
    // stack in the same account carries too (#1267); not the idempotency
    // records, which expire.
    const [data, ...others] = Object.keys(production.findResources('AWS::DynamoDB::Table')).filter(
      (id) => id.startsWith('Runs'),
    );
    expect([data, others]).toEqual([expect.any(String), []]);
    production.resourceCountIs('AWS::Backup::BackupSelection', 1);
    production.hasResourceProperties('AWS::Backup::BackupSelection', {
      BackupSelection: Match.objectLike({
        Resources: [{ 'Fn::GetAtt': [data, 'Arn'] }],
        ListOfTags: Match.absent(),
        Conditions: Match.absent(),
      }),
    });
    development.resourceCountIs('AWS::Backup::BackupPlan', 0);
    development.resourceCountIs('AWS::Backup::BackupVault', 0);
  });

  it('keeps only point-in-time recovery when retentionDays is 0', () => {
    const template = synth('production', { retentionDays: 0 });
    template.resourceCountIs('AWS::Backup::BackupPlan', 0);
    template.hasResourceProperties('AWS::DynamoDB::Table', {
      PointInTimeRecoverySpecification: { PointInTimeRecoveryEnabled: true },
    });
  });

  it('copies the backups to the vault in copyToRegion', () => {
    synth('production', { copyToRegion: 'eu-west-1' }).hasResourceProperties(
      'AWS::Backup::BackupPlan',
      {
        BackupPlan: {
          BackupPlanRule: [
            Match.objectLike({
              CopyActions: [
                {
                  DestinationBackupVaultArn: Match.objectLike({
                    'Fn::Join': ['', Match.arrayWith([':backup:eu-west-1:'])],
                  }),
                  Lifecycle: { DeleteAfterDays: 35 },
                },
              ],
            }),
          ],
        },
      },
    );
  });
});

describe('JobStack data stores', () => {
  it("connects the function to each Connector's data store and alarms on its table", () => {
    const connected: string[] = [];
    const connect: Connect = (scope, fn, rules) => {
      connected.push(fn.node.id);
      return new Table(scope, 'Store', {
        partitionKey: { name: 'pk', type: AttributeType.STRING },
        deletionProtection: rules.keepData,
      });
    };
    const template = synth('production', {}, {}, [['Store', connect]]);
    expect(connected).toEqual(['Handler']);
    template.resourceCountIs('AWS::DynamoDB::Table', TABLES + 1);
    template.resourceCountIs('AWS::CloudWatch::Alarm', LOST_RUN_METRICS.length + 1 + 3 + (TABLES + 1) * 3);
    // Its table is data: backed up with the stack's own (#1267).
    const tables = Object.keys(template.findResources('AWS::DynamoDB::Table'));
    const backedUp = ['Runs', 'Store'].map((name) => tables.find((id) => id.startsWith(name)));
    template.hasResourceProperties('AWS::Backup::BackupSelection', {
      BackupSelection: Match.objectLike({
        Resources: backedUp.map((id) => ({ 'Fn::GetAtt': [id, 'Arn'] })),
      }),
    });
  });

  it('connects the data stores of the rendered infra/connectors.ts by default', () => {
    const template = Template.fromStack(
      new JobStack(new App(), 'test', {
        tier: 'production',
        operations: OPERATIONS,
      }),
    );
    template.resourceCountIs('AWS::DynamoDB::Table', TABLES + DATA_STORES.length);
  });
});

describe('copyVaultName', () => {
  it('names the copy vault after the stack', () => {
    expect(copyVaultName('orders')).toBe('orders-copies');
  });

  it('keeps a long name within 50 characters, and two long names apart', () => {
    const long = `${'a'.repeat(40)}-one`;
    const other = `${'a'.repeat(40)}-two`;
    expect(copyVaultName(long)).toHaveLength(50);
    expect(copyVaultName(long)).not.toBe(copyVaultName(other));
  });
});
