import { App, Stack } from 'aws-cdk-lib';
import type { CfnElement } from 'aws-cdk-lib';
import { Match, Template } from 'aws-cdk-lib/assertions';
import { SecurityGroup, Vpc } from 'aws-cdk-lib/aws-ec2';
import type { IVpc } from 'aws-cdk-lib/aws-ec2';
import { Role, ServicePrincipal } from 'aws-cdk-lib/aws-iam';
import { Code, Function as LambdaFunction, Runtime } from 'aws-cdk-lib/aws-lambda';
import { describe, expect, it } from 'vitest';

import {
  AUTO_PAUSE,
  BACKUP_RETENTION_DAYS,
  DEVELOPMENT_CAPACITY,
  PRODUCTION_CAPACITY,
  RDS_CLUSTER_ARN,
  RDS_DATABASE,
  RDS_DATABASE_NAME,
  RDS_HOST,
  RDS_PORT,
  RDS_SECRET_ARN,
  connectRDS,
} from './connector_rds';
import type { DataStoreUser } from './connector_rds';
import { TIER_RULES } from './standard';
import type { Tier } from './standard';

/** The calls the Data API takes, and the secret read that goes with them. */
const DATA_API_ACTIONS = [
  'rds-data:BatchExecuteStatement',
  'rds-data:BeginTransaction',
  'rds-data:CommitTransaction',
  'rds-data:ExecuteStatement',
  'rds-data:RollbackTransaction',
  'secretsmanager:DescribeSecret',
  'secretsmanager:GetSecretValue',
];

/** A function in `stack`, the cluster's user. */
function probeFunction(stack: Stack): LambdaFunction {
  return new LambdaFunction(stack, 'Fn', {
    runtime: Runtime.NODEJS_24_X,
    handler: 'index.handler',
    code: Code.fromInline('export const handler = async () => {};'),
  });
}

/** A stack holding one function connected to the cluster, for `tier`. */
function synth(tier: Tier): Template {
  const stack = new Stack(new App(), 'test');
  connectRDS(stack, probeFunction(stack), TIER_RULES[tier]);
  return Template.fromStack(stack);
}

