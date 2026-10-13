// The Standard's rules for every stack, checked on the stacks infra/stacks.ts
// defines, built the way `cdk synth` builds them. CDK Nag checks most AWS
// defaults; these check the rest. A failing case here means a stack breaks
// the Standard: fix the stack, never the test.
import { App, Stack } from 'aws-cdk-lib';
import { Annotations, Match, Template } from 'aws-cdk-lib/assertions';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { beforeAll, describe, expect, it } from 'vitest';

import { defineStacks } from './stacks';
import { applyStandard, STANDARD_TAGS } from './standard';

/** A resource in a synthesized template, as far as these rules read it. */
interface Resource {
  readonly Type: string;
  readonly Properties?: Record<string, unknown>;
  readonly DeletionPolicy?: string;
}

// Resources that hold data, which a stack must say to keep or delete.
const STATEFUL_TYPES = [
  'AWS::DynamoDB::Table',
  'AWS::DynamoDB::GlobalTable',
  'AWS::S3::Bucket',
  'AWS::SQS::Queue',
  'AWS::SNS::Topic',
  'AWS::Logs::LogGroup',
  'AWS::RDS::DBInstance',
  'AWS::RDS::DBCluster',
  'AWS::EFS::FileSystem',
  'AWS::KMS::Key',
];

/** The feature flags `cdk synth` reads from cdk.json, so the tests build the same app. */
function cdkContext(): Record<string, unknown> {
  const cdkJson: unknown = JSON.parse(
    readFileSync(path.join(import.meta.dirname, '..', 'cdk.json'), 'utf8'),
  );
  if (typeof cdkJson !== 'object' || cdkJson === null || !('context' in cdkJson)) {
    return {};
  }
  const { context } = cdkJson;
  return typeof context === 'object' && context !== null ? { ...context } : {};
}

function isResource(value: unknown): value is Resource {
  return (
    typeof value === 'object' && value !== null && 'Type' in value && typeof value.Type === 'string'
  );
}

/** The resources in `template`, of one `type` or all. */
function resourcesOf(template: Template, type?: string): [string, Resource][] {
  const resources: unknown = template.toJSON()['Resources'];
  const found: [string, Resource][] = [];
  if (typeof resources === 'object' && resources !== null) {
    for (const [id, value] of Object.entries(resources)) {
      const resource: unknown = value;
      if (isResource(resource) && (type === undefined || resource.Type === type)) {
        found.push([id, resource]);
      }
    }
  }
  return found;
}

/** Every IAM policy statement in `value`: each object with an Effect and an Action or NotAction. */
function statementsIn(value: unknown): Record<string, unknown>[] {
  if (Array.isArray(value)) {
    return value.flatMap((item: unknown) => statementsIn(item));
  }
  if (typeof value !== 'object' || value === null) {
    return [];
  }
  const object: Record<string, unknown> = { ...value };
  const nested = Object.values(object).flatMap((item: unknown) => statementsIn(item));
  return 'Effect' in object && ('Action' in object || 'NotAction' in object)
    ? [object, ...nested]
    : nested;
}

let stacks: Stack[] = [];
let templates: [string, Template][] = [];

beforeAll(() => {
  const app = new App({ context: cdkContext() });
  defineStacks(app);
  applyStandard(app);
  // Every stack in the app, as applyStandard sees them, not only those
  // defineStacks returns.
  stacks = app.node.findAll().filter((node) => Stack.isStack(node));
  // Throws when CDK Nag finds anything not acknowledged with a reason.
  app.synth();
  templates = stacks.map((stack) => [stack.stackName, Template.fromStack(stack)]);
});

describe('the Standard', () => {
  it('has at least one stack', () => {
    expect(stacks.length).toBeGreaterThan(0);
  });

  it('leaves no error or warning on any stack', () => {
    for (const stack of stacks) {
      const annotations = Annotations.fromStack(stack);
      expect(annotations.findError('*', Match.anyValue()), stack.stackName).toEqual([]);
      expect(annotations.findWarning('*', Match.anyValue()), stack.stackName).toEqual([]);
    }
  });

  it('tags every stack with its project, system and container', () => {
    for (const stack of stacks) {
      expect(stack.tags.tagValues(), stack.stackName).toMatchObject(STANDARD_TAGS);
    }
  });

  it('tags every resource that takes a list of tags', () => {
    const wanted = Object.entries(STANDARD_TAGS).map(([Key, Value]) => ({ Key, Value }));
    for (const [name, template] of templates) {
      for (const [id, resource] of resourcesOf(template)) {
        const tags = resource.Properties?.['Tags'];
        if (Array.isArray(tags)) {
          expect(tags, `${name}/${id}`).toEqual(expect.arrayContaining(wanted));
        }
      }
    }
  });

  it('grants no IAM action with a wildcard service or action', () => {
    for (const [name, template] of templates) {
      // A Deny may name every action (a bucket refusing plain HTTP denies
      // s3:*); only what is allowed must be narrow.
      for (const statement of statementsIn(template.toJSON())) {
        if (statement['Effect'] === 'Deny') {
          continue;
        }
        for (const action of [statement['Action'], statement['NotAction']].flat()) {
          if (action !== undefined) {
            expect(action, name).not.toMatch(/^\*$|^[a-z0-9-]+:\*$/);
          }
        }
      }
    }
  });

  it('encrypts every table, bucket, queue and topic at rest', () => {
    for (const [name, template] of templates) {
      for (const [id, table] of resourcesOf(template, 'AWS::DynamoDB::Table')) {
        expect(table.Properties?.['SSESpecification'], `${name}/${id}`).toMatchObject({
          SSEEnabled: true,
        });
      }
      for (const [id, bucket] of resourcesOf(template, 'AWS::S3::Bucket')) {
        expect(bucket.Properties?.['BucketEncryption'], `${name}/${id}`).toBeDefined();
      }
      for (const [id, queue] of resourcesOf(template, 'AWS::SQS::Queue')) {
        const encrypted =
          queue.Properties?.['KmsMasterKeyId'] !== undefined ||
          queue.Properties?.['SqsManagedSseEnabled'] === true;
        expect(encrypted, `${name}/${id}`).toBe(true);
      }
      for (const [id, topic] of resourcesOf(template, 'AWS::SNS::Topic')) {
        expect(topic.Properties?.['KmsMasterKeyId'], `${name}/${id}`).toBeDefined();
      }
    }
  });

  it('keeps every log group for a set time, and gives every function one', () => {
    for (const [name, template] of templates) {
      for (const [id, group] of resourcesOf(template, 'AWS::Logs::LogGroup')) {
        expect(group.Properties?.['RetentionInDays'], `${name}/${id}`).toBeGreaterThan(0);
      }
      for (const [id, fn] of resourcesOf(template, 'AWS::Lambda::Function')) {
        expect(fn.Properties?.['LoggingConfig'], `${name}/${id}`).toHaveProperty('LogGroup');
      }
    }
  });

  it('says what happens to the data of every stateful resource when it is removed', () => {
    for (const [name, template] of templates) {
      for (const [id, resource] of resourcesOf(template)) {
        if (STATEFUL_TYPES.includes(resource.Type)) {
          expect(resource.DeletionPolicy, `${name}/${id}`).toMatch(
            /^(Retain|Delete|Snapshot|RetainExceptOnCreate)$/,
          );
        }
      }
    }
  });
});
