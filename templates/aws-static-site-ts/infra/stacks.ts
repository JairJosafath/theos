import type { App, Stack } from 'aws-cdk-lib';

import packageJson from '../package.json' with { type: 'json' };
import { OPERATIONS } from './operations';
import { SiteStack } from './site_stack';
import { stackNameFor } from './standard';
import type { Tier } from './standard';

/**
 * Every stack of this Container for `tier`, added to `app`. `infra/main.ts`
 * calls it, then applies the Standard's tags and checks to what it returns.
 * The stack is named after the package, which is the Container, and in
 * development after the tier too. It is in us-east-1 wherever its users
 * are: CloudFront serves from every edge location, and takes its web
 * application firewall and certificate, and reports its metrics, only there.
 */
export function defineStacks(app: App, tier: Tier): Stack[] {
  const name = packageJson.name;
  const site = new SiteStack(app, stackNameFor(name, tier), {
    description: `${packageJson.description} An S3 bucket behind a CloudFront distribution.`,
    env: { region: 'us-east-1' },
    tier,
    operations: OPERATIONS,
  });
  return [site];
}
