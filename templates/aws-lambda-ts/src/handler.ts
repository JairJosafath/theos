import { DynamoDBClient } from '@aws-sdk/client-dynamodb';
import { DynamoDBDocumentClient } from '@aws-sdk/lib-dynamodb';
import { IdempotencyConfig, makeIdempotent } from '@aws-lambda-powertools/idempotency';
import { DynamoDBPersistenceLayer } from '@aws-lambda-powertools/idempotency/dynamodb';
import { Logger } from '@aws-lambda-powertools/logger';
import { Metrics, MetricUnit } from '@aws-lambda-powertools/metrics';
import type {
  APIGatewayProxyEventV2,
  APIGatewayProxyStructuredResultV2,
  Context,
} from 'aws-lambda';

import { answer, PUT_ITEM, route } from './api';
import type { Log } from './api';
import { DynamoDbItemStore } from './item_store';
import { answeringOnce, keepingOnlySuccess } from './once';

/** The value of an environment variable the stack sets. */
function setting(name: string): string {
  const value = process.env[name];
  if (value === undefined || value === '') {
    throw new Error(`${name} is not set; the stack sets it.`);
  }
  return value;
}

// Created once per execution environment, outside the handler, so warm
// invocations reuse the clients and their connections. Powertools reads the
// service's name and the metrics' namespace from the variables the stack sets.
const dynamoDb = new DynamoDBClient({});
const store = new DynamoDbItemStore(DynamoDBDocumentClient.from(dynamoDb), setting('TABLE_NAME'));
const logger = new Logger();
const metrics = new Metrics();
const log: Log = (message, attributes) => {
  logger.error(message, { ...attributes });
};

// A PUT sent again with the same Idempotency-Key header gets the first answer
// back and writes nothing twice; one without the header is simply run. Only
// what changed something is kept: a refusal or a failure keeps no record, so
// a corrected retry runs again. The key sent with another path or body is
// refused (422), and so is a retry while the first is still running (409).
const idempotency = new IdempotencyConfig({
  eventKeyJmesPath: 'headers."idempotency-key"',
  payloadValidationJmesPath: '[rawPath, body]',
  throwOnNoIdempotencyKey: false,
});
const putOnce = answeringOnce(
  makeIdempotent(
    keepingOnlySuccess((event: APIGatewayProxyEventV2) => route(event, store)),
    {
      persistenceStore: new DynamoDBPersistenceLayer({
        tableName: setting('IDEMPOTENCY_TABLE_NAME'),
        awsSdkV3Client: dynamoDb,
      }),
      config: idempotency,
    },
  ),
);

/** The Lambda entry point the stack names: wiring only, the work is in `route`. */
export async function handler(
  event: APIGatewayProxyEventV2,
  context: Context,
): Promise<APIGatewayProxyStructuredResultV2> {
  logger.addContext(context);
  idempotency.registerLambdaContext(context);
  metrics.captureColdStartMetric();
  try {
    const response = await answer(
      event,
      event.routeKey === PUT_ITEM ? putOnce : (request) => route(request, store),
      log,
    );
    metrics.addMetric(
      response.statusCode >= 500 ? 'FailedRequests' : 'AnsweredRequests',
      MetricUnit.Count,
      1,
    );
    return response;
  } finally {
    metrics.publishStoredMetrics();
  }
}
