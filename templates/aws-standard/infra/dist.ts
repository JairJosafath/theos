// What a release ships (`make dist`): each tier's cloud assembly that
// `make synth` wrote to cdk.out/<tier>/, flat in one folder, for a
// `cloudFormation` deploy target to read file by file. Per tier and stack,
// `<tier>.manifest.json`, `<tier>.<stack>.template.json` and
// `<tier>.<stack>.assets.json`, their paths rewritten to the flat names; per
// file asset hash, once for both tiers, `asset.<hash>.zip` for a directory,
// zipped the way cdk-assets zips one, or `asset.<hash>.<ext>` for a file. A
// Docker image asset stays in its assets.json and is not shipped. dist/ holds
// a .gitignore of its own, so git never sees it. Nothing here
// deploys or calls AWS. Changing this file is the Standard's job, not a task's.
import {
  copyFileSync,
  existsSync,
  mkdirSync,
  readdirSync,
  readFileSync,
  statSync,
  writeFileSync,
} from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { crc32, deflateRawSync } from 'node:zlib';

/** One tier's cloud assembly: the tier and the folder `cdk synth -o` wrote. */
export interface TierAssembly {
  readonly tier: string;
  readonly dir: string;
}

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

function writeJson(file: string, value: unknown): void {
  writeFileSync(file, `${JSON.stringify(value, null, 2)}\n`);
}

/** The record at `key` in `parent`, refusing anything else. */
function recordAt(parent: Json, key: string, where: string): Json {
  const value = parent[key];
  if (!isRecord(value)) {
    throw new Error(`${where}: ${key} is not an object.`);
  }
  return value;
}

/** The string at `key` in `parent`, refusing anything else. */
function stringAt(parent: Json, key: string, where: string): string {
  const value = parent[key];
  if (typeof value !== 'string') {
    throw new Error(`${where}: ${key} is not a string.`);
  }
  return value;
}

/**
 * Writes the flat assembly of `tiers` into `out`, which it creates. Refuses
 * an assembly with a nested one (a CDK Stage), which the flat names can't
 * hold, and an asset built by a command.
 */
export function writeDist(tiers: readonly TierAssembly[], out: string): void {
  mkdirSync(out, { recursive: true });
  // The folder ignores itself, whatever the Framework's .gitignore says, and
  // a release attaches no dotfile.
  writeFileSync(path.join(out, '.gitignore'), '*\n');
  for (const { tier, dir } of tiers) {
    const where = path.join(dir, 'manifest.json');
    const manifest = readJson(where);
    const artifacts = recordAt(manifest, 'artifacts', where);
    // Every file the assembly names, by its name in cdk.out/<tier>/, mapped
    // to its flat name: the templates and the asset manifests.
    const renamed = new Map<string, string>();
    for (const [id, artifact] of Object.entries(artifacts)) {
      if (!isRecord(artifact)) {
        throw new Error(`${where}: artifact ${id} is not an object.`);
      }
      const type = stringAt(artifact, 'type', `${where}: artifact ${id}`);
      if (type === 'cdk:cloud-assembly') {
        throw new Error(
          `${where}: artifact ${id} is a nested assembly (a Stage), which a release can't ship.`,
        );
      }
      if (type === 'cdk:tree') {
        // The construct tree is for tools, not for a deploy.
        Reflect.deleteProperty(artifacts, id);
        continue;
      }
      const key = { 'aws:cloudformation:stack': 'templateFile', 'cdk:asset-manifest': 'file' }[
        type
      ];
      if (key === undefined) {
        continue;
      }
      const properties = recordAt(artifact, 'properties', `${where}: artifact ${id}`);
      const file = stringAt(properties, key, `${where}: artifact ${id}`);
      const flat = `${tier}.${file}`;
      renamed.set(file, flat);
      properties[key] = flat;
    }
    for (const [file, flat] of renamed) {
      if (!file.endsWith('.assets.json')) {
        copyFileSync(path.join(dir, file), path.join(out, flat));
      }
    }
    for (const [file, flat] of renamed) {
      if (file.endsWith('.assets.json')) {
        writeJson(path.join(out, flat), flattenAssets(path.join(dir, file), dir, out, renamed));
      }
    }
    writeJson(path.join(out, `${tier}.manifest.json`), manifest);
  }
}

/**
 * The asset manifest at `file` with each file asset's source path rewritten
 * to its flat name in `out`, where each asset is written once per hash.
 */
