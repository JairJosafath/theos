import { BatchProcessor, EventType, processPartialResponse } from '@aws-lambda-powertools/batch';
import type { SQSBatchResponse, SQSEvent } from 'aws-lambda';

import type { Work } from './consume';

const processor = new BatchProcessor(EventType.SQS);

/**
 * Runs `work` on every message of `event`, one at a time, and answers the
 * ones it threw on as the batch's item failures: the event source mapping
 * reports them (`ReportBatchItemFailures`), so SQS deletes the rest and
 * delivers only those again. When every message fails it throws Powertools'
 * `FullBatchFailureError`: the invocation fails, and the function's error
 * alarm fires.
 */
export async function consumeBatch(event: SQSEvent, work: Work): Promise<SQSBatchResponse> {
  return processPartialResponse(event, work, processor);
}
