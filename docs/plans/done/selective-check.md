# make check runs only what a change can have broken

GitHub issue #127. Written 2026-11-10, and done in the same change.

## Goal

`make check` spends the device's CPU only where a change occurred. Several
agents share one 4-CPU device, each runs `make check` before it finishes, and
the controller envtest suite alone takes 10 to 15 minutes there. The owner
asked for [digitalocean/gta](https://github.com/digitalocean/gta) to do the
selecting: "gta allows selective testing, only where changes occurred".

A selection that skips a test the change could break is worse than the waste,
so one rule stands over the rest: when in doubt, run more.

## Where we stood

- `make test` tested only the Go packages a change since `origin/main` could
  affect. `hack/test-packages.sh` walked the import graph itself, in bash,
  from `go list -test`, and knew the files Go tests read without importing
  them (the readers table).
- `validate-yaml`, `test-hack` and `ui-test` ran only when their inputs had
  changed since they last passed on this device. A new device or a new cloud
  container holds no such pass, so the first `make check` there ran all
  three, for a change to one Markdown file too.
- `go vet` and `golangci-lint` read the whole module for every change. On a
  cold cache that is 2m22s for vet and 2m41s for lint on 4 CPUs, 8 CPU-minutes
  each.
- `manifests`, `generate` and `fmt` ran for every change.
- Each step said what it did on its own line, far apart. Nothing at the end
  said what ran and what was skipped.

## What gta does

gta takes a list of changed files and a loaded set of Go packages, and
returns the packages the change affects:

1. A package is **changed** when a Go file in its directory changed, a file
   it embeds (`//go:embed`) changed, or a file below its `testdata` did.
2. A package is a **dependent** when it imports a changed package, directly
   or through others, in its own code or in its tests.
3. A package whose `_test.go` files alone changed is selected alone: no other
   package links them.

Its command, `gta`, gets the changed files from `git diff` against a base
branch and prints the packages. Its library takes the two parts as
interfaces, a `Differ` (the changed files) and a `Packager` (the packages and
the graph).

## Why gta's selection alone is not enough here

Read from the source of v0.2.0 and run on this module:

| What | Why it falls short here | What we do |
| --- | --- | --- |
| The command loads every package of every module in the build list (`...`), whatever `-include` says. | It fails in this module: `go: updates to go.mod needed`, though `go mod tidy` changes nothing. | The library, with a packager for this module's packages alone (`./...`). It answers in half a second. |
| The command's git differ reads commits only. | An agent runs `make check` before it commits: staged, unstaged and untracked files count. | The callers list the files themselves (`hack/lib/change.sh`) and hand them to gta's file differ. |
| A package is changed by Go files, embedded files and `testdata`. | Tests here read other files beside them by a relative path. | A package owns every file in its directory and below, down to the next package. |
| The imports of a package's tests are recorded only when the package has Go files besides its tests. | A package of tests alone (`test/chart`, `test/compat`) is never a dependent: a change to `internal/model` would skip `test/chart`. | `hack/gta` adds the packages of tests alone that import an affected package. |
| The library fails on a Go file that is in no loaded package (`... not found`). | A Go file of a nested module below the checkout would fail the selection. | Such a directory counts as holding no package. |
| gta knows Go files and what they embed. | A CRD under `config/crd/bases`, a template, the chart and some `hack/` files are read by tests that import nothing from them. `go.mod` selects no package at all. | The readers table and the fall back to every package stay, in `hack/test-packages.sh`. |
| gta selects Go packages. | The YAML validation, the shell tests and the UI's check are not Go packages. | Each is selected by its own inputs (below). |

gta's narrower traversal (`-test-transitive=false`) is not used. It stops at
a package whose tests alone import the changed one, and so misses a test that
imports a dependent of the changed package. The default is kept, which
selects a little more.

## Decisions, and why

1. **gta is a dependency, and `hack/gta` is our command on its library.**
   The module is `github.com/digitalocean/gta` v0.2.0, Apache-2.0, in
   `go.mod`; `golang.org/x/tools`, which it loads packages with, was there
   already. `hack/gta` is that use and three small corrections (the owner
   of a file, the packages of tests alone, a directory that holds no
   package). It prints `changed PKG` and `dependent PKG` lines.
   `hack/test-packages.sh` no longer walks the graph: it keeps the rules gta
   can't know and asks `hack/gta` for the rest.

2. **Every step of `make check` is selected by the change since the merge
   base,** the way gta selects packages, and no longer by what this device
   happened to run before. `hack/check.sh` is the recipe of `make check`:

   | Step | Runs when |
   | --- | --- |
   | `manifests`, `generate` | a Go file under `api/`, `cmd/` or `internal/` changed (controller-gen reads their markers and types), `go.mod`, `go.sum`, a file they write, or a script they run |
   | `fmt`, `vet`, `lint` | on the packages gta selects; on every package after a change to `go.mod` or `go.sum`, and for `lint` after one to `.golangci.yml` or `.custom-gcl.yml` |
   | `validate-yaml`, `test-hack`, `ui-test` | a file among the tier's inputs changed (`hack/check-cache.sh changed`, from the same list of inputs the tier's cache key is taken from) |
   | `test` | on the packages `hack/test-packages.sh` prints |

   So a docs-only change runs nothing, on a device that never ran `make
   check` too, and a UI-only change runs the UI's check and nothing of Go.

