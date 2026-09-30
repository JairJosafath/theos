# python-fastapi (Framework)

A Python service with [FastAPI](https://fastapi.tiangolo.com/), managed with
[uv](https://docs.astral.sh/uv/), linted with ruff and tested with pytest.

- **Supports:** `service`
- **Writes:** `pyproject.toml`, `.python-version`, `app/`, `tests/`, `README.md`, `.gitignore`
- **Devcontainer:** `.c4factory/devcontainer.json` holds the base settings: the
  `mcr.microsoft.com/devcontainers/python:3.12` image, uv installed with pipx, `uv sync`
  after create, and the Python and ruff VS Code extensions. The operator merges it
  into `.devcontainer/devcontainer.json`; with Connectors, the same image becomes
  the `app` service of the Docker Compose setup.
- **Connectors it knows:** `postgres` adds `psycopg[binary]` to the dependencies and a
  `GET /readyz` that reports whether `DATABASE_URL` is set. Other Connectors are listed
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
