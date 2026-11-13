# A change to go.mod tests no package

GitHub issue #127, after its plan
([selective-check.md](selective-check.md)). Written 2026-11-13, and done in
the same change.

## Goal

A tiny change is checked in minutes. The owner, 2026-11-13: "a full test is
being done when go mod changes. That doesn't make any sense", and, of
[schubergphilis/mcvs-golang-action](https://github.com/schubergphilis/mcvs-golang-action)'s
partial testing: "I want it the same as the mcvs one. Otherwise it doesn't
speed up testing. Testing now takes 20 mins; can't have that for tiny
changes. Only for releases it's necessary to do that full run."

## Where we stood

`hack/test-packages.sh` and `hack/check.sh` took every package (fmt, vet,
lint and test) after any change to `go.mod`, `go.sum` or `go.work`, "which
every package is built from". The *Tests* run of pull request #138, which
moved the toolchain line to Go 1.26.11 for a vulnerability:

```
make check: passed. 17 files changed since origin/main (3a0494ace027); of the steps asked for (CHECK_STEPS), the steps that change selects:
  manifests generate  ran      39s; cmd/theos-session/main.go changed
  fmt                 ran      1s; every package: go.mod changed
  vet                 ran      3m10s; every package: go.mod changed
  test                ran      15m26s; every package (42): go.mod changed, which every package is built from
```

mcvs-golang-action runs gta's own command, `gta -include MODULE/ -base
origin/main`, tests what it prints and skips the tests when it prints
nothing. gta places a changed file in the package of its directory; `go.mod`
lies in none, so a change to it alone prints nothing and tests nothing
there.

## Decisions, and why

1. **`go.mod`, `go.sum` and `go.work` select no Go package,** as gta has
   it: no fmt, vet, lint or test for a change to them alone. With a Go file
   in the same change, gta selects for the Go file as it did. No file tests
   every package any more; every package is tested with `TEST_ALL=1` or
   `FORCE=1`, which a release sets, without a merge base, and when gta fails.

2. **A release is where a bump is tested.** *Release*'s first job runs `make
   check FORCE=1`, every step on every package, and nothing ships unless it
   passes ([selective-check.md](selective-check.md), "Only a release runs
   everything"). So a bump of a module or of the toolchain that breaks a
   test is found at the next release, not on its pull request. That is the
   cost, and the owner's choice. One that no longer compiles shows before
   that: the *Vulnerabilities* and *CodeQL* workflows start for `go.mod` and
   build the module.

3. **Not chosen: testing the packages built from the changed modules.** It
   was built first (`hack/gta` compared `go.mod` with the merge base's and
   selected the packages that import a module whose version changed). It is
   sound, and it does not meet the goal: the controller suite imports
   Kubernetes, controller-runtime, Ginkgo and Gomega, so most bumps select
   it, and it is a quarter of an hour by itself; a toolchain bump still
   selected every package.

4. **`manifests`, `generate`, `validate-yaml` and the shell tests are left
   as they were:** they still run after a change to `go.mod`, which is among
   their inputs. They take seconds to a minute.

## What this does not speed up

The same pull request's run before the toolchain commit, with no change to
`go.mod`:

```
  vet                 ran      2m24s; the 29 packages gta selects; changed: cmd/theos-session internal/model
  test                ran      14m58s; 29 of 42 packages, those the change since origin/main (3a0494ace027) affects; changed: cmd/theos-session internal/model; read by their tests: *.go
```

That is gta's own selection, and mcvs-golang-action would run the same: a
package and every package that depends on it. `internal/model` is the bottom
layer, `internal/controller` depends on it, and its envtest suite takes 517 s
and 827 s in its two processes. So a change to `internal/model`, or to
anything else the controllers import, still takes a quarter of an hour. Two
ways out, neither done here: run the controller suite as several jobs at
once (no test is given up), or leave dependents to the release as well (a
change then tests its own package alone). The first is the one to do next.

## How we know it works

- `hack/test-packages_test.sh`: `#127 go.mod`, `go.sum` and `go.work` test
  no package and say so; `go.mod` with a Go file tests what gta selects for
  the Go file.
- `hack/check_test.sh`: `#127 go.mod` runs `manifests generate`,
  `validate-yaml` and `test-hack` and asks make for no package; with a Go
  file, `vet` and `test` get that file's package and not every one.
- Runs on this repository are in
  [docs/screenshots/127-go-mod-tests-nothing.txt](../../screenshots/127-go-mod-tests-nothing.txt).
