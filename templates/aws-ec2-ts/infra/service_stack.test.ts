import { App } from 'aws-cdk-lib';
import { Match, Template } from 'aws-cdk-lib/assertions';
import { beforeAll, describe, expect, it } from 'vitest';

import { bootCommands, SERVER_PORT, ServiceStack, VPC_CIDR } from './service_stack';

let template: Template;

beforeAll(() => {
  template = Template.fromStack(new ServiceStack(new App(), 'test'));
});

describe('ServiceStack', () => {
  it('keeps items in an on-demand table with point-in-time recovery, kept when the stack goes', () => {
    template.resourceCountIs('AWS::DynamoDB::Table', 1);
    template.hasResource('AWS::DynamoDB::Table', {
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

  it('builds a network with no way out but the AWS endpoints it needs', () => {
    template.hasResourceProperties('AWS::EC2::VPC', { CidrBlock: VPC_CIDR });
    template.resourceCountIs('AWS::EC2::NatGateway', 0);
    template.resourceCountIs('AWS::EC2::InternetGateway', 0);
    const endpoints = Object.values(template.findResources('AWS::EC2::VPCEndpoint')).map(
      (endpoint: unknown) =>
        JSON.stringify(endpoint).match(/com\.amazonaws[^"]*|\.(s3|dynamodb|logs)"/g),
    );
    expect(endpoints.flat().join(' ')).toMatch(/s3/);
    expect(endpoints.flat().join(' ')).toMatch(/dynamodb/);
    expect(endpoints.flat().join(' ')).toMatch(/logs/);
    template.resourceCountIs('AWS::EC2::VPCEndpoint', 3);
    template.hasResourceProperties('AWS::EC2::FlowLog', { TrafficType: 'REJECT' });
  });

  it('runs two to four Graviton instances with IMDSv2, encrypted disks and detailed monitoring', () => {
    template.hasResourceProperties('AWS::AutoScaling::AutoScalingGroup', {
      MinSize: '2',
      MaxSize: '4',
      HealthCheckType: 'ELB',
    });
    template.hasResourceProperties('AWS::EC2::LaunchTemplate', {
      LaunchTemplateData: Match.objectLike({
        InstanceType: 't4g.small',
        MetadataOptions: { HttpTokens: 'required' },
        Monitoring: { Enabled: true },
        BlockDeviceMappings: [
          Match.objectLike({ Ebs: Match.objectLike({ Encrypted: true, VolumeType: 'gp3' }) }),
        ],
      }),
    });
  });

  it('replaces one instance at a time', () => {
    template.hasResource('AWS::AutoScaling::AutoScalingGroup', {
      UpdatePolicy: {
        AutoScalingRollingUpdate: Match.objectLike({ MaxBatchSize: 1, MinInstancesInService: 1 }),
      },
    });
  });

  it('lets the instances only get and put items, read their code and write their logs', () => {
    const role = Object.keys(
      template.findResources('AWS::IAM::Role', {
        Properties: {
          AssumeRolePolicyDocument: Match.objectLike({
            Statement: [Match.objectLike({ Principal: { Service: 'ec2.amazonaws.com' } })],
          }),
        },
      }),
    );
    expect(role).toHaveLength(1);
    const policies = template.findResources('AWS::IAM::Policy', {
      Properties: { Roles: [{ Ref: role[0] }] },
    });
    const actions = Object.values(policies).flatMap(
      (policy: unknown) => JSON.stringify(policy).match(/"[a-z0-9]+:[A-Za-z]+"/g) ?? [],
    );
    expect(actions.sort()).toEqual([
      '"dynamodb:GetItem"',
      '"dynamodb:PutItem"',
      '"logs:CreateLogStream"',
      '"logs:PutLogEvents"',
      '"s3:GetObject"',
    ]);
    template.resourcePropertiesCountIs(
      'AWS::IAM::Role',
      { ManagedPolicyArns: Match.anyValue() },
      0,
    );
  });

  it('puts an internal load balancer in front, open only to the VPC, logging every request', () => {
    template.hasResourceProperties('AWS::ElasticLoadBalancingV2::LoadBalancer', {
      Scheme: 'internal',
      LoadBalancerAttributes: Match.arrayWith([
        { Key: 'routing.http.drop_invalid_header_fields.enabled', Value: 'true' },
        { Key: 'access_logs.s3.enabled', Value: 'true' },
      ]),
    });
    template.hasResourceProperties('AWS::EC2::SecurityGroup', {
      SecurityGroupIngress: [Match.objectLike({ CidrIp: VPC_CIDR, FromPort: 80, ToPort: 80 })],
    });
    // No security group lets anything in from outside the VPC.
    const groups = Object.values(template.findResources('AWS::EC2::SecurityGroup')).flatMap(
      (group: unknown) => JSON.stringify(group).match(/"SecurityGroupIngress":\[[^\]]*\]/g) ?? [],
    );
    const rules = Object.values(template.findResources('AWS::EC2::SecurityGroupIngress')).map(
      (rule: unknown) => JSON.stringify(rule),
    );
    for (const rule of [...groups, ...rules]) {
      expect(rule).not.toContain('0.0.0.0/0');
    }
    template.hasResourceProperties('AWS::S3::BucketPolicy', {
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

  it('sends traffic to the server port and checks /health', () => {
    template.hasResourceProperties('AWS::ElasticLoadBalancingV2::Listener', {
      Port: 80,
      Protocol: 'HTTP',
    });
    template.hasResourceProperties('AWS::ElasticLoadBalancingV2::TargetGroup', {
      Port: SERVER_PORT,
      Protocol: 'HTTP',
      HealthCheckPath: '/health',
      Matcher: { HttpCode: '200' },
    });
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

  it('ships the server log to its log group', () => {
    expect(commands).toContain('"log_group_name":"logs"');
    expect(commands).toContain('amazon-cloudwatch-agent-ctl -a fetch-config');
  });
});
