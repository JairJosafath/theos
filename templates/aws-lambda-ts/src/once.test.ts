import {
  IdempotencyAlreadyInProgressError,
  IdempotencyValidationError,
} from '@aws-lambda-powertools/idempotency';
import type { APIGatewayProxyEventV2 } from 'aws-lambda';
import { describe, expect, it } from 'vitest';

import type { Route } from './api';
import { json, problem } from './items';
import type { HttpResponse } from './items';
import { answeringOnce, keepingOnlySuccess, Refused } from './once';

// A request; the routes here read nothing of it.
const event: APIGatewayProxyEventV2 = {
  version: '2.0',
  routeKey: 'PUT /items/{id}',
  rawPath: '/items/i1',
  rawQueryString: '',
  headers: { 'idempotency-key': 'k1' },
  isBase64Encoded: false,
  requestContext: {
    accountId: '123456789012',
    apiId: 'api',
    domainName: 'example.com',
    domainPrefix: 'example',
    http: {
      method: 'PUT',
      path: '/items/i1',
      protocol: 'HTTP/1.1',
      sourceIp: '192.0.2.1',
      userAgent: 'test',
    },
    requestId: 'request-1',
    routeKey: 'PUT /items/{id}',
    stage: '$default',
    time: '13/Oct/2026:00:00:00 +0000',
    timeEpoch: 0,
  },
};

/** A route that answers `response`. */
function answers(response: HttpResponse): Route {
  return () => Promise.resolve(response);
}

/** A route that throws `error`. */
function throws(error: Error): Route {
  return () => Promise.reject(error);
}

describe('keepingOnlySuccess', () => {
  it('passes an answer that changed something through', async () => {
    const ok = json(200, { id: 'i1' });

    expect(await keepingOnlySuccess(answers(ok))(event)).toBe(ok);
  });

  it('throws a refusal, so no record of it is kept', async () => {
    const refusal = problem(400, 'The body must be JSON.');

    await expect(keepingOnlySuccess(answers(refusal))(event)).rejects.toEqual(new Refused(refusal));
  });

  it('passes a server error through, which answer handles', async () => {
    const failure = problem(500, 'Something went wrong. Try again later.');

    expect(await keepingOnlySuccess(answers(failure))(event)).toBe(failure);
  });
});

describe('answeringOnce', () => {
  it('answers a refusal as the route did', async () => {
    const refusal = problem(400, 'The body must be JSON.');

    expect(await answeringOnce(throws(new Refused(refusal)))(event)).toBe(refusal);
  });

  it('answers 409 while the same request is still running', async () => {
    const response = await answeringOnce(throws(new IdempotencyAlreadyInProgressError('key')))(
      event,
    );

    expect(response.statusCode).toBe(409);
  });

  it('answers 422 when the key was sent with another request', async () => {
    const response = await answeringOnce(throws(new IdempotencyValidationError('key')))(event);

    expect(response.statusCode).toBe(422);
  });

  it('throws anything else on, for answer to log', async () => {
    await expect(answeringOnce(throws(new Error('timeout')))(event)).rejects.toThrow('timeout');
  });
});
