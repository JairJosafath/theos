import { connect } from 'node:net';
import type { AddressInfo } from 'node:net';
import type { Server } from 'node:http';
import { afterAll, beforeAll, describe, expect, it } from 'vitest';

import type { Authenticate } from './auth';
import { MemoryStore } from './memory_store.fake';
import { createApiServer, MAX_BODY_BYTES } from './server';

// A real server on a loopback port the system picks: the tests talk HTTP to
// it, never to AWS.
const logged: Readonly<Record<string, unknown>>[] = [];
let server: Server;
let base: string;

// Accepts only `Bearer good`; src/auth.test.ts tests the Cognito verifier.
const authenticate: Authenticate = (authorization) =>
  Promise.resolve(authorization === 'Bearer good');
const signedIn = { authorization: 'Bearer good' };

beforeAll(async () => {
  server = createApiServer(new MemoryStore(), (entry) => logged.push(entry), authenticate);
  await new Promise<void>((resolve) => {
    server.listen(0, '127.0.0.1', resolve);
  });
  const { port } = server.address() as AddressInfo;
  base = `http://127.0.0.1:${String(port)}`;
});

afterAll(async () => {
  await new Promise<void>((resolve, reject) => {
    server.close((error) => {
      if (error === undefined) {
        resolve();
      } else {
        reject(error);
      }
    });
    server.closeAllConnections();
  });
});

/**
 * Sends `text` as it is on a new connection and returns what the server
 * writes back; with `hangUp`, closes the connection right after sending.
 */
function rawRequest(text: string, hangUp = false): Promise<string> {
  const { port } = server.address() as AddressInfo;
  return new Promise((resolve, reject) => {
    let reply = '';
    const socket = connect(port, '127.0.0.1', () => {
      if (hangUp) {
        socket.end(text);
        socket.destroy();
        resolve('');
      } else {
        socket.write(text);
      }
    });
    socket.setEncoding('utf8');
    socket.on('data', (chunk: string) => {
      reply += chunk;
    });
    socket.on('end', () => {
      resolve(reply);
    });
    socket.on('error', reject);
  });
}

describe('createApiServer', () => {
  it('puts and gets an item over HTTP', async () => {
    const put = await fetch(`${base}/items/i1`, {
      method: 'PUT',
      headers: signedIn,
      body: '{"name":"Lamp","quantity":3}',
    });
    const got = await fetch(`${base}/items/i1?ignored=1`, { headers: signedIn });

    expect(put.status).toBe(200);
    expect(got.status).toBe(200);
    expect(got.headers.get('content-type')).toBe('application/json');
    expect(await got.json()).toEqual({ id: 'i1', name: 'Lamp', quantity: 3 });
  });

  it('answers 401 without a bearer token, and the health check with none', async () => {
    const refused = await fetch(`${base}/items/i1`);
    const wrong = await fetch(`${base}/items/i1`, { headers: { authorization: 'Bearer bad' } });
    const health = await fetch(`${base}/health`);

    expect(refused.status).toBe(401);
    expect(refused.headers.get('www-authenticate')).toBe('Bearer');
    expect(wrong.status).toBe(401);
    expect(health.status).toBe(200);
  });

  it('refuses a body larger than it reads', async () => {
    const response = await fetch(`${base}/items/i2`, {
      method: 'PUT',
      body: 'x'.repeat(MAX_BODY_BYTES + 1),
    });

    expect(response.status).toBe(413);
  });

  it('answers 400 for a target that is not a path, and keeps serving', async () => {
    const reply = await rawRequest('GET // HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n');
    const health = await fetch(`${base}/health`);

    expect(reply).toMatch(/^HTTP\/1\.1 400 /);
    expect(health.status).toBe(200);
  });

  it('keeps serving after a client goes away mid-body', async () => {
    await rawRequest(
      'PUT /items/i3 HTTP/1.1\r\nHost: x\r\nContent-Length: 100\r\n\r\n{"name":',
      true,
    );
    const health = await fetch(`${base}/health`);

    expect(health.status).toBe(200);
  });

  it('logs each request with the trace id the load balancer sends', async () => {
    await fetch(`${base}/health`, { headers: { 'x-amzn-trace-id': 'Root=1-abc' } });

    expect(logged).toContainEqual(
      expect.objectContaining({
        level: 'INFO',
        requestId: 'Root=1-abc',
        method: 'GET',
        path: '/health',
        status: 200,
      }),
    );
  });

  it('keeps idle connections open longer than the load balancer does', () => {
    expect(server.keepAliveTimeout).toBeGreaterThan(60_000);
    expect(server.headersTimeout).toBeGreaterThan(server.keepAliveTimeout);
  });
});
