import { App } from 'aws-cdk-lib';
import { Match, Template } from 'aws-cdk-lib/assertions';
import { AttributeType, Table } from 'aws-cdk-lib/aws-dynamodb';
import { SubnetType } from 'aws-cdk-lib/aws-ec2';
import {
  AuroraPostgresEngineVersion,
  ClusterInstance,
  DatabaseCluster,
  DatabaseClusterEngine,
} from 'aws-cdk-lib/aws-rds';
import { beforeAll, describe, expect, it } from 'vitest';

import { copyVaultName } from './backups';
import { DATA_STORES } from './connectors';
import type { Connect } from './connectors';
import { ServiceStack } from './service_stack';
import type { Operations, Tier } from './standard';

const OPERATIONS: Operations = {
  backups: { retentionDays: 35, copyToRegion: '' },
  recovery: { rpo: '1h', rto: '4h' },
  alerts: { email: '' },
};

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
    new ServiceStack(new App(), 'test', {
      tier,
      operations,
      dataStores,
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

/** The one alarm named `name` in `template`, as its properties. */
function alarmNamed(template: Template, name: string): Record<string, unknown> {
  const found = Object.values(
    template.findResources('AWS::CloudWatch::Alarm', { Properties: { AlarmName: name } }),
  );
  expect(found, name).toHaveLength(1);
  return (found[0] as { Properties: Record<string, unknown> }).Properties;
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

  it('makes a user pool the deployer fills, kept in production, with a strong password policy', () => {
    production.resourceCountIs('AWS::Cognito::UserPool', 1);
    production.hasResource('AWS::Cognito::UserPool', {
      DeletionPolicy: 'Retain',
      Properties: Match.objectLike({
        AdminCreateUserConfig: { AllowAdminCreateUserOnly: true },
        UsernameAttributes: ['email'],
        DeletionProtection: 'ACTIVE',
        Policies: {
          PasswordPolicy: Match.objectLike({
            MinimumLength: 12,
            RequireLowercase: true,
            RequireUppercase: true,
            RequireNumbers: true,
            RequireSymbols: true,
          }),
        },
      }),
    });
    development.hasResource('AWS::Cognito::UserPool', {
      DeletionPolicy: 'Delete',
      Properties: Match.objectLike({ DeletionProtection: 'INACTIVE' }),
    });
  });

  it('signs callers in through one public app client, with no OAuth', () => {
    production.resourceCountIs('AWS::Cognito::UserPoolClient', 1);
    production.hasResourceProperties('AWS::Cognito::UserPoolClient', {
      GenerateSecret: false,
      ExplicitAuthFlows: Match.arrayWith(['ALLOW_USER_PASSWORD_AUTH', 'ALLOW_USER_SRP_AUTH']),
      PreventUserExistenceErrors: 'ENABLED',
      AllowedOAuthFlows: Match.absent(),
    });
  });

  it('outputs the user pool, its client and the issuer', () => {
    const outputs = Object.keys(production.findOutputs('*')).join(' ');
    for (const name of ['UserPoolId', 'UserPoolClientId', 'Issuer']) {
      expect(outputs).toContain(name);
    }
  });

  it('runs the image on arm64 Fargate, read-only, logging to its own group', () => {
    production.hasResourceProperties('AWS::ECS::TaskDefinition', {
      RequiresCompatibilities: ['FARGATE'],
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

  it('gives the app the same CPU and memory in either tier, adding the collector’s to the task', () => {
    // The app alone in development; in production the collector's 256 CPU
    // units and 512 MiB, its memory a hard limit, on top of the app's.
    const app = Match.objectLike({ Name: 'App', Cpu: 256, MemoryReservation: 512 });
    development.hasResourceProperties('AWS::ECS::TaskDefinition', {
      Cpu: '256',
      Memory: '512',
      ContainerDefinitions: [app],
    });
    production.hasResourceProperties('AWS::ECS::TaskDefinition', {
      Cpu: '512',
      Memory: '1024',
      ContainerDefinitions: [app, Match.objectLike({ Name: 'Collector', Cpu: 256, Memory: 512 })],
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
            { Name: 'USER_POOL_ID', Value: { Ref: Match.stringLikeRegexp('^UsersPool') } },
            {
              Name: 'USER_POOL_CLIENT_ID',
              Value: { Ref: Match.stringLikeRegexp('^UsersPoolClient') },
            },
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
          Environment: [
            { Name: 'TABLE_NAME', Value: Match.anyValue() },
            { Name: 'USER_POOL_ID', Value: { Ref: Match.stringLikeRegexp('^UsersPool') } },
            {
              Name: 'USER_POOL_CLIENT_ID',
              Value: { Ref: Match.stringLikeRegexp('^UsersPoolClient') },
            },
          ],
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

  it('rolls a deployment back on a share of 5xx or an unhealthy task, naming alarms the stack has', () => {
    const names = [
      'test-LoadBalancer-5xx-Rate-Critical',
      'test-Service-5xx-Rate-Critical',
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

  it('rolls back on over 5 % of answers 5xx for three minutes in a row, never on a stray one', () => {
    for (const template of [production, development]) {
      for (const [name, expression, metric] of [
        [
          'test-LoadBalancer-5xx-Rate-Critical',
          '100 * errors / (requests + errors)',
          'HTTPCode_ELB_5XX_Count',
        ],
        ['test-Service-5xx-Rate-Critical', '100 * errors / requests', 'HTTPCode_Target_5XX_Count'],
      ] as const) {
        const alarm = alarmNamed(template, name);
        expect(alarm, name).toMatchObject({
          ComparisonOperator: 'GreaterThanThreshold',
          Threshold: 5,
          EvaluationPeriods: 3,
          DatapointsToAlarm: 3,
          TreatMissingData: 'notBreaching',
        });
        const metrics = JSON.stringify(alarm['Metrics']);
        expect(metrics, name).toContain(`"Expression":"${expression}"`);
        expect(metrics, name).toContain(`"MetricName":"${metric}"`);
        expect(metrics, name).toContain('"MetricName":"RequestCount"');
        expect(metrics, name).toContain('"Period":60');
        expect(metrics, name).not.toContain('"Period":300');
      }
      // Any 5xx at all only notifies.
      for (const name of ['test-LoadBalancer-5xx-Warning', 'test-Service-5xx-Warning']) {
        expect(alarmNamed(template, name), name).toMatchObject({
          Threshold: 0,
          Period: 300,
          Statistic: 'Sum',
        });
      }
    }
  });

  it('finds an unhealthy task by the Maximum of one-minute periods, two of three', () => {
    for (const template of [production, development]) {
      const alarm = alarmNamed(template, 'test-Service-Unhealthy-Tasks-Critical');
      expect(alarm).toMatchObject({
        Threshold: 0,
        ComparisonOperator: 'GreaterThanThreshold',
        DatapointsToAlarm: 2,
        EvaluationPeriods: 3,
      });
      expect(alarm['Metrics']).toMatchObject([
        {
          MetricStat: { Metric: { MetricName: 'UnHealthyHostCount' }, Stat: 'Maximum', Period: 60 },
        },
      ]);
    }
  });

  it('counts no data on running tasks as too few', () => {
    for (const template of [production, development]) {
      expect(alarmNamed(template, 'test-Service-Running-Tasks-Critical')).toMatchObject({
        MetricName: 'RunningTaskCount',
        Threshold: 2,
        ComparisonOperator: 'LessThanThreshold',
        TreatMissingData: 'breaching',
      });
    }
  });

  it('runs the release’s image by digest, pulled from the bootstrap’s repository only', () => {
    production.hasParameter('ImageUri', {
      Type: 'String',
      AllowedPattern: Match.stringLikeRegexp('@sha256:'),
    });
    production.hasResourceProperties('AWS::ECS::TaskDefinition', {
      ContainerDefinitions: Match.arrayWith([
        Match.objectLike({ Name: 'App', Image: { Ref: 'ImageUri' } }),
      ]),
    });
    production.hasResourceProperties('AWS::IAM::Policy', {
      Roles: [{ Ref: Match.stringLikeRegexp('^TaskExecutionRole') }],
      PolicyDocument: {
        Statement: Match.arrayWith([
          Match.objectLike({
            Action: [
              'ecr:BatchCheckLayerAvailability',
              'ecr:BatchGetImage',
              'ecr:GetDownloadUrlForLayer',
            ],
            Resource: {
              'Fn::Join': [
                '',
                Match.arrayWith([
                  Match.stringLikeRegexp(':repository/cdk-hnb659fds-container-assets-'),
                ]),
              ],
            },
          }),
        ]),
      },
    });
  });

  it('pulls from the repository of the bootstrap qualifier the deployer copies to', () => {
    const app = new App({ context: { '@aws-cdk/core:bootstrapQualifier': 'myq' } });
    const template = Template.fromStack(
      new ServiceStack(app, 'test', { tier: 'production', operations: OPERATIONS }),
    );
    template.hasResourceProperties('AWS::IAM::Policy', {
      PolicyDocument: {
        Statement: Match.arrayWith([
          Match.objectLike({
            Resource: {
              'Fn::Join': [
                '',
                Match.arrayWith([Match.stringLikeRegexp(':repository/cdk-myq-container-assets-')]),
              ],
            },
          }),
        ]),
      },
    });
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
      // The load balancer's 5xx and their share; the tasks' 5xx and their
      // share, latency, unhealthy tasks, CPU, memory and running tasks; the
      // table's read and write throttles and system errors.
      template.resourceCountIs('AWS::CloudWatch::Alarm', 2 + 7 + 3);
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

  it('leaves the load balancer’s access logs out of the backups: logs, not data', () => {
    const [balancer, ...others]: unknown[] = Object.values(
      production.findResources('AWS::ElasticLoadBalancingV2::LoadBalancer'),
    );
    expect(others).toEqual([]);
    const target = /"Key":"access_logs\.s3\.bucket","Value":\{"Ref":"([^"]+)"\}/.exec(
      JSON.stringify(balancer),
    )?.[1];
    expect(target).toBeDefined();
    // The selection names the stack's table and nothing else (#1267).
    expect(JSON.stringify(production.findResources('AWS::Backup::BackupSelection'))).not.toContain(
      `"${String(target)}"`,
    );
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

describe('ServiceStack data stores', () => {
  it("connects the tasks to each Connector's data store and alarms on its table", () => {
    const connected: string[] = [];
    const connect: Connect = (scope, user, rules) => {
      const store = new Table(scope, 'Store', {
        partitionKey: { name: 'pk', type: AttributeType.STRING },
        deletionProtection: rules.keepData,
      });
      store.grant(user, 'dynamodb:Query');
      user.addEnvironment('STORE_TABLE_NAME', store.tableName);
      connected.push(scope.node.id);
      return store;
    };
    const template = synth('production', {}, {}, [['Store', connect]]);
    // Connected once, in the stack; the task role is what the table grants
    // to, and the app's environment is what gets the table's name.
    expect(connected).toEqual(['test']);
    expect(taskRoleActions(template)).toContain('"dynamodb:Query"');
    template.hasResourceProperties('AWS::ECS::TaskDefinition', {
      ContainerDefinitions: Match.arrayWith([
        Match.objectLike({
          Name: 'App',
          Environment: Match.arrayWith([
            {
              Name: 'STORE_TABLE_NAME',
              Value: { Ref: Match.stringLikeRegexp('^Store') },
            },
          ]),
        }),
      ]),
    });
    template.resourceCountIs('AWS::DynamoDB::Table', 2);
    template.resourceCountIs('AWS::CloudWatch::Alarm', 2 + 7 + 3 * 2);
    // Its table is data: backed up with the stack's own (#1267).
    const tables = Object.keys(template.findResources('AWS::DynamoDB::Table'));
    const backedUp = ['Items', 'Store'].map((name) => tables.find((id) => id.startsWith(name)));
    template.hasResourceProperties('AWS::Backup::BackupSelection', {
      BackupSelection: Match.objectLike({
        Resources: backedUp.map((id) => ({ 'Fn::GetAtt': [id, 'Arn'] })),
      }),
    });
  });

  it('puts a cluster store in the VPC, lets the tasks in, alarms on it and backs it up by ARN', () => {
    // A stand-in for what the rds Connector builds with a network: a
    // Serverless v2 cluster that scales to 8 ACU, in the stack's VPC, that
    // lets the tasks' security group in.
    const connect: Connect = (scope, user) => {
      const network = user.network;
      if (network === undefined) {
        throw new Error('the stack passes no network');
      }
      const cluster = new DatabaseCluster(scope, 'Store', {
        engine: DatabaseClusterEngine.auroraPostgres({
          version: AuroraPostgresEngineVersion.VER_16_6,
        }),
        writer: ClusterInstance.serverlessV2('Writer'),
        serverlessV2MaxCapacity: 8,
        vpc: network.vpc,
        vpcSubnets: { subnetType: SubnetType.PRIVATE_WITH_EGRESS },
      });
      cluster.connections.allowDefaultPortFrom(network.connections, 'The Container');
      return cluster;
    };
    const template = synth('production', {}, {}, [['Store', connect]]);
    template.resourceCountIs('AWS::EC2::VPC', 1);
    template.resourceCountIs('AWS::RDS::DBCluster', 1);
    const [clusterId] = Object.keys(template.findResources('AWS::RDS::DBCluster'));
    // The tasks run in the security group the cluster lets in.
    const [serviceGroup] = Object.keys(
      template.findResources('AWS::EC2::SecurityGroup', {
        Properties: { GroupDescription: 'The service tasks: the load balancer reaches them.' },
      }),
    );
    template.hasResourceProperties('AWS::ECS::Service', {
      NetworkConfiguration: {
        AwsvpcConfiguration: Match.objectLike({
          SecurityGroups: [{ 'Fn::GetAtt': [serviceGroup, 'GroupId'] }],
        }),
      },
    });
    template.hasResourceProperties('AWS::EC2::SecurityGroupIngress', {
      SourceSecurityGroupId: { 'Fn::GetAtt': [serviceGroup, 'GroupId'] },
      GroupId: { 'Fn::GetAtt': [Match.stringLikeRegexp('^StoreSecurityGroup'), 'GroupId'] },
    });
    // The load balancer's, the service's, the table's, and the cluster's two.
    template.resourceCountIs('AWS::CloudWatch::Alarm', 2 + 7 + 3 + 2);
    const connections = alarmNamed(template, 'test-Store-Connections-Critical');
    // The Aurora User Guide's 1,669 at 8 ACU, nine tenths of it.
    expect(connections['Threshold']).toBe(Math.floor(1669 * 0.9));
    expect(alarmNamed(template, 'test-Store-CPU-Usage-Critical')['Threshold']).toBe(90);
    // A deployment that runs the cluster out of connections rolls back.
    template.hasResourceProperties('AWS::ECS::Service', {
      DeploymentConfiguration: Match.objectLike({
        Alarms: Match.objectLike({
          AlarmNames: Match.arrayWith(['test-Store-Connections-Critical']),
        }),
      }),
    });
    // Backed up with the stack's table, after it (#1923).
    const [itemsId] = Object.keys(template.findResources('AWS::DynamoDB::Table'));
    template.hasResourceProperties('AWS::Backup::BackupSelection', {
      BackupSelection: Match.objectLike({
        Resources: [
          { 'Fn::GetAtt': [itemsId, 'Arn'] },
          { 'Fn::GetAtt': [clusterId, 'DBClusterArn'] },
        ],
      }),
    });
  });

  it('connects the data stores of the rendered infra/connectors.ts by default', () => {
    const template = Template.fromStack(
      new ServiceStack(new App(), 'test', {
        tier: 'production',
        operations: OPERATIONS,
      }),
    );
    // One table or one cluster per store, beside the stack's own table.
    const tables = Object.keys(template.findResources('AWS::DynamoDB::Table')).length;
    const clusters = Object.keys(template.findResources('AWS::RDS::DBCluster')).length;
    expect(tables - 1 + clusters).toBe(DATA_STORES.length);
  });

  it.runIf(DATA_STORES.some(([name]) => name === 'RDS'))(
    'puts the rds cluster in the VPC, lets the tasks in and hands them its host and port',
    () => {
      const template = Template.fromStack(
        new ServiceStack(new App(), 'test', {
          tier: 'production',
          operations: OPERATIONS,
        }),
      );
      // No second VPC: the cluster goes in the stack's own.
      template.resourceCountIs('AWS::EC2::VPC', 1);
      template.hasResourceProperties('AWS::EC2::SecurityGroupIngress', {
        SourceSecurityGroupId: {
          'Fn::GetAtt': [Match.stringLikeRegexp('^ServiceSecurityGroup'), 'GroupId'],
        },
        GroupId: { 'Fn::GetAtt': [Match.stringLikeRegexp('^RDSSecurityGroup'), 'GroupId'] },
      });
      template.hasResourceProperties('AWS::ECS::TaskDefinition', {
        ContainerDefinitions: Match.arrayWith([
          Match.objectLike({
            Name: 'App',
            Environment: Match.arrayWith([
              Match.objectLike({ Name: 'RDS_HOST' }),
              Match.objectLike({ Name: 'RDS_PORT' }),
            ]),
          }),
        ]),
      });
    },
  );
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
