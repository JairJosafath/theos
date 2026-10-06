# Release v0.3.0 checks itself, and follows getting-started (#226)

*Verify Release* (`.github/workflows/verify-release.yml`), dispatched for
`tag=v0.3.0` on branch `feat/226-verify-release-also-follows-getting` (whose
`config/samples/first-app/` is main's), installed the published chart 0.3.0 on
a fresh Kind cluster of a hosted runner and ran green:
https://github.com/JairJosafath/c4factory/actions/runs/37408495960

After the release's own checks (the seed, runners `base go`, the manager, the
UI, `orders-api` scaffolded) it followed
[docs/getting-started.md](../getting-started.md) for the first app:

- **The project's pull secret.** Secret `webshop/ghcr` from `GITHUB_TOKEN` and
  ServiceAccount `webshop/default` patched to list it. A pod with
  `imagePullPolicy: Always` and no pull secret of its own pulled
  `ghcr.io/jairjosafath/c4factory/agent-go:0.3.0` through it:
  `Successfully pulled image "ghcr.io/jairjosafath/c4factory/agent-go:0.3.0" in 1.175s`.
- **The first app.** `kubectl apply -k` of main's `config/samples/first-app/`,
  set to the release as the guide says (the `agent-go` tag and the template
  refs of v0.3.0's `templates/versions.txt`; for v0.3.0 nothing changed).
  Container `orders-cli` turned `Ready`, Gitea holds `webshop/orders-cli`
  (`.c4factory.yaml .devcontainer .editorconfig .gitea .gitignore
  .golangci.yml AGENTS.md Makefile README.md docs go.mod internal main.go
  main_test.go`), and Crew `orders-cli` turned `Idle`, "No ready issues".
- **The Agent isn't run:** CI has no Claude token, so the check stops before a
  Task; the job summary says so.

Two earlier tries on the branch: run 37407332867 failed before installing
anything, in `kubectl wait` on a CRD that had no status yet (it filed #230;
main's 2a7fd3d now retries that wait); run 37407693703 was green but
listed no files for `orders-cli`, read the moment it was pushed (the step now
waits for `go.mod`).

The evidence below is from the run's artifact `release-v0.3.0-check`.

## The runner
```
Runner: 5905 MB of memory available, 21101 MB of disk left, 0 OOMKilled or Evicted.
```

## webshop
```
NAME                            SYSTEMS   CONTAINERS   READY   SESSIONS   AGE
project.c4.factory.io/webshop   1         2            2       0          13s

NAME                                 TYPE      PHASE   CHECKS   SESSIONS   REPOSITORY                                                     AGE
container.c4.factory.io/orders-api   service   Ready            0          http://gitea.gitea.svc.cluster.local:3000/webshop/orders-api   14s
container.c4.factory.io/orders-cli   cli       Ready            0          http://gitea.gitea.svc.cluster.local:3000/webshop/orders-cli   4s
```

## The first app's Agents and Crew
```
NAME                                     DISPLAY NAME                        HARNESS       MODEL               IMAGE                                           ROUNDS   AGE
agent.c4.factory.io/claude-implementer   Claude Code, Sonnet (implementer)   claude-code   claude-sonnet-5-5   ghcr.io/jairjosafath/c4factory/agent-go:0.3.0   3        4s
agent.c4.factory.io/claude-reviewer      Claude Code, Opus (reviewer)        claude-code   claude-opus-5-5     ghcr.io/jairjosafath/c4factory/agent-go:0.3.0   3        4s

NAME                            CONTAINER    IMPLEMENTER          REVIEWER          PHASE   PARALLEL   TASKS   AGE
crew.c4.factory.io/orders-cli   orders-cli   claude-implementer   claude-reviewer   Idle    1                  3s
```
