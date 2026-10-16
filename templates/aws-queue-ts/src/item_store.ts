import { PutCommand } from '@aws-sdk/lib-dynamodb';
import type { PutCommandOutput } from '@aws-sdk/lib-dynamodb';

import type { Item } from './items';

/** Where items go. The worker's logic depends on this, never on DynamoDB. */
export interface ItemStore {
  put(item: Item): Promise<void>;
}

/**
 * The one method of DynamoDB's document client the store calls, narrowed to
 * the command it sends, so a test passes a fake without the network.
 */
export interface DocumentSender {
  send(command: PutCommand): Promise<PutCommandOutput>;
}

/** Items in a DynamoDB table whose partition key is the string `id`. */
export class DynamoDbItemStore implements ItemStore {
  constructor(
    private readonly client: DocumentSender,
    private readonly tableName: string,
  ) {}

  async put(item: Item): Promise<void> {
    await this.client.send(new PutCommand({ TableName: this.tableName, Item: { ...item } }));
  }
}
