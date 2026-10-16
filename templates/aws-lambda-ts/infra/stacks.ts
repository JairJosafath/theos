import type { App, Stack } from 'aws-cdk-lib';

import packageJson from '../package.json' with { type: 'json' };
import { ApiStack } from './api_stack';
import { BackupCopyStack } from './backups';
import { OPERATIONS } from './operations';
import { STANDARD_TAGS, TIER_RULES } from './standard';
import type { Tier } from './standard';

// The tag every resource carries, which the backups select by.
const CONTAINER_TAG = 'c4factory:container';

/**
 * Every stack of this Container for `tier`, added to `app`. `infra/main.ts`
 * calls it, then applies the Standard's tags and checks to what it returns.
 * The stack is named after the package, which is the Container. In
 * production with `OPERATIONS.backups.copyToRegion` set, a second stack in
 * that region holds the vault the backups are copied to.
 */
export function defineStacks(app: App, tier: Tier): Stack[] {
  const name = packageJson.name;
  const { backups } = OPERATIONS;
  const api = new ApiStack(app, name, {
    description: `${packageJson.description} An HTTP API, a Lambda function and a DynamoDB table.`,
    tier,
    operations: OPERATIONS,
    backupTag: [CONTAINER_TAG, STANDARD_TAGS[CONTAINER_TAG] ?? name],
  });
  if (!TIER_RULES[tier].backups || backups.retentionDays === 0 || backups.copyToRegion === '') {
    return [api];
  }
  const copy = new BackupCopyStack(app, `${name}-backup-copy`, name, {
    description: `The vault in ${backups.copyToRegion} that the backups of ${name} are copied to.`,
    env: { region: backups.copyToRegion },
  });
  // The vault must be there before the first backup is copied to it.
  api.addStackDependency(copy);
  return [api, copy];
}
