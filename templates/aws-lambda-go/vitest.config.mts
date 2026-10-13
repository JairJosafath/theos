import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    // The function's tests are Go's (`go test`); vitest runs the stacks'.
    include: ['infra/**/*.test.ts'],
    // An infra test synthesizes its stacks in beforeAll, building the
    // function with go build: seconds, and a minute with a cold build cache,
    // not the default 10 s budget on a busy runner.
    hookTimeout: 120_000,
    testTimeout: 60_000,
  },
});
