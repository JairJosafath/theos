# A scaffolded Container carries the license its Project chose

GitHub issue #1788. Agent: otter-1788. Written 2026-10-22.

## Goal

A Container whose Project (or itself) names a license is scaffolded with a
non-empty `LICENSE` at its root, so a release whose source goes out through a
`github` DeployTarget's `source` or a Crew's `publishReleases.source` passes
the `LicenseMissing` check (#1602) without anyone committing a file by hand.
When no license is set, nothing changes, and the `LicenseMissing` message
says how to set one. Found by otter-1602; readied by triage as design work.

## Where we stand

- `model.PublishChecks` refuses a source with no non-empty `LICENSE`,
  `LICENSE.md`, `LICENSE.txt` or `COPYING` at the root, `LicenseMissing`,
  and its message says only to add one.
- No template under `templates/` ships a license file (all 30 checked in the
  review of #1787), so every newly scaffolded Container that publishes source
  fails its first release. The `webshop-github` sample is one.
- The scaffold already writes files no layer owns: `.c4factory.yaml` and
  `.devcontainer/` (`scaffold.Build`, `isOperatorPath`); a layer writing one
  of those paths fails the Container with the path. The Container controller
  builds `scaffold.Input` from the Container, its choices and its
  GitProvider; it reads nothing from the Project yet.

## Decisions, and why

1. **A setting, never a pick.** Which license an application carries is its
   owner's choice, so it is a field (AGENTS.md, "What can be configured is
   configured"), with no default: the factory never puts a license on a
   user's code they didn't choose. With none set a Container is scaffolded as
   today, with no `LICENSE`.
2. **`spec.license` on the Project, which a Container's own `spec.license`
   overrides.** One Project is one namespace and one owner, so the Project is
   where the choice is made once; a Brief's planner creates Containers and
   never has to know about licenses. A Container that must differ (a
   different license for one library) says so itself.
3. **The value is an SPDX id from a fixed set, an enum in the CRD schema.**
   The scaffold has to write the license's text, so it can only offer the
   ids it holds the text for; the schema refuses any other at admission,
   naming the set, and the UI can read the choices from the schema. The set
   is the common ones: `MIT`, `Apache-2.0`, `BSD-2-Clause`, `BSD-3-Clause`,
   `ISC`, `0BSD`, `MPL-2.0`, `GPL-2.0-only`, `GPL-3.0-only`,
   `LGPL-3.0-only`, `AGPL-3.0-only`, `Unlicense`. Each text is the SPDX
   license list's plain text, verbatim, embedded in the operator under
   `internal/scaffold/licenses/<id>.txt`; a test keeps the enum and the
   files in step. Adding a license is a text file and one enum entry. A free
   CEL check of the SPDX grammar was not chosen: it would accept an id the
   scaffold cannot write, and fail the Container later instead of the
   request now.
4. **The copyright line is filled where the license has one.** `MIT`, `ISC`,
   `BSD-2-Clause`, `BSD-3-Clause` and `0BSD` say `Copyright (c) <year>
   <holder>`: the year is the scaffold's, the holder the Project's
   `displayName`, else its name. The others are written as published
   (Apache-2.0's appendix placeholders included). A `holder` field was not
   chosen: the Project already has a name for the owner, and a second field
   for one line is more API than it is worth; the file is the app's after
   the scaffold and anyone may edit it.
5. **The operator writes it, once, as an operator path.** `LICENSE` joins
   `.c4factory.yaml` and `.devcontainer/` in `scaffold.Build`: a layer that
   writes `LICENSE` while a license is set is the existing two-layers
   collision, with the path in the message. It is read at scaffold only
   ("Scaffold once"): changing the setting later rewrites nothing, a
   Standard update never touches it, and `.c4factory.yaml` records the id
   written so a reader sees which. It is the app's file, not one of
   `model.FactoryOwnPaths`: a published source carries it, and agents may
   change it. A Container scaffolded before the setting existed, or before
   its Project set one, gets its `LICENSE` by a commit, as today.
6. **`LicenseMissing` names the setting.** Its message says to set
   `spec.license` on the Project or the Container before it is scaffolded,
   or to commit a license file, and that a Container already scaffolded
   takes the second way. Having the operator add the file to an existing
   repository through a pull request, as a Standard update does, was not
   chosen now: it is a second writer of the app's tree for one file; it may
   come when a second such file wants it.
7. **The webshop sample's Project sets `license: MIT`.** The webshop is the
   project's own reference app, so the choice is the project's; it makes the
   `webshop-github` sample's source publish, and gives `make validate-yaml`
   its valid case. The `first-app` sample stays without one, with a comment
   naming the field, so the empty path is a sample too.

## Steps

Each step is a GitHub issue that names #1788 and lands on its own with
`make check`. Step 1 is the API the others read; 2, 3 and 4 need only it.

| Step | Issue | Depends on | Done when |
| --- | --- | --- | --- |
| 1. This plan and the design change | #1788 (`Refs`) | | landed in `docs/plans/` and `docs/design.md` |
| 2. `spec.license` on Project and Container | #1859 | | the field on both kinds, optional, the enum of decision 3; `make manifests`; the webshop sample's Project sets `MIT` and `first-app`'s has the comment; `make validate-yaml` passes; envtest: an id outside the set is refused on both kinds, naming the field |
| 3. The scaffold writes `LICENSE` | #1861 | #1859 | the texts embedded, a test that the enum and the files match; `scaffold.Input` carries the id, the holder and the year; `scaffold.Build` writes `LICENSE` and records the id in `.c4factory.yaml`; `internal/scaffold` table tests (#1788): the set license, the Container's over the Project's, none set, a layer writing `LICENSE`, the copyright line filled and not; the controller reads the Project's `spec.license` and the Container's; envtest: a Container in a Project with a license is scaffolded with a non-empty `LICENSE` |
| 4. `LicenseMissing` names the setting | #1862 | #1859 | `PublishProblem.String` for `LicenseMissing` says decision 6; the `internal/model` table case and the envtest messages in `crew_publish_source_test.go` and `container_deploy_test.go` updated |
| 5. The UI shows the license | #1863 | #1859 | the Project page and the Container page show the license when set, the Container's with "from the Project" when it is the Project's; a UI test for each; `make ui-test` passes |

## Done when

Every step is merged: a Container in a Project with `spec.license` is
scaffolded with a non-empty `LICENSE` (the `internal/scaffold` table test
and the envtest spec of step 3), its source publishes without
`LicenseMissing`, and with no license set the `LicenseMissing` message names
`spec.license`.
