import { App, Stack } from 'aws-cdk-lib';
import { Match, Template } from 'aws-cdk-lib/assertions';
import { Code, Function as LambdaFunction, Runtime } from 'aws-cdk-lib/aws-lambda';
import { describe, expect, it } from 'vitest';

import { DYNAMODB_ACTIONS, DYNAMODB_TABLE_NAME, connectDynamoDB } from './connector_dynamodb';
import { TIER_RULES } from './standard';
import type { Tier } from './standard';

/** A stack holding one function connected to the table, for `tier`. */
function synth(tier: Tier): Template {
  const stack = new Stack(new App(), 'test');
  const fn = new LambdaFunction(stack, 'Fn', {
    runtime: Runtime.NODEJS_24_X,
    handler: 'index.handler',
    code: Code.fromInline('export const handler = async () => {};'),
  });
  connectDynamoDB(stack, fn, TIER_RULES[tier]);
  return Template.fromStack(stack);
}

describe('connectDynamoDB', () => {
  it('keeps an on-demand table keyed by pk and sk, kept and recoverable in production', () => {
    const template = synth('production');
    template.resourceCountIs('AWS::DynamoDB::Table', 1);
    template.hasResource('AWS::DynamoDB::Table', {
      DeletionPolicy: 'Retain',
      UpdateReplacePolicy: 'Retain',
      Properties: {
        KeySchema: [
          { AttributeName: 'pk', KeyType: 'HASH' },
          { AttributeName: 'sk', KeyType: 'RANGE' },
        ],
        BillingMode: 'PAY_PER_REQUEST',
        SSESpecification: { SSEEnabled: true },
        PointInTimeRecoverySpecification: { PointInTimeRecoveryEnabled: true },
        DeletionProtectionEnabled: true,
      },
    });
  });

  it('throws the table away with the stack in development', () => {
    synth('development').hasResource('AWS::DynamoDB::Table', {
      DeletionPolicy: 'Delete',
      Properties: {
        PointInTimeRecoverySpecification: { PointInTimeRecoveryEnabled: false },
        DeletionProtectionEnabled: false,
      },
    });
  });

  it("hands the function the table's name and lets it make only the item calls", () => {
    const template = synth('production');
    template.hasResourceProperties('AWS::Lambda::Function', {
      Environment: { Variables: { [DYNAMODB_TABLE_NAME]: { Ref: Match.anyValue() } } },
    });
    const statements = Object.values(template.findResources('AWS::IAM::Policy')).flatMap(
      (policy) =>
        (policy as { Properties: { PolicyDocument: { Statement: { Action: unknown }[] } } })
          .Properties.PolicyDocument.Statement,
    );
    const actions = statements
      .flatMap((s) => [s.Action].flat())
      .map(String)
      .sort((a, b) => a.localeCompare(b));
    expect(actions).toEqual([...DYNAMODB_ACTIONS]);
  });
});
