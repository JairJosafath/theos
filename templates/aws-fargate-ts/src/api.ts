import type { ItemStore } from './item_store';
import { checkId, checkItem, json, problem } from './items';
import type { HttpResponse } from './items';

/** Where the service writes its structured logs: one JSON object per entry. */
export type Log = (entry: Readonly<Record<string, unknown>>) => void;

/** One request, as the server read it: free of Node's HTTP types. */
export interface ApiRequest {
  readonly method: string;
  /** The path without its query string, as the client sent it. */
  readonly path: string;
  /** The body, decoded as UTF-8; empty when there is none. */
  readonly body: string;
  /** The load balancer's trace id, or one the server made up. */
  readonly requestId: string;
}

// `/items/{id}`: the id is checked by checkId, so any one segment matches.
const ITEM_PATH = /^\/items\/([^/]*)$/;

/**
 * Answers one request. Every route returns a status code a client can act on;
 * an unexpected failure is logged and answered with 500, without its details.
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
  if (request.path === '/health') {
    // The load balancer's health check: the process answers, so it is alive.
    // It reads no table, so an outage of DynamoDB doesn't stop every task.
    return request.method === 'GET'
      ? json(200, { status: 'ok' })
      : problem(405, `${request.method} is not allowed on /health.`);
  }
  const match = ITEM_PATH.exec(request.path);
  if (match === null) {
    return problem(404, `No route ${request.path}.`);
  }
  const id = checkId(decodeSegment(match[1]));
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
      return problem(405, `${request.method} is not allowed on /items/{id}.`);
  }
}

/** A percent-encoded path segment, or undefined when it doesn't decode. */
function decodeSegment(segment: string | undefined): string | undefined {
  try {
    return segment === undefined ? undefined : decodeURIComponent(segment);
  } catch {
    return undefined;
  }
}
