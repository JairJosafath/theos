// The Standard's rules for every stack, checked on the stacks infra/stacks.ts
// defines for each tier, built the way `cdk synth -c tier=<tier>` builds
// them. CDK Nag checks most AWS defaults; these check the rest, and that the
// stacks keep what infra/operations.ts promises. A failing case here means a
// stack breaks the Standard: fix the stack, never the test.
import { App, Stack } from 'aws-cdk-lib';
import { Annotations, Match, Template } from 'aws-cdk-lib/assertions';
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { beforeAll, describe, expect, it } from 'vitest';

import { defineStacks } from './stacks';
import { applyStandard, stackNameFor, STANDARD_TAGS, TIER_RULES, TIERS, tierOf } from './standard';
import type { Operations, Tier } from './standard';

/** A resource in a synthesized template, as far as these rules read it. */
interface Resource {
  readonly Type: string;
  readonly Properties?: Record<string, unknown>;
  readonly DeletionPolicy?: string;
  readonly UpdateReplacePolicy?: string;
  readonly Condition?: string;
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
  'AWS::Backup::BackupVault',
];

// What the alarms watch: per resource type, the metric each alarm reads.
const ALARMED_METRICS: Readonly<Record<string, readonly string[]>> = {
  'AWS::Lambda::Function': ['Errors', 'Throttles', 'Duration'],
  'AWS::DynamoDB::Table': ['ReadThrottleEvents', 'WriteThrottleEvents', 'SystemErrors'],
  'AWS::ApiGatewayV2::Api': ['5xx'],
  'AWS::CloudFront::Distribution': ['5xxErrorRate'],
};

// What AWS Backup's plan backs up; a stack holding none of it has no plan.
// A data bucket keeps its earlier versions instead.
const BACKED_UP_TYPES = ['AWS::DynamoDB::Table'];

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

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/** Whether `value` has the shape of `Operations`, field by field. */
function isOperations(value: unknown): value is Operations {
  if (!isRecord(value)) {
    return false;
  }
  const { backups, recovery, alerts } = value;
  return (
    isRecord(backups) &&
    typeof backups['retentionDays'] === 'number' &&
    typeof backups['copyToRegion'] === 'string' &&
    isRecord(recovery) &&
    typeof recovery['rpo'] === 'string' &&
    typeof recovery['rto'] === 'string' &&
    isRecord(alerts) &&
    typeof alerts['email'] === 'string'
  );
}

/**
 * The block infra/operations.ts holds, which the factory renders from the
 * Container's spec.operations; undefined in a repository scaffolded before
 * it had one, whose stacks promise nothing per tier.
 */
async function loadOperations(): Promise<Operations | undefined> {
  const file = path.join(import.meta.dirname, 'operations.ts');
  if (!existsSync(file)) {
    return undefined;
  }
  const loaded: unknown = await import(pathToFileURL(file).href);
  const operations = isRecord(loaded) ? loaded['OPERATIONS'] : undefined;
  if (!isOperations(operations)) {
    throw new Error(
      "infra/operations.ts exports no OPERATIONS of the Standard's Operations shape.",
    );
  }
  return operations;
}

function isResource(value: unknown): value is Resource {
  return isRecord(value) && typeof value['Type'] === 'string';
}

/** The resources in `template`, of one `type` or all. */
function resourcesOf(template: Template, type?: string): [string, Resource][] {
  const resources: unknown = template.toJSON()['Resources'];
  const found: [string, Resource][] = [];
  if (isRecord(resources)) {
    for (const [id, resource] of Object.entries(resources)) {
      if (isResource(resource) && (type === undefined || resource.Type === type)) {
        found.push([id, resource]);
      }
    }
  }
  return found;
}

/** Every IAM policy statement in `value`: an object with an `Effect`, anywhere in it. */
function statementsIn(value: unknown): Record<string, unknown>[] {
  if (Array.isArray(value)) {
    return (value as unknown[]).flatMap(statementsIn);
  }
  if (!isRecord(value)) {
    return [];
  }
  const own = 'Effect' in value ? [value] : [];
  return [...own, ...Object.values(value).flatMap(statementsIn)];
}

