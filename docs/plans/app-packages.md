# One app uses another app's released npm or PyPI package

GitHub issue #1087. Agent: otter-1087. It follows
[app-modules.md](done/app-modules.md) (#430), which shares Go modules
between apps and left npm and PyPI to a later issue (its decision 9).

## Goal

A TypeScript or Python app uses a released version of another app's package
the way a Go app uses a module today: a planner writes `uses: [cel-lens-core]`
in a TypeScript or Python app's issue, and that issue is filed once the core's
release is published, with the version in its body. The CLI's agent pod and
its CI then install that version from Gitea.

Done when a Brief round whose TypeScript CLI issue uses a TypeScript core,
and one whose Python CLI uses a Python core, each file the issue only once
the core's package is published, with its install line in the body. The
consumer's pod, CI and release run must install the package from the owner's
registry in Gitea. A used app whose release published nothing other apps can
install keeps the issue waiting, and the Brief says why.

## Decisions, and why

The issue asks for what Go has: publish at tag, the registry set in agent
pods and in the Standards' CI, and `uses:` for these languages. These are
the choices under that, and where they differ from Go.

1. **The pattern stays Go's.** One repository per Container. The release
   is published to the owner's package registry in Gitea (Gitea 1.27 serves
   npm at `<gitea>/api/packages/<owner>/npm/` and PyPI at
   `<gitea>/api/packages/<owner>/pypi`). Pods and CI fetch from it, and a
   `uses:` issue waits for the published version. A version is published once
   and never replaced.
2. **The operator publishes the files the release run built.** A Go module
   is source, so the operator repacks the tag's archive itself. An npm
   package or a Python wheel is built output: compiled TypeScript and
   bundles, wheel metadata. The operator has no toolchain and runs no app's
   code. The release run already builds them. gitea-release's release workflow
   runs `make dist VERSION=<tag>` at the tag and attaches `dist/` to the Gitea
   release. node-cli's `dist` is `npm pack`'s tarball, and a Python app's is
   `uv build`'s wheel and sdist. Both Standards' `release-version` refuse a
   tag that isn't the manifest's version.

   So once the release runs succeed, the operator reads the release's files.
   It uploads each npm tarball and each Python wheel and sdist to the owner's
   registry, with the GitProvider credentials it already holds. This is what
   it already does for the GitHub copy (`GetRelease`, `OpenReleaseAsset`).

   The other way was CI publishing with its job token. That would put a
   package-write credential in CI, where agents' code runs. We also don't know
   whether Gitea 1.27's Actions token may write packages. An upload from the
   operator needs neither, and can be tested below Kind.
   - A file is recognised by its content, not its name:
     - a gzipped tar whose `package/package.json` exists is an npm tarball;
     - a `.whl` with `*.dist-info/METADATA` is a wheel;
     - a gzipped tar with `<name>-<version>/PKG-INFO` is an sdist.

     Anything else (a site, a `.vsix`, a binary, a Lambda zip) is left alone.
   - A file is published only at the tag's version. Its version must equal
     the tag without its `v`, with Python versions compared after PEP 440
     normalisation. Otherwise it is `Invalid` and is not published. A
     version already in the registry is left as it is, so a repeat
     publishes nothing.
   - Some releases attach no package: a Container without gitea-release (a
     service), or a Framework whose `dist` isn't a package (react-vite's site,
     a `.vsix`). Such a release publishes none, and its state says so.
