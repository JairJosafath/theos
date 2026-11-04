# The model proxy: the plan's *Done when*

Evidence for [docs/plans/done/model-proxy.md](../plans/done/model-proxy.md), taken 2026-10-22 on branch `feat/1137-the-claude-token-stays-in-the-release` (main at `3fffdf59` merged in). Steps landed as #1134, #1135, #1136 and #1137.

- A sandbox on the shared token holds no token: `TestBuildSandboxModelProxy`.
- The proxy adds the credential from the one Secret and picks up a rotated token: `internal/modelproxy` tests.
- The fence lets the sandbox reach the proxy and the proxy admits only sandboxes: `TestAgentNetworkFence`, the chart tests, `make validate-yaml`.
- No project namespace holds a copy of the token: envtest `Project sharing Secrets` (8 specs, "#1137") and `brief_round_test.go` "gets no copy of it (#1137)".

```
## modelproxy
--- PASS: TestReplacesTheClientCredential (0.01s)
--- PASS: TestMergesTheOAuthBeta (0.01s)
--- PASS: TestOAuthTokenWinsOverAPIKey (0.00s)
--- PASS: TestPicksUpARotatedToken (0.01s)
--- PASS: TestStreamsWithoutBuffering (0.07s)
--- PASS: TestAnswers503WithoutACredential (0.01s)
--- PASS: TestForwardsPathAndQuery (0.01s)
--- PASS: TestLogsNoCredential (0.01s)
--- PASS: TestReadyWithoutACredentialSaysSo (0.00s)
--- PASS: TestConnectionHeaderKeepsTheCredential (0.00s)
--- PASS: TestDrainFailsReadinessOnly (0.00s)
ok  	github.com/JairJosafath/theos/internal/modelproxy	0.132s
## sandbox+fence unit
--- PASS: TestAgentNetworkFence (0.08s)
--- PASS: TestBuildSandboxGoProxy (0.00s)
--- PASS: TestBuildSandboxModelProxy (0.00s)
ok  	github.com/JairJosafath/theos/internal/controller	0.129s
## envtest focused
ok  	github.com/JairJosafath/theos/internal/controller	30.325s
## chart
--- PASS: TestModelProxyChart (0.42s)
--- PASS: TestModelProxyKustomize (0.00s)
--- PASS: TestModelProxyManagerFlag (0.36s)
ok  	github.com/JairJosafath/theos/test/chart	0.796s
## validate-yaml
All 14 invalid files rejected
The chart's manager Deployment keeps /tmp, POD_NAMESPACE, THEOS_CHART_VERSION, the shared Secrets, the model proxy URL and the UI's ServiceAccount
```
