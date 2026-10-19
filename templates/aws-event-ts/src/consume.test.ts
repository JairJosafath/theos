import { describe, expect, it } from 'vitest';

import { checkingFirst, consume, counting, eventOf, itemOf, Malformed, reporting } from './consume';
import type { Report } from './consume';
import type { Item, ItemEvent } from './items';
import type { ItemStore } from './item_store';

/** A stand-in for the table: keeps what it is given, or fails with `failure`. */
class FakeStore implements ItemStore {
  readonly items: Item[] = [];

  constructor(private readonly failure?: Error) {}

  put(item: Item): Promise<void> {
    if (this.failure !== undefined) {
      return Promise.reject(this.failure);
    }
    this.items.push(item);
    return Promise.resolve();
  }
}

/** A report that records what it is told. */
class Recorder implements Report {
  readonly lines: string[] = [];

  consumed(event: ItemEvent): void {
    this.lines.push(`consumed ${event.id}: ${event.item.id}`);
  }

  failed(_event: unknown, error: unknown): void {
    this.lines.push(`failed: ${error instanceof Error ? error.message : 'unknown'}`);
  }
}

/** An EventBridge event from `orders` carrying `detail`. */
function event(id: string, detail: unknown): Record<string, unknown> {
  return { id, source: 'orders', 'detail-type': 'ItemSubmitted', detail };
}

const LAMP = event('e1', { id: 'i1', name: 'Lamp', quantity: 3 });

describe('consume', () => {
  it('stores the item an event carries', async () => {
    const store = new FakeStore();

    await consume(LAMP, store);

    expect(store.items).toEqual([{ id: 'i1', name: 'Lamp', quantity: 3 }]);
  });

  it('throws Malformed for a detail that is no item, and stores nothing', async () => {
    const store = new FakeStore();

    const consumed = consume(event('e2', { id: 'i1' }), store);

    await expect(consumed).rejects.toBeInstanceOf(Malformed);
    await expect(consumed).rejects.toThrow('The event carries no item: "detail": "name" must be');
    expect(store.items).toEqual([]);
  });

  it('throws what the store throws', async () => {
    const consumed = consume(LAMP, new FakeStore(new Error('throttled')));

    await expect(consumed).rejects.toThrow('throttled');
  });
});

describe('eventOf and itemOf', () => {
  it('are the event, checked, and the item it carries', () => {
    expect(eventOf(LAMP)).toEqual({
      id: 'e1',
      source: 'orders',
      detailType: 'ItemSubmitted',
      item: { id: 'i1', name: 'Lamp', quantity: 3 },
    });
    expect(itemOf(LAMP)).toEqual({ id: 'i1', name: 'Lamp', quantity: 3 });
  });

  it('throw Malformed for what is no EventBridge event', () => {
    expect(() => eventOf({ id: 'i1', name: 'Lamp', quantity: 3 })).toThrow(Malformed);
    expect(() => itemOf([])).toThrow(Malformed);
  });
});

describe('checkingFirst', () => {
  it('runs the work on an event that carries an item', async () => {
    const seen: unknown[] = [];

    await checkingFirst((received) => {
      seen.push(received);
      return Promise.resolve();
    })(LAMP);

    expect(seen).toEqual([LAMP]);
  });

  it('throws Malformed before the work runs on a malformed event', async () => {
    const seen: unknown[] = [];
    const work = checkingFirst((received) => {
      seen.push(received);
      return Promise.resolve();
    });

    await expect(work(event('e2', '{'))).rejects.toBeInstanceOf(Malformed);
    await expect(work({ detail: {} })).rejects.toBeInstanceOf(Malformed);
    expect(seen).toEqual([]);
  });
});

describe('counting', () => {
  it('reports a consumed event', async () => {
    const recorder = new Recorder();

    await counting(() => Promise.resolve(), recorder)(LAMP);

    expect(recorder.lines).toEqual(['consumed e1: i1']);
  });

  it('reports nothing when the work throws, and throws its error on', async () => {
    const recorder = new Recorder();
    const work = counting(() => Promise.reject(new Error('throttled')), recorder);

    await expect(work(LAMP)).rejects.toThrow('throttled');
    expect(recorder.lines).toEqual([]);
  });

  it('counts a redelivered event once when the record answers it', async () => {
    // A stand-in for Powertools' Idempotency: the second delivery of an id
    // returns the stored result without running the work.
    const recorder = new Recorder();
    const seen = new Set<string>();
    const once =
      (work: (event: unknown) => Promise<void>) =>
      async (event: unknown): Promise<void> => {
        const id = eventOf(event).id;
        if (!seen.has(id)) {
          await work(event);
          seen.add(id);
        }
      };
    const work = reporting(once(counting(() => Promise.resolve(), recorder)), recorder);

    await work(LAMP);
    await work(LAMP);

    expect(recorder.lines).toEqual(['consumed e1: i1']);
  });
});

describe('reporting', () => {
  it('reports nothing for an event that was consumed', async () => {
    const recorder = new Recorder();

    await reporting(() => Promise.resolve(), recorder)(LAMP);

    expect(recorder.lines).toEqual([]);
  });

  it('reports a failed event and throws its error on', async () => {
    const recorder = new Recorder();
    const work = reporting(() => Promise.reject(new Error('throttled')), recorder);

    await expect(work(LAMP)).rejects.toThrow('throttled');
    expect(recorder.lines).toEqual(['failed: throttled']);
  });
});
