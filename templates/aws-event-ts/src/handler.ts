import { DynamoDBClient } from '@aws-sdk/client-dynamodb';
import { DynamoDBDocumentClient } from '@aws-sdk/lib-dynamodb';
import { IdempotencyConfig, makeIdempotent } from '@aws-lambda-powertools/idempotency';
import { DynamoDBPersistenceLayer } from '@aws-lambda-powertools/idempotency/dynamodb';
import { Logger } from '@aws-lambda-powertools/logger';
import { Metrics, MetricUnit } from '@aws-lambda-powertools/metrics';
import type { Context } from 'aws-lambda';

import { checkingFirst, consume, reporting } from './consume';
import type { Report } from './consume';
import { DynamoDbItemStore } from './item_store';

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
const report: Report = {
  consumed: (event) => {
    metrics.addMetric('ConsumedEvents', MetricUnit.Count, 1);
    logger.info('The event was consumed.', { eventId: event.id, itemId: event.item.id });
  },
  failed: (event, error) => {
    metrics.addMetric('FailedEvents', MetricUnit.Count, 1);
    // Only the event's id: its detail may hold user data.
    const eventId =
      typeof event === 'object' && event !== null && 'id' in event ? event.id : undefined;
    logger.error('The event failed; Lambda retries it.', { eventId, error });
  },
};

// EventBridge and Lambda both deliver an event at least once: the same event
// delivered again, with the same id, finds its record and writes nothing
// twice. Only what was consumed is kept: a failed event keeps no record, so
// its retry runs again. A malformed event is refused before it is recorded.
// A record lasts a day: longer than the rule's hour of retries and Lambda's
// hour after it, so a late delivery still finds it.
const idempotency = new IdempotencyConfig({
  eventKeyJmesPath: 'id',
  expiresAfterSeconds: 24 * 60 * 60,
});
const consumeOnce = makeIdempotent((event: unknown) => consume(event, store), {
  persistenceStore: new DynamoDBPersistenceLayer({
    tableName: setting('IDEMPOTENCY_TABLE_NAME'),
    awsSdkV3Client: dynamoDb,
  }),
  config: idempotency,
});

/**
 * The Lambda entry point the stack names, which the rule invokes
 * asynchronously: wiring only, the work is in `consume`. An event that throws
 * is retried by Lambda and then sent to the dead-letter queue.
 */
export async function handler(event: unknown, context: Context): Promise<void> {
  logger.addContext(context);
  idempotency.registerLambdaContext(context);
  metrics.captureColdStartMetric();
  try {
    await reporting(checkingFirst(consumeOnce), report)(event);
  } finally {
    metrics.publishStoredMetrics();
  }
}
