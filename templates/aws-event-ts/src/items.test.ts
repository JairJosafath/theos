import { describe, expect, it } from 'vitest';

import { checkEvent, checkId, checkItem } from './items';

/** An EventBridge event as the rule delivers it, carrying `detail`. */
function eventOf(detail: unknown, fields: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    version: '0',
    id: '6a7e8feb-b491-4cf7-a9f1-bf3703467718',
    'detail-type': 'ItemSubmitted',
    source: 'orders',
    account: '111122223333',
    time: '2026-10-16T03:00:00Z',
    region: 'eu-west-1',
    resources: [],
    detail,
    ...fields,
  };
}

describe('checkId', () => {
  it.each(['a', 'item-1', 'ITEM_2', 'x'.repeat(64)])('accepts %j', (id) => {
    expect(checkId(id)).toEqual({ ok: true, value: id });
  });

  it.each([undefined, 1, '', 'x'.repeat(65), 'a b', 'a/b', 'zoë'])('refuses %j', (id) => {
    expect(checkId(id).ok).toBe(false);
  });
});

describe('checkItem', () => {
  it.each([
    {
      value: { id: 'i1', name: 'Lamp', quantity: 3 },
      want: { id: 'i1', name: 'Lamp', quantity: 3 },
    },
    {
      value: { id: 'i1', name: '  Zoë  ', quantity: 0, note: 'ignored' },
      want: { id: 'i1', name: 'Zoë', quantity: 0 },
    },
  ])('accepts $value', ({ value, want }) => {
    expect(checkItem(value)).toEqual({ ok: true, value: want });
  });

  it.each([
    { value: undefined, error: 'The item must be a JSON object.' },
    { value: '{"id":"i1"}', error: 'The item must be a JSON object.' },
    { value: [], error: 'The item must be a JSON object.' },
    { value: null, error: 'The item must be a JSON object.' },
    {
      value: { name: 'Lamp', quantity: 1 },
      error: '"id": The id must be 1 to 64 letters, digits, "-" or "_".',
    },
    {
      value: { id: 'a/b', name: 'Lamp', quantity: 1 },
      error: '"id": The id must be 1 to 64 letters, digits, "-" or "_".',
    },
    {
      value: { id: 'i1', quantity: 1 },
      error: '"name" must be a non-empty string of at most 200 characters.',
    },
    {
      value: { id: 'i1', name: ' ', quantity: 1 },
      error: '"name" must be a non-empty string of at most 200 characters.',
    },
    {
      value: { id: 'i1', name: 'x'.repeat(201), quantity: 1 },
      error: '"name" must be a non-empty string of at most 200 characters.',
    },
    { value: { id: 'i1', name: 'Lamp' }, error: '"quantity" must be a whole number, 0 or more.' },
    {
      value: { id: 'i1', name: 'Lamp', quantity: -1 },
      error: '"quantity" must be a whole number, 0 or more.',
    },
    {
      value: { id: 'i1', name: 'Lamp', quantity: 1.5 },
      error: '"quantity" must be a whole number, 0 or more.',
    },
    {
      value: { id: 'i1', name: 'Lamp', quantity: '1' },
      error: '"quantity" must be a whole number, 0 or more.',
    },
  ])('refuses $value', ({ value, error }) => {
    expect(checkItem(value)).toEqual({ ok: false, error });
  });
});

describe('checkEvent', () => {
  it('accepts an EventBridge event carrying an item', () => {
    expect(checkEvent(eventOf({ id: 'i1', name: 'Lamp', quantity: 3 }))).toEqual({
      ok: true,
      value: {
        id: '6a7e8feb-b491-4cf7-a9f1-bf3703467718',
        source: 'orders',
        detailType: 'ItemSubmitted',
        item: { id: 'i1', name: 'Lamp', quantity: 3 },
      },
    });
  });

  it.each([
    { event: undefined, error: 'The event must be a JSON object.' },
    { event: [], error: 'The event must be a JSON object.' },
    { event: 'ItemSubmitted', error: 'The event must be a JSON object.' },
    // An item invoked by hand, not wrapped in an event.
    {
      event: { id: 'i1', name: 'Lamp', quantity: 3 },
      error: '"source" must be a string of 1 to 256 characters.',
    },
    {
      event: eventOf({ id: 'i1', name: 'Lamp', quantity: 3 }, { id: undefined }),
      error: '"id" must be a string of 1 to 256 characters.',
    },
    {
      event: eventOf({ id: 'i1', name: 'Lamp', quantity: 3 }, { id: '' }),
      error: '"id" must be a string of 1 to 256 characters.',
    },
    {
      event: eventOf({ id: 'i1', name: 'Lamp', quantity: 3 }, { 'detail-type': 7 }),
      error: '"detail-type" must be a string of 1 to 256 characters.',
    },
    { event: eventOf(undefined), error: '"detail": The item must be a JSON object.' },
    {
      event: eventOf({ id: 'i1', name: 'Lamp' }),
      error: '"detail": "quantity" must be a whole number, 0 or more.',
    },
  ])('refuses $event', ({ event, error }) => {
    expect(checkEvent(event)).toEqual({ ok: false, error });
  });
});
