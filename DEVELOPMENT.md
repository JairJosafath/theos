# Development environment

C4 Factory develops directly on Linux or WSL, without a dev container. VS Code
sets the environment up when you open the folder, and everything the project
downloads or writes lives in `.dev/` inside the checkout. Delete the folder and
the project is gone from the machine.

## Getting started

You need git, the GitHub CLI (`gh`), VS Code with the WSL extension, and Docker
Desktop with WSL integration turned on for your distro (not needed on a lite
device).

```sh
git clone https://github.com/JairJosafath/c4factory.git
cd c4factory
code .
```

When the folder opens, the **Dev: open** task runs `./dev setup`. The first run
takes a few minutes; after that it only checks and finishes in seconds. If git
or a C compiler is missing, it asks for your sudo password once to install them
with apt. Then open a new terminal: it starts in the project shell.

The first time, VS Code may ask whether to allow automatic tasks for this
folder. Choose **Allow**.

Log in once per machine: `gh auth login` and `gh auth setup-git` (machine-wide;
`hack/lib/github.sh` takes its token from the git credential helper), and
`claude` in the project shell (per project, stored in `.dev/claude`).

## The `dev` script

| Command | What it does |
| --- | --- |
| `./dev setup` | Downloads what is missing into `.dev/`. Safe to run again. |
| `./dev setup --lite` | The same without Kind, kubectl, Helm and Docker, and with `C4_CLUSTER=none`. The choice is remembered. |
| `./dev setup --full` | Switches a lite device back. |
| `./dev setup --upgrade` | Downloads the newest version of every tool again. |
| `./dev shell` | Opens the project shell outside VS Code, for example in Windows Terminal. |
| `./dev run CMD` | Runs one command with the project environment, for example `./dev run go test ./...`. |
| `./dev clean` | Deletes the Makefile tools left in each checkout's and worktree's `bin/` from before they moved to `.dev/make/bin/`. |
| `./dev nuke` | Deletes this project's Kind clusters and all of `.dev/`, after asking. |

The same actions are VS Code tasks (**Terminal → Run Task → Dev: …**).

A lite device runs every test tier but Kind
([docs/dev/testenv.md](docs/dev/testenv.md#test-tiers)), and those tiers are
the gate, so it verifies every issue:

- `make check`: the table tests, every template rendered, the YAML validated
  offline against the CRDs and their CEL rules, envtest, and the UI's check
  (`make ui-test`: typecheck, eslint, Prettier and its tests).
- `make test-gitea`, which tests `internal/gitprovider` against a Gitea of this
  checkout's own (`make gitea-local`, no Kubernetes; see
  [docs/dev/testenv.md](docs/dev/testenv.md#local-gitea)), and `make
  seed-local`, which seeds that Gitea like Kind's.

Kind runs only when a human asks (`C4_HUMAN_CLUSTER=1`; agents never lease a
cluster), and no issue needs it: the `needs/cluster` label is gone, and `pick`
hands a lite device every ready issue. What only a real node would catch
(Agent Sandbox pods running, node or kubelet behaviour, the Gitea Actions
runner itself) is not tested before release.

## What lives where

| Path | Contents |
| --- | --- |
| `.dev/tools/` | Go (newest 1.26.x) and Node.js (newest LTS) |
| `.dev/bin/` | kubebuilder, Kind, kubectl, Helm, uv, jq, fzf |
| `.dev/npm/` | Claude Code and the Dev Container CLI |
| `.dev/go/`, `.dev/cache/` | Go workspace, module cache, build cache, gopls, uv, npm |
| `.dev/kube/config` | Contexts of the Kind clusters created from this folder |
| `.dev/zsh/` | oh-my-zsh, plugins and shell history |
| `.dev/claude/` | Claude Code login, settings and history for this project |
| `.dev/secrets/claude-token` | The agents' Claude token, mode 0600; `make claude-secret` stores it once in the Kind cluster's `c4-factory-system`, and the operator shares it with every project namespace ([docs/dev/testenv.md](docs/dev/testenv.md)) |
| `.dev/make/bin/` | Tools the Makefile downloads (controller-gen, golangci-lint, kustomize, setup-envtest and the envtest binaries in `k8s/`), under versioned names |
| `bin/` | What `make build` writes (`bin/manager`) |

`hack/dev/env.sh` sets the variables that point every tool at `.dev/`, and
`hack/dev/zshrc` is the shell configuration. Both are versioned; `.dev/` is not.

## Worktrees

A git worktree gets a symlink to the main checkout's `.dev/`, so tools and caches
exist once. `./dev nuke` in a worktree removes only that link.

The Makefile finds the main checkout through git, so a worktree without the link
(a relay worktree) uses the same `.dev/make/bin/`. Several worktrees can install
into it at once: each tool installs under a lock and appears under its versioned
name only when complete. Before this, every checkout had its own tools in `bin/`;
`./dev clean` deletes them in the main checkout and every worktree, once no branch
in flight still predates the change.

## Removing the project

Delete the folder. Every tool, cache, login and the shell history go with it;
Go's module cache is kept writable (`GOFLAGS=-modcacherw`) so a plain delete
works, from WSL or from Windows Explorer.

What lives outside the folder belongs to the machine, not the project:

- **Docker and the GitHub CLI**, installed before the repo. Kind clusters run
  in Docker: delete them first with `./dev nuke`, or later in Docker Desktop.
- **VS Code extensions**, in `~/.vscode-server`, shared with other projects.
- **System packages** (git, zsh, make, gcc).

## Troubleshooting

- **The Go extension can't find Go.** Check that `.dev/tools/go/bin/go` exists
  (run **Dev: set up environment**), then reload the window. If it still fails,
  set `go.alternateTools` in `.vscode/settings.json` to the absolute path.
- **The Kubernetes panel shows no clusters.** Run **Kubernetes: Set Kubeconfig**
  and choose `.dev/kube/config`.
- **Folder-open tasks don't start.** Set `"task.allowAutomaticTasks": "on"` in
  your user settings as well, or run **Dev: open** by hand.
- **`./dev: Permission denied`.** Run `chmod +x dev` once (or `bash dev setup`).
- **Runners offline: CI runs wait in the queue.** While the repository variable
  `RUNS_ON` is `self-hosted`, every job runs on the runners in containers on
  the device's Docker (`make gh-runner`, [docs/dev/testenv.md](docs/dev/testenv.md)).
  On WSL that Docker is Docker Desktop, so the runners need it running, with
  WSL integration on for this distro (Settings > Resources > WSL integration).
  They stop when it stops and come back when it starts, and GitHub has no other
  runner to fall back on: a queued run never fails, it waits. The relay watch
  says "Runners offline since T: N runs queued" in the Maintainer report once
  a run on main has waited 30 minutes with no runner online ("N runs queued
  since T with M runners online" when some are). Run `make gh-runner-status`.
  It says whether Docker answers from this distro, whether each container is up, and
  whether GitHub sees each runner online, and what to do: start Docker
  Desktop, or run `make gh-runner RUNNERS=N` to start the containers that are
  down. Switching `RUNS_ON` back to GitHub's runners is the owner's call.
