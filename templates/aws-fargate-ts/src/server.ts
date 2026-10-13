import { randomUUID } from 'node:crypto';
import type { IncomingHttpHeaders } from 'node:http';

import { answer } from './api';
import type { Log } from './api';
import type { ItemStore } from './item_store';
import { problem } from './items';
import type { HttpResponse } from './items';

/** The largest body the service reads; an item is far smaller. */
export const MAX_BODY_BYTES = 64 * 1024;

/** A body larger than MAX_BODY_BYTES: answered with 413, not read further. */
class BodyTooLarge extends Error {}

/**
 * The parts of Node's `IncomingMessage` the listener reads, so a test passes
 * a stream without a socket.
 */
export interface Incoming extends AsyncIterable<Buffer | string> {
  readonly method?: string | undefined;
  readonly url?: string | undefined;
  readonly headers: IncomingHttpHeaders;
}

/** The parts of Node's `ServerResponse` the listener writes. */
export interface Outgoing {
  writeHead(statusCode: number, headers: Readonly<Record<string, string>>): unknown;
  end(body: string): unknown;
}

/**
 * The server's request listener: it reads each request, hands it to `answer`
 * and writes the response. `newId` names a request the load balancer didn't
 * trace.
 */
export function listener(
  store: ItemStore,
  log: Log,
  newId: () => string = randomUUID,
): (request: Incoming, response: Outgoing) => Promise<void> {
  return async (request, response) => {
    const requestId = traceId(request) ?? newId();
    let answered: HttpResponse;
    try {
      const body = await readBody(request, MAX_BODY_BYTES);
      answered = await answer(
        {
          method: request.method ?? '',
          path: new URL(request.url ?? '/', 'http://localhost').pathname,
          body,
          requestId,
        },
        store,
        log,
      );
    } catch (error: unknown) {
      const refused =
        error instanceof BodyTooLarge
          ? problem(413, `The body must be at most ${String(MAX_BODY_BYTES)} bytes.`)
          : problem(400, 'The request could not be read.');
      // The rest of the body is unread: close the connection rather than
      // read it to reuse the connection.
      answered = { ...refused, headers: { ...refused.headers, connection: 'close' } };
    }
    response.writeHead(answered.statusCode, answered.headers);
    response.end(answered.body);
  };
}

/** The trace id the Application Load Balancer adds, `Root=1-...`. */
function traceId(request: Incoming): string | undefined {
  const header = request.headers['x-amzn-trace-id'];
  return typeof header === 'string' && header !== '' ? header : undefined;
}

/** Reads a body as UTF-8, refusing one larger than `limit` bytes. */
export async function readBody(
  stream: AsyncIterable<Buffer | string>,
  limit: number,
): Promise<string> {
  const chunks: Buffer[] = [];
  let size = 0;
  for await (const chunk of stream) {
    const bytes = typeof chunk === 'string' ? Buffer.from(chunk) : chunk;
    size += bytes.length;
    if (size > limit) {
      throw new BodyTooLarge();
    }
    chunks.push(bytes);
  }
  return Buffer.concat(chunks).toString('utf8');
}
