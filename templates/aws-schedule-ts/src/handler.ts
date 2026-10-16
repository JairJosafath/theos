import { DynamoDBClient } from '@aws-sdk/client-dynamodb';
import { DynamoDBDocumentClient } from '@aws-sdk/lib-dynamodb';
import { IdempotencyConfig, makeIdempotent } from '@aws-lambda-powertools/idempotency';
import { DynamoDBPersistenceLayer } from '@aws-lambda-powertools/idempotency/dynamodb';
import { Logger } from '@aws-lambda-powertools/logger';
import { Metrics, MetricUnit } from '@aws-lambda-powertools/metrics';
import type { Context } from 'aws-lambda';

import { checkingFirst, reporting, runJob } from './job';
import type { Report } from './job';
import { DynamoDbRunStore } from './run_store';

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
const store = new DynamoDbRunStore(DynamoDBDocumentClient.from(dynamoDb), setting('TABLE_NAME'));
const logger = new Logger();
const metrics = new Metrics();
const report: Report = {
  completed: (run) => {
    metrics.addMetric('CompletedRuns', MetricUnit.Count, 1);
    logger.info('The run finished.', { ...run });
  },
  failed: (event, error) => {
    metrics.addMetric('FailedRuns', MetricUnit.Count, 1);
    logger.error('The run failed; Lambda retries it.', { event, error });
  },
};

// The schedule and Lambda both deliver a run at least once: the same run
// delivered again, with the same scheduledTime, finds its record and does
// nothing twice. Only what finished is kept: a failed run keeps no record,
// so its retry runs again. A malformed event is refused before it is recorded.
// A record lasts a day: longer than the schedule's hour of retries and
// Lambda's hour after it, so a late delivery still finds it.
const idempotency = new IdempotencyConfig({
  eventKeyJmesPath: 'scheduledTime',
  expiresAfterSeconds: 24 * 60 * 60,
});
const runOnce = makeIdempotent(
  async (event: unknown) => {
    await runJob(event, store, () => new Date());
  },
  {
    persistenceStore: new DynamoDBPersistenceLayer({
      tableName: setting('IDEMPOTENCY_TABLE_NAME'),
      awsSdkV3Client: dynamoDb,
    }),
    config: idempotency,
  },
);

/**
 * The Lambda entry point the stack names, which the schedule invokes
 * asynchronously: wiring only, the work is in `runJob`. A run that throws is
 * retried by Lambda and then sent to the dead-letter queue.
 */
export async function handler(event: unknown, context: Context): Promise<void> {
  logger.addContext(context);
  idempotency.registerLambdaContext(context);
  metrics.captureColdStartMetric();
  try {
    await reporting(checkingFirst(runOnce), report)(event);
  } finally {
    metrics.publishStoredMetrics();
  }
}
