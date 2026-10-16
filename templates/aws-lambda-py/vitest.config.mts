import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    // The function is Python, tested with pytest; vitest runs the stacks' tests.
    include: ['infra/**/*.test.ts'],
    // An infra test synthesizes a stack and bundles the function with uv,
    // which takes seconds, not the default 5 s budget on a busy runner; a
    // beforeAll that synthesizes both tiers, and may wait on PyPI for the
    // first bundle, takes over a minute there, not the default 10 s.
    testTimeout: 60_000,
    hookTimeout: 180_000,
  },
});
