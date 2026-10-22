import { App } from 'aws-cdk-lib';
import { Match, Template } from 'aws-cdk-lib/assertions';
import { beforeAll, describe, expect, it } from 'vitest';

import { ApiStack } from './api_stack';
import { copyVaultName } from './backups';
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
    new ApiStack(new App(), 'test', {
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

describe('ApiStack', () => {
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

  it('runs the handler on arm64 and the newest Python, with its limits and Powertools set', () => {
    production.hasResourceProperties('AWS::Lambda::Function', {
      Handler: 'app.handler.handler',
      Runtime: 'python3.14',
      Architectures: ['arm64'],
      MemorySize: 256,
      Timeout: 10,
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

  it('lets the handler only get and put items, keep idempotency records, and write its logs', () => {
    const own = [
      '"dynamodb:DeleteItem"',
      '"dynamodb:GetItem"',
      '"dynamodb:GetItem"',
      '"dynamodb:PutItem"',
      '"dynamodb:PutItem"',
      '"dynamodb:UpdateItem"',
      '"logs:CreateLogStream"',
      '"logs:PutLogEvents"',
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

  it('routes GET and PUT /items/{id} to the handler, signed with IAM', () => {
    for (const routeKey of ['GET /items/{id}', 'PUT /items/{id}']) {
      production.hasResourceProperties('AWS::ApiGatewayV2::Route', {
        RouteKey: routeKey,
        AuthorizationType: 'AWS_IAM',
      });
    }
    production.resourceCountIs('AWS::ApiGatewayV2::Route', 2);
  });

  it('logs every request as JSON and throttles the stage', () => {
    production.hasResourceProperties('AWS::ApiGatewayV2::Stage', {
      StageName: '$default',
      AutoDeploy: true,
      // A JSON object per request, which Logs Insights queries by field.
      AccessLogSettings: {
        DestinationArn: Match.anyValue(),
        Format: Match.serializedJson(
          Match.objectLike({
            requestId: '$context.requestId',
            routeKey: '$context.routeKey',
            status: '$context.status',
            responseLatency: '$context.responseLatency',
            integrationError: '$context.integrationErrorMessage',
          }),
        ),
      },
      DefaultRouteSettings: { ThrottlingRateLimit: 50, ThrottlingBurstLimit: 100 },
    });
  });

  it('alarms on the API, the function and both tables, in either tier', () => {
    for (const template of [production, development]) {
      // 5xx; errors, throttles and duration; per table read and write
      // throttles and system errors.
      template.resourceCountIs('AWS::CloudWatch::Alarm', 1 + 3 + 2 * 3);
      template.resourceCountIs('AWS::CloudWatch::Dashboard', 1);
      template.hasOutput('AlarmTopicArn', { Value: { Ref: Match.anyValue() } });
      template.hasOutput('EntryPoint', {
        Value: { 'Fn::GetAtt': [Match.anyValue(), 'ApiEndpoint'] },
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
