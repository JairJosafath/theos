import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    include: ['src/**/*.test.ts', 'infra/**/*.test.ts'],
    // An infra test synthesizes a stack and bundles the function with
    // esbuild, which takes seconds, not the default 5 s budget on a busy runner.
    testTimeout: 60_000,
  },
});