/** Whether `statement` is KMS's default key policy statement, which hands the key's use to IAM. */
function isKeyDefault(statement: Record<string, unknown>): boolean {
  return (
    statement['Action'] === 'kms:*' && JSON.stringify(statement['Principal']).endsWith(':root"]]}}')
  );
}

/** The one item of `items`, failing the test with `what` when there are more or none. */
function only<T>(items: readonly T[], what: string): T {
  const [item] = items;
  if (items.length !== 1 || item === undefined) {
    throw new Error(`${what}: want exactly one, found ${String(items.length)}.`);
  }
  return item;
}

/** A JSON reference to the resource `id`, as a template writes it. */
function ref(id: string): string {
  return JSON.stringify({ Ref: id });
}

/** A stack that holds only the vault a backup is copied to, and its key: it serves nothing. */
function holdsOnlyACopyVault(template: Template): boolean {
  const types = new Set(resourcesOf(template).map(([, resource]) => resource.Type));
  types.delete('AWS::CDK::Metadata');
  return (
    types.has('AWS::Backup::BackupVault') &&
    [...types].every((type) => ['AWS::Backup::BackupVault', 'AWS::KMS::Key'].includes(type))
  );
}

/** Whether `template` holds a resource AWS Backup's plan backs up. */
function holdsBackedUpData(template: Template): boolean {
  return BACKED_UP_TYPES.some((type) => resourcesOf(template, type).length > 0);
}

/**
 * The buckets in `template` that hold data: every bucket but those another
 * resource writes its logs to (a bucket's access logs, a distribution's, a
 * load balancer's), which are not data and expire.
 */
function dataBuckets(template: Template): [string, Resource][] {
  const logging = resourcesOf(template).flatMap(([, resource]) => {
    const properties = resource.Properties ?? {};
    const distribution = properties['DistributionConfig'];
    const attributes = properties['LoadBalancerAttributes'];
    return [
      properties['LoggingConfiguration'],
      isRecord(distribution) ? distribution['Logging'] : undefined,
      Array.isArray(attributes)
        ? (attributes as unknown[]).filter(
            (attribute) => isRecord(attribute) && attribute['Key'] === 'access_logs.s3.bucket',
          )
        : undefined,
    ].map((value: unknown) => JSON.stringify(value ?? null));
  });
  return resourcesOf(template, 'AWS::S3::Bucket').filter(
    ([id]) =>
      !logging.some((json) => json.includes(ref(id)) || json.includes(`{"Fn::GetAtt":["${id}",`)),
  );
}

/** One tier's app, as `cdk synth -c tier=<tier>` builds it. */
interface Built {
  readonly stacks: Stack[];
  readonly templates: [string, Template][];
}

/** Builds and synthesizes the app for `tier`; synth throws on an unacknowledged CDK Nag finding. */
function build(tier: Tier): Built {
  const app = new App({ context: { ...cdkContext(), tier } });
  // A stacks.ts written before tiers takes the app alone, and fits this too.
  const define: (app: App, tier: Tier) => Stack[] = defineStacks;
  define(app, tierOf(app));
  applyStandard(app, tier);
  // Every stack in the app, as applyStandard sees them, not only those
  // defineStacks returns.
  const stacks = app.node.findAll().filter((node) => Stack.isStack(node));
  app.synth();
  return { stacks, templates: stacks.map((stack) => [stack.stackName, Template.fromStack(stack)]) };
}

const operations = await loadOperations();
const built = new Map<Tier, Built>();

/** The app built for `tier`. */
function app(tier: Tier): Built {
  const found = built.get(tier);
  if (found === undefined) {
    throw new Error(`No app was built for ${tier}.`);
  }
  return found;
}

beforeAll(() => {
  for (const tier of TIERS) {
    built.set(tier, build(tier));
  }
});

