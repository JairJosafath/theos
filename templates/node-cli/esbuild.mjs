// Bundles the program into out/cli.js, the file package.json's bin runs.
//
//   node esbuild.mjs           build once (npm pack runs this first)
//   node esbuild.mjs --watch   rebuild on every change
import { argv } from 'node:process';

import * as esbuild from 'esbuild';

/** @type {import('esbuild').BuildOptions} */
const options = {
  entryPoints: ['src/main.ts'],
  outfile: 'out/cli.js',
  bundle: true,
  // One ES module for Node 22 and later (engines.node), with every dependency
  // inside: the package needs no node_modules of its own.
  format: 'esm',
  platform: 'node',
  target: 'node22',
  banner: {
    // The shebang makes the bin runnable. createRequire lets a bundled
    // CommonJS dependency require Node's built-in modules from an ES module.
    js: [
      '#!/usr/bin/env node',
      "import { createRequire } from 'node:module';",
      'const require = createRequire(import.meta.url);',
    ].join('\n'),
  },
  // Readable stack traces in bug reports: no minifying, no source map to ship.
  minify: false,
  logLevel: 'info',
};

if (argv.includes('--watch')) {
  const context = await esbuild.context(options);
  await context.watch();
} else {
  await esbuild.build(options);
}
