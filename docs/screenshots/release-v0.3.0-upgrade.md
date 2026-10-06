# Verify Release also upgrades from the previous release (#307)

*Verify Release* now runs two checks per release, from a `plan` job that finds
the previous release: **install** (as before) and **upgrade** (install the
previous release, follow its samples until the Crew is `Idle`, `helm upgrade`
with the same values, then check). Three dispatches of
`feat/307-verify-release-also-upgrades-from-the` for `tag=v0.3.0`:

| Run | Upgrade | Result |
| --- | --- | --- |
| [37449727784](https://github.com/JairJosafath/c4factory/actions/runs/37449727784) | v0.2.0 → v0.3.0, found from the tags | **fails, rightly**: Secrets `gitea-admin` and `gitea-runner` change (v0.3.0 dropped the chart templates that rendered them, so `helm upgrade` deletes them and Gitea's credentials container makes new ones). CRDs, objects, logs and repositories pass. |
| [37453408885](https://github.com/JairJosafath/c4factory/actions/runs/37453408885) | v0.3.0 → v0.3.0, branch `demo/307-upgrade-catches-a-break`: the new chart drops Crew `spec.containerRef`, which the first-app Crew sets | **fails, as meant**: `Crew webshop/orders-cli was phase Idle, Ready True; after the upgrade phase Waiting, Ready False` |
| [37454594924](https://github.com/JairJosafath/c4factory/actions/runs/37454594924) | v0.3.0 → v0.3.0 (`-f from=v0.3.0`), at 81675a5 | **green**: install and upgrade |

No pair of published releases can be green yet: v0.2.0 → v0.3.0 really
rotates the Gitea Secrets, and v0.1.0's chart has no Gitea to start from. The
first real green upgrade is v0.3.0 → v0.4.0, which Release runs by itself.

## v0.2.0 → v0.3.0: the generated Secrets change

```diff
--- before
+++ after
@@ -1,2 +1,2 @@
-gitea-admin a28ca3d56d1d59e2
-gitea-runner 0a5706b90c3fa702
+gitea-admin a821c74f1101f1cd
+gitea-runner 334328a6f4320f7a
```

## The demo: a removed CRD field

```diff
--- before
+++ after
@@ -4,7 +4,7 @@
 Connector	webshop	postgres	-	-
 Container	webshop	orders-api	Ready	-
 Container	webshop	orders-cli	Ready	-
-Crew	webshop	orders-cli	Idle	True
+Crew	webshop	orders-cli	Waiting	False
 DataStore	webshop	orders-db	-	-
 Deployment	webshop	k8s-manifests	-	-
 Framework	webshop	go-cli	-	-
```

## The green run: what the upgrade kept

Every object before the upgrade, with its phase and `Ready` (kind,
namespace, name, phase, Ready); after the upgrade the list was the same, as
were the Secrets' hashes and the repositories' heads:

```
Agent	webshop	claude-implementer	-	-
Agent	webshop	claude-reviewer	-	-
Compute	webshop	small	-	-
Connector	webshop	postgres	-	-
Container	webshop	orders-api	Ready	-
Container	webshop	orders-cli	Ready	-
Crew	webshop	orders-cli	Idle	True
DataStore	webshop	orders-db	-	-
Deployment	webshop	k8s-manifests	-	-
Framework	webshop	go-cli	-	-
Framework	webshop	python-fastapi	-	-
GitProvider	webshop	gitea	-	-
Person	webshop	customer	-	-
Person	webshop	staff	-	-
Project	webshop	webshop	-	-
Relationship	webshop	customer-uses-webshop-core	-	-
Relationship	webshop	orders-api-to-orders-db	-	-
Relationship	webshop	staff-uses-orders-cli	-	-
Relationship	webshop	staff-uses-webshop-core	-	-
SoftwareSystem	webshop	webshop-core	-	-
Standard	webshop	go-standard	-	-
```

```
REVISION	UPDATED                 	STATUS    	CHART           	APP VERSION	DESCRIPTION     
1       	Tue Oct  6 11:20:00 2026	superseded	c4-factory-0.3.0	0.3.0      	Install complete
2       	Tue Oct  6 11:21:12 2026	deployed  	c4-factory-0.3.0	0.3.0      	Upgrade complete
```