3. **npm packages are named `@c4factory/<container>`.** npm sends a scope
   to a registry, and nothing finer: an unscoped name is fetched from the
   default registry, npmjs.org. So the factory's packages share one scope,
   `@c4factory`, and pods and CI send it to the owner's npm registry, as
   `c4factory.local/` is for Go.
   - The name is unique within the owner, as repository names are.
   - Why not `@<project>`: a project named like a public scope (`types`,
     `aws-sdk`) would hide that scope from its own apps.
   - node-cli names its package `@c4factory/{{ .Container }}` in
     `package.json` and its lock. It keeps `"private": true`, which only stops
     `npm publish` to npmjs.org. A Container scaffolded before renames its
     package in an issue of its own (scaffold once).
   - An npm tarball by another name is that app's own release file, a CLI
     people install from the release page. It is left unpublished. The state's
     message names it, with no Warning, since most apps are never used by
     another.

   The consumer's `package-lock.json` pins the `resolved` URL in Gitea and
   the `integrity`, so `npm ci` fetches those bytes from Gitea and never asks
   npmjs.org for them. The URL is the GitProvider's. In the chart, Gitea's
   `ROOT_URL`, the runners and the seeded GitProvider all use one URL, so a
   lock written in a pod installs in CI.

   A person's own `npm install` needs the same scope line; the Standard's
   README says how. Without it, npm would ask npmjs.org for `@c4factory/...`.
   Owning that scope on npmjs.org would close the gap, and only the owner can
   claim it (#1234, filed as proposed).
4. **PyPI packages keep the name `pyproject.toml` declares** (`{{ .Container }}`).
   PyPI has no scopes, but uv pins a package to one index. The consumer's
   `pyproject.toml` declares the owner's index as `c4factory`, with
   `explicit = true`, and `[tool.uv.sources]` pins each factory package to
   it. An explicit index serves only the packages pinned to it. So a factory
   package never comes from pypi.org, and a public package never comes from
   Gitea, even when an app shares its name. `uv.lock` records the index URL;
   it is the GitProvider's, as for npm. The Python Standards install with
   `uv sync --locked`, so pip needs no config.
5. **The registry is set in pods and CI, with credentials already held.**
   - **Agent pods.** `gitprovider.Repository` gains `NpmRegistry` and
     `PyPIIndex`, built from the GitProvider's URL and owner as `GoProxy` is.
     The implementer's and reviewer's pods get `C4_NPM_REGISTRY` and
     `C4_PYPI_INDEX`; the planner's gets neither, as with Go.
   - **c4agent.** It writes `~/.npmrc` (mode 0600) with
     `@c4factory:registry=<registry>` and the bot token as `_authToken` for
     the registry's path. uv reads the `~/.netrc` that c4agent already writes.
   - **Token.** The agent bot's token already has `read:package`
     (`AgentTokenScopes`).
   - **No loopback proxy.** Go has one (#518) because Go can't send one path
     to a proxy of its own. npm sends each scope, and uv each package, to its
     own registry, so our names never reach a public registry and public names
     never reach Gitea.
   - **CI.** These workflows write the same `~/.npmrc` and `~/.netrc` from the
     job's `gitea.token`, as go-standard's `check.yaml` writes its netrc:
     - the check workflows of ts-standard, python-standard and aws-standard;
     - gitea-release's release workflow, because its `make dist` installs
       the dependencies too.
   - **No new Secret.** No credential is stored that isn't already (the
     `secrets` skill).
6. **The Crew records what it published.** `status.lastRelease.packages`
   holds:
   - `state`:
     - `Waiting` until the release runs succeed;
     - `Published` once every package file of the release is in the
       registry;
     - `NoPackage` when the release has none;
     - `Invalid` when one can't be published at the tag's version;
     - `Failed` while the host can't be reached, tried again each poll.
   - `message`.
   - `published`: one `{ecosystem: npm|PyPI, name, version}` per package.

   Alongside the status there is:
   - a condition `PackagesPublished`;
   - a Normal Event `PackagePublished` and the log line `Published package`
     for an upload;
   - a Warning Event `InvalidPackage`, once per release.

   `goModule` stays as it is: a Container can have both, such as
   aws-lambda-go's CDK `package.json` beside its `go.mod`.
7. **`uses:` takes every language.** The plan check stays as it is, since it
   can't see a repository's files.

   A used app is ready once its release has settled both ways and published
   something other apps install:
   - settled: `goModule` is neither `Failed` nor `Waiting`, and `packages`
     is neither `Waiting` nor `Failed`;
   - published something: a Go module or packages.

   The issue's body gets one line per thing published, above the Brief's
   marker, each pinning the release:
   - `Uses c4factory.local/cel-lens/cel-lens-core v0.1.0: go get it at that version.`
     (unchanged)
   - `Uses npm package @c4factory/cel-lens-core 0.1.0: npm install --save-exact it at that version.`
   - `Uses PyPI package cel-lens-core 0.1.0 from the c4factory index <url>: uv add it at that version, pinned to that index.`

   Step 5 settles the exact words. Step 1's test shows the commands work.
   ts-standard's and python-standard's `AGENTS.md` say how to add such a
   dependency: the exact version, and for Python the explicit index and its
   source pin.

   A settled release that published nothing keeps the issue waiting. The
   Brief's message names the app, the release and why: no `go.mod`, and no
   package attached.

   The planner guide documents `uses:` for Go, TypeScript and Python:
   - `uses:` links apps of one language;
   - a TypeScript app that others use is a node-cli named
     `@c4factory/<name>`, with gitea-release;
   - a Python app that others use is a python-cli with gitea-release.
8. **Gitea only, for now.** The Registry port grows npm and PyPI as an
   optional capability, beside the Go module. The other hosts' rows in
   `docs/design.md` say what they offer: GitHub Packages has npm but no PyPI,
   and GitLab has both. They are not built here. The port's shape belongs to
   #1166, so step 1 adds its methods where `PublishGoModule` is when it
   starts.

## Steps

Each step is a GitHub issue, filed `status/proposed`, with one pull request
each and its own Done when. Their bodies name #1087.

| Step | Issue | Depends on | Done when |
| --- | --- | --- | --- |
| 1. Publish a release's npm and PyPI files to Gitea (`internal/gitprovider`) | #1229 | | Unit tests cover recognising and reading an npm tarball, a wheel and an sdist, the name and version checks (unscoped, a version that isn't the tag's, PEP 440 normalisation) and the upload requests. `make test-gitea` publishes a release's tarball, wheel and sdist, then installs each with the real `npm` (scope routed to the local Gitea) and the real `uv` (explicit index), using decision 7's commands; skipped with a message when `npm` or `uv` is not on `PATH`. A repeat publishes nothing. |
| 2. The Crew publishes its release's packages (`internal/controller`, `api/v1alpha1`) | #1232 | #1229 | envtest on the fake Git shows `packages` `Waiting` while the release runs run, `Published` with the list after they succeed, `NoPackage` for a release with no package file, an unscoped tarball left alone and named, `Invalid` for a version that isn't the tag's, and `Failed` retried, with decision 6's Events. |
| 3. Agent pods fetch from Gitea's npm and PyPI registries (`internal/controller` pod spec, `internal/agentrunner`, `cmd/c4agent`) | #1230 | | Pod-spec unit tests show `C4_NPM_REGISTRY` and `C4_PYPI_INDEX` for an in-cluster and an external GitProvider URL, and none on the planner's pod. The runner's test shows `~/.npmrc` written with mode 0600. `make test-gitea` installs a published scoped package as the restricted agent account with c4agent's `.npmrc`, over the TLS front the Go proxy test uses. |
| 4. The Standards and the release workflow fetch from Gitea; node-cli names its package `@c4factory/<container>` (`templates/`, `versions.txt`, the chart's catalog) | #1231 | | ts-standard, python-standard, aws-standard and gitea-release write `~/.npmrc` and `~/.netrc` from `gitea.token`. The AGENTS.md of ts-standard and python-standard say how to add a factory package. node-cli's `package.json` and lock carry the scoped name. New tags are in `versions.txt` and the catalog. `make validate-yaml` and the scaffold tests pass. |
| 5. `uses:` takes npm and PyPI packages (`internal/model`, `internal/controller/brief_uses.go`, the planner guide) | #1233 | #1232, #1230, #1231 | Table tests show the body line for each ecosystem and the message when nothing is published. envtest applies a two-Container round whose TypeScript CLI issue uses a TypeScript core: it stays unready until the core's packages are `Published`, then is ready with the npm line. A Python pair does the same, and a restart in between finishes the round. `internal/model/planner.md` documents decision 7. This plan moves to `done/`. |

#1087 waits on these steps (`status/waiting`) and closes itself when the last
one closes. #1234, claiming the npm scope, is a follow-up that blocks no step.

## What only Kind would show

That a TypeScript or Python CLI's agent, on a real node with the real agent
image, installs `@c4factory/cel-lens-core@0.1.0` or `cel-lens-core==0.1.0`
from the in-cluster Gitea and builds. Also that the consumer's check and
release runs, on the Gitea Actions runner, install the same version from its
lock file with the job's token.

`make test-gitea` covers the publish and the install with a real Gitea and
the real `npm` and `uv`. envtest covers the release's state, the pods' env
and the round's order.
