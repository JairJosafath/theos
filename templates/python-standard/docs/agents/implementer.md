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
   repeat (such as "works offline" or a response-time budget).
3. **Read the related repositories** under `/workspace/related/<name>`, if any:
   the parts this issue touches, such as the protocol or flags the other side
   uses. They are read-only; never change them or copy their code.
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
- **Update what users read** in the same change when behaviour they see changes:
  `README.md`, the API's models and descriptions or `--help` text, error
  messages, docstrings.
- **`make check` green before every commit.** When a rule fires, fix the code.
- **Protected files** (the Standard's and the platform's, listed in
  `AGENTS.md`) change only through a Standard change, below. The Git host
  refuses them in a normal pull request.

## When CI runs the full check

The Container's owner chooses it (its `checks` setting). When
`.gitea/workflows/check.yaml` has a `full` job, the choice is `BeforeRelease`:
CI runs only `make check-light` (lint and the type or vet checks; no
tests) on your pull request and on `main`, so your own `make check` before every
commit is the only full check your change gets until a release. The full
`make check` runs on a push to `release-check/vX.Y.Z` at the commit a release
will be tagged at, and a release is tagged only once that `full` job passes. If
you are asked to tag a release by hand, push that branch at the commit first,
wait for `check / full (push)` to pass, then tag the commit and delete the
branch; never tag a commit whose `full` job failed. Without a `full` job, CI runs
`make check` on every change.

## Standard changes

If the issue truly needs a change to a protected file, make it in the same
work. The factory moves that edit to a proposal branch and files a separate
Task that reviews and merges it. Then:

- The protected change merges first, so it must work on `main` without the
  rest of your work. Never have it call a file only your branch has; use a hook
  that does nothing until the rest lands, such as a `Makefile` target that runs
  an optional script only when it exists
  (`[ ! -f scripts/dist.py ] || uv run --no-sync python scripts/dist.py`).
- The rest of your work merges after it and may rely on it.
- End your summary with a line: `Standard change: <why the issue needs it>`.
- Never change `AGENTS.md`, `docs/agents/` or `.theos.yaml`. No Standard
  change can.
- When your own Task is the Standard change, change the paths the issue names.
  Change another file only where `make check` fails without it, such as a test
  that checks what those paths produce; change no other protected file, and do
  not propose another Standard change.

## Python notes

- Iterate with `uv run pytest tests/test_<module>.py`, `uv run mypy .` and
  `uv run ruff check`; finish with `make check`, which also builds the wheel.
- New logic goes in a module of its own under the package, with its tests in
  `tests/test_<module>.py`; keep entry points (the app's routes, the
  command-line function) to wiring.
- Tables with `@pytest.mark.parametrize` and `ids`; fixtures in
  `tests/conftest.py` once two test modules share them.
- Tests never measure time: no `time.sleep`, no wall-clock bounds such as
  "responds within 500 ms", no best-of-N samples. They pass in your sandbox and
  fail on the shared CI runner.
- A budget in the issue or the needs ("within 200 ms", "fast on 10,000
  orders") gets a measurement on realistic input with `timeit`, not a test; put
  the numbers and the input size in the summary.
- Timing logic (timeouts, retries, expiry) takes an injected clock or sleep
  function the test controls; the test advances it and checks the outcome.
- New packages: `uv add <package>` (`--dev` for test tools), which updates
  `pyproject.toml` and `uv.lock`; name each one and why in the summary. Prefer
  the standard library. Never edit `uv.lock` by hand.

## Your summary

Your summary becomes the pull request description, in the shape of
`.gitea/PULL_REQUEST_TEMPLATE.md`:

- **Summary:** what changed and why, in two or three sentences.
- **Each acceptance criterion:** met or not met; where (file and function);
  the test that proves it (test name).
- **How it was checked:** `make check`, numbers for any budget, manual runs.
- **Could not be verified:** what and why (needs a cluster, a real database, the
  network). Never claim what you didn't run.
- **Follow-ups:** problems you saw outside the issue's scope.
