# python-standard (Standard)

How Python code in a C4 Factory Container is written, checked and reviewed:
ruff with every rule, ruff's formatter, mypy `--strict`, pytest, the agent guide
and role guides, offline style digests, issue and pull request templates, and
the `make check` contract CI runs.

- **Supports:** `service`, `cli`, `worker` (since v1.4.0, with `python-cli`);
  Frameworks: `python-fastapi`, `python-cli`
- **Writes:** `ruff.toml`, `mypy.ini`, `.editorconfig`, `Makefile`, `AGENTS.md`,
  `docs/style/`, `docs/agents/implementer.md`, `docs/agents/reviewer.md`,
  `.gitea/workflows/check.yaml`, `.gitea/ISSUE_TEMPLATE/task.md`,
  `.gitea/PULL_REQUEST_TEMPLATE.md`
- **Lints:** ruff 0.16 with `select = ["ALL"]` less four rules (trailing
  commas, which the formatter decides; copyright headers; a TODO's author and
  the ban on TODOs, as a TODO must still name its issue), Google docstrings,
  no relative imports; tests may `assert` and skip docstrings. mypy 2.4 with
  `strict`, `warn_unreachable` and the optional error codes that catch real
  bugs (`ignore-without-code`, `redundant-expr`, `truthy-bool`,
  `possibly-undefined`, `explicit-override`, ...). Both read their own files
  before `pyproject.toml`, so a `[tool.ruff]` or `[tool.mypy]` table there
  changes nothing.
- **Check contract:** `make check` = `uv sync --locked` (fails when `uv.lock`
  is stale), `ruff format --diff`, `ruff check`, `mypy .`,
  `pytest --strict-markers --strict-config`, and `uv build` into a temporary
  directory, so a packaging problem fails CI. `.gitea/workflows/check.yaml`
  runs it on pull requests and on `main` on a runner labelled `base`, whose
  toolchain image has uv; uv installs the Python `.python-version` pins. Its
  cache and that Python (about 250 MB for python-fastapi) go in `.uv/` in the
  checkout, in the runner's memory-backed work directory, which the job's end
  frees; the checks skip `.uv/`. The status context is `check / check`, which
  the protected `main` requires (Gitea Actions' check contexts, `model.CI` in `internal/model/ci.go`).
  A change to only `CHANGELOG.md`, `docs/` or images (`.png`, `.jpg`, `.gif`,
  `.svg`) skips the steps after the checkout and passes, so the context still
  reports; any other file, `README.md` included, or a first push runs them.
- **Checks before a release** (`checksBeforeRelease: true`): with the
  Container's `checks: BeforeRelease`, `check.yaml` renders a `check` job that
  runs `make check-light` (`uv sync --locked`, `ruff format --diff`, `ruff
  check`, `mypy .`; no tests) on pull requests and on `main`, with the same
  skip, and a `full` job that runs `make check` on a push to a
  `release-check/**` branch, whatever the push changes (status context `check
  / full (push)`). With `EveryChange`, the default, it renders as before.
- **Releases:** `make dist VERSION=v1.2.3` builds `dist/<name>-1.2.3.tar.gz`
  and the wheel, and fails when the tag doesn't match `pyproject.toml`'s
  `version`; gitea-release attaches both for a CLI.
- **Factory packages:** the index comes from the GitProvider's `spec.packages`
  (the host's own PyPI index by default,
  `<gitea>/api/packages/<owner>/pypi/simple`); the agent's pod and CI log in
  to its host through a `~/.netrc` they write (since v1.5.0). On your own
  machine, add `machine <index host> login <you> password <token>` to
  `~/.netrc` (mode 0600) with a token of yours that may read packages.
  `AGENTS.md` says how a package is declared in `pyproject.toml`.
- **Framework contract:** the Framework owns `pyproject.toml`, `uv.lock` and
  `.python-version`. `pyproject.toml` lists this Standard's tools in its `dev`
  dependency group with exact versions (`ruff`, `mypy`, `pytest`), has a
  `[build-system]` that `uv build` can use, and sets pytest's `testpaths`;
  `uv.lock` matches it. Its code passes every check. python-fastapi (v1.3.0
  and later) and python-cli do all of this.
- **Role guides:** `docs/agents/implementer.md` and `docs/agents/reviewer.md`,
  which the factory gives its implementer and reviewer Agents, read from `main`.
- **Issues:** `.gitea/ISSUE_TEMPLATE/task.md` has the sections the factory needs;
  an issue without an unchecked item under "Acceptance criteria" is never picked up.
- **Style digests:** `docs/style/` distils PEP 8, PEP 257, the Google Python
  Style Guide, the typing and mypy docs, pytest's good practices and the
  FastAPI tutorial (for services) and the `argparse` docs (for CLIs), plus
  cross-language principles. Every rule is marked
  `[lint]` or `[review]`; `docs/style/SOURCES.md` lists the sources with dates.
- **Protect:** list `AGENTS.md`, `Makefile`, `ruff.toml`, `mypy.ini`,
  `.editorconfig`, `docs/style/` and `docs/agents/` in the catalog entry's
  `protectedPaths`; `.gitea/` is always protected.

## How a template works

Everything in this repository is copied into the new Container repository at the
same path, except `.c4factory/`, which is input for the factory. Files ending in
`.tmpl` are rendered with Go `text/template` (missing keys are errors) and lose the
suffix. Other files, such as the workflow with its `${{ }}` expressions, are copied
byte for byte.
