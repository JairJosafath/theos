# c4session devcontainer feature

Installs `c4session`, the C4 Factory Session client, and runs `c4session start` on
every start of the devcontainer. The client reads the repository's `.c4factory.yaml`,
registers a Session on its Container, and sends a heartbeat every 30 seconds.

Stopping a container only signals its first process, so the `app` service in the
operator's `.devcontainer/compose.yaml` passes SIGTERM on to the client, which then
deletes its Session. A devcontainer without Connectors has no compose file; there,
and whenever a devcontainer dies without a clean stop, the operator marks the
Session `Stale` two minutes after its last heartbeat.

The operator writes this feature into every repository it scaffolds, as
`.devcontainer/c4session/`, and references it as `./c4session`.

| Option | Default | Meaning |
| --- | --- | --- |
| `binaryUrl` | empty | URL of a linux `c4session` binary; `${arch}` becomes `amd64` or `arm64`. The operator sets it from `--session-binary-url`. The same directory must serve a `SHA256SUMS` (in `sha256sum`'s format) that lists the binary: the download is checked against it and refused on a mismatch or without one. The chart's defaults, the UI's `/downloads/` and the GitHub release, both publish it. |
| `version` | `latest` | Module version for `go install`, used when `binaryUrl` is empty and Go is present. |
| `actor` | `human` | `human` or `agent`. |
| `summary` | empty | One line about the work. |

Installing never fails the devcontainer build: without a binary, or with one
that doesn't match its `SHA256SUMS`, the container still works, it just doesn't
show up as a Session; the build log says why.

The client needs to reach the cluster, with a kubeconfig (`KUBECONFIG` or
`~/.kube/config`) for the project's `c4-session` ServiceAccount, or in-cluster
credentials. In v1 that account can touch any Session in its project.
