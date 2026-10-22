# One app uses another app's released npm or PyPI package

GitHub issue #1087. Agent: otter-1087. It follows
[app-modules.md](done/app-modules.md) (#430), which shares Go modules
between apps and left npm and PyPI to a later issue (its decision 9).
Amended by #1234 (wren-1234, 2026-10-22) on the owner's direction: the npm
scope, the npm registry and the PyPI index are settings (decisions 3 and 5),
and the project claims no name on npmjs.org.

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
3. **npm packages are named `<scope>/<container>`, and the scope is a
   setting.** npm sends a scope to a registry, and nothing finer: an
   unscoped name is fetched from the default registry, npmjs.org. So the
   factory's packages share one scope, and pods and CI send it to the npm
   registry decision 5 names, as `c4factory.local/` is for Go. The scope is
   `GitProvider.spec.packages.npm.scope`, `@c4factory` by default.
   - The name is unique within the owner, as repository names are.
   - Why not `@<project>`: a project named like a public scope (`types`,
     `aws-sdk`) would hide that scope from its own apps.
   - Why a setting, and why the project claims nothing on npmjs.org (#1234,
     the owner's direction): where a user publishes is their choice, and a
     choice that depends on the user's environment is a setting with a
     working default. By default nothing leaves the cluster: the scope
     routes to the in-cluster Gitea everywhere the factory installs, and a
     name nobody holds on npmjs.org is never asked for there. A user who
     publishes elsewhere sets a scope they hold, with their registry
     (decision 5); their scope is their claim, wherever they made it. A
     scope is `@` and a package-name part (CEL: `^@[a-z0-9][a-z0-9._-]*$`).
   - node-cli names its package `{{ .NpmScope }}/{{ .Container }}` in
     `package.json` and its lock; `scaffold.Input` gains `NpmScope`, filled
     from the GitProvider. It keeps `"private": true`, which only stops
     `npm publish` to npmjs.org. A Container scaffolded before renames its
     package in an issue of its own (scaffold once); so does one whose
     GitProvider's scope changed after it was scaffolded.
   - An npm tarball under another scope, or none, is that app's own release
     file, a CLI people install from the release page. It is left
     unpublished. The state's message names it and the scope it lacks, with
     no Warning, since most apps are never used by another.

   The consumer's `package-lock.json` pins the `resolved` URL in the
   registry and the `integrity`, so `npm ci` fetches those bytes from it and
   never asks npmjs.org for them. The URL is the registry setting's. In the
   chart, Gitea's `ROOT_URL`, the runners and the seeded GitProvider all use
   one URL, so a lock written in a pod installs in CI.

   A person's own `npm install` needs the same scope line; the Standard's
   README says how, naming the scope and registry the GitProvider holds.
   Without it, npm would ask npmjs.org, which holds nothing under the scope
   unless the user publishes there.
4. **PyPI packages keep the name `pyproject.toml` declares** (`{{ .Container }}`).
   PyPI has no scopes, but uv pins a package to one index. The consumer's
   `pyproject.toml` declares the factory's index as `c4factory`, with
   `explicit = true`, and `[tool.uv.sources]` pins each factory package to
   it. `c4factory` is the index's name inside `pyproject.toml`, whatever its
   URL (decision 5); it claims nothing anywhere. An explicit index serves
   only the packages pinned to it. So a factory
   package never comes from pypi.org, and a public package never comes from
   Gitea, even when an app shares its name. `uv.lock` records the index URL;
   it is the index setting's, as for npm. The Python Standards install with
   `uv sync --locked`, so pip needs no config.
5. **The registries are settings on the GitProvider, defaulting to the
   host's own.** `GitProvider.spec.packages` is optional:

   ```yaml
   spec:
     packages:            # absent: the host's own registries, scope @c4factory
       npm:
         scope: "@acme"
         registry: https://npm.example.com/
         credentialsSecretRef: {name: npm-acme}
       pypi:
         index: https://pypi.example.com/simple
         upload: https://pypi.example.com/upload
         credentialsSecretRef: {name: pypi-acme}
   ```

   Each field has a default: the scope `@c4factory`, and the URLs the host's
   own (Gitea 1.27: `<url>/api/packages/<owner>/npm/`,
   `<url>/api/packages/<owner>/pypi/simple` and
   `<url>/api/packages/<owner>/pypi`). A URL matches `^https?://`. With no
   `credentialsSecretRef`, the operator publishes with the GitProvider's
   credentials and pods and CI install with the credentials they already
   hold, so the default stores no new Secret. A registry's Secret has the
   key `token`, which publishes and which only the operator reads, and may
   have `readToken`, which pods and CI install with; without one, installs
   send no credential (a public registry, such as npmjs.org for a public
   scope). The chart passes `packages` through from `gitProviders[]` and
   `gitea.projects[]` and renders no Secret: the user makes it, as they
   make a DeployTarget's (the `secrets` skill). This is `docs/design.md`'s
   rule that Helm decides what is installed and custom resources what is
   used: the GitProvider names the host, and the registries default to the
   host's own. Any registry that takes `npm publish`'s PUT document, and any
   index that takes twine's upload form, works through the settings; the
   operator speaks only those two protocols (decision 8).
   - **Agent pods.** `gitprovider.Repository` gains `NpmScope`,
     `NpmRegistry`, `PyPIIndex` and `PyPIUpload`, built from `spec.packages`
     with the defaults from the GitProvider's URL and owner, as `GoProxy`
     is. The implementer's and reviewer's pods get `C4_NPM_SCOPE`,
     `C4_NPM_REGISTRY` and `C4_PYPI_INDEX`; the planner's gets none, as
     with Go. The token a pod installs with is the agent bot's for the
     host's own registry, else the Secret's `readToken`, an env from the
     Secret as the bot token is.
   - **c4agent.** It writes `~/.npmrc` (mode 0600) with
     `<scope>:registry=<registry>` and the token as `_authToken` for the
     registry's host and path. uv reads the `~/.netrc` that c4agent already
     writes; an index on another host gets its own line there.
   - **Token.** The agent bot's token already has `read:package`
     (`AgentTokenScopes`).
   - **No loopback proxy.** Go has one (#518) because Go can't send one path
     to a proxy of its own. npm sends each scope, and uv each package, to its
     own registry, so our names never reach a public registry and public names
     never reach ours.
   - **CI.** The workflows read the settings from the owner's Actions
     variables and secrets, with the host's own as the fallback:
     `vars.C4_NPM_SCOPE` (else `@c4factory`), `vars.C4_NPM_REGISTRY` and
     `vars.C4_PYPI_INDEX` (else `gitea.server_url` and
     `gitea.repository_owner` in the paths above), `secrets.C4_NPM_TOKEN`
     and `secrets.C4_PYPI_TOKEN`. The fallback to `gitea.token` holds only
     while the matching variable is unset (the host's own registry); with
     `vars.C4_NPM_REGISTRY` or `vars.C4_PYPI_INDEX` set and its secret
     unset, the workflow installs without a credential, never sending
     `gitea.token` to another registry. The operator sets
     them on the GitProvider's owner (Gitea's organisation Actions variables
     and secrets, `PUT /orgs/{org}/actions/{variables,secrets}/{name}`)
     when `spec.packages` departs from the default, and deletes them when
     it returns to it; by default none is set and the workflows need
     nothing. The owner-level secrets hold the registry Secrets' `readToken`,
     never `token`: a workflow installs and never publishes with them, and
     `token` reaches no code the agents wrote. A registry Secret without
     `readToken` sets no owner secret, and the workflow installs without a
     credential (`internal/controller/project_packages.go`). The workflows write `~/.npmrc` and `~/.netrc` from them, as
     go-standard's `check.yaml` writes its netrc:
     - the check workflows of ts-standard, python-standard and aws-standard;
     - gitea-release's release workflow, because its `make dist` installs
       the dependencies too.
   - **Credentials.** The default stores no credential that isn't already
     stored (the `secrets` skill). `docs/design.md`'s rule that a user's
     credential never reaches code the agents wrote holds for `token`, the
     one that publishes. `readToken` is one the user makes for installs
     alone and gives for that; it travels as the agent bot's token does,
     and the design says so.
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
     (`@c4factory` is the scope setting's value)
   - `Uses PyPI package cel-lens-core 0.1.0 from the c4factory index <url>: uv add it at that version, pinned to that index.`
     (`<url>` is the index setting's)

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
     `<scope>/<name>`, with gitea-release;
   - a Python app that others use is a python-cli with gitea-release.
8. **Gitea's protocols only, for now.** The Registry port grows npm and
   PyPI as an optional capability, beside the Go module. The operator
   speaks npm's publish document and twine's upload form, which Gitea,
   npmjs.org, GitHub Packages (npm) and GitLab all take, so decision 5's
   settings reach them with no adapter of their own. The other hosts' rows
   in `docs/design.md` say what each offers: GitHub Packages has npm but no
   PyPI, and GitLab has both. Their adapters are not built here. The port's shape belongs to
   #1166, so step 1 adds its methods where `PublishGoModule` is when it
   starts.

## Steps

Each step is a GitHub issue, filed `status/proposed`, with one pull request
each and its own Done when. Their bodies name #1087.

| Step | Issue | Depends on | Done when |
| --- | --- | --- | --- |
| 1. Publish a release's npm and PyPI files to Gitea (`internal/gitprovider`) | #1229 | | Unit tests cover recognising and reading an npm tarball, a wheel and an sdist, the name and version checks (unscoped, a version that isn't the tag's, PEP 440 normalisation) and the upload requests, with the default and a custom scope and registry URLs: under scope `@acme`, a tarball named `@c4factory/x` is left alone, and an upload goes to the custom registry and upload URLs. `make test-gitea` publishes a release's tarball, wheel and sdist, then installs each with the real `npm` (scope routed to the local Gitea) and the real `uv` (explicit index), using decision 7's commands; skipped with a message when `npm` or `uv` is not on `PATH`. A repeat publishes nothing. |
| 2. The Crew publishes its release's packages (`internal/controller`, `api/v1alpha1`) | #1232 | #1229 | envtest on the fake Git shows `packages` `Waiting` while the release runs run, `Published` with the list after they succeed, `NoPackage` for a release with no package file, an unscoped tarball left alone and named, `Invalid` for a version that isn't the tag's, and `Failed` retried, with decision 6's Events; and, on a GitProvider whose `spec.packages` sets scope `@acme`, a tarball named `@c4factory/x` left alone and named with the scope. |
| 3. Agent pods fetch from the npm registry and PyPI index the GitProvider names (`api/v1alpha1` `spec.packages`, `internal/controller` pod spec, `internal/agentrunner`, `cmd/c4agent`) | #1230 | | `GitProvider.spec.packages` is in the CRD with its CEL (the scope pattern, `^https?://`), and envtest shows a bad scope refused. Pod-spec unit tests show `C4_NPM_SCOPE`, `C4_NPM_REGISTRY` and `C4_PYPI_INDEX` for an in-cluster and an external GitProvider URL with the defaults, and for a `spec.packages` with its own scope, URLs and `readToken`; none on the planner's pod. The runner's test shows `~/.npmrc` written with mode 0600, with the default and a custom scope. A unit test on the fake Git shows the owner's Actions variables and secrets set for a non-default `spec.packages` and deleted for the default. `make test-gitea` installs a published scoped package as the restricted agent account with c4agent's `.npmrc`, over the TLS front the Go proxy test uses. |
| 4. The Standards and the release workflow fetch from the registry settings; node-cli names its package `<scope>/<container>` (`templates/`, `versions.txt`, the chart's catalog, `internal/scaffold`) | #1231 | | ts-standard, python-standard, aws-standard and gitea-release write `~/.npmrc` and `~/.netrc` from the owner's Actions variables and secrets, falling back to `gitea.server_url` and `gitea.token`. The AGENTS.md of ts-standard and python-standard say how to add a factory package. node-cli's `package.json` and lock carry the scoped name, and a scaffold test shows it with the default scope and with a custom one. New tags are in `versions.txt` and the catalog. `make validate-yaml` and the scaffold tests pass. |
| 5. `uses:` takes npm and PyPI packages (`internal/model`, `internal/controller/brief_uses.go`, the planner guide) | #1233 | #1232, #1230, #1231 | Table tests show the body line for each ecosystem, with the default and a custom scope, and the message when nothing is published. envtest applies a two-Container round whose TypeScript CLI issue uses a TypeScript core: it stays unready until the core's packages are `Published`, then is ready with the npm line. A Python pair does the same, and a restart in between finishes the round. `internal/model/planner.md` documents decision 7. This plan moves to `done/`. |

#1087 waits on these steps (`status/waiting`) and closes itself when the last
one closes. #1234 amended this plan before any step was built: steps 1 to 4
name it under `Depends on`, and it closed with the amendment.

## What only Kind would show

That a TypeScript or Python CLI's agent, on a real node with the real agent
image, installs `@c4factory/cel-lens-core@0.1.0` or `cel-lens-core==0.1.0`
from the in-cluster Gitea and builds, and, with a `spec.packages` naming a
registry outside the cluster, from that registry with its `readToken`. Also that the consumer's check and
release runs, on the Gitea Actions runner, install the same version from its
lock file with the job's token.

`make test-gitea` covers the publish and the install with a real Gitea and
the real `npm` and `uv`. envtest covers the release's state, the pods' env
and the round's order.
