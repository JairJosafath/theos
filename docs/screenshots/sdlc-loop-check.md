# sdlc-loop: the last `make -k check`

Run on branch `claude/upbeat-wozniak-1udw3t` at 69e4b079 (the
Verify merge, every step of [the plan](../plans/done/sdlc-loop.md) in), on
2026-11-05, in a cloud sandbox without a cluster. `make validate-yaml` was
rerun with `FORCE=1` afterwards and passed: "All valid", "All 16 invalid
files rejected", "The chart's CRDs match config/crd/bases".

The one failing tier is `hack/relay_test.sh`: its two process-cleanup cases
fail in this sandbox on the untouched c4factory code (`b45ff31`) too.

```
0 issues.
ok   hack/chart-bundles_test.sh (1s)
ok   hack/chart-crds_test.sh (10s)
ok   hack/chart-dns_test.sh (15s)
ok   hack/chart-manager_test.sh (12s)
ok   hack/chart-pools_test.sh (1s)
ok   hack/chart-role_test.sh (2s)
ok   hack/check-cache_test.sh (31s)
ok   hack/check-download-retries_test.sh (6s)
ok   hack/check-pinned-images_test.sh (1s)
ok   hack/check-workflows_test.sh (13s)
ok   hack/claude-secret_test.sh (5s)
ok   hack/close-guard_test.sh (14s)
ok   hack/context-guard_test.sh (3s)
ok   hack/dev/dev_test.sh (3s)
ok   hack/gh-runner/entrypoint_test.sh (1s)
ok   hack/gh-runner/gh-runner_test.sh (15s)
ok   hack/gh-runner/job-started_test.sh (2s)
ok   hack/go-changed_test.sh (3s)
ok   hack/go-test_test.sh (10s)
ok   hack/grep-q-pipe_test.sh (2s)
ok   hack/heavy-guard_test.sh (16s)
ok   hack/heavy-slot_test.sh (17s)
ok   hack/keep-mtime_test.sh (10s)
ok   hack/kind_test.sh (3s)
ok   hack/lib/github_test.sh (51s)
ok   hack/load-images_test.sh (2s)
ok   hack/previous-release_test.sh (4s)
ok   hack/prune-caches_test.sh (6s)
ok   hack/pull_test.sh (4s)
ok   hack/relay-watch_test.sh (25s)
FAIL hack/relay_test.sh (exit 1, 71s):
    FAIL idle: what claude started ended too: got [running] want []
    FAIL stray #1341: the session and its child are gone: got [child] want []
ok   hack/release-notes_test.sh (8s)
ok   hack/release_test.sh (9s)
ok   hack/runner/buildkitd_test.sh (1s)
ok   hack/runner/register_test.sh (1s)
ok   hack/runner/tmp-sweep_test.sh (1s)
ok   hack/seed_test.sh (11s)
ok   hack/test-hack_test.sh (4s)
ok   hack/test-packages_test.sh (15s)
ok   hack/toolchain-go/go-cache_test.sh (2s)
ok   hack/toolchain-go/operator-tools_test.sh (1s)
ok   hack/toolchain-uv_test.sh (0s)
ok   hack/trim-go-cache_test.sh (7s)
ok   hack/ui-check_test.sh (3s)
test-hack: 1 of 44 shell tests failed
      Tests  812 passed (812)
# fail 0
ok  	github.com/JairJosafath/theos/api/v1alpha1	(cached)
ok  	github.com/JairJosafath/theos/cmd	1.402s
ok  	github.com/JairJosafath/theos/cmd/model-proxy	(cached)
ok  	github.com/JairJosafath/theos/features	(cached)
ok  	github.com/JairJosafath/theos/hack/runner-fence	(cached)
ok  	github.com/JairJosafath/theos/hack/template-go-kubebuilder	0.011s
ok  	github.com/JairJosafath/theos/internal/adapters/argocd	50.038s
ok  	github.com/JairJosafath/theos/internal/adapters/cloudformation	0.217s
ok  	github.com/JairJosafath/theos/internal/adapters/git	(cached)
ok  	github.com/JairJosafath/theos/internal/adapters/gitea	(cached)
ok  	github.com/JairJosafath/theos/internal/adapters/github	(cached)
ok  	github.com/JairJosafath/theos/internal/adapters/gitlab	(cached)
ok  	github.com/JairJosafath/theos/internal/agentrunner	41.397s
ok  	github.com/JairJosafath/theos/internal/buildinfo	(cached)
ok  	github.com/JairJosafath/theos/internal/model	0.403s
ok  	github.com/JairJosafath/theos/internal/modelproxy	(cached)
ok  	github.com/JairJosafath/theos/internal/oci	(cached)
ok  	github.com/JairJosafath/theos/internal/ports	(cached)
ok  	github.com/JairJosafath/theos/internal/ports/fake	(cached)
ok  	github.com/JairJosafath/theos/internal/scaffold	5.778s
ok  	github.com/JairJosafath/theos/internal/sessionclient	22.979s
ok  	github.com/JairJosafath/theos/test/chart	41.973s
ok  	github.com/JairJosafath/theos/test/compat	0.267s
ok  	github.com/JairJosafath/theos/test/e2e/standin	0.007s
ok  	github.com/JairJosafath/theos/internal/controller	460.236s
ok  	github.com/JairJosafath/theos/internal/controller	720.561s
real	14m13.491s
```
