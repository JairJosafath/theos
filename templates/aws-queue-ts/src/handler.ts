import { DynamoDBClient } from '@aws-sdk/client-dynamodb';
import { DynamoDBDocumentClient } from '@aws-sdk/lib-dynamodb';
import { IdempotencyConfig, makeIdempotent } from '@aws-lambda-powertools/idempotency';
import { DynamoDBPersistenceLayer } from '@aws-lambda-powertools/idempotency/dynamodb';
import { Logger } from '@aws-lambda-powertools/logger';
import { Metrics, MetricUnit } from '@aws-lambda-powertools/metrics';
import type { Context, SQSBatchResponse, SQSEvent } from 'aws-lambda';

import { consumeBatch } from './batch';
import { checkingFirst, consume, reporting } from './consume';
import type { Message, Report } from './consume';
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
  consumed: () => {
    metrics.addMetric('ConsumedMessages', MetricUnit.Count, 1);
  },
  failed: (messageId, error) => {
    metrics.addMetric('FailedMessages', MetricUnit.Count, 1);
    logger.error('A message failed; SQS delivers it again.', { messageId, error });
  },
};

// SQS delivers a message at least once: the same message delivered again,
// with the same messageId, finds its record and writes nothing twice. Only
// what was consumed is kept: a failed message keeps no record, so its next
// delivery runs again. A malformed message is refused before it is recorded.
const idempotency = new IdempotencyConfig({ eventKeyJmesPath: 'messageId' });
const consumeOnce = makeIdempotent((message: Message) => consume(message, store), {
  persistenceStore: new DynamoDBPersistenceLayer({
    tableName: setting('IDEMPOTENCY_TABLE_NAME'),
    awsSdkV3Client: dynamoDb,
  }),
  config: idempotency,
});

/**
 * The Lambda entry point the stack names: wiring only, the work is in
 * `consume`, and `consumeBatch` answers the messages that failed.
 */
export async function handler(event: SQSEvent, context: Context): Promise<SQSBatchResponse> {
  logger.addContext(context);
  idempotency.registerLambdaContext(context);
  metrics.captureColdStartMetric();
  try {
    return await consumeBatch(event, reporting(checkingFirst(consumeOnce), report));
  } finally {
    metrics.publishStoredMetrics();
  }
}
