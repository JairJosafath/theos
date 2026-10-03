# Implementer guide

You implement one Gitea issue in this repository, as one pull request.
`AGENTS.md` holds the rules; this guide is how to work an issue. A reviewer
checks your work against `docs/agents/reviewer.md`, so read that too.

## Before you write code

1. **Read the issue.** Goal, every acceptance criterion, Out of scope, Notes.
   The acceptance criteria are the contract: meet each one, add nothing they
   don't ask for. Out of scope stays out.
2. **Read the C4 context** you were given: the system's needs, this Container,
   its relationships. Needs bind every change, even ones the issue doesn't
   repeat (such as "never gets in the way of other YAML tooling").
3. **Read the related repositories** under `/workspace/related/<name>`, if any:
   the parts this issue touches, such as the language server's command line
   and the LSP features it offers. They are read-only; never change them or
   copy their code.
4. **Read the code** you'll change and its tests. Find where each criterion lands.
5. **Plan briefly:** for each criterion, the test that will prove it and the
   code that will make it pass. If a criterion is ambiguous, choose the reading
   that fits the system's needs and say so in your summary. Don't widen scope.

## While you work

- **Test first.** For each criterion, write a test that fails, then make it pass.
  A bug fix starts with a test that shows the bug.
- **The smallest change that meets every criterion.** No drive-by refactors,
  renames or reformatting outside the issue; note them as follow-ups instead.
- **Keep the change focused:** one issue, one pull request, small Conventional
  Commits.
- **Update what users read** in the same change when behaviour they see
  changes: `README.md` (settings, commands), the setting and command
  descriptions in `package.json`, and a `CHANGELOG.md` entry under "Unreleased".
- **`make check` green before every commit.** When a rule fires, fix the code.
- **Protected files** (the Standard's and the platform's, listed in
  `AGENTS.md`) change only through a Standard change, below. The Git host
  refuses them in a normal pull request.

## Standard changes

If the issue truly needs a change to a protected file, make it in the same
work. The factory moves that edit to a proposal branch and files a separate
Task that reviews and merges it. Then:

- The rest of your work must run on `main` alone. Never call something only the
  proposal has; use a hook the protected file can pick up later, such as a `Makefile` target that calls `npm run dist --if-present`, or an
  npm script that runs an optional step only when it exists.
- End your summary with a line: `Standard change: <why the issue needs it>`.
- Never change `AGENTS.md`, `docs/agents/` or `.c4factory.yaml`. No Standard
  change can.
- When your own Task is the Standard change, change only the paths the issue
  names, and nothing else. Do not propose another Standard change.

## TypeScript and VS Code notes

- Put logic in modules that don't import `vscode` and test them with plain
  vitest; keep `src/extension.ts` to wiring.
- Code that must touch `vscode` is tested against `test/vscode.ts`: add only the
  members your test needs, typed with `satisfies` against the real API, and
  clear them in `reset()`.
- Iterate with `npx vitest` and `npx tsc --noEmit`; finish with `make check`,
  which also packages the `.vsix`.
- New settings and commands go in `package.json`'s `contributes` with defaults
  and descriptions, and in the README.
- Tests never measure time: no real timers or `setTimeout` waits, no bounds
  such as "responds within 500 ms" on `performance.now()` or `Date.now()`.
  They pass in your sandbox and fail on the shared CI runner.
- A budget in the issue or the needs ("within 200 ms", "fast on a 10,000-line
  file") gets a vitest `bench` on realistic input, not a test; put the numbers
  and the input size in the summary.
- Timing logic (debounce, timeouts, retries) is tested with fake timers
  (`vi.useFakeTimers()`, then `vi.advanceTimersByTime`), or by awaiting the
  event it fires.
- You can't open VS Code here. Say which behaviour needs a person to try in a
  real editor.

## Your summary

Your summary becomes the pull request description, in the shape of
`.gitea/PULL_REQUEST_TEMPLATE.md`:

- **Summary:** what changed and why, in two or three sentences.
- **Each acceptance criterion:** met or not met; where (file and function);
  the test that proves it (test name).
- **How it was checked:** `make check`, numbers for any budget, manual runs.
- **Could not be verified:** what and why (needs a real editor, a display, the
  network). Never claim what you didn't run.
- **Follow-ups:** problems you saw outside the issue's scope.
