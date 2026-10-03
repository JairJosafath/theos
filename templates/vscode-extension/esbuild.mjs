// Bundles the extension into out/extension.js, the `main` VS Code loads.
//
//   node esbuild.mjs                 a development build, with a source map
//   node esbuild.mjs --production    a minified build, as packaged
//   node esbuild.mjs --watch         rebuilds on every change
import { argv } from 'node:process';

import * as esbuild from 'esbuild';

const production = argv.includes('--production');

/** @type {import('esbuild').BuildOptions} */
const options = {
  entryPoints: ['src/extension.ts'],
  outfile: 'out/extension.js',
  bundle: true,
  // VS Code loads extensions as CommonJS, in its own Node: Node 20 for
  // engines.vscode ^1.100.0.
  format: 'cjs',
  platform: 'node',
  target: 'node20',
  // VS Code provides the vscode module at run time.
  external: ['vscode'],
  minify: production,
  sourcemap: !production,
  sourcesContent: false,
  logLevel: 'info',
};

if (argv.includes('--watch')) {
  const context = await esbuild.context(options);
  await context.watch();
} else {
  await esbuild.build(options);
}
