import { GetCommand, PutCommand } from '@aws-sdk/lib-dynamodb';
import type { GetCommandOutput, PutCommandOutput } from '@aws-sdk/lib-dynamodb';

import { checkFields } from './items';
import type { Item } from './items';

/** Where items live. The handler's logic depends on this, never on DynamoDB. */
export interface ItemStore {
  get(id: string): Promise<Item | undefined>;
  put(item: Item): Promise<void>;
}

/**
 * The one method of DynamoDB's document client the store calls, narrowed to
 * the commands it sends, so a test passes a fake without the network.
 */
export interface DocumentSender {
  send(command: GetCommand | PutCommand): Promise<GetCommandOutput | PutCommandOutput>;
}

/** Items in a DynamoDB table whose partition key is the string `id`. */
export class DynamoDbItemStore implements ItemStore {
  constructor(
    private readonly client: DocumentSender,
    private readonly tableName: string,
  ) {}

  async get(id: string): Promise<Item | undefined> {
    const output = await this.client.send(
      new GetCommand({ TableName: this.tableName, Key: { id } }),
    );
    if (!('Item' in output) || output.Item === undefined) {
      return undefined;
    }
    const checked = checkFields(id, output.Item);
    if (!checked.ok) {
      throw new Error(`Item ${id} in table ${this.tableName} is malformed: ${checked.error}`);
    }
    return checked.value;
  }

  async put(item: Item): Promise<void> {
    await this.client.send(new PutCommand({ TableName: this.tableName, Item: { ...item } }));
  }
}
