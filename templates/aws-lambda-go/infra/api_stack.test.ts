import { App } from 'aws-cdk-lib';
import { Capture, Match, Template } from 'aws-cdk-lib/assertions';
import { readdirSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { beforeAll, describe, expect, it } from 'vitest';

import { ApiStack } from './api_stack';

let outdir: string;
let template: Template;

beforeAll(() => {
  const app = new App();
  template = Template.fromStack(new ApiStack(app, 'test'));
  outdir = app.outdir;
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

  it('runs the Go binary on arm64 and Amazon Linux 2023, with its limits set', () => {
    template.hasResourceProperties('AWS::Lambda::Function', {
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
    template.hasResourceProperties('AWS::Lambda::Function', { Code: { S3Key: key } });
    const dir = path.join(outdir, `asset.${key.asString().replace(/\.zip$/, '')}`);
    expect(readdirSync(dir)).toEqual(['bootstrap']);
    // An ELF executable for AArch64: e_machine 183 at offset 18.
    const binary = readFileSync(path.join(dir, 'bootstrap'));
    expect(binary.subarray(0, 4).toString('latin1')).toBe('\x7fELF');
    expect(binary.readUInt16LE(18)).toBe(183);
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
