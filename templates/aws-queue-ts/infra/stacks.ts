import type { App, Stack } from 'aws-cdk-lib';

import packageJson from '../package.json' with { type: 'json' };
import { BackupCopyStack } from './backups';
import { OPERATIONS } from './operations';
import { QueueStack } from './queue_stack';
import { STANDARD_TAGS, stackNameFor, TIER_RULES } from './standard';
import type { Tier } from './standard';

// The tag every resource carries, which the backups select by.
const CONTAINER_TAG = 'c4factory:container';

/**
 * Every stack of this Container for `tier`, added to `app`. `infra/main.ts`
 * calls it, then applies the Standard's tags and checks to what it returns.
 * The stack is named after the package, which is the Container, and in
 * development after the tier too. In production with
 * `OPERATIONS.backups.copyToRegion` set, a second stack in that region holds
 * the vault the backups are copied to.
 */
export function defineStacks(app: App, tier: Tier): Stack[] {
  const name = packageJson.name;
  const { backups } = OPERATIONS;
  const worker = new QueueStack(app, stackNameFor(name, tier), {
    description: `${packageJson.description} An SQS queue with a dead-letter queue, a Lambda function consuming it and a DynamoDB table.`,
    tier,
    operations: OPERATIONS,
    backupTag: [CONTAINER_TAG, STANDARD_TAGS[CONTAINER_TAG] ?? name],
  });
  if (!TIER_RULES[tier].backups || backups.retentionDays === 0 || backups.copyToRegion === '') {
    return [worker];
  }
  const copy = new BackupCopyStack(app, `${name}-backup-copy`, name, {
    description: `The vault in ${backups.copyToRegion} that the backups of ${name} are copied to.`,
    env: { region: backups.copyToRegion },
  });
  // The vault must be there before the first backup is copied to it.
  worker.addStackDependency(copy);
  return [worker, copy];
}