describe('connectRDS', () => {
  it('keeps an encrypted Aurora Serverless v2 PostgreSQL cluster with a reader in production', () => {
    const template = synth('production');
    template.resourceCountIs('AWS::RDS::DBCluster', 1);
    template.hasResource('AWS::RDS::DBCluster', {
      DeletionPolicy: 'Snapshot',
      UpdateReplacePolicy: 'Snapshot',
      Properties: {
        Engine: 'aurora-postgresql',
        EngineVersion: Match.stringLikeRegexp('^16\\.'),
        DatabaseName: RDS_DATABASE_NAME,
        DeletionProtection: true,
        BackupRetentionPeriod: BACKUP_RETENTION_DAYS,
        EnableHttpEndpoint: true,
        EnableIAMDatabaseAuthentication: true,
        StorageEncrypted: true,
        KmsKeyId: { 'Fn::GetAtt': [Match.stringLikeRegexp('^RDSKey'), 'Arn'] },
        ServerlessV2ScalingConfiguration: {
          MinCapacity: PRODUCTION_CAPACITY.min,
          MaxCapacity: PRODUCTION_CAPACITY.max,
        },
      },
    });
    template.resourceCountIs('AWS::RDS::DBInstance', 2);
    template.hasResourceProperties('AWS::RDS::DBInstance', { PromotionTier: 0 });
    template.hasResource('AWS::KMS::Key', {
      DeletionPolicy: 'Retain',
      Properties: { EnableKeyRotation: true },
    });
    template.hasResource('AWS::SecretsManager::Secret', { DeletionPolicy: 'Retain' });
  });

  it('rotates the secret every 30 days in production, through an endpoint in its own VPC', () => {
    const template = synth('production');
    template.hasResourceProperties('AWS::SecretsManager::RotationSchedule', {
      RotationRules: { ScheduleExpression: 'rate(30 days)' },
    });
    template.hasResourceProperties('AWS::EC2::VPCEndpoint', {
      VpcEndpointType: 'Interface',
      ServiceName: {
        'Fn::Join': ['', Match.arrayWith([Match.stringLikeRegexp('secretsmanager')])],
      },
    });
    // The endpoint lets in the rotation function's group alone: an open one
    // would let in the VPC's CIDR, a token CDK Nag's EC23 rule throws on.
    template.hasResourceProperties('AWS::EC2::SecurityGroupIngress', {
      IpProtocol: 'tcp',
      FromPort: 443,
      ToPort: 443,
      GroupId: {
        'Fn::GetAtt': [Match.stringLikeRegexp('^RDSVpcSecretsManagerSecurityGroup'), 'GroupId'],
      },
      SourceSecurityGroupId: {
        'Fn::GetAtt': [Match.stringLikeRegexp('^RDSRotationSecurityGroup'), 'GroupId'],
      },
    });
    for (const [id, group] of Object.entries(template.findResources('AWS::EC2::SecurityGroup'))) {
      expect(group['Properties'], id).not.toHaveProperty('SecurityGroupIngress');
    }
  });

  it('gives a user without a network a VPC of two isolated zones with a flow log and no way out', () => {
    const template = synth('development');
    template.resourceCountIs('AWS::EC2::VPC', 1);
    template.resourceCountIs('AWS::EC2::Subnet', 2);
    template.resourceCountIs('AWS::EC2::NatGateway', 0);
    template.resourceCountIs('AWS::EC2::InternetGateway', 0);
    template.hasResourceProperties('AWS::EC2::FlowLog', {
      ResourceType: 'VPC',
      TrafficType: 'REJECT',
      LogDestinationType: 'cloud-watch-logs',
    });
  });

  it('throws the cluster away with the stack in development, pausing when idle and never rotating', () => {
    const template = synth('development');
    template.hasResource('AWS::RDS::DBCluster', {
      DeletionPolicy: 'Delete',
      UpdateReplacePolicy: 'Delete',
      Properties: {
        DeletionProtection: false,
        BackupRetentionPeriod: 1,
        ServerlessV2ScalingConfiguration: {
          MinCapacity: DEVELOPMENT_CAPACITY.min,
          MaxCapacity: DEVELOPMENT_CAPACITY.max,
          SecondsUntilAutoPause: AUTO_PAUSE.toSeconds(),
        },
      },
    });
    template.resourceCountIs('AWS::RDS::DBInstance', 1);
    template.resourceCountIs('AWS::SecretsManager::RotationSchedule', 0);
    template.resourceCountIs('AWS::EC2::VPCEndpoint', 0);
    template.hasResource('AWS::KMS::Key', { DeletionPolicy: 'Delete' });
    template.hasResource('AWS::SecretsManager::Secret', { DeletionPolicy: 'Delete' });
  });

  it('hands the function the cluster, its secret and the database, and the Data API calls', () => {
    const template = synth('production');
    template.hasResourceProperties('AWS::Lambda::Function', {
      Environment: {
        Variables: {
          [RDS_CLUSTER_ARN]: Match.anyValue(),
          [RDS_SECRET_ARN]: { Ref: Match.stringLikeRegexp('^RDSSecret') },
          [RDS_DATABASE]: RDS_DATABASE_NAME,
        },
      },
    });
    const [fn] = Object.values(template.findResources('AWS::Lambda::Function'));
    const variables = (fn as { Properties: { Environment: { Variables: object } } }).Properties
      .Environment.Variables;
    expect(Object.keys(variables).sort()).toEqual([RDS_CLUSTER_ARN, RDS_DATABASE, RDS_SECRET_ARN]);
    expect(grantedActions(template, /^FnServiceRole/)).toEqual(DATA_API_ACTIONS);
  });

  it("puts the cluster in the user's VPC when it has one, and opens its port to the user", () => {
    const stack = new Stack(new App(), 'test');
    // tsconfig's exactOptionalPropertyTypes keeps a Vpc from passing as an IVpc.
    const vpc = new Vpc(stack, 'Vpc', { maxAzs: 2 }) as IVpc;
    const group = new SecurityGroup(stack, 'Group', { vpc });
    const fn = probeFunction(stack);
    const user: DataStoreUser = Object.assign(fn, {
      network: { vpc, connections: group.connections },
    });
    connectRDS(stack, user, TIER_RULES.production);
    const template = Template.fromStack(stack);
    template.resourceCountIs('AWS::EC2::VPC', 1);
    template.resourceCountIs('AWS::EC2::VPCEndpoint', 0);
    template.hasResourceProperties('AWS::RDS::DBSubnetGroup', {
      SubnetIds: [
        { Ref: Match.stringLikeRegexp('^VpcPrivateSubnet1') },
        { Ref: Match.stringLikeRegexp('^VpcPrivateSubnet2') },
      ],
    });
    const port = { 'Fn::GetAtt': [Match.stringLikeRegexp('^RDS'), 'Endpoint.Port'] };
    template.hasResourceProperties('AWS::EC2::SecurityGroupIngress', {
      IpProtocol: 'tcp',
      FromPort: port,
      ToPort: port,
      GroupId: { 'Fn::GetAtt': [Match.stringLikeRegexp('^RDSSecurityGroup'), 'GroupId'] },
      SourceSecurityGroupId: { 'Fn::GetAtt': [Match.stringLikeRegexp('^Group'), 'GroupId'] },
    });
    template.hasResourceProperties('AWS::Lambda::Function', {
      Environment: {
        Variables: {
          [RDS_HOST]: { 'Fn::GetAtt': [Match.stringLikeRegexp('^RDS'), 'Endpoint.Address'] },
          [RDS_PORT]: { 'Fn::GetAtt': [Match.stringLikeRegexp('^RDS'), 'Endpoint.Port'] },
        },
      },
    });
  });

  it('takes any grantee with an environment, not only a Lambda: a task role and its container', () => {
    const stack = new Stack(new App(), 'test');
    const role = new Role(stack, 'TaskRole', {
      assumedBy: new ServicePrincipal('ecs-tasks.amazonaws.com'),
    });
    const environment: Record<string, string> = {};
    const user: DataStoreUser = {
      grantPrincipal: role,
      addEnvironment: (key, value) => {
        environment[key] = value;
      },
    };
    const cluster = connectRDS(stack, user, TIER_RULES.production);
    expect(environment).toEqual({
      [RDS_CLUSTER_ARN]: cluster.clusterArn,
      [RDS_SECRET_ARN]: cluster.secret?.secretArn,
      [RDS_DATABASE]: RDS_DATABASE_NAME,
    });
    const template = Template.fromStack(stack);
    template.resourceCountIs('AWS::Lambda::Function', 0);
    template.hasResourceProperties('AWS::IAM::Policy', {
      Roles: [{ Ref: stack.getLogicalId(role.node.defaultChild as CfnElement) }],
    });
    expect(grantedActions(template, /^TaskRole/)).toEqual(DATA_API_ACTIONS);
  });
});

/** The policies the stack attaches to roles whose logical id matches `role`. */
interface Policy {
  Properties: {
    PolicyDocument: { Statement: { Action: unknown }[] };
    Roles: { Ref: string }[];
  };
}

/** Every action the stack's policies grant the roles matching `role`, sorted. */
function grantedActions(template: Template, role: RegExp): string[] {
  const statements = Object.values(template.findResources('AWS::IAM::Policy'))
    .map((policy) => policy as Policy)
    .filter((policy) => policy.Properties.Roles.some((r) => role.test(r.Ref)))
    .flatMap((policy) => policy.Properties.PolicyDocument.Statement);
  return statements
    .flatMap((s) => [s.Action].flat())
    .map(String)
    .sort((a, b) => a.localeCompare(b));
}
