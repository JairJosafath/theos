// The CDK app `cdk synth` runs (cdk.json's "app"): the Framework's stacks with
// the Standard applied. Changing this file is the Standard's job, not a task's.
import { App } from 'aws-cdk-lib';

import { defineStacks } from './stacks';
import { applyStandard } from './standard';

const app = new App();
defineStacks(app);
applyStandard(app);
app.synth();
