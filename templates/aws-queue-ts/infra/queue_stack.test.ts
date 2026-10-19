import { App } from 'aws-cdk-lib';
import { Match, Template } from 'aws-cdk-lib/assertions';
import { beforeAll, describe, expect, it } from 'vitest';

import { copyVaultName } from './backups';
import { MAX_MESSAGE_AGE, MAX_RECEIVE_COUNT, QueueStack } from './queue_stack';
import type { Operations, Tier } from './standard';

const OPERATIONS: Operations = {
  backups: { retentionDays: 35, copyToRegion: '' },
  recovery: { rpo: '1h', rto: '4h' },
  alerts: { email: '' },
};

/** The stack's template for `tier`, with `operations` changed as given. */
function synth(
  tier: Tier,
  backups: Partial<Operations['backups']> = {},
  alerts: Partial<Operations['alerts']> = {},
): Template {
  const operations: Operations = {
    ...OPERATIONS,
    backups: { ...OPERATIONS.backups, ...backups },
    alerts: { ...OPERATIONS.alerts, ...alerts },
  };
  return Template.fromStack(
    new QueueStack(new App(), 'test', {
      tier,
      operations,
    }),
  );
}

/** The IAM actions the stack's policies allow, sorted. */
function allowedActions(template: Template): string[] {
  return Object.values(template.findResources('AWS::IAM::Policy'))
    .flatMap((policy: unknown) => JSON.stringify(policy).match(/"[a-z0-9]+:[A-Za-z]+"/g) ?? [])
    .sort();
}

let production: Template;
let development: Template;

beforeAll(() => {
  production = synth('production');
  development = synth('development');
});

describe('QueueStack', () => {
  it('keeps items and idempotency records in on-demand tables, kept and recoverable in production', () => {
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
      Timeout: 30,
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

  it('lets the handler only put items, keep idempotency records, consume the queue and write its logs', () => {
    const own = [
      '"dynamodb:DeleteItem"',
      '"dynamodb:GetItem"',
      '"dynamodb:PutItem"',
      '"dynamodb:PutItem"',
      '"dynamodb:UpdateItem"',
      '"logs:CreateLogStream"',
      '"logs:PutLogEvents"',
      '"sqs:ChangeMessageVisibility"',
      '"sqs:DeleteMessage"',
      '"sqs:GetQueueAttributes"',
      '"sqs:GetQueueUrl"',
      '"sqs:ReceiveMessage"',
    ];
    expect(allowedActions(development)).toEqual(own);
    expect(allowedActions(production)).toEqual(
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

  it('consumes the queue in batches, reporting the messages that failed', () => {
    production.hasResourceProperties('AWS::Lambda::EventSourceMapping', {
      EventSourceArn: { 'Fn::GetAtt': [Match.stringLikeRegexp('^Messages'), 'Arn'] },
      BatchSize: 10,
      MaximumBatchingWindowInSeconds: 5,
      FunctionResponseTypes: ['ReportBatchItemFailures'],
      ScalingConfig: { MaximumConcurrency: 10 },
    });
    production.resourceCountIs('AWS::Lambda::EventSourceMapping', 1);
  });

  it('moves a message to the dead-letter queue after its last delivery', () => {
    production.hasResourceProperties('AWS::SQS::Queue', {
      VisibilityTimeout: 6 * 30 + 5,
      MessageRetentionPeriod: 4 * 24 * 3600,
      RedrivePolicy: {
        deadLetterTargetArn: { 'Fn::GetAtt': [Match.stringLikeRegexp('^DeadLetters'), 'Arn'] },
        maxReceiveCount: MAX_RECEIVE_COUNT,
      },
    });
    production.hasResourceProperties('AWS::SQS::Queue', {
      MessageRetentionPeriod: 14 * 24 * 3600,
      RedrivePolicy: Match.absent(),
    });
  });

  it('encrypts both queues and refuses requests to them without TLS', () => {
    for (const template of [production, development]) {
      template.resourceCountIs('AWS::SQS::Queue', 2);
      template.allResourcesProperties('AWS::SQS::Queue', { SqsManagedSseEnabled: true });
      template.resourceCountIs('AWS::SQS::QueuePolicy', 2);
      template.allResourcesProperties('AWS::SQS::QueuePolicy', {
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

  it('keeps the queues with the stack in production, and throws them away in development', () => {
    production.allResources('AWS::SQS::Queue', { DeletionPolicy: 'Retain' });
    development.allResources('AWS::SQS::Queue', { DeletionPolicy: 'Delete' });
  });

  it('alarms on the queues, the function and both tables, in either tier', () => {
    for (const template of [production, development]) {
      // The oldest message's age and the dead-letter queue's depth; errors,
      // throttles and duration; per table read and write throttles and
      // system errors.
      template.resourceCountIs('AWS::CloudWatch::Alarm', 2 + 3 + 2 * 3);
      template.hasResourceProperties('AWS::CloudWatch::Alarm', {
        Threshold: MAX_MESSAGE_AGE.toSeconds(),
        ComparisonOperator: 'GreaterThanThreshold',
        Metrics: Match.arrayWith([
          Match.objectLike({
            MetricStat: Match.objectLike({
              Metric: Match.objectLike({ MetricName: 'ApproximateAgeOfOldestMessage' }),
            }),
          }),
        ]),
      });
      // Any message in the dead-letter queue: a person looks.
      const [deadLetters] = Object.keys(template.findResources('AWS::SQS::Queue')).filter((id) =>
        id.startsWith('DeadLetters'),
      );
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
      template.hasOutput('EntryPoint', { Value: { Ref: Match.stringLikeRegexp('^Messages') } });
      template.hasOutput('QueueArn', {
        Value: { 'Fn::GetAtt': [Match.stringLikeRegexp('^Messages'), 'Arn'] },
      });
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
      (id) => id.startsWith('Items'),
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
