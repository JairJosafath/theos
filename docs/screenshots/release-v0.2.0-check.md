# Release v0.2.0 checks itself (#191)

*Verify Release* (`.github/workflows/verify-release.yml`) installed the
published chart `oci://ghcr.io/jairjosafath/c4factory/charts/c4-factory`
0.2.0 on a fresh Kind cluster of a hosted runner, building nothing, and ran
green: https://github.com/JairJosafath/c4factory/actions/runs/37394587730

The run came from the workflow's temporary `pull_request` trigger on its branch
(pull request #194), which stood in for `workflow_dispatch` with `tag=v0.2.0`
until the file is on main; the trigger was removed before merging. The first
try, https://github.com/JairJosafath/c4factory/actions/runs/37393852524, passed
every check of the release and failed only in the workflow's own evidence step
(the checkout removed the evidence directory made before it); that was fixed.

Every step passed: Agent Sandbox v1.0.4, the `ghcr` pull secret from
`GITHUB_TOKEN`, `agent-base` and `agent-go` pulled and loaded into Kind,
`helm install --wait`, the seed's repositories, runners `base go`, the manager
and the UI rolled out, and `orders-api` scaffolded into the release's Gitea.
"File the failure" was skipped. The evidence below is from the run's artifact
`release-v0.2.0-check`.

## The runner
```
== free -m
               total        used        free      shared  buff/cache   available
Mem:            7937        2009         269          49        6022        5927
Swap:           3071          64        3007
== df -h /
Filesystem      Size  Used Avail Use% Mounted on
/dev/root        72G   51G   21G  72% /
Runner: 5924 MB of memory available, 21105 MB of disk left, 0 OOMKilled or Evicted.
```

To fit, the go runner ran without its Go build cache and with 100m CPU and 128Mi requested, as in *Test Chart*.

## Gitea's repositories
```
templates/gitea-release
templates/go-cli
templates/go-standard
templates/hello
templates/hello-standard
templates/k8s-manifests
templates/postgres
templates/python-fastapi
templates/small
templates/ts-standard
templates/vscode-extension
webshop/orders-api
```

## The webshop sample
```
NAME                            SYSTEMS   CONTAINERS   READY   SESSIONS   AGE
project.c4.factory.io/webshop   1         1            1       0          6s

NAME                              PROVIDER   URL                                         OWNER     AGE
gitprovider.c4.factory.io/gitea   gitea      http://gitea.gitea.svc.cluster.local:3000   webshop   10s

NAME                                        DISPLAY NAME   CONTAINERS   READY   SESSIONS   AGE
softwaresystem.c4.factory.io/webshop-core   Webshop Core   1            1       0          6s

NAME                                 TYPE      PHASE   CHECKS   SESSIONS   REPOSITORY                                                     AGE
container.c4.factory.io/orders-api   service   Ready            0          http://gitea.gitea.svc.cluster.local:3000/webshop/orders-api   7s

NAME                                CONNECTOR   RESOLVED   AGE
datastore.c4.factory.io/orders-db   postgres    True       7s

NAME                                                    SOURCE       TARGET         DESCRIPTION                    APPLIED   AGE
relationship.c4.factory.io/customer-uses-webshop-core   customer     webshop-core   Browses, orders and reorders             6s
relationship.c4.factory.io/orders-api-to-orders-db      orders-api   orders-db      Reads and writes orders        True      6s
```

## Pods
```
NAMESPACE             NAME                                                 READY  STATUS   RESTARTS
agent-sandbox-system  agent-sandbox-controller-5d58fb5df9-7w67q            1/1    Running  0
c4-factory-system     c4-factory-controller-manager-57655c4597-xqrn7       1/1    Running  0
c4-factory-system     c4-factory-ui-6d94984cd9-qnc59                       1/1    Running  0
gitea                 gitea-f5fc8bd76-tzsj6                                1/1    Running  0
gitea                 gitea-runner-base-75b4f5c877-bd984                   1/1    Running  0
gitea                 gitea-runner-go-659b7bf849-dqzmg                     1/1    Running  0
kube-system           coredns-559f6c778d-dmczv                             1/1    Running  0
kube-system           coredns-559f6c778d-gbcjj                             1/1    Running  0
kube-system           etcd-release-check-control-plane                     1/1    Running  0
kube-system           kindnet-t98s6                                        1/1    Running  0
kube-system           kube-apiserver-release-check-control-plane           1/1    Running  0
kube-system           kube-controller-manager-release-check-control-plane  1/1    Running  0
kube-system           kube-proxy-64n7x                                     1/1    Running  0
kube-system           kube-scheduler-release-check-control-plane           1/1    Running  0
local-path-storage    local-path-provisioner-75f7fc7dc5-qm58v              1/1    Running  0
```
