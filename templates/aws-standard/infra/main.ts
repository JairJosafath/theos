// The CDK app `cdk synth` runs (cdk.json's "app"): the Framework's stacks for
// the tier the `tier` context value names (`development` when it names none),
// with the Standard applied. Changing this file is the Standard's job, not a
// task's.
import { App } from 'aws-cdk-lib';
import type { Stack } from 'aws-cdk-lib';

import { defineStacks } from './stacks';
import { applyStandard, tierOf } from './standard';
import type { Tier } from './standard';

const app = new App();
const tier = tierOf(app);
// A stacks.ts written before tiers takes the app alone, and fits this too.
const define: (app: App, tier: Tier) => Stack[] = defineStacks;
define(app, tier);
applyStandard(app, tier);
app.synth();
