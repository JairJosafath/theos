// First: tracing patches node:http as it loads, so it starts before the
// modules below load it.
import { tracing } from './start_tracing';

import { DynamoDBClient } from '@aws-sdk/client-dynamodb';
import { DynamoDBDocumentClient } from '@aws-sdk/lib-dynamodb';
import { createServer } from 'node:http';

import type { Log } from './api';
import { DynamoDbItemStore } from './item_store';
import { listener } from './server';

// The entry point the image runs: wiring only, the work is in `listener`.
// Configuration is read once, here, and the process fails at start-up when
// any is missing.
const tableName = process.env['TABLE_NAME'];
if (tableName === undefined || tableName === '') {
  throw new Error('TABLE_NAME is not set; the stack sets it to the table the service uses.');
}
const port = Number(process.env['PORT'] ?? '8080');
if (!Number.isInteger(port) || port <= 0 || port > 65_535) {
  throw new Error(`PORT must be a port number, not ${process.env['PORT'] ?? ''}.`);
}

// ECS sends what the container writes to CloudWatch Logs, one entry a line.
const log: Log = (entry) => {
  process.stderr.write(`${JSON.stringify(entry)}\n`);
};

const store = new DynamoDbItemStore(DynamoDBDocumentClient.from(new DynamoDBClient({})), tableName);
const listen = listener(store, log);
const server = createServer((request, response) => {
  void listen(request, response);
});
// Longer than the load balancer's 60-second idle timeout, so the server never
// closes a connection the load balancer is about to reuse.
server.keepAliveTimeout = 65_000;
server.headersTimeout = 66_000;

server.listen(port, () => {
  log({ level: 'INFO', message: 'Listening.', port });
});

// ECS stops a task with SIGTERM, then SIGKILL after the stop timeout: finish
// the requests in flight, take no new ones, and let the process end.
process.once('SIGTERM', () => {
  log({ level: 'INFO', message: 'Stopping.' });
  // Once the last request is answered, send the spans not yet sent.
  server.close(() => {
    void tracing?.shutdown();
  });
  server.closeIdleConnections();
});
