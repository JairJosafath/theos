import { describe, expect, it } from 'vitest';

import { answer } from './api';
import type { ApiRequest } from './api';
import type { Authenticate } from './auth';
import { MemoryStore } from './memory_store.fake';

function request(method: string, path: string, body = ''): ApiRequest {
  return { method, path, body, requestId: 'request-1', authorization: 'Bearer good' };
}

// Accepts only `Bearer good`; src/auth.test.ts tests the Cognito verifier.
const authenticate: Authenticate = (authorization) =>
  Promise.resolve(authorization === 'Bearer good');

const noLog = (): void => undefined;

describe('answer', () => {
  it('puts an item, then gets it', async () => {
    const store = new MemoryStore();

    const put = await answer(
      request('PUT', '/items/i1', '{"name":"Lamp","quantity":3}'),
      store,
      noLog,
      authenticate,
    );
    const got = await answer(request('GET', '/items/i1'), store, noLog, authenticate);

    expect(put.statusCode).toBe(200);
    expect(got).toEqual({
      statusCode: 200,
      headers: { 'content-type': 'application/json' },
      body: '{"id":"i1","name":"Lamp","quantity":3}',
    });
  });

  it('answers the health check', async () => {
    const response = await answer(
      request('GET', '/health'),
      new MemoryStore(true),
      noLog,
      authenticate,
    );

    expect(response.statusCode).toBe(200);
    expect(JSON.parse(response.body)).toEqual({ status: 'ok' });
  });

  it.each([
    { name: 'a missing item', req: request('GET', '/items/nope'), status: 404 },
    { name: 'a bad id', req: request('GET', '/items/a%20b'), status: 400 },
    { name: 'a malformed escape', req: request('GET', '/items/%E0%A4%A'), status: 400 },
    { name: 'no id', req: request('GET', '/items/'), status: 400 },
    { name: 'a bad body', req: request('PUT', '/items/i1', '{"name":""}'), status: 400 },
    { name: 'no body', req: request('PUT', '/items/i1'), status: 400 },
    { name: 'an unknown method', req: request('DELETE', '/items/i1'), status: 405 },
    { name: 'a health check that is not a GET', req: request('POST', '/health'), status: 405 },
    { name: 'an unknown path', req: request('GET', '/things/i1'), status: 404 },
    { name: 'a nested path', req: request('GET', '/items/i1/more'), status: 404 },
  ])('answers $status for $name', async ({ req, status }) => {
    const response = await answer(req, new MemoryStore(), noLog, authenticate);

    expect(response.statusCode).toBe(status);
    expect(JSON.parse(response.body)).toHaveProperty('message');
  });

  it('answers 500 without details when the store fails, and logs why', async () => {
    const logged: Readonly<Record<string, unknown>>[] = [];

    const response = await answer(
      request('GET', '/items/i1'),
      new MemoryStore(true),
      (entry) => logged.push(entry),
      authenticate,
    );

    expect(response.statusCode).toBe(500);
    expect(response.body).not.toContain('timeout');
    expect(logged).toEqual([
      expect.objectContaining({ level: 'ERROR', requestId: 'request-1', error: 'timeout' }),
    ]);
  });

  it.each([
    { name: 'no token', authorization: undefined },
    { name: 'a token that does not verify', authorization: 'Bearer bad' },
  ])('answers 401 for $name, before it reads the store', async ({ authorization }) => {
    const response = await answer(
      { ...request('GET', '/items/i1'), authorization },
      new MemoryStore(true),
      noLog,
      authenticate,
    );

    expect(response.statusCode).toBe(401);
    expect(response.headers['www-authenticate']).toBe('Bearer');
  });

  it('answers the health check without a token', async () => {
    const response = await answer(
      { ...request('GET', '/health'), authorization: undefined },
      new MemoryStore(),
      noLog,
      authenticate,
    );

    expect(response.statusCode).toBe(200);
  });
});
