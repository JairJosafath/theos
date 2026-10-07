import { describe, expect, it } from 'vitest';

import { name, version } from '../package.json';
import { EXIT_OK, EXIT_USAGE, run } from './cli';

/** Runs the program on `args` and returns its exit code and output. */
function runWith(args: string[]): { code: number; stdout: string; stderr: string } {
  let stdout = '';
  let stderr = '';
  const code = run(args, {
    stdout: (text) => {
      stdout += text;
    },
    stderr: (text) => {
      stderr += text;
    },
  });
  return { code, stdout, stderr };
}

describe('run', () => {
  it.each([
    { args: [], want: 'Hello!\n' },
    { args: ['--name', 'Ada'], want: 'Hello, Ada!\n' },
    { args: ['-n', 'Zoë'], want: 'Hello, Zoë!\n' },
    { args: ['--name=Grace'], want: 'Hello, Grace!\n' },
  ])('greets for $args', ({ args, want }) => {
    expect(runWith(args)).toEqual({ code: EXIT_OK, stdout: want, stderr: '' });
  });

  it('prints the version from package.json', () => {
    expect(runWith(['--version'])).toEqual({
      code: EXIT_OK,
      stdout: `${name} ${version}\n`,
      stderr: '',
    });
  });

  it.each([['--help'], ['-h']])('prints the help for %s', (flag) => {
    const got = runWith([flag]);
    expect(got.code).toBe(EXIT_OK);
    expect(got.stdout).toContain(`Usage: ${name} [flags]`);
    expect(got.stderr).toBe('');
  });

  it.each([
    { args: ['--nope'], says: "Unknown option '--nope'" },
    { args: ['extra'], says: "Unexpected argument 'extra'" },
    { args: ['--name'], says: "Option '-n, --name <value>' argument missing" },
  ])('rejects $args', ({ args, says }) => {
    const got = runWith(args);
    expect(got.code).toBe(EXIT_USAGE);
    expect(got.stdout).toBe('');
    expect(got.stderr).toContain(says);
    expect(got.stderr).toContain(`Run '${name} --help' for usage.`);
  });
});
