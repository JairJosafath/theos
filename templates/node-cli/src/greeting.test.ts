import { describe, expect, it } from 'vitest';

import { greeting } from './greeting';

describe('greeting', () => {
  it.each([
    { name: 'Ada', want: 'Hello, Ada!' },
    { name: '  Grace  ', want: 'Hello, Grace!' },
    { name: 'Zoë', want: 'Hello, Zoë!' },
    { name: '', want: 'Hello!' },
    { name: ' \t', want: 'Hello!' },
  ])('greets "$name"', ({ name, want }) => {
    expect(greeting(name)).toBe(want);
  });
});
