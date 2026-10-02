import { describe, expect, it } from 'vitest';

import { greeting } from './greeting';

describe('greeting', () => {
  it.each([
    { appName: 'Visual Studio Code', want: 'Hello from Visual Studio Code!' },
    { appName: '  VSCodium  ', want: 'Hello from VSCodium!' },
    { appName: '', want: 'Hello!' },
    { appName: ' \t', want: 'Hello!' },
  ])('greets "$appName"', ({ appName, want }) => {
    expect(greeting(appName)).toBe(want);
  });
});
