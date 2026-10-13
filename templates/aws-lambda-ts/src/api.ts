import type { APIGatewayProxyEventV2 } from 'aws-lambda';

import type { ItemStore } from './item_store';
import { checkId, checkItem, json, problem } from './items';
import type { HttpResponse } from './items';

/** Where the API writes its structured logs: one JSON object per entry. */
export type Log = (entry: Readonly<Record<string, unknown>>) => void;

/**
 * Answers one request of the HTTP API. Every route returns a status code a
 * client can act on; an unexpected failure is logged and answered with 500,
 * without its details.
 */
export async function answer(
  event: APIGatewayProxyEventV2,
  store: ItemStore,
  log: Log,
): Promise<HttpResponse> {
  try {
    return await route(event, store);
  } catch (error: unknown) {
    log({
      level: 'ERROR',
      message: 'The request failed.',
      requestId: event.requestContext.requestId,
      routeKey: event.routeKey,
      error: error instanceof Error ? error.message : String(error),
    });
    return problem(500, 'Something went wrong. Try again later.');
  }
}

async function route(event: APIGatewayProxyEventV2, store: ItemStore): Promise<HttpResponse> {
  const id = checkId(event.pathParameters?.['id']);
  if (!id.ok) {
    return problem(400, id.error);
  }
  switch (event.routeKey) {
    case 'GET /items/{id}': {
      const item = await store.get(id.value);
      return item === undefined ? problem(404, `No item ${id.value}.`) : json(200, item);
    }
    case 'PUT /items/{id}': {
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
