# make check runs only what a change can have broken

GitHub issue #127. Written 2026-11-10, and done in the same change. The
owner then set the rule the last section builds: only a release runs
everything, and every other check is partial, against main.

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
   still read every package.

4. **The device's cache stays, behind the selection.** A tier the change
   selects is still answered from `hack/check-cache.sh` when those inputs
   passed here before, and Go's test cache still answers a package whose
   inputs are the same. The whole-tree cache stays as it was.

5. **Everything runs when the change can't be known:** there is no merge
   base. A tier whose inputs can't be compared runs, and when gta fails every
   package is formatted, vetted, linted and tested. By hand, `TEST_ALL=1`
   selects every step and `FORCE=1` also passes by every cache. The branch
   `main`, the Makefile and the scripts that select the steps ran everything
   at first; they no longer do (see "Only a release runs everything").

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

## Only a release runs everything

The owner's rule, 2026-11-10: "only when releases are done, a full test must
be done; all other tests are always partial testing against the main branch."

And, the same day: "when running make checks locally it should also be
partial; full ones are only done at release."

Before it, these still ran everything without being asked: `make check` and
`make test` on the branch `main`; `make check` after a change to the
Makefile, a `*.mk` or a script that selects the steps; `make test` after a
change to the Makefile, a workflow, a Dockerfile, `README.md`, any
`testdata` or a file in no listed path, in case a Go test read it; the
*Tests* workflow on every push to main, on a pushed tag and on a manual run
(`TEST_ALL=1`); and the *Lint* workflow, whose `make lint`, `make
validate-yaml` and `make test-hack` never selected at all. And a release ran
no check of its own: *Tests* tested every package on the tag beside it, and
*Release* published whatever that said.

1. **On a device the check is always partial,** on the branch `main` too:
   there the change is what main holds that `origin/main` doesn't. The rule
   "the branch is main, so everything" is gone from `hack/lib/change.sh`.

   **No file selects every step.** The Makefile selects the steps whose tools
   it pins (`manifests`, `generate`, and `lint` on every package) and the
   three tiers, whose inputs list it. A `*.mk` and `hack/check-cache.sh`
   select the tiers, the other selecting scripts the shell tests, which test
   them, and `hack/gta/` its own package. "A selection can't vouch for
   itself" was the reason to run everything after such a change; the shell
   tests of those scripts are what vouches for them, and the release for the
   rest.

   **A file tests the packages whose tests read it, and no others.** The
   fall back to every package is gone for all but `go.mod` and `go.sum`,
   which every package is built from. What reads what was measured, not
   guessed: every package's test binary was run with Go's own test log
   (`-test.testlogfile`, what `go test` keys its cache on), which names each
   file a test opens. Outside their own directories the tests open:

   | Package | Opens |
   | --- | --- |
   | `internal/ports` | every Go file of the repository (`TestHostNeutral`) |
   | `cmd` | every Dockerfile and `.dockerignore` (`TestDockerContextHasEmbeddedFiles`) |
   | `test/chart` | `config/`, `dist/`, `templates/`, `hack/seed/`, `docs/getting-started.md`, the Release and Verify Release workflows, `ui/Dockerfile` |
   | `internal/controller` | `config/`, `templates/`, `test/crds/`, and the Go files of `internal/scaffold` and `internal/agentrunner`, which it imports |
   | `internal/model` | `config/`, `dist/`, the CloudFormation adapter's `testdata/` |
   | `internal/scaffold` | `templates/`, `api/` |
   | `internal/sessionclient`, `test/compat` | `config/` |
   | `internal/adapters/argocd` | `test/crds/` |
   | `hack/template-go-kubebuilder` | `templates/` |
   | `test/e2e/standin` | `test/e2e/testdata/` |

   No test opens the Makefile, a `*.mk`, `README.md` or any other file at the
   top. The readers table in `hack/test-packages.sh` now holds what was
   missing from this list (the Go files for `internal/ports`, which no
   selection before this one knew, the Dockerfiles, the workflows, the guide,
   the two `testdata` directories another package reads), and a path of it
   may be a pattern. A file below a package's `testdata` tests that package
   alone, as gta has it. A file in no package and no path of the table tests
   nothing.

