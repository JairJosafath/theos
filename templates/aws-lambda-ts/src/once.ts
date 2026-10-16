import {
  IdempotencyAlreadyInProgressError,
  IdempotencyValidationError,
} from '@aws-lambda-powertools/idempotency';

import type { Route } from './api';
import { problem } from './items';
import type { HttpResponse } from './items';

/**
 * A refusal (4xx) thrown out of the idempotent work, so Powertools keeps no
 * record of it and a corrected request with the same key runs again.
 */
export class Refused extends Error {
  constructor(readonly response: HttpResponse) {
    super(`Refused with ${String(response.statusCode)}.`);
  }
}

/** `route`, throwing its refusals as `Refused`: only what changed something is kept. */
export function keepingOnlySuccess(route: Route): Route {
  return async (event) => {
    const response = await route(event);
    if (response.statusCode >= 400 && response.statusCode < 500) {
      throw new Refused(response);
    }
    return response;
  };
}

/**
 * `once`, the idempotent work, answering what it throws that a client can
 * act on: a refusal as it was, a request still running with the same key
 * with 409, and the key sent with another request with 422. Anything else
 * is thrown on, for `answer` to log and answer with 500.
 */
export function answeringOnce(once: Route): Route {
  return async (event) => {
    try {
      return await once(event);
    } catch (error: unknown) {
      if (error instanceof Refused) {
        return error.response;
      }
      if (error instanceof IdempotencyAlreadyInProgressError) {
        return problem(409, 'The same request is still running. Try again shortly.');
      }
      if (error instanceof IdempotencyValidationError) {
        return problem(422, 'This Idempotency-Key was sent with another request.');
      }
      throw error;
    }
  };
}