describe('the tier', () => {
  it('is development when the app names none', () => {
    expect(tierOf(new App())).toBe('development');
  });

  it('is the one the tier context names', () => {
    for (const tier of TIERS) {
      expect(tierOf(new App({ context: { tier } }))).toBe(tier);
    }
  });

  it('refuses a tier it does not know', () => {
    expect(() => tierOf(new App({ context: { tier: 'staging' } }))).toThrow(/staging/);
  });
});

describe.each(TIERS)('the Standard, in %s', (tier) => {
  it('has at least one stack', () => {
    expect(app(tier).stacks.length).toBeGreaterThan(0);
  });

  it('leaves no error or warning on any stack', () => {
    for (const stack of app(tier).stacks) {
      const annotations = Annotations.fromStack(stack);
      expect(annotations.findError('*', Match.anyValue()), stack.stackName).toEqual([]);
      expect(annotations.findWarning('*', Match.anyValue()), stack.stackName).toEqual([]);
    }
  });

  it('tags every stack with its project, system and container', () => {
    for (const stack of app(tier).stacks) {
      expect(stack.tags.tagValues(), stack.stackName).toMatchObject(STANDARD_TAGS);
    }
  });

  it('tags every resource that takes a list of tags', () => {
    // An Auto Scaling group's tags also say whether instances get them.
    const wanted: unknown[] = Object.entries(STANDARD_TAGS).map(([Key, Value]): unknown =>
      expect.objectContaining({ Key, Value }),
    );
    for (const [name, template] of app(tier).templates) {
      for (const [id, resource] of resourcesOf(template)) {
        const tags = resource.Properties?.['Tags'];
        if (Array.isArray(tags)) {
          expect(tags, `${name}/${id}`).toEqual(expect.arrayContaining(wanted));
        }
      }
    }
  });

  it('grants no IAM action with a wildcard service or action', () => {
    for (const [name, template] of app(tier).templates) {
      for (const statement of statementsIn(template.toJSON())) {
        // A Deny may name every action, such as a bucket refusing plain HTTP;
        // a key's default policy lets the account's IAM decide who uses it.
        if (statement['Effect'] === 'Deny' || isKeyDefault(statement)) {
          continue;
        }
        for (const key of ['Action', 'NotAction']) {
          for (const action of [statement[key] ?? []].flat()) {
            expect(action, name).not.toMatch(/^\*$|^[a-z0-9-]+:\*$/);
          }
        }
      }
    }
  });

  it('encrypts every table, bucket, queue and topic at rest', () => {
    for (const [name, template] of app(tier).templates) {
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
    for (const [name, template] of app(tier).templates) {
      for (const [id, group] of resourcesOf(template, 'AWS::Logs::LogGroup')) {
        expect(group.Properties?.['RetentionInDays'], `${name}/${id}`).toBeGreaterThan(0);
      }
      for (const [id, fn] of resourcesOf(template, 'AWS::Lambda::Function')) {
        expect(fn.Properties?.['LoggingConfig'], `${name}/${id}`).toHaveProperty('LogGroup');
      }
    }
  });

  it('says what happens to the data of every stateful resource when it is removed', () => {
    for (const [name, template] of app(tier).templates) {
      for (const [id, resource] of resourcesOf(template)) {
        if (STATEFUL_TYPES.includes(resource.Type)) {
          expect(resource.DeletionPolicy, `${name}/${id}`).toMatch(
            /^(Retain|Delete|Snapshot|RetainExceptOnCreate)$/,
          );
        }
      }
    }
  });

  it('protects every stack from termination where the tier keeps data', () => {
    for (const stack of app(tier).stacks) {
      expect(stack.terminationProtection, stack.stackName).toBe(TIER_RULES[tier].keepData);
    }
  });
});

// What infra/operations.ts promises, per tier: one describe per item of the
// Standard's digest. A repository with no infra/operations.ts was scaffolded
// before the block, and its stacks don't read it.
describe.runIf(operations !== undefined).each(TIERS)(
  'what infra/operations.ts promises, in %s',
  (tier) => {
    const rules = TIER_RULES[tier];
    const promised = (): Operations => {
      if (operations === undefined) {
        throw new Error('infra/operations.ts is gone.');
      }
      return operations;
    };
    // The stacks that serve: all but one holding only the vault backups are copied to.
    const serving = (): [string, Template][] =>
      app(tier).templates.filter(([, template]) => !holdsOnlyACopyVault(template));

    describe('the stack names', () => {
      it("name every stack for the tier, so a deploy without one never changes production's", () => {
        for (const stack of app(tier).stacks) {
          const base = stack.stackName.replace(/-development$/, '');
          expect(stack.stackName).toBe(stackNameFor(base, tier));
        }
      });
    });

    describe('the removal policies', () => {
      it('keep every table, data bucket, backup vault and vault key where the tier keeps data, else delete them', () => {
        const policy = rules.keepData ? 'Retain' : 'Delete';
        for (const [name, template] of app(tier).templates) {
          for (const [id, data] of [
            ...resourcesOf(template, 'AWS::DynamoDB::Table'),
            ...dataBuckets(template),
          ]) {
            expect([data.DeletionPolicy, data.UpdateReplacePolicy], `${name}/${id}`).toEqual([
              policy,
              policy,
            ]);
          }
          for (const [id, vault] of resourcesOf(template, 'AWS::Backup::BackupVault')) {
            expect(vault.DeletionPolicy, `${name}/${id}`).toBe('Retain');
            const key = JSON.stringify(vault.Properties?.['EncryptionKeyArn']);
            for (const [keyId, vaultKey] of resourcesOf(template, 'AWS::KMS::Key')) {
              if (key.includes(`"${keyId}"`)) {
                expect(vaultKey.DeletionPolicy, `${name}/${keyId}`).toBe('Retain');
              }
            }
          }
        }
      });
    });

    describe('deletion and termination protection', () => {
      it('protect every table and stack where the tier keeps data, and nothing else', () => {
        for (const [name, template] of app(tier).templates) {
          for (const [id, table] of resourcesOf(template, 'AWS::DynamoDB::Table')) {
            expect(table.Properties?.['DeletionProtectionEnabled'] === true, `${name}/${id}`).toBe(
              rules.keepData,
            );
          }
        }
        for (const stack of app(tier).stacks) {
          expect(stack.terminationProtection, stack.stackName).toBe(rules.keepData);
        }
      });
    });

    describe('point-in-time recovery', () => {
      it('is on for every table in production when the RPO is under a day', () => {
        const [, count = '', unit = ''] = /^(\d+)([mhd])$/.exec(promised().recovery.rpo) ?? [];
        const minutes = Number(count) * ({ m: 1, h: 60, d: 1440 }[unit] ?? Number.NaN);
        expect(minutes, `recovery.rpo ${promised().recovery.rpo}`).toBeGreaterThan(0);
        if (tier !== 'production' || minutes >= 1440) {
          return;
        }
        for (const [name, template] of app(tier).templates) {
          for (const [id, table] of resourcesOf(template, 'AWS::DynamoDB::Table')) {
            expect(table.Properties?.['PointInTimeRecoverySpecification'], `${name}/${id}`).toEqual(
              expect.objectContaining({ PointInTimeRecoveryEnabled: true }),
            );
          }
        }
      });

      it('is on for every table where the tier recovers data, and off elsewhere', () => {
        for (const [name, template] of app(tier).templates) {
          for (const [id, table] of resourcesOf(template, 'AWS::DynamoDB::Table')) {
            const spec = table.Properties?.['PointInTimeRecoverySpecification'];
            const enabled = isRecord(spec) && spec['PointInTimeRecoveryEnabled'] === true;
            expect(enabled, `${name}/${id}`).toBe(rules.pointInTimeRecovery);
          }
        }
      });
    });

    describe('the versions of data buckets', () => {
      it('are kept, and the earlier ones expire, where the tier keeps data', () => {
        if (!rules.keepData) {
          return;
        }
        for (const [name, template] of app(tier).templates) {
          for (const [id, bucket] of dataBuckets(template)) {
            expect(bucket.Properties?.['VersioningConfiguration'], `${name}/${id}`).toEqual({
              Status: 'Enabled',
            });
            const lifecycle = bucket.Properties?.['LifecycleConfiguration'];
            const lifecycleRules: unknown[] = isRecord(lifecycle)
              ? [lifecycle['Rules']].flat()
              : [];
            expect(lifecycleRules, `${name}/${id}`).toContainEqual(
              expect.objectContaining({
                Status: 'Enabled',
                NoncurrentVersionExpiration: { NoncurrentDays: expect.any(Number) as unknown },
              }),
            );
          }
        }
      });
    });

    describe('the backup plan and vault', () => {
      it('back up daily for retentionDays in a vault with its own key, copied to copyToRegion', () => {
        const { retentionDays, copyToRegion } = promised().backups;
        const backedUp = rules.backups && retentionDays > 0;
        for (const [name, template] of serving()) {
          const plans = resourcesOf(template, 'AWS::Backup::BackupPlan');
          const vaults = resourcesOf(template, 'AWS::Backup::BackupVault');
          const selections = resourcesOf(template, 'AWS::Backup::BackupSelection');
          if (!backedUp || !holdsBackedUpData(template)) {
            expect([plans, vaults, selections], name).toEqual([[], [], []]);
            continue;
          }
          const [vaultId, vault] = only(vaults, `${name}'s backup vaults`);
          expect(vault.Properties?.['EncryptionKeyArn'], name).toEqual({
            'Fn::GetAtt': [expect.any(String), 'Arn'],
          });
          const plan = only(plans, `${name}'s backup plans`)[1].Properties?.['BackupPlan'];
          const planRules: unknown[] = isRecord(plan) ? [plan['BackupPlanRule']].flat() : [];
          expect(planRules.length, name).toBeGreaterThan(0);
          // Daily: a minute and an hour, every day of every month.
          const daily: unknown = expect.stringMatching(/^cron\(\S+ \S+ \* \* \? \*\)$/);
          for (const rule of planRules) {
            expect(rule, name).toMatchObject({
              ScheduleExpression: daily,
              Lifecycle: { DeleteAfterDays: retentionDays },
              TargetBackupVault: { 'Fn::GetAtt': [vaultId, 'BackupVaultName'] },
            });
            const copies: unknown[] = isRecord(rule) ? [rule['CopyActions'] ?? []].flat() : [];
            if (copyToRegion === '') {
              expect(copies, name).toEqual([]);
            } else {
              expect(copies, name).toEqual([
                expect.objectContaining({ Lifecycle: { DeleteAfterDays: retentionDays } }),
              ]);
              expect(JSON.stringify(copies), name).toContain(`:backup:${copyToRegion}:`);
            }
          }
          // The plan backs up what carries the Container's tag, which the
          // Standard puts on every resource.
          const container = STANDARD_TAGS['c4factory:container'];
          const selection = only(selections, `${name}'s backup selections`)[1];
          expect(selection.Properties?.['BackupSelection'], name).toMatchObject({
            ListOfTags: [
              {
                ConditionKey: 'c4factory:container',
                ConditionType: 'STRINGEQUALS',
                ConditionValue: container,
              },
            ],
          });
        }
        const copyStacks = app(tier).stacks.filter((stack) =>
          holdsOnlyACopyVault(Template.fromStack(stack)),
        );
        if (backedUp && copyToRegion !== '' && serving().some(([, t]) => holdsBackedUpData(t))) {
          expect(copyStacks.map((stack) => stack.region)).toEqual([copyToRegion]);
        } else {
          expect(copyStacks).toEqual([]);
        }
      });
    });

    describe('the runbook', () => {
      it('restores from the copy region, when there is one', () => {
        const { retentionDays, copyToRegion } = promised().backups;
        const runbook = path.join(import.meta.dirname, '..', 'docs', 'runbook.md');
        if (retentionDays === 0 || copyToRegion === '' || !existsSync(runbook)) {
          return;
        }
        expect(readFileSync(runbook, 'utf8')).toContain(copyToRegion);
      });
    });

    describe('the alarms and topic', () => {
      it('alarm on every function, table, API and distribution, to the one encrypted topic, with a dashboard', () => {
        for (const [name, template] of serving()) {
          const [topicId] = only(resourcesOf(template, 'AWS::SNS::Topic'), `${name}'s topics`);
          expect(resourcesOf(template, 'AWS::CloudWatch::Dashboard').length, name).toBe(1);
          const alarms = resourcesOf(template, 'AWS::CloudWatch::Alarm');
          for (const [id, alarm] of alarms) {
            expect(alarm.Properties?.['AlarmActions'], `${name}/${id}`).toEqual([{ Ref: topicId }]);
          }
          for (const [type, metrics] of Object.entries(ALARMED_METRICS)) {
            for (const [watchedId] of resourcesOf(template, type)) {
              for (const metric of metrics) {
                const watching = alarms.filter(([, alarm]) => {
                  const json = JSON.stringify(alarm.Properties);
                  return json.includes(`"MetricName":"${metric}"`) && json.includes(ref(watchedId));
                });
                expect(watching.length, `${name}/${watchedId} ${metric}`).toBeGreaterThan(0);
              }
            }
          }
        }
      });
    });

    describe('the subscription', () => {
      it('emails the AlertEmail parameter, which defaults to alerts.email, only when it is set', () => {
        for (const [name, template] of serving()) {
          template.hasParameter('AlertEmail', { Type: 'String', Default: promised().alerts.email });
          const [topicId] = only(resourcesOf(template, 'AWS::SNS::Topic'), `${name}'s topics`);
          const [id, subscription] = only(
            resourcesOf(template, 'AWS::SNS::Subscription'),
            `${name}'s subscriptions`,
          );
          expect(subscription.Properties, `${name}/${id}`).toMatchObject({
            Protocol: 'email',
            Endpoint: { Ref: 'AlertEmail' },
            TopicArn: { Ref: topicId },
          });
          const conditions: unknown = template.toJSON()['Conditions'];
          const condition = isRecord(conditions)
            ? conditions[subscription.Condition ?? '']
            : undefined;
          expect(condition, `${name}/${id}`).toEqual({
            'Fn::Not': [{ 'Fn::Equals': [{ Ref: 'AlertEmail' }, ''] }],
          });
        }
      });
    });

    describe('tracing', () => {
      it('traces every function with X-Ray where the tier traces, and none elsewhere', () => {
        for (const [name, template] of app(tier).templates) {
          for (const [id, fn] of resourcesOf(template, 'AWS::Lambda::Function')) {
            const config = fn.Properties?.['TracingConfig'];
            const active = isRecord(config) && config['Mode'] === 'Active';
            expect(active, `${name}/${id}`).toBe(rules.tracing);
          }
        }
      });
    });

    describe('log retention', () => {
      it("keeps every log group for the tier's time", () => {
        for (const [name, template] of app(tier).templates) {
          for (const [id, group] of resourcesOf(template, 'AWS::Logs::LogGroup')) {
            expect(group.Properties?.['RetentionInDays'], `${name}/${id}`).toBe(rules.logRetention);
          }
        }
      });
    });

    describe('the two outputs', () => {
      it("name every serving stack's entry point and its alarm topic", () => {
        for (const [name, template] of serving()) {
          const [topicId] = only(resourcesOf(template, 'AWS::SNS::Topic'), `${name}'s topics`);
          template.hasOutput('AlarmTopicArn', { Value: { Ref: topicId } });
          template.hasOutput('EntryPoint', { Value: Match.anyValue() });
        }
      });
    });
  },
);
