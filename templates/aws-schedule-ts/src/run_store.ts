import { PutCommand } from '@aws-sdk/lib-dynamodb';
import type { PutCommandOutput } from '@aws-sdk/lib-dynamodb';

import type { RunRecord } from './runs';

/** Where the job keeps its runs. The job's logic depends on this, never on DynamoDB. */
export interface RunStore {
  put(record: RunRecord): Promise<void>;
}

/**
 * The one method of DynamoDB's document client the store calls, narrowed to
 * the command it sends, so a test passes a fake without the network.
 */
export interface DocumentSender {
  send(command: PutCommand): Promise<PutCommandOutput>;
}

/** Runs in a DynamoDB table whose partition key is the string `id`, the scheduled time. */
export class DynamoDbRunStore implements RunStore {
  constructor(
    private readonly client: DocumentSender,
    private readonly tableName: string,
  ) {}

  async put(record: RunRecord): Promise<void> {
    await this.client.send(new PutCommand({ TableName: this.tableName, Item: { ...record } }));
  }
}
