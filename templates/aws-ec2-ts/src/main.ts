import { DynamoDBClient } from '@aws-sdk/client-dynamodb';
import { DynamoDBDocumentClient } from '@aws-sdk/lib-dynamodb';

import type { Log } from './api';
import { DynamoDbItemStore } from './item_store';
import { createApiServer } from './server';

// The entry point the instances run (`node server.mjs` under systemd): wiring
// only, the work is in `src/api.ts`. The stack's user data sets the
// environment.

const tableName = process.env['TABLE_NAME'];
if (tableName === undefined || tableName === '') {
  throw new Error('TABLE_NAME is not set; the stack sets it to the table the service uses.');
}
const port = Number(process.env['PORT'] ?? '8080');

// systemd writes what the server prints to a file the CloudWatch agent ships,
// one entry a line.
const log: Log = (entry) => {
  process.stdout.write(`${JSON.stringify(entry)}\n`);
};

const store = new DynamoDbItemStore(DynamoDBDocumentClient.from(new DynamoDBClient({})), tableName);
const server = createApiServer(store, log);
server.listen(port, () => {
  log({ level: 'INFO', message: 'Listening.', port });
});

// The Auto Scaling group stops an instance by stopping the service: finish the
// requests in flight, then exit.
process.on('SIGTERM', () => {
  server.close(() => {
    log({ level: 'INFO', message: 'Stopped.' });
  });
  server.closeIdleConnections();
});
