import { parseArgs } from 'node:util';

import { name, version } from '../package.json';
import { greeting } from './greeting';

/** Exit codes, as shells and CI read them. */
export const EXIT_OK = 0; // success, including --help and --version
export const EXIT_USAGE = 2; // the command line was wrong

/** Where `run` writes: results to stdout, diagnostics to stderr. */
export interface Output {
  readonly stdout: (text: string) => void;
  readonly stderr: (text: string) => void;
}

const options = {
  name: { type: 'string', short: 'n', default: '' },
  version: { type: 'boolean', default: false },
  help: { type: 'boolean', short: 'h', default: false },
} as const;

const USAGE = `Usage: ${name} [flags]

Prints a greeting.

Flags:
  -n, --name <name>  who to greet
      --version      print the version and exit
  -h, --help         print this help and exit
`;

/**
 * The whole program behind `main.ts`: parses `args` (the command line without
 * `node` and the script), writes to `out` and returns the exit code. It never
 * exits or touches global state, so tests call it directly.
 */
export function run(args: readonly string[], out: Output): number {
  let values;
  try {
    ({ values } = parseArgs({ args: [...args], options, strict: true, allowPositionals: false }));
  } catch (error: unknown) {
    const message = error instanceof Error ? error.message : String(error);
    out.stderr(`${name}: ${message}\nRun '${name} --help' for usage.\n`);
    return EXIT_USAGE;
  }
  if (values.help) {
    out.stdout(USAGE);
    return EXIT_OK;
  }
  if (values.version) {
    out.stdout(`${name} ${version}\n`);
    return EXIT_OK;
  }
  out.stdout(`${greeting(values.name)}\n`);
  return EXIT_OK;
}
