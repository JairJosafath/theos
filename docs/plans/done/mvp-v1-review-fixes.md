# MVP v1 review fixes

Fixes from the review of [PR #1](https://github.com/JairJosafath/c4factory/pull/1),
made before it merges.

## Goal

Scaffold once, and stop for good on failure, as [the design](../../design.md) asks;
close the gaps the review found in the Git provider, the renderer and the templates.

## Steps

Each step is one commit with a test that fails first.

1. **A Failed Container stops.** The Container's own status writes no longer
   trigger a reconcile. It tries again when an input changes (its spec, a catalog
   entry, the GitProvider, a DataStore or a Relationship), or when someone sets the
   `theos.factory.io/retry` annotation after fixing the repository.
2. **Ready stays Ready.** A missing SoftwareSystem sets `ReferencesResolved=False`
   on a scaffolded Container but doesn't change its phase.
3. **Git calls get their own deadline,** so a timed-out attempt still records its
   `GitError` condition.
4. **Any branch is foreign content.** A repository with commits on a branch other
   than `main` fails with `ForeignCommits`.
5. **Commit pins are fetched with tags,** so a commit reachable only from a tag
   is found.
6. **Rendered paths are checked,** so `.git.tmpl` can't write `.git`, nor can a
   `.git` below the root, and a `null` Framework `devcontainer.json` is a
   `TemplateError`, not a panic.
7. **Templates learn the layers.** They receive the chosen Framework, Compute and
   Deployment names; `k8s-manifests` patches `compute.yaml` only when there is one.
8. **Links can't be retargeted.** Relationship `spec.target` and DataStore
   `spec.connectorRef` are immutable, like `source` already is.

Left for later, as GitHub issues: filtering the Project and `waitingContainers`
watches; a Relationship deleted and recreated under the same name with a new
target; and reporting only a missing commit, not any checkout error, as
`UnpinnedRef`.

## Done when

`make check`, `make ui-test` and `make e2e` pass, CI is green on the PR, and the
design says what changed.

## Progress

All eight steps are done, each with a spec that failed first. Along the way:

- **The repeat-reconcile check had gone quiet.** The "a repeat reconcile changing
  nothing" spec poked the Container with an annotation that step 1 now ignores, so
  it pokes with `theos.factory.io/retry` instead.
- **Two templates were released.** `k8s-manifests` v1.1.0 guards the Compute patch,
  and `python-fastapi` v1.2.1 lists the new template inputs in its README; tags
  never move.
- **The screenshots in [results.md](../../results.md)** predate these fixes and show
  the earlier template versions.

## Secrets and CI

GitGuardian flagged three hardcoded secrets in the PR, and CI ran every workflow
twice on each push. Before merging:

1. **No commit holds a secret.** Each fix is folded into the commit that
   introduced the problem, and the branch is force-pushed:
   - `make gitea` generates the Gitea admin password for each cluster into
     Secret `gitea/gitea-admin`; no password is committed.
   - The devcontainer Postgres has no password. It accepts the connections it
     receives and publishes no port, so only the devcontainer's compose services
     reach it.
   - `hack/seed/seed.sh` checks that its credentials are set in a loop, not with
     the line GitGuardian read as a password (it never held a value).
2. **CI runs what a change needs, once.** Each workflow has path filters, runs on
   pull requests and on pushes to `main` (not on every branch push as well), and
   a newer push cancels the older run on the same branch.

Done when GitGuardian passes on the rewritten branch, a fresh cluster seeds with a
generated password, a devcontainer's Postgres accepts the app without one, and CI
skips workflows a change can't affect.

Both are done:

- **No commit holds the old values.** A scan of every commit on the branch finds
  neither the old Gitea password nor a Postgres password, and the tip passes
  `make check`.
- **The generated password works.** On the local cluster, `make gitea` replaced
  the password that used to be committed: the old one now gets HTTP 401, the new
  one 200, and `make seed` signs in with it.
- **Postgres takes the app without a password.** The rendered compose fragment,
  run with Docker Compose, lets `app` connect with its `DATABASE_URL` and
  publishes no port.
- **The filters were checked before pushing.** actionlint accepts the workflows,
  and a simulation of GitHub's `paths` rules over sample changes gave the intended
  runs. It also showed that `**.go` style patterns can match only at the root, so
  the filters use `**/*.go`.
