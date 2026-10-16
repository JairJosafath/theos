import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    // The site's logic lives in modules without the DOM, so its tests run in
    // plain Node, beside the stacks' assertions.
    include: ['src/**/*.test.ts', 'infra/**/*.test.ts'],
    // An infra test synthesizes a stack and builds the site with Vite, which
    // takes seconds, not the default 5 s budget on a busy runner; a
    // beforeAll that synthesizes both tiers, not the default 10 s.
    testTimeout: 60_000,
    hookTimeout: 120_000,
  },
});
