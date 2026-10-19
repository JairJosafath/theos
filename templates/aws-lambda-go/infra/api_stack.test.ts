import { App } from 'aws-cdk-lib';
import { Capture, Match, Template } from 'aws-cdk-lib/assertions';
import { readdirSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { beforeAll, describe, expect, it } from 'vitest';

import { ApiStack } from './api_stack';
import { copyVaultName } from './backups';
import type { Operations, Tier } from './standard';

const OPERATIONS: Operations = {
  backups: { retentionDays: 35, copyToRegion: '' },
  recovery: { rpo: '1h', rto: '4h' },
  alerts: { email: '' },
};

/** The stack's app and template for `tier`, with `operations` changed as given. */
function build(
  tier: Tier,
  backups: Partial<Operations['backups']> = {},
  alerts: Partial<Operations['alerts']> = {},
): { app: App; template: Template } {
  const operations: Operations = {
    ...OPERATIONS,
    backups: { ...OPERATIONS.backups, ...backups },
    alerts: { ...OPERATIONS.alerts, ...alerts },
  };
  const app = new App();
  const template = Template.fromStack(
    new ApiStack(app, 'test', {
      tier,
      operations,
    }),
  );
  return { app, template };
}

/** The stack's template for `tier`, with `operations` changed as given. */
function synth(
  tier: Tier,
  backups: Partial<Operations['backups']> = {},
  alerts: Partial<Operations['alerts']> = {},
): Template {
  return build(tier, backups, alerts).template;
}

/** The IAM actions the stack's policies allow, sorted. */
function allowedActions(template: Template): string[] {
  return Object.values(template.findResources('AWS::IAM::Policy'))
    .flatMap((policy: unknown) => JSON.stringify(policy).match(/"[a-z0-9]+:[A-Za-z]+"/g) ?? [])
    .sort();
}

let outdir: string;
let production: Template;
let development: Template;

beforeAll(() => {
  const built = build('production');
  outdir = built.app.outdir;
  production = built.template;
  development = synth('development');
});

describe('ApiStack', () => {
  it('keeps items in an on-demand table, kept and recoverable in production', () => {
    production.resourceCountIs('AWS::DynamoDB::Table', 1);
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
  });

  it('throws the table away with the stack in development', () => {
    development.resourceCountIs('AWS::DynamoDB::Table', 1);
    development.allResources('AWS::DynamoDB::Table', {
      DeletionPolicy: 'Delete',
      Properties: {
        PointInTimeRecoverySpecification: { PointInTimeRecoveryEnabled: false },
        DeletionProtectionEnabled: false,
      },
    });
  });

  it('runs the Go binary on arm64 and Amazon Linux 2023, with its limits set', () => {
    production.hasResourceProperties('AWS::Lambda::Function', {
      Handler: 'bootstrap',
      Runtime: 'provided.al2023',
      Architectures: ['arm64'],
      MemorySize: 256,
      Timeout: 10,
      Environment: { Variables: { TABLE_NAME: Match.anyValue() } },
    });
  });

  it('builds src/ into one bootstrap binary for arm64 Linux', () => {
    const key = new Capture();
    production.hasResourceProperties('AWS::Lambda::Function', { Code: { S3Key: key } });
    const dir = path.join(outdir, `asset.${key.asString().replace(/\.zip$/, '')}`);
    expect(readdirSync(dir)).toEqual(['bootstrap']);
    // An ELF executable for AArch64: e_machine 183 at offset 18.
    const binary = readFileSync(path.join(dir, 'bootstrap'));
    expect(binary.subarray(0, 4).toString('latin1')).toBe('\x7fELF');
    expect(binary.readUInt16LE(18)).toBe(183);
  });

  it('traces with X-Ray in production only', () => {
    production.hasResourceProperties('AWS::Lambda::Function', {
      TracingConfig: { Mode: 'Active' },
    });
    development.hasResourceProperties('AWS::Lambda::Function', {
      TracingConfig: Match.absent(),
    });
  });

  it('lets the handler only get and put items, and write its logs', () => {
    const own = [
      '"dynamodb:GetItem"',
      '"dynamodb:PutItem"',
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

  it('logs every request and throttles the stage', () => {
    production.hasResourceProperties('AWS::ApiGatewayV2::Stage', {
      StageName: '$default',
      AutoDeploy: true,
      AccessLogSettings: { DestinationArn: Match.anyValue(), Format: Match.anyValue() },
      DefaultRouteSettings: { ThrottlingRateLimit: 50, ThrottlingBurstLimit: 100 },
    });
  });

  it('alarms on the API, the function and the table, in either tier', () => {
    for (const template of [production, development]) {
      // 5xx; errors, throttles and duration; the table's read and write
      // throttles and system errors.
      template.resourceCountIs('AWS::CloudWatch::Alarm', 1 + 3 + 3);
      template.resourceCountIs('AWS::CloudWatch::Dashboard', 1);
      // The p99 duration alarm fires at 8 of the function's 10 seconds.
      template.hasResourceProperties('AWS::CloudWatch::Alarm', {
        Metrics: [
          Match.objectLike({
            MetricStat: Match.objectLike({
              Metric: Match.objectLike({ MetricName: 'Duration' }),
              Stat: 'p99',
            }),
          }),
        ],
        Threshold: 8000,
      });
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
    // stack in the same account carries too (#1267).
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
