import type { App, Stack } from 'aws-cdk-lib';

import packageJson from '../package.json' with { type: 'json' };
import { BackupCopyStack } from './backups';
import { OPERATIONS } from './operations';
import { JobStack } from './job_stack';
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
  const job = new JobStack(app, stackNameFor(name, tier), {
    description: `${packageJson.description} An EventBridge Scheduler schedule invoking a Lambda function, a dead-letter queue and a DynamoDB table.`,
    tier,
    operations: OPERATIONS,
    backupTag: [CONTAINER_TAG, STANDARD_TAGS[CONTAINER_TAG] ?? name],
  });
  if (!TIER_RULES[tier].backups || backups.retentionDays === 0 || backups.copyToRegion === '') {
    return [job];
  }
  const copy = new BackupCopyStack(app, `${name}-backup-copy`, name, {
    description: `The vault in ${backups.copyToRegion} that the backups of ${name} are copied to.`,
    env: { region: backups.copyToRegion },
  });
  // The vault must be there before the first backup is copied to it.
  job.addStackDependency(copy);
  return [job, copy];
}