3. **`vet` and `lint` read the affected packages only.** A finding in a
   package depends on that package and on what it imports, so a package that
   neither changed nor depends on a changed one can't have a new finding. On
   a warm cache both were already cheap (about a second each); the gain is on
   a cold one, a new device or cloud container, where a leaf package no
   longer pays for the whole module. `make vet` and `make lint` by themselves
   still read every package, and so does the *Lint* workflow.

4. **The device's cache stays, behind the selection.** A tier the change
   selects is still answered from `hack/check-cache.sh` when those inputs
   passed here before, and Go's test cache still answers a package whose
   inputs are the same. The whole-tree cache stays as it was.

5. **Everything runs when the selection can't vouch for the result:**
   `TEST_ALL` or `FORCE` is set, the branch is `main`, there is no merge
   base, or the Makefile, a `*.mk` or a script that selects the steps changed
   (`hack/check.sh`, `hack/check-cache.sh`, `hack/test-packages.sh`,
   `hack/lib/change.sh`, `hack/gta/`). A tier whose inputs can't be compared
   runs, and when gta fails every package is formatted, vetted, linted and
   tested. `FORCE=1` also passes by every cache.

6. **A check that selects no step takes no heavy-job slot.** The Makefile
   asks `hack/check.sh --idle` before it queues, as it asks the tree cache: a
   docs-only check would else wait behind two running checks to run nothing.

7. **The run ends with one block:** each step, whether it ran, was skipped,
   was answered from a cache or failed, how long it took and why. A failing
   step ends the check; the block says where, and lists the later steps as
   not run.

   ```
   make check: passed. 1 file changed since origin/main (fc9adcb1d2e3); the steps that change selects:
     manifests generate  ran      5s; internal/buildinfo/buildinfo.go changed
     fmt                 ran      1s; the 4 packages gta selects; changed: internal/buildinfo
     vet                 ran      0s; the 4 packages gta selects; changed: internal/buildinfo
     lint                ran      4s; the 4 packages gta selects; changed: internal/buildinfo
     validate-yaml       skipped  none of its inputs changed: the YAML, api/, templates/ and hack/render
     test-hack           skipped  none of its inputs changed: hack/ and the rest outside the Go, UI and docs trees
     ui-test             skipped  none of its inputs changed: ui/
     test                ran      33s; 4 of 42 packages, those the change since origin/main (fc9adcb1d2e3) affects; changed: internal/buildinfo
   ```

8. **`LINT_CHANGED_ONLY` and `hack/go-changed.sh` are gone.** The factory's
   own CI set the variable to lint only when Go had changed; every `make
   check` now selects `lint` that way, and by package. The workflow still
   sets the variable, which nothing reads: `.gitea/workflows/check.yaml` is
   the `factory-self` Standard's file, byte for byte, and is the owner's to
   change with the template.

## What changes for a change to a test file

gta's third rule is new here. `hack/test-packages.sh` used to test a package
and every package whose test binary links it, whatever file changed. A change
to `internal/model/abandoned_test.go` tested 24 packages, the controller
suite among them; it now tests `internal/model`. For a change to any other
file, the selection was compared with the old one on one file of each kind in
each package directory (74 files): gta's selection, with the corrections
above, held every package the old one did.

## How we know it works

- `hack/gta/main_test.go`, a table test on a throwaway module: a Go file, a
  test file, an embedded file, a file a package owns, a package of tests
  alone, a nested module, a constrained file, a package that is gone.
- `hack/test-packages_test.sh`: the cases it had, now through gta, plus a
  test file tested alone, a package tested through another's tests, and a gta
  that fails.
- `hack/check_test.sh`, new: each line of the issue's "How you would know it
  works", on a throwaway module with a `make` that logs what it is asked and
  an empty cache; the failing step, the cached tier, the file `make
  manifests` writes, and the Makefile's dry run and its idle check.
- `hack/check-cache_test.sh`: `changed`, `cached` and `inputs`.
- Runs on this repository are in
  [docs/screenshots/127-selective-check.txt](../../screenshots/127-selective-check.txt).

## Not done, and why

- **gta's own fixes.** The two faults found in v0.2.0 (a package of tests
  alone is never a dependent; a Go file in no loaded package fails the run)
  belong upstream. They are worked around in `hack/gta` and can go when a
  release has them.
- **The workflows on GitHub** keep their `paths` filters and their own
  commands: *Lint* lints and vets every package, *Tests* selects with
  `hack/test-packages.sh` on a pull request and tests every package on main.
  That run on main is what catches a selection that missed.
- **`make test` by itself** still runs `manifests`, `generate`, `fmt` and
  `vet` on every package first. It is the target a person runs by hand;
  `make check` is the gate, and the one that was asked for.
