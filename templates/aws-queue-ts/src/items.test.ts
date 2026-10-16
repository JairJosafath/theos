import { describe, expect, it } from 'vitest';

import { checkId, checkMessage } from './items';

describe('checkId', () => {
  it.each(['a', 'item-1', 'ITEM_2', 'x'.repeat(64)])('accepts %j', (id) => {
    expect(checkId(id)).toEqual({ ok: true, value: id });
  });

  it.each([undefined, 1, '', 'x'.repeat(65), 'a b', 'a/b', 'zoë'])('refuses %j', (id) => {
    expect(checkId(id).ok).toBe(false);
  });
});

describe('checkMessage', () => {
  it.each([
    {
      body: '{"id":"i1","name":"Lamp","quantity":3}',
      want: { id: 'i1', name: 'Lamp', quantity: 3 },
    },
    {
      body: '{"id":"i1","name":"  Zoë  ","quantity":0,"note":"ignored"}',
      want: { id: 'i1', name: 'Zoë', quantity: 0 },
    },
  ])('accepts $body', ({ body, want }) => {
    expect(checkMessage(body)).toEqual({ ok: true, value: want });
  });

  it.each([
    { body: '', error: 'The body must be JSON.' },
    { body: '{', error: 'The body must be JSON.' },
    { body: '[]', error: 'The body must be a JSON object.' },
    { body: 'null', error: 'The body must be a JSON object.' },
    {
      body: '{"name":"Lamp","quantity":1}',
      error: '"id": The id must be 1 to 64 letters, digits, "-" or "_".',
    },
    {
      body: '{"id":"a/b","name":"Lamp","quantity":1}',
      error: '"id": The id must be 1 to 64 letters, digits, "-" or "_".',
    },
    {
      body: '{"id":"i1","quantity":1}',
      error: '"name" must be a non-empty string of at most 200 characters.',
    },
    {
      body: '{"id":"i1","name":" ","quantity":1}',
      error: '"name" must be a non-empty string of at most 200 characters.',
    },
    {
      body: JSON.stringify({ id: 'i1', name: 'x'.repeat(201), quantity: 1 }),
      error: '"name" must be a non-empty string of at most 200 characters.',
    },
    { body: '{"id":"i1","name":"Lamp"}', error: '"quantity" must be a whole number, 0 or more.' },
    {
      body: '{"id":"i1","name":"Lamp","quantity":-1}',
      error: '"quantity" must be a whole number, 0 or more.',
    },
    {
      body: '{"id":"i1","name":"Lamp","quantity":1.5}',
      error: '"quantity" must be a whole number, 0 or more.',
    },
    {
      body: '{"id":"i1","name":"Lamp","quantity":"1"}',
      error: '"quantity" must be a whole number, 0 or more.',
    },
  ])('refuses $body', ({ body, error }) => {
    expect(checkMessage(body)).toEqual({ ok: false, error });
  });
});
