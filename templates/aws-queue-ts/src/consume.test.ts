import { describe, expect, it } from 'vitest';

import { checkingFirst, consume, itemOf, Malformed, reporting } from './consume';
import type { Message, Report } from './consume';
import type { Item } from './items';
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

  consumed(messageId: string): void {
    this.lines.push(`consumed ${messageId}`);
  }

  failed(messageId: string, error: unknown): void {
    this.lines.push(`failed ${messageId}: ${error instanceof Error ? error.message : 'unknown'}`);
  }
}

const LAMP: Message = { messageId: 'm1', body: '{"id":"i1","name":"Lamp","quantity":3}' };

describe('consume', () => {
  it('stores the item a message carries', async () => {
    const store = new FakeStore();

    await consume(LAMP, store);

    expect(store.items).toEqual([{ id: 'i1', name: 'Lamp', quantity: 3 }]);
  });

  it('throws Malformed for a body that is no item, and stores nothing', async () => {
    const store = new FakeStore();

    const consumed = consume({ messageId: 'm2', body: '{"id":"i1"}' }, store);

    await expect(consumed).rejects.toBeInstanceOf(Malformed);
    await expect(consumed).rejects.toThrow('Message m2 is malformed: "name" must be');
    expect(store.items).toEqual([]);
  });

  it('throws what the store throws', async () => {
    const consumed = consume(LAMP, new FakeStore(new Error('throttled')));

    await expect(consumed).rejects.toThrow('throttled');
  });
});

describe('itemOf', () => {
  it('is the item a message carries', () => {
    expect(itemOf(LAMP)).toEqual({ id: 'i1', name: 'Lamp', quantity: 3 });
  });

  it('throws Malformed for a body that is no item', () => {
    expect(() => itemOf({ messageId: 'm2', body: '[]' })).toThrow(Malformed);
  });
});

describe('checkingFirst', () => {
  it('runs the work on a message that carries an item', async () => {
    const seen: string[] = [];

    await checkingFirst((message) => {
      seen.push(message.messageId);
      return Promise.resolve();
    })(LAMP);

    expect(seen).toEqual(['m1']);
  });

  it('throws Malformed before the work runs on a malformed message', async () => {
    const seen: string[] = [];
    const work = checkingFirst((message) => {
      seen.push(message.messageId);
      return Promise.resolve();
    });

    await expect(work({ messageId: 'm2', body: '{' })).rejects.toBeInstanceOf(Malformed);
    expect(seen).toEqual([]);
  });
});

describe('reporting', () => {
  it('reports a consumed message', async () => {
    const recorder = new Recorder();

    await reporting(() => Promise.resolve(), recorder)(LAMP);

    expect(recorder.lines).toEqual(['consumed m1']);
  });

  it('reports a failed message and throws its error on', async () => {
    const recorder = new Recorder();
    const work = reporting(() => Promise.reject(new Error('throttled')), recorder);

    await expect(work(LAMP)).rejects.toThrow('throttled');
    expect(recorder.lines).toEqual(['failed m1: throttled']);
  });
});
