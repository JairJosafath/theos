import { App } from 'aws-cdk-lib';
import { Match, Template } from 'aws-cdk-lib/assertions';
import { beforeAll, describe, expect, it } from 'vitest';

import { copyVaultName } from './backups';
import {
  bootCommands,
  INSTALL_COMMAND,
  MIN_INSTANCES,
  SERVER_PORT,
  ServiceStack,
  VPC_CIDR,
} from './service_stack';
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
  return Template.fromStack(new ServiceStack(new App(), 'test', { tier, operations }));
}

/** The one resource of `type` in `template`, with its logical id. */
function onlyOf(template: Template, type: string): [string, Record<string, unknown>] {
  const found = Object.entries(template.findResources(type));
  expect(found, type).toHaveLength(1);
  const [entry] = found;
  if (entry === undefined) {
    throw new Error(`no ${type}`);
  }
  return entry;
}

let production: Template;
let development: Template;

beforeAll(() => {
  production = synth('production');
  development = synth('development');
});

describe('ServiceStack', () => {
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
    development.hasResource('AWS::DynamoDB::Table', {
      DeletionPolicy: 'Delete',
      Properties: {
        PointInTimeRecoverySpecification: { PointInTimeRecoveryEnabled: false },
        DeletionProtectionEnabled: false,
      },
    });
  });

  it('keeps its logs a year in production and a month in development', () => {
    production.allResourcesProperties('AWS::Logs::LogGroup', { RetentionInDays: 365 });
    development.allResourcesProperties('AWS::Logs::LogGroup', { RetentionInDays: 30 });
    production.hasResourceProperties('AWS::S3::Bucket', {
      LifecycleConfiguration: { Rules: [Match.objectLike({ ExpirationInDays: 365 })] },
    });
    development.hasResourceProperties('AWS::S3::Bucket', {
      LifecycleConfiguration: { Rules: [Match.objectLike({ ExpirationInDays: 30 })] },
    });
  });

  it('builds a network with no way out but the AWS endpoints it needs', () => {
    production.hasResourceProperties('AWS::EC2::VPC', { CidrBlock: VPC_CIDR });
    production.resourceCountIs('AWS::EC2::NatGateway', 0);
    production.resourceCountIs('AWS::EC2::InternetGateway', 0);
    const endpoints = Object.values(production.findResources('AWS::EC2::VPCEndpoint'))
      .map((endpoint: unknown) =>
        JSON.stringify(endpoint).match(/\.(s3|dynamodb|logs|cloudformation)"/g),
      )
      .flat()
      .join(' ');
    for (const service of ['s3', 'dynamodb', 'logs', 'cloudformation']) {
      expect(endpoints).toContain(`.${service}"`);
    }
    production.resourceCountIs('AWS::EC2::VPCEndpoint', 4);
    production.hasResourceProperties('AWS::EC2::FlowLog', { TrafficType: 'REJECT' });
  });

  it('runs two to four Graviton instances with IMDSv2, encrypted disks and detailed monitoring', () => {
    production.hasResourceProperties('AWS::AutoScaling::AutoScalingGroup', {
      MinSize: String(MIN_INSTANCES),
      MaxSize: '4',
      HealthCheckType: 'ELB',
    });
    production.hasResourceProperties('AWS::EC2::LaunchTemplate', {
      LaunchTemplateData: Match.objectLike({
        InstanceType: 't4g.small',
        MetadataOptions: { HttpTokens: 'required' },
        Monitoring: { Enabled: true },
        BlockDeviceMappings: [
          Match.objectLike({
            Ebs: Match.objectLike({
              Encrypted: true,
              VolumeType: 'gp3',
              DeleteOnTermination: true,
            }),
          }),
        ],
      }),
    });
  });

  it('waits for each new instance to signal it is healthy, and replaces one at a time', () => {
    for (const template of [production, development]) {
      template.hasResource('AWS::AutoScaling::AutoScalingGroup', {
        CreationPolicy: { ResourceSignal: { Count: MIN_INSTANCES, Timeout: 'PT15M' } },
        UpdatePolicy: {
          AutoScalingRollingUpdate: Match.objectLike({
            MaxBatchSize: 1,
            MinInstancesInService: MIN_INSTANCES,
            WaitOnResourceSignals: true,
            PauseTime: 'PT15M',
          }),
        },
      });
    }
  });

  it('signals the group from the boot script, with its exit status', () => {
    const [groupId] = onlyOf(production, 'AWS::AutoScaling::AutoScalingGroup');
    const [, launchTemplate] = onlyOf(production, 'AWS::EC2::LaunchTemplate');
    const userData = JSON.stringify(launchTemplate);
    expect(userData).toContain('trap exitTrap EXIT');
    expect(userData).toContain(
      `/opt/aws/bin/cfn-signal --stack test --resource ${groupId} --region `,
    );
    expect(userData).toContain('-e $exitCode');
    // cfn-signal is installed before anything that may fail.
    expect(userData.indexOf(INSTALL_COMMAND)).toBeLessThan(userData.indexOf('aws s3 cp'));
  });

  it('sends the scaling events to the alarm topic, whose key lets Auto Scaling publish', () => {
    const [topicId] = onlyOf(production, 'AWS::SNS::Topic');
    production.hasResourceProperties('AWS::AutoScaling::AutoScalingGroup', {
      NotificationConfigurations: [
        Match.objectLike({
          TopicARN: { Ref: topicId },
          NotificationTypes: Match.arrayWith(['autoscaling:EC2_INSTANCE_LAUNCH_ERROR']),
        }),
      ],
    });
    production.hasResourceProperties('AWS::KMS::Key', {
      KeyPolicy: {
        Statement: Match.arrayWith([
          Match.objectLike({
            Action: ['kms:Decrypt', 'kms:GenerateDataKey*'],
            Condition: Match.objectLike({
              ArnEquals: {
                'aws:PrincipalArn': {
                  'Fn::Join': [
                    '',
                    Match.arrayWith([
                      ':role/aws-service-role/autoscaling.amazonaws.com/AWSServiceRoleForAutoScaling',
                    ]),
                  ],
                },
              },
            }),
          }),
        ]),
      },
    });
  });

  it('lets the instances only get and put items, read their code, write their logs and signal the stack', () => {
    const role = Object.keys(
      production.findResources('AWS::IAM::Role', {
        Properties: {
          AssumeRolePolicyDocument: Match.objectLike({
            Statement: [Match.objectLike({ Principal: { Service: 'ec2.amazonaws.com' } })],
          }),
        },
      }),
    );
    expect(role).toHaveLength(1);
    const policies = production.findResources('AWS::IAM::Policy', {
      Properties: { Roles: [{ Ref: role[0] }] },
    });
    const actions = Object.values(policies).flatMap(
      (policy: unknown) => JSON.stringify(policy).match(/"[a-z0-9]+:[A-Za-z]+"/g) ?? [],
    );
    expect(actions.sort()).toEqual([
      '"cloudformation:SignalResource"',
      '"dynamodb:GetItem"',
      '"dynamodb:PutItem"',
      '"logs:CreateLogStream"',
      '"logs:PutLogEvents"',
      '"s3:GetObject"',
    ]);
    // AWS Backup's role, in production, is the only one with a managed policy.
    development.resourcePropertiesCountIs(
      'AWS::IAM::Role',
      { ManagedPolicyArns: Match.anyValue() },
      0,
    );
  });

  it('puts an internal load balancer in front, open only to the VPC over HTTPS, logging every request', () => {
    production.hasResourceProperties('AWS::ElasticLoadBalancingV2::LoadBalancer', {
      Scheme: 'internal',
      LoadBalancerAttributes: Match.arrayWith([
        { Key: 'routing.http.drop_invalid_header_fields.enabled', Value: 'true' },
        { Key: 'access_logs.s3.enabled', Value: 'true' },
      ]),
    });
    production.hasResourceProperties('AWS::EC2::SecurityGroup', {
      GroupDescription: 'The load balancer: HTTPS from inside the VPC.',
      SecurityGroupIngress: [Match.objectLike({ CidrIp: VPC_CIDR, FromPort: 443, ToPort: 443 })],
    });
    // No security group lets anything in from outside the VPC.
    const groups = Object.values(production.findResources('AWS::EC2::SecurityGroup')).flatMap(
      (group: unknown) => JSON.stringify(group).match(/"SecurityGroupIngress":\[[^\]]*\]/g) ?? [],
    );
    const rules = Object.values(production.findResources('AWS::EC2::SecurityGroupIngress')).map(
      (rule: unknown) => JSON.stringify(rule),
    );
    for (const rule of [...groups, ...rules]) {
      expect(rule).not.toContain('0.0.0.0/0');
    }
    production.hasResourceProperties('AWS::S3::BucketPolicy', {
      PolicyDocument: {
        Statement: Match.arrayWith([
          Match.objectLike({
            Effect: 'Allow',
            Principal: { Service: 'logdelivery.elasticloadbalancing.amazonaws.com' },
            Action: 's3:PutObject',
          }),
        ]),
      },
    });
  });

  it('serves HTTPS with the CertificateArn parameter, and no plain HTTP', () => {
    production.hasParameter('CertificateArn', { Type: 'String' });
    production.resourceCountIs('AWS::ElasticLoadBalancingV2::Listener', 1);
    production.hasResourceProperties('AWS::ElasticLoadBalancingV2::Listener', {
      Port: 443,
      Protocol: 'HTTPS',
      Certificates: [{ CertificateArn: { Ref: 'CertificateArn' } }],
      SslPolicy: Match.stringLikeRegexp('^ELBSecurityPolicy-TLS13-'),
    });
    production.hasResourceProperties('AWS::ElasticLoadBalancingV2::TargetGroup', {
      Port: SERVER_PORT,
      Protocol: 'HTTP',
      HealthCheckPath: '/health',
      Matcher: { HttpCode: '200' },
    });
  });

  it('alarms on the load balancer, the instances and the table, in either tier', () => {
    for (const template of [production, development]) {
      // The load balancer's own 5xx; the targets' 5xx, latency and unhealthy
      // hosts; the group's CPU and instances in service; the table's read and
      // write throttles and system errors.
      template.resourceCountIs('AWS::CloudWatch::Alarm', 1 + 3 + 2 + 3);
      template.resourceCountIs('AWS::CloudWatch::Dashboard', 1);
      template.hasOutput('AlarmTopicArn', { Value: { Ref: Match.anyValue() } });
      template.hasOutput('EntryPoint', {
        Value: { 'Fn::GetAtt': [Match.anyValue(), 'DNSName'] },
      });
    }
    production.hasResourceProperties('AWS::CloudWatch::Alarm', {
      AlarmName: 'test-Instances-In-Service-Critical',
      MetricName: 'GroupInServiceInstances',
      Threshold: MIN_INSTANCES,
      ComparisonOperator: 'LessThanThreshold',
    });
  });

  it('defaults the alert email to alerts.email', () => {
    synth('development', {}, { email: 'ops@example.com' }).hasParameter('AlertEmail', {
      Default: 'ops@example.com',
    });
  });

  it('backs up its own table daily for retentionDays in production, and nothing in development', () => {
    production.hasResourceProperties('AWS::Backup::BackupPlan', {
      BackupPlan: {
        BackupPlanRule: [Match.objectLike({ Lifecycle: { DeleteAfterDays: 35 }, CopyActions: [] })],
      },
    });
    const [tableId] = onlyOf(production, 'AWS::DynamoDB::Table');
    const [, selection] = onlyOf(production, 'AWS::Backup::BackupSelection');
    // By ARN, never by the Container's tag, which the instances, their
    // volumes and the development stack's table carry too.
    expect(selection['Properties']).toEqual({
      BackupPlanId: expect.anything() as unknown,
      BackupSelection: {
        IamRoleArn: expect.anything() as unknown,
        Resources: [{ 'Fn::GetAtt': [tableId, 'Arn'] }],
        SelectionName: expect.any(String) as unknown,
      },
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

describe('bootCommands', () => {
  const commands = bootCommands('/tmp/server.mjs', 'items', 'logs', 'eu-west-1').join('\n');

  it('runs the server under its own user, with the table and region it needs', () => {
    expect(commands).toContain('User=server');
    expect(commands).toContain('Environment=TABLE_NAME=items');
    expect(commands).toContain('Environment=AWS_REGION=eu-west-1');
    expect(commands).toContain(`Environment=PORT=${String(SERVER_PORT)}`);
    expect(commands).toContain('systemctl enable --now server.service');
  });

  it('restarts the server however often it stops, and rotates its log', () => {
    expect(commands).toContain('StartLimitIntervalSec=0');
    expect(commands).toContain('Restart=always');
    expect(commands).toContain('copytruncate');
  });

  it('signals only once the server answers its health check', () => {
    expect(commands).not.toContain('dnf install');
    expect(commands.trimEnd().split('\n').at(-1)).toMatch(
      new RegExp(
        `^timeout \\d+ bash -c 'until curl -fsS -o /dev/null http://127\\.0\\.0\\.1:${String(SERVER_PORT)}/health; do sleep 5; done'$`,
      ),
    );
  });

  it('ships the server log to its log group', () => {
    expect(commands).toContain('"log_group_name":"logs"');
    expect(commands).toContain('amazon-cloudwatch-agent-ctl -a fetch-config');
  });
});
