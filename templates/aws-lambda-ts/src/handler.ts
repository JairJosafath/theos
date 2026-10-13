import { DynamoDBClient } from '@aws-sdk/client-dynamodb';
import { DynamoDBDocumentClient } from '@aws-sdk/lib-dynamodb';
import type { APIGatewayProxyEventV2, APIGatewayProxyStructuredResultV2 } from 'aws-lambda';

import { answer } from './api';
import type { Log } from './api';
import { DynamoDbItemStore } from './item_store';

// Created once per execution environment, outside the handler, so warm
// invocations reuse the client and its connections.
const tableName = process.env['TABLE_NAME'];
if (tableName === undefined || tableName === '') {
  throw new Error('TABLE_NAME is not set; the stack sets it to the table the function uses.');
}
const store = new DynamoDbItemStore(DynamoDBDocumentClient.from(new DynamoDBClient({})), tableName);

// Lambda sends what the function writes to CloudWatch Logs, one entry a line.
const log: Log = (entry) => {
  process.stderr.write(`${JSON.stringify(entry)}\n`);
};

/** The Lambda entry point the stack names: wiring only, the work is in `answer`. */
export async function handler(
  event: APIGatewayProxyEventV2,
): Promise<APIGatewayProxyStructuredResultV2> {
  return answer(event, store, log);
}
