# python-cli (Framework)

A command-line program in Python, managed with [uv](https://docs.astral.sh/uv/):
flags parsed with the standard library's `argparse`, a `main` tests call
directly, a pure module with its unit tests, and a `[project.scripts]` entry
point, released as a wheel and an sdist. As a `worker`, the same project runs a
long-running process instead: `python -m app` does a round of work, waits, and
stops cleanly on SIGTERM. As a `job`, `python -m app` does its work once and
exits.

- **Supports:** `cli`, `worker` (since v1.1.0), `job` (since v1.2.0)
- **Layout (`cli`):** `app/cli.py` holds `main(argv=None) -> int`, the entry point
  `[project.scripts]` maps the Container's name to; it parses, writes results
  to stdout and returns the exit code (argparse exits 0 after `--help` and
  `--version`, 2 for a wrong command line). Logic in `app/*.py` modules,
  `app/__main__.py` for `python -m app`, tests in `tests/`.
- **Layout (`worker`):** `app/worker.py` holds `main() -> int`, the entry
  point, which logs to stderr, installs the SIGTERM and SIGINT handlers and
  calls `run(work, stop)`: a round of `work`, then `stop.sleep`, until a
  signal asks to stop. The handler only notes the signal, so the round in
  progress finishes, and the wait looks every tenth of a second; `main`
  returns 0. A round that raises is logged with its traceback and the worker
  goes on, waiting `app/backoff.py`'s `next_delay(failures)`: 10 seconds,
  doubled per failure in a row, at most 300. `tests/test_worker.py` drives
  `run` with a fake `Stop` and sends a real SIGTERM to `python -m app`. The
  worker has no `cli.py`, `greeting.py` or flags, and no `--version`.
- **Layout (`job`):** `app/job.py` holds `main() -> int`, the entry point,
  which logs to stderr and calls `run(work)`: `work` once, then the exit
  code, 0 when it returned and 1 when it raised (logged with its
  traceback). A job must run once and return: exit 0 when done and
  non-zero on failure, and never loop or wait for SIGTERM the way a worker
  does; work that goes on is a worker. A run must be safe to redo, as a
  failed one is retried (k8s-manifests' Job retries it up to its
  `backoffLimit`, 3). A job has its own `job.py` rather than the worker's
  loop with a note, so the scaffold already does what a job owes its
  runner. `tests/test_job.py` drives `run` and `main` with fakes. Like the
  worker, it has no `cli.py`, `greeting.py`, `backoff.py`, flags or
  `--version`.
- **Writes:** `pyproject.toml`, `uv.lock`, `.python-version`, `app/`, `tests/`,
  `README.md`, `CHANGELOG.md`, `.gitignore`; the files of the other type
  render empty and are left out.
- **Deployment:** k8s-manifests (v1.3.0 and later) builds a worker's image
  (`CMD ["python", "-m", "app"]`, user 1000) and runs it without a port, probes
  or Service; v1.9.0 and later run a job's image the same way in a `batch/v1`
  Job.
- **Package:** `name` is the Container, version `0.1.0`, Python 3.12 or later,
  no dependencies; `uv_build` builds the `app` package from the root. The
  program reads its version from the installed metadata
  (`importlib.metadata`), so `pyproject.toml` is the one place it is set.
- **Flags (`cli`):** `-n, --name`, `--version` (prints `<name> <version>`) and
  `-h, --help`.
- **Standard:** fits python-standard's contract (v1.1.0 and later): its `dev`
  group pins `ruff`, `mypy` and `pytest` exactly, `uv_build` builds it, and its
  code passes ruff with every rule and mypy `--strict`. With a Standard,
  `pyproject.toml` leaves out its own `[tool.ruff]` tables. A worker needs
  python-standard v1.4.0 or later, whose agent guide has the worker's rules,
  and a job python-standard v1.6.0 or later.
- **Release:** python-standard's `make dist VERSION=v1.2.3` writes
  `dist/<name>-1.2.3-py3-none-any.whl` and `dist/<name>-1.2.3.tar.gz` (the
  name with `_` for `-`), which gitea-release attaches to the release.
- **Lock:** `uv.lock.tmpl` is what `uv lock` writes for the rendered project,
  with only the Container's name templated. After changing the dependencies,
  render it (`go run ./hack/render`), run `uv lock`, and template the name
  again; the lock is right when `uv lock --check` passes in a render.
- **Devcontainer:** `.theos/devcontainer.json` uses the
  `mcr.microsoft.com/devcontainers/python:3.12` image, installs uv with pipx,
  runs `uv sync` after create, and sets up the Python and ruff VS Code
  extensions.

## How a template works

Everything in this repository is copied into the new Container repository at the
same path, except `.theos/`, which is input for the factory. Files ending in
`.tmpl` are rendered with Go `text/template` (missing keys are errors) and lose the
suffix; a `.tmpl` that renders to only whitespace is left out. Other files are copied
byte for byte. Paths are never rendered, only contents, so the package is `app`
whatever the Container is called.

Data: `.Project`, `.SoftwareSystem`, `.Container`, `.DisplayName`, `.Type`, the chosen
`.Framework`, `.Standard`, `.Compute` and `.Deployment` (empty when none is chosen),
and `.Connectors` (sorted names). Functions: `has`, `snake`, `upper`, `lower`, `dart`,
plus the `text/template` builtins.

Each path belongs to exactly one layer; `.devcontainer/**` and `.theos.yaml`
belong to the operator. Release a change by tagging a new version and bumping it in
`templates/versions.txt` of theos; a tag is never moved.
