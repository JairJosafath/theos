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
  `README.md`, the CRD field docs (the doc comments on `api/` types), status
  conditions and events, error messages.
- **`make check` green before every commit.** When a linter fires, fix the code.
- **Protected files** (the Standard's and the platform's, listed in
  `AGENTS.md`) change only through a Standard change, below. The Git host
  refuses them in a normal pull request.

## Standard changes

If the issue truly needs a change to a protected file, make it in the same
work. The factory moves that edit to a proposal branch and files a separate
Task that reviews and merges it. Then:

- The protected change merges first, so it must work on `main` without the
  rest of your work. Never have it call a file only your branch has; use a hook
  that does nothing until the rest lands, such as a `Makefile` target that runs
  an optional script only when it exists
  (`[ ! -x scripts/dist.sh ] || scripts/dist.sh`).
- The rest of your work merges after it and may rely on it.
- End your summary with a line: `Standard change: <why the issue needs it>`.
- Never change `AGENTS.md`, `docs/agents/` or `.c4factory.yaml`. No Standard
  change can.
- When your own Task is the Standard change, change the paths the issue names.
  Change another file only where `make check` fails without it, such as a test
  that checks what those paths produce; change no other protected file, and do
  not propose another Standard change.

## Go notes

- This is a kubebuilder project: types in `api/<version>/`, reconcilers in
  `internal/controller/`, webhooks in `internal/webhook/`, other code in
  `internal/<package>/`; `cmd/main.go` only wires the manager. Add a kind,
  controller or webhook with `kubebuilder create api` or
  `kubebuilder create webhook`, never by hand, and keep the
  `// +kubebuilder:scaffold:*` markers.
- After editing `api/` types or `// +kubebuilder` markers, run
  `make manifests generate` and commit what it writes. Never edit
  `config/crd/bases/`, `config/rbac/role.yaml`, `zz_generated.*` or `PROJECT`
  by hand; `make check` fails when they are not what the generators write.
- Reconcilers and CEL validation rules are tested in envtest (the
  `suite_test.go` next to them; a real API server and etcd, no cluster):
  a reconcile that creates, updates and cleans up, the status it reports, and
  every CEL rule's failing case. Iterate with `make test`; finish with
  `make check`. `make test-e2e` needs a Kind cluster: don't run it.
- Table tests with `t.Run` for logic outside the controllers; golden files in
  `testdata/` behind an `-update` flag; a `FuzzX` for anything that parses
  untrusted input.
- Pass `ctx` through and test that cancelling it stops the work.
- Unit tests never measure time: no `time.Sleep`, no wall-clock bounds such as
  "arrives within 500ms", no best-of-N samples. They pass in your sandbox and
  fail on the shared CI runner or under `-race`.
- A budget in the issue or the needs ("within 200 ms", "fast on a 10,000-line
  file") gets a `BenchmarkX` with `for b.Loop()` on realistic input, not a
  test; put `ns/op` and the input size in the summary.
- Timing logic (debounce, timeouts, retries) takes an injected clock or a
  channel the test controls; the test advances it, or waits for an event, and
  checks the outcome.
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
