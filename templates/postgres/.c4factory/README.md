# postgres (Connector)

Connects a Container to a PostgreSQL database. A Container gets it through a
Relationship to a DataStore backed by this Connector.

- **Supports:** `service`, `worker`, `job`
- **Writes:** `deploy/k8s/connector-postgres.yaml` and `.env.example`. The
  manifests (since v1.2.0) run the database on Kubernetes, all named
  `<container>-postgres`: a single-replica StatefulSet of
  `postgres:17.11-alpine` pinned by digest (user 70, a 1Gi volume,
  `pg_isready` probes), a headless Service, and a ConfigMap with
  `DATABASE_URL`, loaded by the Deployment layer through `envFrom`.
- **Password:** from a Secret `<container>-postgres` with key `password`,
  which you make before applying (`kubectl create secret generic
  <container>-postgres --from-literal=password=...`). Postgres reads it as
  `POSTGRES_PASSWORD` and the app as `PGPASSWORD` (k8s-manifests sets it);
  `DATABASE_URL` holds no secret and the repository holds no password.
- **Devcontainer:** `.c4factory/compose.yaml.tmpl` becomes
  `.devcontainer/compose.postgres.yaml`. It adds a `postgres` service
  (`postgres:17-alpine`, user `postgres`, database named after the Container in
  snake case, a health check) and gives the `app` service `DATABASE_URL` and a
  dependency on a healthy `postgres`. It has no password, since Postgres accepts
  the connections it receives and publishes no port, so only the devcontainer
  reaches it.
- **Frameworks:** python-fastapi adds the driver (`psycopg[binary]`) and a `/readyz`
  endpoint when this Connector is present.

The Connector only scaffolds. To use a database provisioned elsewhere (a
managed service, an operator), drop the StatefulSet and Service and point the
ConfigMap's `DATABASE_URL` at it.

See the python-fastapi template's `.c4factory/README.md` for how templates are rendered.
