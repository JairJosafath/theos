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
