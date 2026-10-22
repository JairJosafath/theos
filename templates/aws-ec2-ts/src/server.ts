import { randomUUID } from 'node:crypto';
import { createServer } from 'node:http';
import type { IncomingMessage, Server, ServerResponse } from 'node:http';

import { answer } from './api';
import type { ApiRequest, Log } from './api';
import type { Authenticate } from './auth';
import type { ItemStore } from './item_store';
import { problem } from './items';
import type { HttpResponse } from './items';

/** The largest body the server reads; an item is far smaller. */
export const MAX_BODY_BYTES = 64 * 1024;

/**
 * The HTTP server the instances run: reads each request, hands it to `answer`
 * and writes one JSON log line for it. `authenticate` checks each request's
 * bearer token. It listens on nothing until the caller
 * calls `listen`.
 */
export function createApiServer(store: ItemStore, log: Log, authenticate: Authenticate): Server {
  const server = createServer((incoming, outgoing) => {
    // serve answers every failure itself; this catch only keeps a bug in it
    // from ending the process, which would drop every request in flight.
    serve(incoming, outgoing, store, log, authenticate).catch((error: unknown) => {
      log({ level: 'ERROR', message: 'Serving a request failed.', error: String(error) });
      outgoing.destroy();
    });
  });
  // The load balancer keeps connections open for 60 s; the server must not
  // close them first, or a request racing the close fails with a 502.
  server.keepAliveTimeout = 65_000;
  server.headersTimeout = 66_000;
  return server;
}

async function serve(
  incoming: IncomingMessage,
  outgoing: ServerResponse,
  store: ItemStore,
  log: Log,
  authenticate: Authenticate,
): Promise<void> {
  const started = performance.now();
  // The load balancer's trace id, so a log line can be found from its access log.
  const trace = incoming.headers['x-amzn-trace-id'];
  const requestId = typeof trace === 'string' && trace !== '' ? trace : randomUUID();
  const method = incoming.method ?? 'GET';
  const path = pathOf(incoming.url);
  let response: HttpResponse;
  let body: string | undefined;
  try {
    body = await readBody(incoming);
  } catch {
    // The client went away mid-body: there is no one to answer.
    outgoing.destroy();
    return;
  }
  if (path === undefined) {
    response = problem(400, 'The request target is not a valid path.');
  } else if (body === undefined) {
    response = problem(413, `The body must be at most ${String(MAX_BODY_BYTES)} bytes.`);
  } else {
    const request: ApiRequest = {
      method,
      path,
      body,
      authorization: incoming.headers.authorization,
      requestId,
    };
    response = await answer(request, store, log, authenticate);
  }
  outgoing.writeHead(response.statusCode, response.headers);
  outgoing.end(response.body);
  log({
    level: 'INFO',
    message: 'Answered a request.',
    requestId,
    method,
    path: path ?? incoming.url,
    status: response.statusCode,
    durationMs: Math.round(performance.now() - started),
  });
}

/**
 * The path of a request target such as `/items/i1?x=1`; undefined when the
 * target is not an origin-form path (`//host/x`, `*`, a full URL).
 */
function pathOf(target: string | undefined): string | undefined {
  if (target === undefined || !target.startsWith('/') || target.startsWith('//')) {
    return undefined;
  }
  try {
    return new URL(target, 'http://localhost').pathname;
  } catch {
    return undefined;
  }
}

/** The request's body as text; undefined when it is longer than MAX_BODY_BYTES. */
async function readBody(incoming: IncomingMessage): Promise<string | undefined> {
  const chunks: Buffer[] = [];
  let length = 0;
  for await (const chunk of incoming) {
    const buffer = Buffer.isBuffer(chunk) ? chunk : Buffer.from(String(chunk));
    length += buffer.length;
    if (length > MAX_BODY_BYTES) {
      return undefined;
    }
    chunks.push(buffer);
  }
  return Buffer.concat(chunks).toString('utf8');
}
