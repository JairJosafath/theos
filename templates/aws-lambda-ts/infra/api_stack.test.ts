import { App } from 'aws-cdk-lib';
import { Match, Template } from 'aws-cdk-lib/assertions';
import { beforeAll, describe, expect, it } from 'vitest';

import { ApiStack } from './api_stack';

let template: Template;

beforeAll(() => {
  template = Template.fromStack(new ApiStack(new App(), 'test'));
});

describe('ApiStack', () => {
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

  it('runs the handler on arm64 and the newest Node, with its limits set', () => {
    template.hasResourceProperties('AWS::Lambda::Function', {
      Handler: 'index.handler',
      Runtime: 'nodejs24.x',
      Architectures: ['arm64'],
      MemorySize: 256,
      Timeout: 10,
      Environment: { Variables: { TABLE_NAME: Match.anyValue() } },
    });
  });

  it('lets the handler only get and put items, and write its logs', () => {
    const policies = template.findResources('AWS::IAM::Policy');
    const actions = Object.values(policies).flatMap(
      (policy: unknown) => JSON.stringify(policy).match(/"[a-z0-9]+:[A-Za-z]+"/g) ?? [],
    );
    expect(actions.sort()).toEqual([
      '"dynamodb:GetItem"',
      '"dynamodb:PutItem"',
      '"logs:CreateLogStream"',
      '"logs:PutLogEvents"',
    ]);
    template.resourcePropertiesCountIs(
      'AWS::IAM::Role',
      { ManagedPolicyArns: Match.anyValue() },
      0,
    );
  });

  it('routes GET and PUT /items/{id} to the handler, signed with IAM', () => {
    for (const routeKey of ['GET /items/{id}', 'PUT /items/{id}']) {
      template.hasResourceProperties('AWS::ApiGatewayV2::Route', {
        RouteKey: routeKey,
        AuthorizationType: 'AWS_IAM',
      });
    }
    template.resourceCountIs('AWS::ApiGatewayV2::Route', 2);
  });

  it('logs every request and throttles the stage', () => {
    template.hasResourceProperties('AWS::ApiGatewayV2::Stage', {
      StageName: '$default',
      AutoDeploy: true,
      AccessLogSettings: { DestinationArn: Match.anyValue(), Format: Match.anyValue() },
      DefaultRouteSettings: { ThrottlingRateLimit: 50, ThrottlingBurstLimit: 100 },
    });
  });
});
