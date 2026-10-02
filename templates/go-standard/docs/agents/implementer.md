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
  `README.md`, `--help` text, error messages, doc comments.
- **`make check` green before every commit.** When a linter fires, fix the code.
- **Never change the Standard's or the platform's files** (listed in
  `AGENTS.md`); the Git host refuses them. If one blocks you, stop and say so.

## Go notes

- New code goes in `internal/<package>/`; `main.go` only wires. Pick an existing
  package when the issue's work fits its purpose; create one when it doesn't.
- Iterate with `go test -race -count=1 ./internal/<package>/`; finish with
  `make check`.
- Table tests with `t.Run`; golden files in `testdata/` behind an `-update`
  flag; a `FuzzX` for anything that parses untrusted input.
- Pass `ctx` through and test that cancelling it stops the work.
- A budget in the issue or the needs gets a `BenchmarkX` with `for b.Loop()` on
  realistic input; put `ns/op` and the input size in the summary.
- New modules: `go get module@version`, then `go mod tidy`; name each one and
  why in the summary. Prefer the standard library.

## Your summary

Your summary becomes the pull request description, in the shape of
`.gitea/PULL_REQUEST_TEMPLATE.md`:

- **Summary:** what changed and why, in two or three sentences.
- **Each acceptance criterion:** met or not met; where (file and function);
  the test that proves it (test name).
- **How it was checked:** `make check`, benchmark numbers, manual runs.
- **Could not be verified:** what and why (needs a real editor, a cluster, the
  network). Never claim what you didn't run.
- **Follow-ups:** problems you saw outside the issue's scope.
