// The program's entry point, which esbuild bundles into out/cli.js: it hands
// the command line and the process's streams to run and exits with its code.
import { argv, stderr, stdout } from 'node:process';

import { run } from './cli';

// Piped into a reader that stops early (`| head`), writes fail with EPIPE.
// That is the reader's choice, not a crash: stop quietly, without a trace.
stdout.on('error', (error: NodeJS.ErrnoException) => {
  if (error.code !== 'EPIPE') {
    throw error;
  }
});

// Setting exitCode instead of calling process.exit lets pending output flush.
process.exitCode = run(argv.slice(2), {
  stdout: (text) => stdout.write(text),
  stderr: (text) => stderr.write(text),
});
