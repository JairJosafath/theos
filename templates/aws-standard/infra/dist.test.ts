// What `make dist` writes, checked on a probe app with one asset of each kind
// and on the Framework's own stacks for each tier, synthesized the way
// `make synth` synthesizes them: offline, without Docker or credentials. A
// failing case here means a release would ship an assembly a deploy target
// can't read. Changing this file is the Standard's job, not a task's.
import { App, Stack } from 'aws-cdk-lib';
import { Code, Function as LambdaFunction, Runtime } from 'aws-cdk-lib/aws-lambda';
import { Asset } from 'aws-cdk-lib/aws-s3-assets';
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  rmSync,
  statSync,
  writeFileSync,
} from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { inflateRawSync } from 'node:zlib';
import { afterAll, describe, expect, it } from 'vitest';

import { writeDist, zipDirectory } from './dist';
import type { TierAssembly } from './dist';
import { defineStacks } from './stacks';
import { applyStandard, TIERS, tierOf } from './standard';
import type { Tier } from './standard';

const scratch = mkdtempSync(path.join(tmpdir(), 'dist-'));

afterAll(() => {
  rmSync(scratch, { recursive: true, force: true });
});

type Json = Record<string, unknown>;

function isRecord(value: unknown): value is Json {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function readJson(file: string): Json {
  const parsed: unknown = JSON.parse(readFileSync(file, 'utf8'));
  if (!isRecord(parsed)) {
    throw new Error(`${file} is not a JSON object.`);
  }
  return parsed;
}

/** The record at `key` in `parent`, or an empty one. */
function recordAt(parent: Json, key: string): Json {
  const value = parent[key];
  return isRecord(value) ? value : {};
}

/** The feature flags `cdk synth` reads from cdk.json, so the tests build the same app. */
function cdkContext(): Json {
  const cdkJson = readJson(path.join(import.meta.dirname, '..', 'cdk.json'));
  return recordAt(cdkJson, 'context');
}

/** A new, empty folder under the scratch folder. */
function folder(name: string): string {
  const dir = path.join(scratch, name);
  mkdirSync(dir, { recursive: true });
  return dir;
}

/** The files a zip holds, by name, read from its central directory. */
function unzip(zip: Buffer): Map<string, string> {
  const end = zip.lastIndexOf(Buffer.from([0x50, 0x4b, 0x05, 0x06]));
  const count = zip.readUInt16LE(end + 10);
  let at = zip.readUInt32LE(end + 16);
  const files = new Map<string, string>();
  for (let i = 0; i < count; i++) {
    expect(zip.readUInt32LE(at)).toBe(0x02014b50);
    const size = zip.readUInt32LE(at + 20);
    const nameLength = zip.readUInt16LE(at + 28);
    const local = zip.readUInt32LE(at + 42);
    const name = zip.toString('utf8', at + 46, at + 46 + nameLength);
    const data = local + 30 + zip.readUInt16LE(local + 26) + zip.readUInt16LE(local + 28);
    files.set(name, inflateRawSync(zip.subarray(data, data + size)).toString('utf8'));
    at += 46 + nameLength;
  }
  return files;
}

/** Each tier's assembly of a probe app with a directory asset and a file asset. */
function probe(): TierAssembly[] {
  const code = folder('probe-code');
  mkdirSync(path.join(code, 'lib'), { recursive: true });
  writeFileSync(path.join(code, 'index.js'), 'exports.handler = async () => "ok";\n');
  writeFileSync(path.join(code, 'lib', 'util.js'), 'module.exports = 1;\n');
  const page = path.join(folder('probe-file'), 'page.html');
  writeFileSync(page, '<p>ok</p>\n');
  return TIERS.map((tier) => {
    const outdir = folder(`probe-${tier}`);
    const app = new App({ outdir, context: { ...cdkContext(), tier } });
    const stack = new Stack(app, `probe-${tier}`);
    new LambdaFunction(stack, 'Fn', {
      runtime: Runtime.NODEJS_22_X,
      handler: 'index.handler',
      code: Code.fromAsset(code),
    });
    new Asset(stack, 'Page', { path: page });
    app.synth();
    return { tier, dir: outdir };
  });
}

/** Every file the flat assembly's manifests and asset manifests name. */
function named(out: string): string[] {
  const names: string[] = [];
  for (const tier of TIERS) {
    const manifest = `${tier}.manifest.json`;
    names.push(manifest);
    for (const artifact of Object.values(
      recordAt(readJson(path.join(out, manifest)), 'artifacts'),
    )) {
      const properties = isRecord(artifact) ? recordAt(artifact, 'properties') : {};
      for (const key of ['templateFile', 'file']) {
        const file = properties[key];
        if (typeof file === 'string') {
          names.push(file);
        }
      }
    }
  }
  for (const assets of names.filter((name) => name.endsWith('.assets.json'))) {
    for (const asset of Object.values(recordAt(readJson(path.join(out, assets)), 'files'))) {
      const source = isRecord(asset) ? recordAt(asset, 'source') : {};
      if (typeof source['path'] === 'string') {
        names.push(source['path']);
      }
    }
  }
  return [...new Set(names)].sort();
}

describe('make dist', () => {
  it('writes each tier flat, with its paths rewritten to the flat names', () => {
    const out = folder('probe-dist');
    writeDist(probe(), out);
    const written = readdirSync(out).sort();
    for (const name of written) {
      expect(statSync(path.join(out, name)).isFile(), `${name} is a file`).toBe(true);
    }
    // Everything it writes is named, and everything named is written.
    expect(written.filter((name) => name !== '.gitignore')).toEqual(named(out));
    expect(readFileSync(path.join(out, '.gitignore'), 'utf8')).toBe('*\n');
    for (const tier of TIERS) {
      expect(written).toContain(`${tier}.probe-${tier}.template.json`);
      expect(written).toContain(`${tier}.probe-${tier}.assets.json`);
      const manifest = readJson(path.join(out, `${tier}.manifest.json`));
      expect(JSON.stringify(manifest)).not.toContain('cdk:tree');
    }
  });

  it('ships each asset once for both tiers, a directory zipped and a file as it is', () => {
    const out = folder('probe-dist-assets');
    writeDist(probe(), out);
    const assets = readdirSync(out).filter((name) => name.startsWith('asset.'));
    // The tiers' templates differ, but their two assets don't.
    expect(assets).toHaveLength(2);
    const zip = assets.find((name) => name.endsWith('.zip'));
    const html = assets.find((name) => name.endsWith('.html'));
    expect(zip).toBeDefined();
    expect(html).toBeDefined();
    expect(readFileSync(path.join(out, html ?? ''), 'utf8')).toBe('<p>ok</p>\n');
    expect(unzip(readFileSync(path.join(out, zip ?? '')))).toEqual(
      new Map([
        ['index.js', 'exports.handler = async () => "ok";\n'],
        ['lib/util.js', 'module.exports = 1;\n'],
      ]),
    );
    for (const tier of TIERS) {
      const files = recordAt(
        readJson(path.join(out, `${tier}.probe-${tier}.assets.json`)),
        'files',
      );
      const sources = Object.values(files).map((asset) =>
        isRecord(asset) ? recordAt(asset, 'source') : {},
      );
      expect(sources).toContainEqual({ path: zip, packaging: 'file' });
      expect(sources).toContainEqual(expect.objectContaining({ path: html }));
    }
  });

  it('zips the same files to the same bytes', () => {
    const dir = folder('zip-twice');
    writeFileSync(path.join(dir, 'a.txt'), 'a\n');
    expect(zipDirectory(dir).equals(zipDirectory(dir))).toBe(true);
  });

  it("writes the Framework's stacks for both tiers", { timeout: 300_000 }, () => {
    const tiers = TIERS.map((tier: Tier) => {
      const outdir = folder(`app-${tier}`);
      const app = new App({ outdir, context: { ...cdkContext(), tier } });
      // A stacks.ts written before tiers takes the app alone, and fits this too.
      const define: (app: App, tier: Tier) => Stack[] = defineStacks;
      define(app, tierOf(app));
      applyStandard(app, tier);
      app.synth();
      return { tier, dir: outdir };
    });
    const out = folder('app-dist');
    writeDist(tiers, out);
    const written = readdirSync(out).sort();
    expect(written.filter((name) => name !== '.gitignore')).toEqual(named(out));
    for (const tier of TIERS) {
      expect(written).toContain(`${tier}.manifest.json`);
      expect(
        written.some((name) => name.startsWith(`${tier}.`) && name.endsWith('.template.json')),
      ).toBe(true);
    }
    expect(existsSync(path.join(out, 'manifest.json'))).toBe(false);
  });
});
