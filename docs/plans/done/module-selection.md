# A change to go.mod tests the packages built from the changed modules

GitHub issue #127, after its plan
([selective-check.md](selective-check.md)). Written 2026-11-13, and done in
the same change.

## Goal

A bump of one dependency tests the packages that import it, not every
package. The owner, 2026-11-13: "a full test is being done when go mod
changes. That doesn't make any sense. Go mod might change and just one
package was updated or added", and asked why gta is not used as
[schubergphilis/mcvs-golang-action](https://github.com/schubergphilis/mcvs-golang-action)
uses it for its partial testing.

## Where we stood

`hack/test-packages.sh` and `hack/check.sh` took every package (fmt, vet,
lint and test) after any change to `go.mod` or `go.sum`, "which every package
is built from": a comment, one version, a checksum.

mcvs-golang-action runs gta's own command, `gta -include MODULE/ -base
origin/main`, and tests what it prints. Run on this repository at 7a2e380:

| Change | gta's own command | Here, before |
| --- | --- | --- |
| a Go file | fails: `go: updates to go.mod needed` ([selective-check.md](selective-check.md) says why) | the packages `hack/gta` selects with gta's library |
| `go.mod` alone, one version | prints nothing, exit 0: mcvs then says "No packages changed" and runs no test | every package, 42 of 42 |

So for `go.mod` neither is right. gta knows Go files and what they embed; a
change to `go.mod` selects no package there, and a dependency bump that
breaks the build would pass untested. Every package is the safe answer and
the wasteful one.

## What a change to go.mod can reach

A package is built from its own files, the packages it imports, and the
toolchain. So:

- **A module whose version changed reaches the packages that depend on one
  of its packages,** in their own code or in their tests, and no others.
  With a pruned module graph (`go 1.17` and later) `go.mod` lists every
  module that provides a package to this module's builds, at the version
  selected, so a version that moved because another module asked for it
  shows as its own changed `require` line. A `go.mod` that doesn't hold what
  the packages need fails `go list`, and then every package is taken.
- **The `go` and `toolchain` lines reach every package:** the language
  version each is compiled at, and the compiler and standard library. So do
  `godebug`, `module`, `ignore` and a line this code doesn't know.
- **A comment (`// indirect`), a `retract` line and a `tool` line build no
  package.**
- **In `go.sum`, a changed checksum of a module's files names that module.**
  A checksum of a module's `go.mod` alone (`MODULE VERSION/go.mod`) vouches
  for what decides the versions, and the versions a package is built from
  are `go.mod`'s `require` lines: it selects nothing.

## Decisions, and why

1. **`hack/gta` does it** (`modules.go`), since it already holds the
   corrections to gta and both scripts call it. It takes `-base REVISION`,
   reads `go.mod` and `go.sum` as that revision has them (`git show`),
   compares them with the working tree's line by line
   (`golang.org/x/mod/modfile`, a dependency already), and walks `go list
   -test -deps ./...` for the packages of this module that reach a package of
   a changed module. It prints `module PKG` for each, `modules M...` for the
   changed modules some package uses, or `all REASON` alone when every
   package is affected. The root `go.mod` and `go.sum` never reach gta's
   library; a nested module's stay files like any other.

2. **A package whose tests alone import the module is selected alone,** not
   its dependents: their binaries don't link another package's tests. That is
   narrower than gta's default for Go files, which is kept as it was.

3. **When in doubt, every package,** as before: no base to compare with, a
   `go.mod` that is new or doesn't parse, a `go list` that fails, a line of
   an unknown kind. `go.work` still takes every package.

4. **`manifests` and `generate`, `validate-yaml` and `test-hack` are left as
   they were:** they still run after a change to `go.mod`. controller-gen
   reads the types of the Kubernetes modules, `validate-yaml` builds
   `hack/render` from them, and shell tests read `go.mod`'s Go version. The
   first takes seconds; narrowing the other two is not done here.

## How we know it works

- `hack/gta/modules_test.go`: a table on `diffModules` (a version, a module
  added or gone, `replace`, `exclude`, a comment, a line moved into a block,
  `retract` and `tool`, `go`, `toolchain`, `godebug`, `module`, both kinds of
  `go.sum` line), and `run` on a throwaway module in a git repository that
  builds on two replaced modules: the users of a module through their own
  code, through their tests, a package of tests alone, a module no package
  uses, `go.mod` with a Go file, `go.sum` alone, no base, a base that is no
  commit, and a `go.mod` the packages can't be built from.
- `hack/test-packages_test.sh` and `hack/check_test.sh`: the same through the
  scripts, with what each says, and the `go` line and `go.work` still taking
  every package.
- A run on this repository, before and after, is in
  [docs/screenshots/127-module-selection.txt](../../screenshots/127-module-selection.txt).

## Not done, and why

- **Upstream.** That gta selects nothing for `go.mod` belongs in gta; this
  can go when a release has it.
- **The tiers** (decision 4).