function flattenAssets(
  file: string,
  dir: string,
  out: string,
  renamed: ReadonlyMap<string, string>,
): Json {
  const assets = readJson(file);
  const files = assets['files'] ?? {};
  if (!isRecord(files)) {
    throw new Error(`${file}: files is not an object.`);
  }
  for (const [hash, asset] of Object.entries(files)) {
    const where = `${file}: asset ${hash}`;
    if (!isRecord(asset)) {
      throw new Error(`${where} is not an object.`);
    }
    const source = recordAt(asset, 'source', where);
    if (source['executable'] !== undefined) {
      throw new Error(`${where} is built by a command, which a release can't run.`);
    }
    const from = stringAt(source, 'path', where);
    const template = renamed.get(from);
    if (template !== undefined) {
      source['path'] = template;
      continue;
    }
    const full = path.join(dir, from);
    if (statSync(full).isDirectory()) {
      // Shipped zipped, as cdk-assets would upload it, so the target uploads
      // the file as it is.
      const flat = `asset.${hash}.zip`;
      if (!existsSync(path.join(out, flat))) {
        writeFileSync(path.join(out, flat), zipDirectory(full));
      }
      source['path'] = flat;
      source['packaging'] = 'file';
    } else {
      const flat = `asset.${hash}${path.extname(from)}`;
      if (!existsSync(path.join(out, flat))) {
        copyFileSync(full, path.join(out, flat));
      }
      source['path'] = flat;
    }
  }
  return assets;
}

/** Every file under `dir`, as `/`-separated paths relative to it, sorted. */
function filesUnder(dir: string, prefix = ''): string[] {
  return readdirSync(path.join(dir, prefix), { withFileTypes: true })
    .flatMap((entry) => {
      const name = prefix === '' ? entry.name : `${prefix}/${entry.name}`;
      return statSync(path.join(dir, name)).isDirectory() ? filesUnder(dir, name) : [name];
    })
    .sort();
}

// A zip's timestamps in DOS form: 1980-01-01 00:00, as cdk-assets writes
// them, so the same files always make the same bytes.
const DOS_TIME = 0;
const DOS_DATE = (1 << 5) | 1;

/**
 * The files under `dir` as a zip, the way cdk-assets zips a directory asset:
 * files only, sorted, deflated, at a fixed date, with their Unix modes.
 */
export function zipDirectory(dir: string): Buffer {
  const locals: Buffer[] = [];
  const centrals: Buffer[] = [];
  let offset = 0;
  for (const name of filesUnder(dir)) {
    const full = path.join(dir, name);
    const data = readFileSync(full);
    const deflated = deflateRawSync(data);
    const crc = crc32(data);
    const nameBytes = Buffer.from(name, 'utf8');
    const local = Buffer.alloc(30);
    local.writeUInt32LE(0x04034b50, 0);
    local.writeUInt16LE(20, 4); // version needed
    local.writeUInt16LE(0x0800, 6); // UTF-8 names
    local.writeUInt16LE(8, 8); // deflate
    local.writeUInt16LE(DOS_TIME, 10);
    local.writeUInt16LE(DOS_DATE, 12);
    local.writeUInt32LE(crc, 14);
    local.writeUInt32LE(deflated.length, 18);
    local.writeUInt32LE(data.length, 22);
    local.writeUInt16LE(nameBytes.length, 26);
    local.writeUInt16LE(0, 28);
    const central = Buffer.alloc(46);
    central.writeUInt32LE(0x02014b50, 0);
    central.writeUInt16LE((3 << 8) | 20, 4); // made by Unix
    central.writeUInt16LE(20, 6);
    central.writeUInt16LE(0x0800, 8);
    central.writeUInt16LE(8, 10);
    central.writeUInt16LE(DOS_TIME, 12);
    central.writeUInt16LE(DOS_DATE, 14);
    central.writeUInt32LE(crc, 16);
    central.writeUInt32LE(deflated.length, 20);
    central.writeUInt32LE(data.length, 24);
    central.writeUInt16LE(nameBytes.length, 28);
    // Extra field, comment, disk and internal attributes stay 0.
    central.writeUInt32LE(((0o100000 | (statSync(full).mode & 0o777)) << 16) >>> 0, 38);
    central.writeUInt32LE(offset, 42);
    locals.push(local, nameBytes, deflated);
    centrals.push(central, nameBytes);
    offset += local.length + nameBytes.length + deflated.length;
  }
  const directory = Buffer.concat(centrals);
  const end = Buffer.alloc(22);
  end.writeUInt32LE(0x06054b50, 0);
  end.writeUInt16LE(centrals.length / 2, 8);
  end.writeUInt16LE(centrals.length / 2, 10);
  end.writeUInt32LE(directory.length, 12);
  end.writeUInt32LE(offset, 16);
  return Buffer.concat([...locals, directory, end]);
}

// `tsx infra/dist.ts <out> <tier>...`, as `make dist` runs it after synth.
if (process.argv[1] !== undefined && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const [out, ...tiers] = process.argv.slice(2);
  if (out === undefined || tiers.length === 0) {
    throw new Error('Usage: tsx infra/dist.ts <out> <tier>...');
  }
  writeDist(
    tiers.map((tier) => ({ tier, dir: path.join('cdk.out', tier) })),
    out,
  );
}
