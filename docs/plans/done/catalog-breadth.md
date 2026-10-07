# Plan: more kinds of app and popular languages in the catalog

## Goal

A Brief's idea decides the kind of app, not the catalog. The catalog installed
by the chart covers the most common first ideas, each with the same quality
checks: a web app with pages, a service, a command-line tool, in Go, Python and
TypeScript. When an idea needs a kind of app the catalog doesn't have, the
Brief says so instead of quietly building something else.

Reported as #399 by `gray-lenovo`: asked for "a very simple todo list with a
fun theme", the planner built a Go CLI because it was "the smallest kind of app
the catalog can build and ship" and Go "the catalog's only framework for
command-line apps". The chart's catalog has three Frameworks (`go-cli`,
`python-fastapi`, `vscode-extension`), and `python-fastapi` is the only one
without a Standard.

## Where things stand

- Container types are an enum: `static-site`, `service`, `job`, `worker`,
  `mobile-app`, `cli`, `extension` (`api/v1alpha1/shared_types.go`). A web app
  with pages that runs in the browser is a `static-site`; one with its own
  server is a `service`. No catalog entry supports `static-site` or
  `mobile-app` (the e2e-only `hello-html` aside).
- A catalog entry is a folder in `templates/`, a line in
  `templates/versions.txt`, an entry in `dist/chart/values.yaml`'s
  `catalog.entries` at the same ref (`make validate-yaml` checks the pair), and
  a combo in `internal/scaffold/templates_test.go` (every folder needs one).
- A template renders `Input.Framework` and the other choices, and a template
  that renders to only whitespace leaves its file out
  (`internal/scaffold/scaffold.go`), so one Standard or Deployment can differ
  per Framework.
- CI runners: `base` (Node, bash, git, make) and `go` (Go, Node,
  golangci-lint). Neither has Python. Agents run in `agent-go`, built on the
  `go` toolchain. `gitea-release` runs `make dist` on the `go` runner.
- `k8s-manifests` is the only Deployment for an HTTP app, and its Dockerfile
  assumes `python-fastapi` (its README calls that a v1 limitation).
- The planner's guide (`internal/model/planner.md`) already allows
  `needs-input.md` for "a catalog entry the idea needs and the catalog doesn't
  have", but says nothing against building a different kind of app instead.

## Decisions

