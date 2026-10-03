# Tests, the Kind cluster and deploying

How to test and run the factory. The e2e tests run only against the dedicated
Kind cluster `c4factory` (`make cluster`), never a real cluster.

## Testing & Development

```bash
make test                # Unit and envtest tests (real K8s API + etcd, no cluster needed)
make check               # manifests, generate, fmt, vet, lint and test
make cluster gitea seed  # Kind cluster, Gitea, and the seeded templates
make deploy sample       # Build and deploy the operator into Kind, apply the webshop sample
make session             # Run the test Session pod; make session-kill kills its client
make test-gitea          # gitprovider against the seeded Gitea
make toolchain runner agent-sandbox agent-image  # Slim toolchain, CI runner, Agent Sandbox, agent image
make toolchain-android runner-android  # Optional: the 20 GB release runner for signed APKs
make toolbox             # The toolbox story; then kubectl apply -k config/samples/toolbox/tasks
make toolchain-go runner-go agent-image-go  # Go and Node toolchain, CI runner (label go) and agent image
make cel-lens cel-lens-backlog  # The CEL Lens story and its issues; then kubectl apply -k config/samples/cel-lens/crews
make claude-secret CLAUDE_TOKEN=...  # The real agent's token, from `claude setup-token`
make claude-secret-copy FROM=toolbox TO=cel-lens  # Reuse that Secret in another project
make e2e                 # All of the above, then check every milestone in Kind
make down / make up       # Stop the factory to free the machine; bring it back healthy
make doctor              # Check every part of the factory and say what is wrong
```

Controller tests use **Ginkgo + Gomega** (BDD style) with the real manager and every
reconciler; see `internal/controller/suite_test.go`. A fake Git host serves the real
`templates/`, so envtest also proves the templates render together. Business-rule tests
use the standard `testing` package with table tests. In tests, write with merge patches
(`patch(obj, func(){...})`), not `Update`: the operator may adopt the object meanwhile.

`make run` runs the operator on your machine, but it can't reach Gitea's in-cluster
address; use `make deploy` for anything that scaffolds.

## Deployment Workflow

```bash
# 1. Regenerate manifests
make manifests generate

# 2. Build, load into Kind, deploy
make deploy            # builds c4factory/operator:dev, kind-loads it, applies config/default, restarts

# 3. Try it
make sample            # the webshop story
kubectl get c4 -n webshop

# 4. Debug
kubectl logs -n c4-factory-system deployment/c4-factory-controller-manager -c manager -f
make gitea-forward     # then open http://localhost:3000
```

## Work on the factory: GitHub issues and the relay

Work on the factory itself is tracked as issues on `JairJosafath/c4factory`; the
apps it builds keep their issues in Gitea. Sessions hand off through issue
comments (the `handoff` skill), and `hack/lib/github.sh` does every GitHub call,
with the token from the git credential helper.

```bash
hack/lib/github.sh pick            # the issue to work next
hack/lib/github.sh show 14         # an issue, its newest handoff comment and what came after
hack/lib/github.sh labels          # create the status/priority/kind/source labels (safe to repeat)
RELAY_DRY_RUN=1 hack/relay.sh      # the issue and prompt the next headless session would get
hack/relay.sh --now                # headless sessions one after another, overnight (billed)
touch .relay/stop                  # stop the relay after the current session
```

The relay picks an open `status/in-progress` issue, else the oldest
`status/ready` + `priority/urgent` one, else the oldest `status/ready` one, and
stops when a session leaves no handoff comment and doesn't close the issue or set
`status/needs-human`. With nothing ready it runs `hack/evaluate.sh` at most every
6 hours, then picks once more. After each session it runs `make backup-releases`
if the target exists. Its log is `.relay/relay.log`, with one account per session
next to it. A handoff that couldn't reach GitHub waits in
`.relay/pending-handoff-<issue>.md`, and the next run posts it first.
