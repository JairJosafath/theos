import { describe, expect, it } from 'vitest';

import { answer } from './api';
import type { ApiRequest } from './api';
import type { ItemStore } from './item_store';
import type { Item } from './items';

/** An in-memory store; `failing` makes every call throw, as a DynamoDB outage would. */
class MemoryStore implements ItemStore {
  readonly items = new Map<string, Item>();

  constructor(private readonly failing = false) {}

  get(id: string): Promise<Item | undefined> {
    return this.failing
      ? Promise.reject(new Error('timeout'))
      : Promise.resolve(this.items.get(id));
  }

  put(item: Item): Promise<void> {
    if (this.failing) {
      return Promise.reject(new Error('timeout'));
    }
    this.items.set(item.id, item);
    return Promise.resolve();
  }
}

function request(method: string, path: string, body = ''): ApiRequest {
  return { method, path, body, requestId: 'request-1' };
}

const noLog = (): void => undefined;

describe('answer', () => {
  it('puts an item, then gets it', async () => {
    const store = new MemoryStore();

    const put = await answer(
      request('PUT', '/items/i1', '{"name":"Lamp","quantity":3}'),
      store,
      noLog,
    );
    const got = await answer(request('GET', '/items/i1'), store, noLog);

    expect(put.statusCode).toBe(200);
    expect(got).toEqual({
      statusCode: 200,
      headers: { 'content-type': 'application/json' },
      body: '{"id":"i1","name":"Lamp","quantity":3}',
    });
  });

  it('answers the health check without reading the store', async () => {
    const response = await answer(request('GET', '/health'), new MemoryStore(true), noLog);

    expect(response.statusCode).toBe(200);
    expect(response.body).toBe('{"status":"ok"}');
  });

  it.each([
    { name: 'a missing item', request: request('GET', '/items/nope'), status: 404 },
    { name: 'a bad id', request: request('GET', '/items/a%20b'), status: 400 },
    {
      name: 'an id that is not percent-encoded right',
      request: request('GET', '/items/%E0'),
      status: 400,
    },
    { name: 'no id', request: request('GET', '/items/'), status: 400 },
    { name: 'a bad body', request: request('PUT', '/items/i1', '{"name":""}'), status: 400 },
    { name: 'an unknown path', request: request('GET', '/things/i1'), status: 404 },
    { name: 'a nested path', request: request('GET', '/items/i1/parts'), status: 404 },
    { name: 'an unknown method', request: request('DELETE', '/items/i1'), status: 405 },
    { name: 'a write to the health check', request: request('POST', '/health'), status: 405 },
  ])('answers $status for $name', async ({ request, status }) => {
    const response = await answer(request, new MemoryStore(), noLog);

    expect(response.statusCode).toBe(status);
    expect(JSON.parse(response.body)).toHaveProperty('message');
  });

  it('answers 500 without details when the store fails, and logs why', async () => {
    const logged: Readonly<Record<string, unknown>>[] = [];

    const response = await answer(request('GET', '/items/i1'), new MemoryStore(true), (entry) =>
      logged.push(entry),
    );

    expect(response.statusCode).toBe(500);
    expect(response.body).not.toContain('timeout');
    expect(logged).toEqual([
      expect.objectContaining({ level: 'ERROR', requestId: 'request-1', error: 'timeout' }),
    ]);
  });
});