| # | Topic | Decision | Why |
| --- | --- | --- | --- |
| C1 | The planner is honest first | `planner.md` says: pick the Container type a person picturing the idea would expect (a todo list people click in has a screen), not the one the catalog makes easiest. When no Framework in `model/catalog.yaml` supports that type, write `needs-input.md` naming the kind of app and that the catalog can't build it yet; don't build another kind instead. `choices.md` says why the type fits the idea, never "the catalog only has". A follow-up prompt ("a CLI is fine") settles it. | The reporter's expectation, and it costs nothing; it lands before any new template, so every later step has the planner already choosing by the idea. |
| C2 | Python toolchain | Add a pinned `uv` to the `base` and `go` toolchain images (`hack/toolchain-base`, `hack/toolchain-go`), and so to the runners and agent images built on them. uv installs the Python a project pins (`.python-version`). No new runner label or image. | Every runner and agent can then check, build and release a Python project; a third runner Deployment would cost memory on every device for one language. |
| C3 | Python checks | A `python-standard` Standard for `python-fastapi`, then `python-cli`: ruff (lint, a strict rule set, and format), mypy `--strict`, pytest, `make check` and `make dist` (`uv build`), `AGENTS.md`, `docs/style/python.md`, the implementer and reviewer guides, the PR and issue templates, and `.gitea/workflows/check.yaml` on `runs-on: base`. Its protected paths mirror go-standard's. `python-fastapi` is bumped if its code doesn't pass. | The same contract as go-standard and ts-standard, so a Python app is held to the same bar. |
| C4 | Web app with pages | A `react-vite` Framework: React and TypeScript built with Vite, Vitest, type `static-site`, one page and a pure module with its tests, like `vscode-extension`. `ts-standard` bumps to support it; its style guides are Framework-specific through `Input.Framework` (`vscode.md` only for `vscode-extension`, `react.md` only for `react-vite`). | A page in the browser is the most common first idea; React and Vite are the most asked-for pair; a static site needs no server, so it fits the smallest Compute. One TypeScript Standard keeps one set of rules for one language. |
| C5 | Shipping a web app | `k8s-manifests` bumps to pick its Dockerfile by Framework: the uv image for `python-fastapi` as now, a Node build then `nginxinc/nginx-unprivileged` for `react-vite`; it supports `static-site`, and so does `small` (catalog spec only). | One Deployment stays "how an app runs on Kubernetes"; the per-Framework Dockerfile ends its v1 limitation without a second Deployment with the same files. |
| C6 | CLIs in other languages | A `python-cli` Framework (uv, a `[project.scripts]` entry point, stdlib `argparse`, a version flag, tests) under `python-standard`, and a `node-cli` Framework (TypeScript, Node's `util.parseArgs`, esbuild to one file, Vitest, a `bin` entry) under `ts-standard`. Both type `cli`, released by `gitea-release` through the Standard's `make dist` (a wheel and sdist; an `npm pack` tarball). | The two most asked-for languages after Go, with no dependencies beyond the toolchain, like `go-cli`. |
| C7 | Not now | A full-stack web app (pages and an API in one Container), `mobile-app`, and other languages wait for a report asking for them. A Brief that needs them gets C1's answer. | Each needs more than a template (a mobile build has no runner); the planner can already pair a `react-vite` page with a `python-fastapi` service. |
| C8 | Each step ships whole | A new entry lands with its folder, tag, `versions.txt` line, chart entry, combo row, `.c4factory/README.md` and `docs/getting-started.md` line. The chart catalog adds it in the same step. | A template not in the chart is invisible to a Brief. |

## Steps

Each step is its own issue and pull request and leaves main working.

1. **The design.** (#399) This plan. *Done when:* merged.
2. **The planner says when the catalog can't build the idea.** (#446) C1 in
   `internal/model/planner.md`. *Done when:* `make check` passes with a test
   that the embedded guide states the rule.
3. **uv in the toolchain images.** (#447) C2: a pinned uv in both toolchain
   Dockerfiles, the version in one place, the `gitea-release` and runner docs
   saying so. *Done when:* `make check` passes and both toolchain images build with
   `docker build` and run `uv --version` (where the device has Docker; the
   pull request says if it didn't). Kind would show a Python check passing on
   a runner; that is for a human.
4. **python-standard.** (#450) C3, for `python-fastapi`. Depends on step 3. *Done
   when:* `make check` passes with the combo rendered, `make validate-yaml`
   with the chart entry, and the scaffolded `python-fastapi` repository's
   `make check` passing locally under the Standard (`uv` on the device).
   *Choices made in the step:* the Standard writes `ruff.toml` and
   `mypy.ini`, which ruff and mypy read before `pyproject.toml`, so the rules
   are protected while the Framework keeps `pyproject.toml` (agents must add
   packages); python-fastapi drops its `[tool.ruff]` tables when a Standard is
   chosen. ruff selects `ALL` less four rules, pinned exactly so a ruff bump
   is a Standard change. python-fastapi ships `uv.lock.tmpl`, as
   vscode-extension ships its `package-lock.json`, so `uv sync --locked` passes
   on the first commit to `main`. CI keeps uv's cache and Python in `.uv/` in
   the checkout: the `base` runner's `/tmp` (256 MiB) can't hold them, and its
   disk is what the ts-standard workflow keeps npm off.
5. **react-vite.** (#448) C4: the Framework, the `ts-standard` bump. *Done when:*
   `make check` passes with the combo rendered and the scaffolded
   repository's `make check` passing locally.
6. **k8s-manifests for a static site.** (#451) C5. Depends on step 5. *Done when:*
   `make check` passes with both Dockerfiles rendered by combo, and, where
   the device has Docker, the scaffolded `react-vite` repository's image
   builds and serves its page.
7. **python-cli.** (#452) C6, under `python-standard` and `gitea-release`. Depends on
   step 4. *Done when:* `make check` passes and the scaffolded repository's
   `make check` and `make dist` pass locally.
   *Choices made in the step:* the package is `app` whatever the Container
   is called, as in python-fastapi, because template paths are never
   rendered; a CLI installs into its own environment (`uv tool install`), so
   the name doesn't collide. `main(argv=None) -> int` lets argparse exit for
   `--help`, `--version` and usage errors, the idiom its docs show, and tests
   catch that `SystemExit`. The version is read from the installed metadata,
   so `pyproject.toml` stays the one place it is set. python-standard v1.1.0
   adds a command-line section to `python.md`; gitea-release v1.0.3 puts uv's
   cache and Python in `.uv/` in the checkout, as the check workflow does,
   since the `go` runner's disk would stall the node.
8. **node-cli.** (#449) C6, under `ts-standard` and `gitea-release`. Depends
   on step 5 (both bump `ts-standard`). *Done when:*
   `make check` passes and the scaffolded repository's `make check` and
   `make dist` pass locally.

## Done

The chart's catalog lists `go-cli`, `python-cli`, `node-cli`,
`python-fastapi`, `react-vite` and `vscode-extension`, each with a Standard,
and a Brief whose idea needs anything else says so in `NeedsInput`.
