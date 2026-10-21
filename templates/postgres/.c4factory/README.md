# postgres (Connector)

Connects a Container to a PostgreSQL database. A Container gets it through a
Relationship to a DataStore backed by this Connector.

- **Supports:** `service`, `worker`, `job`
- **Writes:** `deploy/k8s/connector-postgres.yaml` (a ConfigMap `<container>-postgres`
  with `DATABASE_URL`, loaded by the Deployment layer) and `.env.example`.
- **Devcontainer:** `.c4factory/compose.yaml.tmpl` becomes
  `.devcontainer/compose.postgres.yaml`. It adds a `postgres` service
  (`postgres:17-alpine`, user `postgres`, database named after the Container in
  snake case, a health check) and gives the `app` service `DATABASE_URL` and a
  dependency on a healthy `postgres`. It has no password, since Postgres accepts
  the connections it receives and publishes no port, so only the devcontainer
  reaches it.
- **Frameworks:** python-fastapi adds the driver and its pool (`psycopg[binary,pool]`),
  `app/db.py` and a `/readyz` that runs `SELECT 1` when this Connector is present.

The Connector only scaffolds. Creating real databases is out of scope; the
ConfigMap's `DATABASE_URL` points at a database provisioned elsewhere.

See the python-fastapi template's `.c4factory/README.md` for how templates are rendered.
