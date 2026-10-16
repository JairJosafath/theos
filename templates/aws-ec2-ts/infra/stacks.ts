import type { App, Stack } from 'aws-cdk-lib';

import packageJson from '../package.json' with { type: 'json' };
import { BackupCopyStack } from './backups';
import { OPERATIONS } from './operations';
import { ServiceStack } from './service_stack';
import { stackNameFor, TIER_RULES } from './standard';
import type { Tier } from './standard';

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
  const service = new ServiceStack(app, stackNameFor(name, tier), {
    description: `${packageJson.description} An internal load balancer, an Auto Scaling group of EC2 instances and a DynamoDB table.`,
    tier,
    operations: OPERATIONS,
  });
  if (!TIER_RULES[tier].backups || backups.retentionDays === 0 || backups.copyToRegion === '') {
    return [service];
  }
  const copy = new BackupCopyStack(app, `${name}-backup-copy`, name, {
    description: `The vault in ${backups.copyToRegion} that the backups of ${name} are copied to.`,
    env: { region: backups.copyToRegion },
  });
  // The vault must be there before the first backup is copied to it.
  service.addStackDependency(copy);
  return [service, copy];
}
