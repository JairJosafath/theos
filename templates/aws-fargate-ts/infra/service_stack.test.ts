import { App } from 'aws-cdk-lib';
import { Match, Template } from 'aws-cdk-lib/assertions';
import { beforeAll, describe, expect, it } from 'vitest';

import { copyVaultName } from './backups';
import { ServiceStack } from './service_stack';
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
    new ServiceStack(new App(), 'test', {
      tier,
      operations,
      backupTag: ['c4factory:container', 'test'],
    }),
  );
}

/** The IAM actions the task role's policies allow, sorted. */
function taskRoleActions(template: Template): string[] {
  const policies = template.findResources('AWS::IAM::Policy', {
    Properties: { Roles: [{ Ref: Match.stringLikeRegexp('^TaskTaskRole') }] },
  });
  return Object.values(policies)
    .flatMap((policy: unknown) => JSON.stringify(policy).match(/"[a-z0-9]+:[A-Za-z]+"/g) ?? [])
    .sort();
}

/** The names of the containers in the template's one task definition, in order. */
function containerNames(template: Template): unknown[] {
  const [definition, ...others]: unknown[] = Object.values(
    template.findResources('AWS::ECS::TaskDefinition'),
  );
  expect(others).toEqual([]);
  const containers = (definition as { Properties?: { ContainerDefinitions?: unknown } } | undefined)
    ?.Properties?.ContainerDefinitions;
  return Array.isArray(containers)
    ? containers.map((container: unknown) => (container as { Name?: unknown }).Name)
    : [];
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
    development.allResources('AWS::DynamoDB::Table', {
      DeletionPolicy: 'Delete',
      Properties: {
        PointInTimeRecoverySpecification: { PointInTimeRecoveryEnabled: false },
        DeletionProtectionEnabled: false,
      },
    });
  });

  it('runs the image on arm64 Fargate, read-only, logging to its own group', () => {
    production.hasResourceProperties('AWS::ECS::TaskDefinition', {
      RequiresCompatibilities: ['FARGATE'],
      Cpu: '256',
      Memory: '512',
      RuntimePlatform: { CpuArchitecture: 'ARM64', OperatingSystemFamily: 'LINUX' },
      ContainerDefinitions: Match.arrayWith([
        Match.objectLike({
          Name: 'App',
          Essential: true,
          PortMappings: [Match.objectLike({ ContainerPort: 8080 })],
          ReadonlyRootFilesystem: true,
          LogConfiguration: Match.objectLike({ LogDriver: 'awslogs' }),
        }),
      ]),
    });
  });

  it('traces through an ADOT collector beside the app in production', () => {
    expect(containerNames(production)).toEqual(['App', 'Collector']);
    production.hasResourceProperties('AWS::ECS::TaskDefinition', {
      ContainerDefinitions: [
        Match.objectLike({
          Name: 'App',
          DependsOn: [{ ContainerName: 'Collector', Condition: 'START' }],
          Environment: [
            { Name: 'TABLE_NAME', Value: Match.anyValue() },
            { Name: 'OTEL_EXPORTER_OTLP_ENDPOINT', Value: 'http://localhost:4318' },
            { Name: 'OTEL_SERVICE_NAME', Value: 'test' },
          ],
        }),
        Match.objectLike({
          Name: 'Collector',
          Image: Match.stringLikeRegexp(
            '^public\\.ecr\\.aws/aws-observability/aws-otel-collector:v\\d+\\.\\d+\\.\\d+$',
          ),
          Command: ['--config=/etc/ecs/ecs-xray.yaml'],
          Essential: false,
          ReadonlyRootFilesystem: true,
          LogConfiguration: Match.objectLike({ LogDriver: 'awslogs' }),
        }),
      ],
    });
  });

  it('runs the app alone, untraced, in development', () => {
    expect(containerNames(development)).toEqual(['App']);
    development.hasResourceProperties('AWS::ECS::TaskDefinition', {
      ContainerDefinitions: [
        Match.objectLike({
          Environment: [{ Name: 'TABLE_NAME', Value: Match.anyValue() }],
          DependsOn: Match.absent(),
        }),
      ],
    });
  });

  it('lets the tasks only get and put items, and write traces in production', () => {
    const own = ['"dynamodb:GetItem"', '"dynamodb:PutItem"'];
    expect(taskRoleActions(development)).toEqual(own);
    expect(taskRoleActions(production)).toEqual(
      [...own, '"xray:PutTelemetryRecords"', '"xray:PutTraceSegments"'].sort(),
    );
    // No role carries a managed policy, but AWS Backup's in production.
    production.resourcePropertiesCountIs(
      'AWS::IAM::Role',
      { ManagedPolicyArns: Match.anyValue() },
      1,
    );
    development.resourcePropertiesCountIs(
      'AWS::IAM::Role',
      { ManagedPolicyArns: Match.anyValue() },
      0,
    );
  });

  it('keeps two tasks in private subnets, rolling a failed deployment back', () => {
    for (const template of [production, development]) {
      template.hasResourceProperties('AWS::ECS::Service', {
        LaunchType: 'FARGATE',
        DesiredCount: 2,
        DeploymentConfiguration: Match.objectLike({
          MinimumHealthyPercent: 100,
          DeploymentCircuitBreaker: { Enable: true, Rollback: true },
        }),
        NetworkConfiguration: {
          AwsvpcConfiguration: Match.objectLike({ AssignPublicIp: 'DISABLED' }),
        },
      });
      template.hasResourceProperties('AWS::ApplicationAutoScaling::ScalableTarget', {
        MinCapacity: 2,
        MaxCapacity: 4,
      });
    }
  });

  it('rolls a deployment back on a 5xx or an unhealthy task, naming alarms the stack has', () => {
    const names = [
      'test-LoadBalancer-5xx-Critical',
      'test-Service-5xx-Critical',
      'test-Service-Unhealthy-Tasks-Critical',
    ];
    for (const template of [production, development]) {
      template.hasResourceProperties('AWS::ECS::Service', {
        DeploymentConfiguration: Match.objectLike({
          Alarms: { AlarmNames: names, Enable: true, Rollback: true },
        }),
      });
      for (const name of names) {
        template.hasResourceProperties('AWS::CloudWatch::Alarm', { AlarmName: name });
      }
    }
  });

  it('serves HTTPS with the deployer’s certificate, and redirects HTTP to it', () => {
    production.hasParameter('CertificateArn', { Type: 'String' });
    production.hasResourceProperties('AWS::ElasticLoadBalancingV2::Listener', {
      Port: 443,
      Protocol: 'HTTPS',
      Certificates: [{ CertificateArn: { Ref: 'CertificateArn' } }],
      SslPolicy: Match.stringLikeRegexp('^ELBSecurityPolicy-TLS13'),
    });
    production.hasResourceProperties('AWS::ElasticLoadBalancingV2::Listener', {
      Port: 80,
      Protocol: 'HTTP',
      DefaultActions: [
        Match.objectLike({
          Type: 'redirect',
          RedirectConfig: Match.objectLike({ Protocol: 'HTTPS', Port: '443' }),
        }),
      ],
    });
  });

  it('checks the tasks’ health on /health', () => {
    production.hasResourceProperties('AWS::ElasticLoadBalancingV2::TargetGroup', {
      Port: 8080,
      TargetType: 'ip',
      HealthCheckPath: '/health',
    });
  });

  it('logs every request the load balancer takes, and drops invalid headers', () => {
    for (const attribute of [
      { Key: 'access_logs.s3.enabled', Value: 'true' },
      { Key: 'routing.http.drop_invalid_header_fields.enabled', Value: 'true' },
    ]) {
      production.hasResourceProperties('AWS::ElasticLoadBalancingV2::LoadBalancer', {
        Scheme: 'internet-facing',
        LoadBalancerAttributes: Match.arrayWith([attribute]),
      });
    }
    production.hasResourceProperties('AWS::S3::BucketPolicy', {
      PolicyDocument: {
        Statement: Match.arrayWith([
          Match.objectLike({
            Effect: 'Allow',
            Action: 's3:PutObject',
            Principal: { Service: 'logdelivery.elasticloadbalancing.amazonaws.com' },
          }),
        ]),
      },
    });
  });

  it('expires the access logs after the tier’s log retention, keeping the bucket in either tier', () => {
    // Logs, not data; a bucket holding objects can't be deleted, so it stays
    // and empties itself.
    for (const [template, days] of [
      [production, 365],
      [development, 30],
    ] as const) {
      template.hasResource('AWS::S3::Bucket', {
        DeletionPolicy: 'Retain',
        Properties: {
          LifecycleConfiguration: {
            Rules: [Match.objectLike({ ExpirationInDays: days, Status: 'Enabled' })],
          },
        },
      });
    }
  });

  it('keeps DynamoDB traffic in the VPC and logs rejected traffic', () => {
    production.hasResourceProperties('AWS::EC2::VPCEndpoint', {
      VpcEndpointType: 'Gateway',
      ServiceName: Match.anyValue(),
    });
    production.hasResourceProperties('AWS::EC2::FlowLog', { TrafficType: 'REJECT' });
  });

  it('has a NAT gateway in each zone in production, and one in development', () => {
    production.resourceCountIs('AWS::EC2::NatGateway', 2);
    development.resourceCountIs('AWS::EC2::NatGateway', 1);
  });

  it('alarms on the load balancer, the service and the table, in either tier', () => {
    for (const template of [production, development]) {
      // The load balancer's 5xx; the tasks' 5xx, latency, unhealthy tasks,
      // CPU, memory and running tasks; the table's read and write throttles
      // and system errors.
      template.resourceCountIs('AWS::CloudWatch::Alarm', 1 + 6 + 3);
      template.resourceCountIs('AWS::CloudWatch::Dashboard', 1);
      template.hasOutput('AlarmTopicArn', { Value: { Ref: Match.anyValue() } });
      template.hasOutput('EntryPoint', {
        Value: { 'Fn::GetAtt': [Match.stringLikeRegexp('^LoadBalancer'), 'DNSName'] },
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
    production.hasResourceProperties('AWS::Backup::BackupSelection', {
      BackupSelection: Match.objectLike({
        ListOfTags: [
          {
            ConditionKey: 'c4factory:container',
            ConditionType: 'STRINGEQUALS',
            ConditionValue: 'test',
          },
        ],
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
