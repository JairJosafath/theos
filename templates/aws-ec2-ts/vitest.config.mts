import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    include: ['src/**/*.test.ts', 'infra/**/*.test.ts'],
    // An infra test synthesizes the stack, checks it with CDK Nag and bundles
    // the server with esbuild in its beforeAll, which takes seconds, not the
    // default 5 s budget on a busy runner; a beforeAll that synthesizes both
    // tiers, not the default 10 s.
    testTimeout: 60_000,
    hookTimeout: 120_000,
  },
});
