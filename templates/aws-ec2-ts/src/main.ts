import { DynamoDBClient } from '@aws-sdk/client-dynamodb';
import { DynamoDBDocumentClient } from '@aws-sdk/lib-dynamodb';

import type { Log } from './api';
import { bearerAuthenticator, cognitoVerifier } from './auth';
import { DynamoDbItemStore } from './item_store';
import { createApiServer } from './server';

// The entry point the instances run (`node server.mjs` under systemd): wiring
// only, the work is in `src/api.ts`. The stack's user data sets the
// environment.

const tableName = process.env['TABLE_NAME'];
if (tableName === undefined || tableName === '') {
  throw new Error('TABLE_NAME is not set; the stack sets it to the table the service uses.');
}
// The Cognito user pool and app client whose access tokens the service
// accepts on every route but /health.
const userPoolId = process.env['USER_POOL_ID'];
const userPoolClientId = process.env['USER_POOL_CLIENT_ID'];
if (
  userPoolId === undefined ||
  userPoolId === '' ||
  userPoolClientId === undefined ||
  userPoolClientId === ''
) {
  throw new Error(
    'USER_POOL_ID and USER_POOL_CLIENT_ID must be set; the stack sets them to its user pool.',
  );
}
const port = Number(process.env['PORT'] ?? '8080');

// systemd writes what the server prints to a file the CloudWatch agent ships,
// one entry a line.
const log: Log = (entry) => {
  process.stdout.write(`${JSON.stringify(entry)}\n`);
};

const store = new DynamoDbItemStore(DynamoDBDocumentClient.from(new DynamoDBClient({})), tableName);
const authenticate = bearerAuthenticator(cognitoVerifier(userPoolId, userPoolClientId));
const server = createApiServer(store, log, authenticate);
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
