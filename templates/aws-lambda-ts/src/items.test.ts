import { describe, expect, it } from 'vitest';

import { checkId, checkItem, problem } from './items';

describe('checkId', () => {
  it.each(['a', 'item-1', 'ITEM_2', 'x'.repeat(64)])('accepts %j', (id) => {
    expect(checkId(id)).toEqual({ ok: true, value: id });
  });

  it.each([undefined, '', 'x'.repeat(65), 'a b', 'a/b', 'zoë'])('refuses %j', (id) => {
    expect(checkId(id).ok).toBe(false);
  });
});

describe('checkItem', () => {
  it.each([
    { body: '{"name":"Lamp","quantity":3}', want: { id: 'i1', name: 'Lamp', quantity: 3 } },
    { body: '{"name":"  Zoë  ","quantity":0}', want: { id: 'i1', name: 'Zoë', quantity: 0 } },
  ])('accepts $body', ({ body, want }) => {
    expect(checkItem('i1', body)).toEqual({ ok: true, value: want });
  });

  it.each([
    { body: undefined, error: 'The body must be JSON.' },
    { body: '', error: 'The body must be JSON.' },
    { body: '{', error: 'The body must be JSON.' },
    { body: '[]', error: 'The body must be a JSON object.' },
    { body: 'null', error: 'The body must be a JSON object.' },
    {
      body: '{"quantity":1}',
      error: '"name" must be a non-empty string of at most 200 characters.',
    },
    {
      body: '{"name":" ","quantity":1}',
      error: '"name" must be a non-empty string of at most 200 characters.',
    },
    {
      body: JSON.stringify({ name: 'x'.repeat(201), quantity: 1 }),
      error: '"name" must be a non-empty string of at most 200 characters.',
    },
    { body: '{"name":"Lamp"}', error: '"quantity" must be a whole number, 0 or more.' },
    {
      body: '{"name":"Lamp","quantity":-1}',
      error: '"quantity" must be a whole number, 0 or more.',
    },
    {
      body: '{"name":"Lamp","quantity":1.5}',
      error: '"quantity" must be a whole number, 0 or more.',
    },
    {
      body: '{"name":"Lamp","quantity":"1"}',
      error: '"quantity" must be a whole number, 0 or more.',
    },
  ])('refuses $body', ({ body, error }) => {
    expect(checkItem('i1', body)).toEqual({ ok: false, error });
  });
});

describe('problem', () => {
  it('answers JSON with only the message', () => {
    expect(problem(404, 'No item i1.')).toEqual({
      statusCode: 404,
      headers: { 'content-type': 'application/json' },
      body: '{"message":"No item i1."}',
    });
  });
});
