import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    // The function is Python, tested with pytest; vitest runs the stacks' tests.
    include: ['infra/**/*.test.ts'],
    // An infra test synthesizes a stack in beforeAll and bundles the function
    // with uv, which takes seconds, not the default 5 s and 10 s budgets on a
    // busy runner.
    testTimeout: 60_000,
    hookTimeout: 60_000,
  },
});
