---
name: factory-ops
description: For a human only (agents never touch a cluster) - watch and drive the running C4 Factory in a Kind cluster of the pool (c4factory, c4factory-2, ...) - health, Crews, Tasks, agent sandboxes, reviews and issues in Gitea, pausing and resuming agents, deploying the operator, rebuilding images and templates. Use when a human asks about the factory's state or to run, pause, resume or change it.
---

# Running the factory

Commands for a Kind cluster of the device's pool: `c4factory`, `c4factory-2`,
... Kind is a tier only a human runs: an agent never leases, creates or
deploys to a cluster, and a headless session (the relay) never uses this
skill; `make check` is its gate. A human who changes or verifies a cluster
first leases one (`C4_HUMAN_CLUSTER=1 hack/lib/github.sh cluster acquire`
prints its name; `cluster pool` shows them all); make targets in its checkout then use that cluster
(`hack/lib/github.sh cluster name`), and kubectl names it on every call:
`kubectl --context kind-<name>`, written `kubectl` below; never
`kubectl config use-context`, the kubeconfig is shared by every checkout. Keep output small:
`-o jsonpath`, `jq`, `--tail=N`, `head`. `P` is a project (namespace), `T` a
Task, `C` a Crew.

## State at a glance

```bash
kubectl -n P get crews,tasks                 # phases, rounds, checks, verdicts
kubectl top node; kubectl top pods -A --sort-by=cpu | head -6
make doctor                                  # every part, from Docker to Secrets
make ui-forward                              # http://localhost:8080/?area=work
```

## One Task

```bash
kubectl -n P get task T -o jsonpath='{.status.summary}'          # implementer's account
kubectl -n P get task T -o jsonpath='{.status.review.summary}'   # reviewer's verdict
kubectl -n P get task T -o jsonpath='{range .status.history[*]}{.phase} r{.round} {.reason}{"\n"}{end}'
kubectl -n P logs T-r1-implement --tail=20                      # while it runs
```

## Crews and backlogs

```bash
kubectl -n P patch crew C --type merge -p '{"spec":{"paused":true}}'   # false resumes
kubectl -n P patch crew C --type merge -p '{"spec":{"parallel":2}}'
```

A paused Crew starts nothing; running Tasks finish. Gitea numbers issues and pull
requests in one sequence.

## Gitea

```bash
kubectl -n gitea port-forward svc/gitea 3917:3000 >/dev/null 2>&1 &
ADMIN_PW=$(kubectl -n gitea get secret gitea-admin -o jsonpath='{.data.password}' | base64 -d)
curl -s -u "c4admin:$ADMIN_PW" "http://localhost:3917/api/v1/repos/ORG/REPO/pulls?state=all" | jq -c '.[] | {n: .number, title, merged}'
```

Never print a password or token; read it into a variable. Browse with
`make gitea-forward` (http://localhost:3000 for `c4factory`, 3001 for
`c4factory-2`; `GITEA_PORT` overrides), and pick a different local port than
3917 above when another session may forward too.

## Changing the factory

| Change | Do | Mind |
| --- | --- | --- |
| Operator code | `make deploy` | The new pod waits up to a minute for the leader lease; Tasks carry on |
| c4agent or the agent image | `make agent-image-go` | Loads about 6 GB into Kind, which has restarted Gitea: pause the Crews and let running Tasks finish first |
| A template | new version in `templates/versions.txt`, then `make seed`, then bump the catalog entry's `ref` | Tags never move. A Standard's bump reaches its Ready Containers as an update pull request the operator merges (`status.standard`, condition `StandardUpdated`); other templates reach only new Containers |
| Claude token | `make claude-secret`: one Secret in the operator's namespace, which the operator copies into every opted-in project namespace | Never print it |
| The UI | `make ui-test ui` | |

## Shell gotchas

`pkill -f pattern` kills your own shell: use `pgrep -f '[p]attern' | xargs -r kill`.
Foreground `sleep N` is blocked: wait with `until <check>; do sleep 5; done`.
Chained `make x | tail` hides make's exit code: `set -o pipefail` first.
