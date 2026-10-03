# Dev containers

Three configurations are available. The first two provide Go, Docker, kubectl, and
Kind; the light one has no cluster. The Makefile downloads any other tools it needs
into `bin/`.

| Configuration | Base image | What it adds |
| --- | --- | --- |
| [`devcontainer.json`](devcontainer.json) (default) | Debian with Docker-in-Docker | Go 1.26, kubebuilder, kubectl, Helm, Kind, Node, uv, the Dev Container CLI, GitHub CLI, Claude Code, and a zsh that suggests as you type |
| [`operator/devcontainer.json`](operator/devcontainer.json) | `golang:1.26` | Kubebuilder's scaffolded setup: [`operator/post-install.sh`](operator/post-install.sh) installs Kind, Kubebuilder, and kubectl with bash completion |
| [`lite/devcontainer.json`](lite/devcontainer.json) | Debian, no Docker | Go, Node, git, GitHub CLI, Claude Code and the same zsh, with `C4_CLUSTER=none`: for a device with too little disk for Kind. Its relay takes only issues without `needs/cluster`; see the README, "A device without the cluster" |

The default and light containers empty Go's build cache on start once it passes
4 GB ([`hack/trim-go-cache.sh`](../hack/trim-go-cache.sh)).

## What the default container is for

| Tool | Used for |
| --- | --- |
| Go, kubebuilder | The operator |
| Docker-in-Docker, Kind, kubectl, Helm | The in-cluster test environment: Gitea, the operator and the sample project |
| uv | Running and testing the Python templates |
| Node, Dev Container CLI | The read-only UI, and testing scaffolded devcontainers and the Session client |
| Claude Code, GitHub CLI | Working with Claude, and GitHub from the terminal |

[`post-create.sh`](post-create.sh) installs kubebuilder, uv, fzf and the Dev Container
CLI, and sets up zsh. It runs once when the container is created and is safe to run
again.

## The shell

The terminal opens in zsh with:

- **Suggestions as you type**, in grey, from your history and then from completions.
  Press Right arrow or End to accept, Ctrl+Right to accept one word.
- **Syntax highlighting**: a command turns green when it exists and red when it doesn't.
- **Tab completion** for kubectl, Helm, Kind, kubebuilder, Go, Docker, gh and uv.
  `k` is short for `kubectl`.
- **Ctrl+R** for fuzzy search through your history.

Shell history and Claude Code's login live in volumes, and Docker images in the
Docker-in-Docker volume, so all three survive a rebuild.

## Gitea

Port 3000 is forwarded as "Gitea". Start the forward with:

```sh
kubectl port-forward -n gitea svc/gitea 3000:3000
```

## Lockfile

`devcontainer-lock.json` pins feature versions. The feature list changed, so delete
the old lockfile; the next build writes a new one.
