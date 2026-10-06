# Test without a cluster: the plan's *Done when*

Evidence for [docs/plans/done/test-without-cluster.md](../plans/done/test-without-cluster.md), taken 2026-10-06 on main at `17c4edd` plus the docs of #355. Steps 2–8 landed as #348, #353, #349, #350, #351, #354 and #352 (all closed).

## `hack/lib/github_test.sh`: acquire refuses without the opt-in; pick hands out a former `needs/cluster` issue

```
ok   acquire, no human opt-in: refused
ok   acquire, no human opt-in: names it
ok   acquire, opt-in not 1: refused
ok   pick, no cluster: a needs/cluster issue
... 179 ok, exit 0
```

## `grep -rn "cluster acquire" hack/relay.sh .claude/skills`: only the human path

```
.claude/skills/factory-ops/SKILL.md:12:first leases one (`C4_HUMAN_CLUSTER=1 hack/lib/github.sh cluster acquire`
.claude/skills/handoff/SKILL.md:53:would check. Kind is a tier only a human runs: `cluster acquire` refuses
.claude/skills/handoff/SKILL.md:62:2. `C4_HUMAN_CLUSTER=1 hack/lib/github.sh cluster acquire` prints the cluster
hack/relay.sh:57:# human runs (hack/lib/github.sh cluster acquire refuses without
```

## `go vet -tags=e2e ./test/e2e/...`, the four specs and the guard

```
go vet: ok
test/e2e/brief_test.go:78:	It("plans a Container on hello-html, its Crew and an issue the Crew picks up", func() {
test/e2e/agents_test.go:58:	It("scaffolds a Container from hello-html and protects main", func() {
test/e2e/agents_test.go:84:	It("takes a Task two rounds to a merge with the stand-in agent", func() {
test/e2e/agents_test.go:112:	It("carries a Standard bump to main through a pull request it merges", func() {
=== RUN   TestCheck
=== RUN   TestCheck/stand-in
=== RUN   TestCheck/claude-code
=== RUN   TestCheck/harness_left_to_the_default
=== RUN   TestCheck/credentials_Secret
=== RUN   TestCheck/Secret_in_env
--- PASS: TestCheck (0.00s)
ok  	github.com/JairJosafath/c4factory/test/e2e/standin	0.004s
```

## Workflows that start Kind run on `workflow_dispatch` only

```
test-chart.yml:   workflow_dispatch:
test-e2e.yml:   workflow_dispatch:
verify-release.yml:   workflow_dispatch:
```

## `grep -rn "on Kind" ROADMAP.md`: nothing

```
(no match)
```
