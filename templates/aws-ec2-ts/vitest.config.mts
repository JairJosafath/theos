import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    include: ['src/**/*.test.ts', 'infra/**/*.test.ts'],
    // An infra test synthesizes the stack, checks it with CDK Nag and bundles
    // the server with esbuild in its beforeAll, which takes seconds, not the
    // default 5 s (10 s for a hook) budget on a busy runner.
    testTimeout: 60_000,
    hookTimeout: 60_000,
  },
});
