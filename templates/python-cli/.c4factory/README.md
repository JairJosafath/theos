# python-cli (Framework)

A command-line program in Python, managed with [uv](https://docs.astral.sh/uv/):
flags parsed with the standard library's `argparse`, a `main` tests call
directly, a pure module with its unit tests, and a `[project.scripts]` entry
point, released as a wheel and an sdist.

- **Supports:** `cli`
- **Layout:** `app/cli.py` holds `main(argv=None) -> int`, the entry point
  `[project.scripts]` maps the Container's name to; it parses, writes results
  to stdout and returns the exit code (argparse exits 0 after `--help` and
  `--version`, 2 for a wrong command line). Logic in `app/*.py` modules,
  `app/__main__.py` for `python -m app`, tests in `tests/`.
- **Writes:** `pyproject.toml`, `uv.lock`, `.python-version`, `app/`, `tests/`,
  `README.md`, `CHANGELOG.md`, `.gitignore`
- **Package:** `name` is the Container, version `0.1.0`, Python 3.12 or later,
  no dependencies; `uv_build` builds the `app` package from the root. The
  program reads its version from the installed metadata
  (`importlib.metadata`), so `pyproject.toml` is the one place it is set.
- **Flags:** `-n, --name`, `--version` (prints `<name> <version>`) and
  `-h, --help`.
- **Standard:** fits python-standard's contract (v1.1.0 and later): its `dev`
  group pins `ruff`, `mypy` and `pytest` exactly, `uv_build` builds it, and its
  code passes ruff with every rule and mypy `--strict`. With a Standard,
  `pyproject.toml` leaves out its own `[tool.ruff]` tables.
- **Release:** python-standard's `make dist VERSION=v1.2.3` writes
  `dist/<name>-1.2.3-py3-none-any.whl` and `dist/<name>-1.2.3.tar.gz` (the
  name with `_` for `-`), which gitea-release attaches to the release.
- **Lock:** `uv.lock.tmpl` is what `uv lock` writes for the rendered project,
  with only the Container's name templated. After changing the dependencies,
  render it (`go run ./hack/render`), run `uv lock`, and template the name
  again; the lock is right when `uv lock --check` passes in a render.
- **Devcontainer:** `.c4factory/devcontainer.json` uses the
  `mcr.microsoft.com/devcontainers/python:3.12` image, installs uv with pipx,
  runs `uv sync` after create, and sets up the Python and ruff VS Code
  extensions.

## How a template works

Everything in this repository is copied into the new Container repository at the
same path, except `.c4factory/`, which is input for the factory. Files ending in
`.tmpl` are rendered with Go `text/template` (missing keys are errors) and lose the
suffix; a `.tmpl` that renders to only whitespace is left out. Other files are copied
byte for byte. Paths are never rendered, only contents, so the package is `app`
whatever the Container is called.

Data: `.Project`, `.SoftwareSystem`, `.Container`, `.DisplayName`, `.Type`, the chosen
`.Framework`, `.Standard`, `.Compute` and `.Deployment` (empty when none is chosen),
and `.Connectors` (sorted names). Functions: `has`, `snake`, `upper`, `lower`, `dart`,
plus the `text/template` builtins.

Each path belongs to exactly one layer; `.devcontainer/**` and `.c4factory.yaml`
belong to the operator. Release a change by tagging a new version and bumping it in
`templates/versions.txt` of C4 Factory; a tag is never moved.
