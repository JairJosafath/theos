import { App } from 'aws-cdk-lib';
import { Match, Template } from 'aws-cdk-lib/assertions';
import { beforeAll, describe, expect, it } from 'vitest';

import { ServiceStack } from './service_stack';

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

  it('runs the image on arm64 Fargate, read-only, logging to its own group', () => {
    template.hasResourceProperties('AWS::ECS::TaskDefinition', {
      RequiresCompatibilities: ['FARGATE'],
      Cpu: '256',
      Memory: '512',
      RuntimePlatform: { CpuArchitecture: 'ARM64', OperatingSystemFamily: 'LINUX' },
      ContainerDefinitions: [
        Match.objectLike({
          PortMappings: [Match.objectLike({ ContainerPort: 8080 })],
          ReadonlyRootFilesystem: true,
          Environment: [{ Name: 'TABLE_NAME', Value: Match.anyValue() }],
          LogConfiguration: Match.objectLike({ LogDriver: 'awslogs' }),
        }),
      ],
    });
  });

  it('keeps two tasks in private subnets, rolling a failed deployment back', () => {
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
  });

  it('lets the tasks only get and put items', () => {
    const policies = template.findResources('AWS::IAM::Policy', {
      Properties: { Roles: [{ Ref: Match.stringLikeRegexp('^TaskTaskRole') }] },
    });
    const actions = Object.values(policies).flatMap(
      (policy: unknown) => JSON.stringify(policy).match(/"[a-z0-9]+:[A-Za-z]+"/g) ?? [],
    );
    expect(actions.sort()).toEqual(['"dynamodb:GetItem"', '"dynamodb:PutItem"']);
    template.resourcePropertiesCountIs(
      'AWS::IAM::Role',
      { ManagedPolicyArns: Match.anyValue() },
      0,
    );
  });

  it('serves HTTPS with the deployer’s certificate, and redirects HTTP to it', () => {
    template.hasParameter('CertificateArn', { Type: 'String' });
    template.hasResourceProperties('AWS::ElasticLoadBalancingV2::Listener', {
      Port: 443,
      Protocol: 'HTTPS',
      Certificates: [{ CertificateArn: { Ref: 'CertificateArn' } }],
      SslPolicy: Match.stringLikeRegexp('^ELBSecurityPolicy-TLS13'),
    });
    template.hasResourceProperties('AWS::ElasticLoadBalancingV2::Listener', {
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
    template.hasResourceProperties('AWS::ElasticLoadBalancingV2::TargetGroup', {
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
      template.hasResourceProperties('AWS::ElasticLoadBalancingV2::LoadBalancer', {
        Scheme: 'internet-facing',
        LoadBalancerAttributes: Match.arrayWith([attribute]),
      });
    }
    template.hasResourceProperties('AWS::S3::BucketPolicy', {
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

  it('keeps DynamoDB traffic in the VPC and logs rejected traffic', () => {
    template.hasResourceProperties('AWS::EC2::VPCEndpoint', {
      VpcEndpointType: 'Gateway',
      ServiceName: Match.anyValue(),
    });
    template.hasResourceProperties('AWS::EC2::FlowLog', { TrafficType: 'REJECT' });
  });
});
