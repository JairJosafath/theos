// Starts tracing when the stack names a collector, before any other module
// loads node:http: src/main.ts imports this module first.
import { startTracing } from './tracing';

/** The running tracer provider, or undefined where the tier doesn't trace. */
export const tracing = startTracing(process.env);
