import { Readable } from 'node:stream';
import { describe, expect, it } from 'vitest';

import type { Authenticate } from './auth';
import type { ItemStore } from './item_store';
import type { Item } from './items';
import { listener, MAX_BODY_BYTES, readBody } from './server';
import type { Incoming, Outgoing } from './server';

/** An in-memory store. */
class MemoryStore implements ItemStore {
  readonly items = new Map<string, Item>();

  get(id: string): Promise<Item | undefined> {
    return Promise.resolve(this.items.get(id));
  }

  put(item: Item): Promise<void> {
    this.items.set(item.id, item);
    return Promise.resolve();
  }
}

// Accepts only `Bearer good`; src/auth.test.ts tests the Cognito verifier.
const authenticate: Authenticate = (authorization) =>
  Promise.resolve(authorization === 'Bearer good');

/** What the listener wrote back, recorded instead of sent. */
class RecordedResponse implements Outgoing {
  statusCode = 0;
  headers: Readonly<Record<string, string>> = {};
  body = '';

  writeHead(statusCode: number, headers: Readonly<Record<string, string>>): this {
    this.statusCode = statusCode;
    this.headers = headers;
    return this;
  }

  end(body: string): this {
    this.body = body;
    return this;
  }
}

/**
 * A request as Node's server hands it over: a readable body with a method, URL
 * and headers, signed in unless `headers` says otherwise.
 */
function incoming(
  method: string,
  url: string,
  chunks: (string | Buffer)[] = [],
  headers: Record<string, string> = {},
): Incoming {
  return Object.assign(Readable.from(chunks), {
    method,
    url,
    headers: { authorization: 'Bearer good', ...headers },
  });
}

async function send(
  request: Incoming,
  store: ItemStore = new MemoryStore(),
  logged: Readonly<Record<string, unknown>>[] = [],
): Promise<RecordedResponse> {
  const response = new RecordedResponse();
  await listener(
    store,
    (entry) => logged.push(entry),
    authenticate,
    () => 'made-up-id',
  )(request, response);
  return response;
}

describe('listener', () => {
  it('puts an item from a body sent in chunks, then gets it', async () => {
    const store = new MemoryStore();

    const put = await send(
      incoming('PUT', '/items/i1', ['{"name":"Zo', Buffer.from('ë","quantity":1}')]),
      store,
    );
    const got = await send(incoming('GET', '/items/i1?fields=all'), store);

    expect(put.statusCode).toBe(200);
    expect(got.statusCode).toBe(200);
    expect(got.headers).toEqual({ 'content-type': 'application/json' });
    expect(got.body).toBe('{"id":"i1","name":"Zoë","quantity":1}');
  });

  it('answers 401 for a request without the token, and the health check', async () => {
    const refused = await send(incoming('GET', '/items/i1', [], { authorization: '' }));
    const health = await send(incoming('GET', '/health', [], { authorization: '' }));

    expect(refused.statusCode).toBe(401);
    expect(health.statusCode).toBe(200);
  });

  it('refuses a body larger than the limit with 413', async () => {
    const response = await send(incoming('PUT', '/items/i1', ['x'.repeat(MAX_BODY_BYTES), 'x']));

    expect(response.statusCode).toBe(413);
    expect(response.headers).toHaveProperty('connection', 'close');
  });

  it('answers 400 when the body cannot be read', async () => {
    const broken = Object.assign(
      new Readable({
        read() {
          this.destroy(new Error('aborted'));
        },
      }),
      { method: 'PUT', url: '/items/i1', headers: {} },
    );

    const response = await send(broken);

    expect(response.statusCode).toBe(400);
  });

  it.each<{ name: string; headers: Record<string, string>; want: string }>([
    {
      name: "the load balancer's trace id",
      headers: { 'x-amzn-trace-id': 'Root=1-abc' },
      want: 'Root=1-abc',
    },
    { name: 'a made-up id without one', headers: {}, want: 'made-up-id' },
  ])('logs a failure with $name', async ({ headers, want }) => {
    const failing: ItemStore = {
      get: () => Promise.reject(new Error('timeout')),
      put: () => Promise.reject(new Error('timeout')),
    };
    const logged: Readonly<Record<string, unknown>>[] = [];

    const response = await send(incoming('GET', '/items/i1', [], headers), failing, logged);

    expect(response.statusCode).toBe(500);
    expect(logged).toEqual([expect.objectContaining({ requestId: want })]);
  });
});

describe('readBody', () => {
  it('joins the chunks as UTF-8', async () => {
    expect(await readBody(Readable.from([Buffer.from([0xc3]), Buffer.from([0xab])]), 10)).toBe('ë');
  });

  it('reads an empty body as the empty string', async () => {
    expect(await readBody(Readable.from([]), 10)).toBe('');
  });

  it('accepts a body of exactly the limit', async () => {
    expect(await readBody(Readable.from(['abc']), 3)).toBe('abc');
  });
});
