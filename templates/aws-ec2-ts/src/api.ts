import type { ItemStore } from './item_store';
import { checkId, checkItem, json, problem } from './items';
import type { HttpResponse } from './items';

/** Where the API writes its structured logs: one JSON object per entry. */
export type Log = (entry: Readonly<Record<string, unknown>>) => void;

/** One HTTP request, as `src/server.ts` reads it off the socket. */
export interface ApiRequest {
  readonly method: string;
  /** The path without its query string, such as `/items/i1`. */
  readonly path: string;
  readonly body: string;
  /** The id the server gives the request, which the log names. */
  readonly requestId: string;
}

// The one resource the API serves; the id is checked by checkId, not here.
const ITEM_PATH = /^\/items\/([^/]*)$/;

/**
 * Answers one request of the HTTP API. Every route returns a status code a
 * client can act on; an unexpected failure is logged and answered with 500,
 * without its details.
 */
export async function answer(
  request: ApiRequest,
  store: ItemStore,
  log: Log,
): Promise<HttpResponse> {
  try {
    return await route(request, store);
  } catch (error: unknown) {
    log({
      level: 'ERROR',
      message: 'The request failed.',
      requestId: request.requestId,
      method: request.method,
      path: request.path,
      error: error instanceof Error ? error.message : String(error),
    });
    return problem(500, 'Something went wrong. Try again later.');
  }
}

async function route(request: ApiRequest, store: ItemStore): Promise<HttpResponse> {
  // The load balancer's health check: the process is up and serving.
  if (request.path === '/health') {
    return request.method === 'GET'
      ? json(200, { status: 'ok' })
      : problem(405, 'Only GET /health.');
  }
  const match = ITEM_PATH.exec(request.path);
  if (match === null) {
    return problem(404, `No route ${request.method} ${request.path}.`);
  }
  const id = checkId(decodePathSegment(match[1]));
  if (!id.ok) {
    return problem(400, id.error);
  }
  switch (request.method) {
    case 'GET': {
      const item = await store.get(id.value);
      return item === undefined ? problem(404, `No item ${id.value}.`) : json(200, item);
    }
    case 'PUT': {
      const item = checkItem(id.value, request.body);
      if (!item.ok) {
        return problem(400, item.error);
      }
      // A put replaces the whole item, so repeating a request is harmless.
      await store.put(item.value);
      return json(200, item.value);
    }
    default:
      return problem(405, `No route ${request.method} /items/{id}.`);
  }
}

/** A path segment without its percent-encoding; undefined when it is malformed. */
function decodePathSegment(segment: string | undefined): string | undefined {
  if (segment === undefined) {
    return undefined;
  }
  try {
    return decodeURIComponent(segment);
  } catch {
    return undefined;
  }
}
