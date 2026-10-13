import type { App, Stack } from 'aws-cdk-lib';

import packageJson from '../package.json' with { type: 'json' };
import { ApiStack } from './api_stack';

/**
 * Every stack of this Container, added to `app`. `infra/main.ts` calls it, then
 * applies the Standard's tags and checks to what it returns. The stack is
 * named after the package, which is the Container.
 */
export function defineStacks(app: App): Stack[] {
  return [
    new ApiStack(app, packageJson.name, {
      description: `${packageJson.description} An HTTP API, a Lambda function and a DynamoDB table.`,
    }),
  ];
}
