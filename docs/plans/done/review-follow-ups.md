# Review follow-ups

The four issues left open after the review of
[PR #1](https://github.com/JairJosafath/c4factory/pull/1), fixed together on one
branch.

## Goal

Close issues #2–#5: fewer needless reconciles, a precise `UnpinnedRef`, an
`Applied` condition that notices a recreated Relationship, and screenshots that
show the current templates.

## Steps

Each step is one commit with a test.

1. **Only a missing commit is `UnpinnedRef`** (#4). `FetchTemplate` maps
   `plumbing.ErrObjectNotFound` from the checkout to `ErrUnpinnedRef` and wraps
   any other error as it is, so the Container controller retries it. A unit test
   covers both paths through `checkoutError`.
2. **Filtered watches** (#2). The Project wakes for creations and deletions of
   the kinds it adopts and for a Container's phase change; Sessions already pass
   only phase changes. The Container controller's catalog, GitProvider and
   DataStore watches pass only spec changes (`GenerationChangedPredicate`), so
   adoption and DataStore status patches no longer re-run a Failed Container's
   Git path. The "not trying again on its own" envtest now also patches a
   DataStore's labels and status and a Framework's labels, and checks that the
   repository history isn't read again.
3. **Applied compares targets** (#3). A Container records what each applied
   Relationship added, in a new `status.appliedConnectors` (relationship,
   dataStore, connector) next to `status.appliedRelationships`, and in the
   commit trailer, now `Applied-Relationship: <name> DataStore/<store>
   Connector/<connector>`. The Relationship controller compares the record with
   its current target and that DataStore's Connector. Name-only records, from
   status or commits made before this change, still count as applied. Envtest
   covers a Relationship recreated under the same name pointing at `cache-db`
   (`Applied=False, AddedAfterScaffold`) and a name-only commit
   (`Applied=True`).
4. **Fresh screenshots** (#5). Recreate the Kind cluster, run `make e2e`, deploy
   the UI with `make ui`, run `hack/evidence.sh` and commit `docs/screenshots/`.

## Done when

`make check`, `make ui-test` and `make e2e` pass on a fresh Kind cluster, the
results page shows `k8s-manifests@v1.1.0` and `python-fastapi@v1.2.1`, and CI is
green on the pull request.