2. **A workflow's job runs its part of `make check`,** selected the same way:
   `CHECK_STEPS` names the steps to take. The jobs keep their names, which the
   ruleset on main requires (`.github/required-checks`):

   | Job | Runs |
   | --- | --- |
   | *Tests* | `make check CHECK_STEPS="generate fmt vet test"`, then `make check CHECK_STEPS=test-gitea` |
   | *Lint* | `make lint-config`, `make check CHECK_STEPS="generate lint validate-yaml"`, `make check-generated` |
   | *Shell tests* | `make check CHECK_STEPS=test-hack` |

   `test-gitea` is a step only when named. It runs when `make test` tests
   `internal/adapters/gitea` (its code, what it builds on, or `templates/`,
   which its tests read) or `hack/seed/` or `hack/gitea-local.sh` changed. A
   run of a part records no pass of the tree.

3. **What a run is compared with** is `TEST_BASE`: a pull request's base
   branch, main's head before a push to main (`github.event.before`), main
   for a manual run. Both workflows clone the whole history for it.

4. **Each push to main gets its own run.** *Tests* cancelled the run of a
   superseded head of main, and GitHub dropped *Lint*'s pending run for a
   newer push. That was sound while a run tested everything: the newest run
   covered the others. A partial run covers only its own push, so the group
   is now one per pull request (a new push still replaces the run there) and
   one per commit on main, with nothing cancelled on main. The runs are
   small, which is the point of them.

5. **A release runs every step, and waits for it.** *Release* has a first
   job, *Full check*: `make check FORCE=1` (every step, every package, past
   the device's caches and Go's test cache), `make check-generated` and `make
   test-gitea`. *Build and publish* needs it, so nothing ships unless it
   passes. It refuses a tag the chart doesn't name before it starts. *Tests*
   no longer runs on a pushed tag: the full run happens once, where it gates.
   A tag whose full check fails is not a release, as a tag whose build failed
   was not: fix main and tag the next patch.

6. **Not chosen.** Running the full check in `hack/release.sh` before the
   tag: it would catch a failure before the tag exists, but only on the
   device of whoever runs the script, and a tag pushed by hand would pass by
   it. A `full` switch on a manual run of *Tests*: `make check FORCE=1` on a
   device does that. Comparing a push to main with the last head whose run
   passed, instead of the head before the push: it would retest a red main's
   change on every later push, but needs the Actions API in every run.

`hack/check-workflows.sh` holds the workflows to this: *Lint* and *Tests*
clone the whole history, compare a push with the head before it, carry the
new group and never ask for every step; no workflow but *Release* names
`TEST_ALL` or `FORCE`; *Tests* runs on no tag; *Release* has a job that runs
`make check FORCE=1`, and the job that pushes the images needs it.

What this costs: a selection that misses a test is found at the next
release, not at the next push to main or the next change to the Makefile. A
test that starts to read a file the table doesn't name is such a miss:
`hack/test-packages_test.sh` catches the ones that climb to a path of the
table or name a `hack/` path, not every one. A push whose run fails or is
cancelled by hand leaves its change unchecked until the release, too.

## Not done, and why

- **gta's own fixes.** The two faults found in v0.2.0 (a package of tests
  alone is never a dependent; a Go file in no loaded package fails the run)
  belong upstream. They are worked around in `hack/gta` and can go when a
  release has them.
- **The UI workflow** keeps its own commands. Its `paths` filter is its
  selection: it runs when `ui/` changed, and then runs all of the UI's check
  and its end-to-end tests. Those end-to-end tests are not part of a
  release's full check either: `make check` leaves them out, as a device may
  have no Chromium.
- **The factory's own check on Gitea** (`.gitea/workflows/check.yaml`) is the
  `factory-self` Standard's file and is left as it is. It runs `make check`,
  so it is partial on a pull request and on a push to main already; it has
  no run of every step at a release.
- **`make test` by itself** still runs `manifests`, `generate`, `fmt` and
  `vet` on every package first. It is the target a person runs by hand;
  `make check` is the gate, and what the workflows now run.
- **Nothing here ran on GitHub.** The workflows were checked with
  `hack/check-workflows.sh`, actionlint and zizmor, not by a run: the first
  pull request, the first push to main and the next release are their first
  runs.
