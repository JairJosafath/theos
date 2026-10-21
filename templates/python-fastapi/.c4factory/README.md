# python-fastapi (Framework)

A Python service with [FastAPI](https://fastapi.tiangolo.com/), managed with
[uv](https://docs.astral.sh/uv/), linted with ruff and tested with pytest.

- **Supports:** `service`
- **Writes:** `pyproject.toml`, `uv.lock`, `.python-version`, `app/`, `tests/`, `README.md`, `.gitignore`
- **Standard:** fits python-standard's contract: its `dev` group pins `ruff`,
  `mypy` and `pytest` exactly, `uv_build` builds it, and its code passes ruff
  with every rule and mypy `--strict`. With a Standard, `pyproject.toml` leaves
  out its own `[tool.ruff]` tables (the Standard's `ruff.toml` decides).
- **Lock:** `uv.lock.tmpl` is what `uv lock` writes for the rendered project,
  with the Container's name and the `postgres` packages templated. After
  changing the dependencies, render both variants (`go run ./hack/render`),
  run `uv lock` in each, and rebuild the template from the two locks; the
  lock is right when `uv lock --check` passes in each render.
- **Devcontainer:** `.c4factory/devcontainer.json` holds the base settings: the
  `mcr.microsoft.com/devcontainers/python:3.12` image, uv installed with pipx, `uv sync`
  after create, and the Python and ruff VS Code extensions. The operator merges it
  into `.devcontainer/devcontainer.json`; with Connectors, the same image becomes
  the `app` service of the Docker Compose setup.
- **Logs:** `app/logs.py` sends every log record to stdout as a JSON line, at
  `LOG_LEVEL`, and a middleware logs one line per request (method, path without
  its query, status, `durationMs`, `requestId` from `X-Request-Id` or a new one,
  echoed in the response). uvicorn's access log is off; only the standard library.
- **Connectors it knows:** `postgres` adds `psycopg[binary,pool]` to the dependencies,
  `app/db.py` (a connection pool on `DATABASE_URL`, opened for the app's lifetime) and a
  `GET /readyz` that runs `SELECT 1` on it and answers 503 when that fails. Other Connectors are listed
  in the README only.

## How a template works

Everything in this repository is copied into the new Container repository at the
same path, except `.c4factory/`, which is input for the factory. Files ending in
`.tmpl` are rendered with Go `text/template` (missing keys are errors) and lose the
suffix; a `.tmpl` that renders to only whitespace is left out. Other files are copied
byte for byte.

Data: `.Project`, `.SoftwareSystem`, `.Container`, `.DisplayName`, `.Type`, the chosen
`.Framework`, `.Compute` and `.Deployment` (empty when none is chosen), and
`.Connectors` (sorted names). Functions: `has`, `snake`, `upper`, `lower`, plus the
`text/template` builtins.

Each path belongs to exactly one layer; `.devcontainer/**` and `.c4factory.yaml`
belong to the operator. Release a change by tagging a new version and bumping it in
`templates/versions.txt` of C4 Factory; a tag is never moved.
