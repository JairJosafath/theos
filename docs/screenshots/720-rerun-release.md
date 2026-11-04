# A Crew re-runs its failed release run on request (#720)

Gitea 1.27.3's API (`/swagger.v1.json` of `make gitea-local`) has
`POST /repos/{owner}/{repo}/actions/runs/{run}/rerun-failed-jobs` (201), so the
operator calls that instead of deleting and pushing the tag again.

```text
$ make test-gitea
--- PASS: TestGiteaGoProxyIntegration (0.71s)
--- PASS: TestGiteaIntegration (1.63s)
--- PASS: TestGiteaTemplatesIntegration (0.32s)
--- PASS: TestGiteaChecksIntegration (0.53s)
--- PASS: TestGiteaPullRequestUpdateIntegration (4.99s)
--- PASS: TestGiteaIssuesIntegration (0.33s)
--- PASS: TestGiteaCrossRepositoryDependencyIntegration (0.25s)
--- PASS: TestGiteaReleasesIntegration (1.44s)
--- PASS: TestGiteaMilestonesIntegration (0.13s)
--- PASS: TestGiteaGoModuleIntegration (1.41s)
PASS
ok  	github.com/JairJosafath/theos/internal/gitprovider	11.747s

$ go test ./internal/model/ ./internal/gitprovider/ -run "FailedReleaseRuns|ReleaseRerun|RerunFailedJobs" -v
--- PASS: TestFailedReleaseRuns (0.00s)
--- PASS: TestReleaseRerun (0.00s)
ok  	github.com/JairJosafath/theos/internal/model	0.005s
--- PASS: TestRerunFailedJobs (0.00s)
ok  	github.com/JairJosafath/theos/internal/gitprovider	0.009s
```

```text
$ go test ./internal/controller/ -run TestControllers -ginkgo.focus "rerun-release|release run" -ginkgo.v
Ran 4 of 254 Specs in 22.200 seconds
SUCCESS! -- 4 Passed | 0 Failed | 0 Pending | 250 Skipped
```

The local Gitea runs no Actions, so `TestGiteaReleasesIntegration` checks that the
call reaches Gitea's re-run handler (its HTTP 404 names the run, where a wrong
path gets the router's bare 404 page); re-running a real failed run is what a
human run on Kind would show: a failed `release.yaml` run, the annotation set,
the run's new attempt in Gitea and `status.lastRelease.run` following it.
