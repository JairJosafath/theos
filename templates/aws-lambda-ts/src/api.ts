import type { APIGatewayProxyEventV2 } from 'aws-lambda';

import type { ItemStore } from './item_store';
import { checkId, checkItem, json, problem } from './items';
import type { HttpResponse } from './items';

/** Where the API logs a failure: a message and what helps find its cause. */
export type Log = (message: string, attributes: Readonly<Record<string, unknown>>) => void;

/** Does the work of one request, throwing on a failure no client can fix. */
export type Route = (event: APIGatewayProxyEventV2) => Promise<HttpResponse>;

/**
 * Answers one request of the HTTP API with what `route` returns. Every route
 * returns a status code a client can act on; an unexpected failure is logged
 * and answered with 500, without its details.
 */
export async function answer(
  event: APIGatewayProxyEventV2,
  route: Route,
  log: Log,
): Promise<HttpResponse> {
  try {
    return await route(event);
  } catch (error: unknown) {
    log('The request failed.', {
      requestId: event.requestContext.requestId,
      routeKey: event.routeKey,
      error: error instanceof Error ? error.message : String(error),
    });
    return problem(500, 'Something went wrong. Try again later.');
  }
}

/** The route that changes data, which a client may send again with the same Idempotency-Key. */
export const PUT_ITEM = 'PUT /items/{id}';

/**
 * Routes one request to `store` and answers it. What the store throws is
 * thrown on, for `answer` to log and answer with 500.
 */
export async function route(
  event: APIGatewayProxyEventV2,
  store: ItemStore,
): Promise<HttpResponse> {
  const id = checkId(event.pathParameters?.['id']);
  if (!id.ok) {
    return problem(400, id.error);
  }
  switch (event.routeKey) {
    case 'GET /items/{id}': {
      const item = await store.get(id.value);
      return item === undefined ? problem(404, `No item ${id.value}.`) : json(200, item);
    }
    case PUT_ITEM: {
      const body =
        event.isBase64Encoded && event.body !== undefined
          ? Buffer.from(event.body, 'base64').toString('utf8')
          : event.body;
      const item = checkItem(id.value, body);
      if (!item.ok) {
        return problem(400, item.error);
      }
      // A put replaces the whole item, so repeating a request is harmless.
      await store.put(item.value);
      return json(200, item.value);
    }
    default:
      return problem(404, `No route ${event.routeKey}.`);
  }
}
