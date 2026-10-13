import { BundlingOutput, DockerImage } from 'aws-cdk-lib';
import { Asset } from 'aws-cdk-lib/aws-s3-assets';
import type { Construct } from 'constructs';
import { buildSync } from 'esbuild';
import path from 'node:path';

// The repository root: the server's code and node_modules are there.
const ROOT = path.join(import.meta.dirname, '..');

/**
 * The server, bundled by the installed esbuild into one file, `server.mjs`,
 * with the AWS SDK pinned in package-lock.json, and staged as an S3 asset the
 * instances download at boot. Synth bundles it locally; never with Docker,
 * which CI doesn't have.
 */
export function serverAsset(scope: Construct, id: string): Asset {
  return new Asset(scope, id, {
    path: path.join(ROOT, 'src'),
    bundling: {
      // Required by the type; local bundling always succeeds, so it is never pulled.
      image: DockerImage.fromRegistry('unused'),
      outputType: BundlingOutput.SINGLE_FILE,
      local: {
        tryBundle(outputDir: string): boolean {
          buildSync({
            absWorkingDir: ROOT,
            entryPoints: [path.join(ROOT, 'src', 'main.ts')],
            outfile: path.join(outputDir, 'server.mjs'),
            bundle: true,
            platform: 'node',
            target: 'node22',
            format: 'esm',
            // Some of the SDK's dependencies call require(); give the bundle one.
            banner: {
              js: "import { createRequire } from 'node:module'; const require = createRequire(import.meta.url);",
            },
            logLevel: 'warning',
          });
          return true;
        },
      },
    },
  });
}
