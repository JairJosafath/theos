import type { APIGatewayProxyEventV2 } from 'aws-lambda';
import { describe, expect, it } from 'vitest';

import { answer, route } from './api';
import type { Log } from './api';
import type { ItemStore } from './item_store';
import type { HttpResponse, Item } from './items';

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

function request(
  routeKey: string,
  id: string | undefined,
  body?: string,
  isBase64Encoded = false,
): APIGatewayProxyEventV2 {
  const [method = '', path = ''] = routeKey.split(' ');
  return {
    version: '2.0',
    routeKey,
    rawPath: path.replace('{id}', id ?? ''),
    rawQueryString: '',
    headers: {},
    ...(id === undefined ? {} : { pathParameters: { id } }),
    ...(body === undefined ? {} : { body }),
    isBase64Encoded,
    requestContext: {
      accountId: '123456789012',
      apiId: 'api',
      domainName: 'example.com',
      domainPrefix: 'example',
      http: { method, path, protocol: 'HTTP/1.1', sourceIp: '192.0.2.1', userAgent: 'test' },
      requestId: 'request-1',
      routeKey,
      stage: '$default',
      time: '13/Oct/2026:00:00:00 +0000',
      timeEpoch: 0,
    },
  };
}

const noLog = (): void => undefined;

/** Answers `event` from `store`, as the handler does. */
function send(
  event: APIGatewayProxyEventV2,
  store: ItemStore,
  log: Log = noLog,
): Promise<HttpResponse> {
  return answer(event, (request) => route(request, store), log);
}

describe('answer', () => {
  it('puts an item, then gets it', async () => {
    const store = new MemoryStore();

    const put = await send(request('PUT /items/{id}', 'i1', '{"name":"Lamp","quantity":3}'), store);
    const got = await send(request('GET /items/{id}', 'i1'), store);

    expect(put.statusCode).toBe(200);
    expect(got).toEqual({
      statusCode: 200,
      headers: { 'content-type': 'application/json' },
      body: '{"id":"i1","name":"Lamp","quantity":3}',
    });
  });

  it('reads a base64-encoded body', async () => {
    const body = Buffer.from('{"name":"Zoë","quantity":1}').toString('base64');

    const put = await send(request('PUT /items/{id}', 'i1', body, true), new MemoryStore());

    expect(put.body).toBe('{"id":"i1","name":"Zoë","quantity":1}');
  });

  it.each([
    { name: 'a missing item', event: request('GET /items/{id}', 'nope'), status: 404 },
    { name: 'a bad id', event: request('GET /items/{id}', 'a b'), status: 400 },
    { name: 'no id', event: request('GET /items/{id}', undefined), status: 400 },
    { name: 'a bad body', event: request('PUT /items/{id}', 'i1', '{"name":""}'), status: 400 },
    { name: 'an unknown route', event: request('DELETE /items/{id}', 'i1'), status: 404 },
  ])('answers $status for $name', async ({ event, status }) => {
    const response = await send(event, new MemoryStore());

    expect(response.statusCode).toBe(status);
    expect(JSON.parse(response.body)).toHaveProperty('message');
  });

  it('answers 500 without details when the store fails, and logs why', async () => {
    const logged: Readonly<Record<string, unknown>>[] = [];

    const response = await send(
      request('GET /items/{id}', 'i1'),
      new MemoryStore(true),
      (message, attributes) => logged.push({ message, ...attributes }),
    );

    expect(response.statusCode).toBe(500);
    expect(response.body).not.toContain('timeout');
    expect(logged).toEqual([
      expect.objectContaining({
        message: 'The request failed.',
        requestId: 'request-1',
        error: 'timeout',
      }),
    ]);
  });
});
