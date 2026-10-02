---
title: Check files from the command line, for CI
labels: status/ready, kind/feature
milestone: M1 Diagnostics
depends: 04-check-policies, 05-check-crd-rules
---
## Context

Policy authors also need the checks in CI, where no editor runs: a policy that
would fail on `kubectl apply` should fail the pipeline (see the system's KPIs).

## Goal

`cel-language-server check [--format text|json] PATH...`, where a path is a
file or a directory searched for `*.yaml` and `*.yml`.

## Acceptance criteria

- [ ] Text output is one line per diagnostic, `path:line:col: severity: message`, with 1-based line and column as compilers print them, sorted by path and position.
- [ ] `--format json` prints a JSON array with path, range, severity and message, documented in the README.
- [ ] Exit code 0 without errors, 1 with at least one error, 2 for usage problems (unknown flag, missing path); warnings alone exit 0.
- [ ] `cel-language-server --help` and `check --help` explain the commands with an example; `--version` prints the version.
- [ ] Tests run the command in-process over `testdata/` and compare its output with golden files.
- [ ] The README shows the command in a Gitea or GitHub Actions step.

## Out of scope

Watching files; fixing them.

## Notes

- The go-cli template's `run(args, stdout, stderr) int` keeps `main` testable;
  use the standard library's `flag` package with a subcommand per verb.
