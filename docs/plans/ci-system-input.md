# Templates render the Container's CI system's files

Issue: #1095. Written 2026-10-15, after #1093 put the per-CI table in
`internal/model` and named the CI system an input to scaffolding
([design](../design.md#factory-components-ports-and-adapters)).

## Goal

The CI system a Container's Git host runs is an input to scaffolding and to
the rules that protect its files. Gitea Actions stays the only one, and every
Container renders and is protected as before.

## Choices

- **One row per CI system in `internal/model` (`ci.go`).** A `CI` names the
  system (`giteaActions`, the member name `GitProvider.spec.ci` will use), the
  check contexts a protected `main` requires and the paths the platform
  protects for it. `model.HostCI` gives a host's own CI system: Gitea Actions
  on `gitea`. `UnpassedChecks`, `PlatformProtectedPaths`,
  `AgentProtectedPaths`, `FactoryOwnPaths` and the merge refusals take the
  `CI` (or its contexts) instead of reading constants.
- **No `GitProvider.spec.ci` yet.** Its only member would be `giteaActions`,
  which is already the default, so the field would offer a choice the
  operator cannot make. It comes with the second CI system, with its CEL rule
  allowing exactly one member. Until then the controllers take the CI from
  `GitProvider.spec.provider`.
- **A template keeps each CI system's files under `.c4factory/ci/<name>/`.**
  That folder is rendered at the repository root, as the rest of the layer
  is, only when it names the Container's CI system; the other systems'
  folders are left out. A template with a `.c4factory/ci/` folder but none
  for the Container's CI system doesn't support it: `scaffold` fails with an
  `UnsupportedCIError` and the Container with reason `UnsupportedCI`, as an
  unsupported Container type fails it. A template with no `.c4factory/ci/`
  (a Framework) works with any CI system. This needs no path templating:
  a CI system's files differ in content as well as in path (a workflow folder
  against one `.gitlab-ci.yml`), so a whole subtree per system is simpler
  than templated paths.
- **`Input.CI` is the `CI` row**, so a template's text can name the system
  (`{{ .CI.Name }}`) and the check contexts.
- **The eight templates move their `.gitea/` folder under
  `.c4factory/ci/giteaActions/`,** unchanged inside, and take a new minor tag: an older operator would
  leave their CI files out.
  They render the same files for Gitea Actions; a Standard update to the new
  tag plans no change for them.
- **The agent runner trusts the operator's list.** The sandbox already gets
  the full protected list (`C4_PROTECTED_PATHS`, from `AgentProtectedPaths`);
  the runner adds only the operator's own paths, which are the same for every
  CI system.

## Steps

1. `internal/model`: the CI table, and the rules taking it, with table tests.
2. `internal/scaffold`: `Input.CI`, the per-CI subtree and the refusal, with
   table tests rendering every Standard and gitea-release for Gitea Actions
   to today's paths and refusing another CI system.
3. Templates moved and tagged; the chart's catalog follows `versions.txt`.
4. Controllers and the agent runner pass the host's CI; reason `UnsupportedCI`.

## Done when

`make check` passes with the tests above, and the issue's checklist holds.
