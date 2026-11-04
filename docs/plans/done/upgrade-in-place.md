# Plan: an in-place upgrade leaves running Tasks alone, and a Brief pauses in one step

GitHub issue #677, reported by gray-lenovo while upgrading v0.10.1 to v0.11.0.
Agent: otter-677.

## Goal

A person upgrades the factory with `helm upgrade` whenever a release is out,
while Tasks are running, and those Tasks carry on: same sandbox, same round,
same token. Someone who wants a quiet moment anyway pauses a Brief, and with
it every Crew the Brief made, in one step, sees when nothing is running any
more, and resumes it in one step. The guide's upgrade section says all of
this.

Done when the steps below are merged.

## What happened

gray-lenovo's Crews build 4 issues at once, so a moment with no Task running
never came; they paused three Crews with `kubectl patch` and remembered to
undo it. The guide says only "add it to `gitea.projects` and `helm upgrade`",
and they had seen that an upgrade re-creates the catalog and the Agents.

What an upgrade does to running work today, from the code:

- **The operator restart is safe.** A Task's whole state is in its status
  (phase, round, review attempts, reruns, branch, pull request, the sandbox
  and its pod's UID); the restarted operator finds the sandbox by the name it
  derives from that status (`task_sandbox.go` `runState`), and reads the
  run's result from the pod's termination message, so a run that ends while
  the operator is away is still picked up. Sandboxes are never patched: a new
  Agent image or operator flag applies from the next run. The only in-memory
  state is the Crew's Event dedupe, so an Event may be posted twice.
- **The sandbox's token is safe.** A sandbox reads `<container>-agent-git`
  once, as env, when its pod starts. The seed never touches that Secret, and
  when the operator re-mints it, the old tokens are revoked only once no
  sandbox older than the re-mint is left (`revokeOldAgentTokensOnce`).
- **The Agents, the catalog and the GitProviders are deleted and
  re-created.** They are Helm hooks (`post-install,post-upgrade`,
  `before-hook-creation`, `dist/chart/templates/gitea/agents.yaml`,
  `catalog.yaml`, `gitproviders.yaml`, `gitprovider/gitproviders.yaml`),
  because the CRDs are release templates and the objects of their kinds must
  wait for them. Nothing is owned by an Agent, so nothing cascades, but a
  reconcile in the gap sets the Task's `Progressing=False AgentNotFound`,
  stalls Crews and makes a Brief ask for its Agent, and a Git step in the gap
  fails for want of its GitProvider. Every object comes back with a new UID,
  and every hand edit of them is lost. This is what the reporter saw.
- **Gitea restarts when its image changes** (`strategy: Recreate`, SQLite on
  a ReadWriteOnce volume). A sandbox's clone or push retries for about a
  minute (`internal/agentrunner/retry.go`); a Gitea that takes longer to come
  back fails the run, and that costs the Task a round.
- **A new Standard ref is the designed update.** The re-created Standard
  carries the release's template tag, and each Container's Standard update
  pull request moves `main`; a running Task's pull request goes behind and
  takes the merge round it would take for any other merge.
- **A Crew's `spec.paused` stops it starting issues; a Task already started
  runs to its end.** A paused Crew still tags its releases. A Brief has no
  pause.

## Decisions, and why

1. **Running Tasks survive an upgrade, and the guide says so.** The only
   disruptions are the hook gap and a long Gitea restart; both are removed
   below, so nobody has to pause or wait. Pausing stays for those who want a
   quiet moment for their own reasons.
2. **The seed applies the Agents, the catalog and the GitProviders in place,
   instead of Helm re-creating them.** The chart renders them into a
   ConfigMap `theos-catalog` (an ordinary release object), and the seed
   job, already a post-install and post-upgrade hook that runs once the CRDs
   exist, applies it with `kubectl apply --server-side --force-conflicts
   --field-manager=theos-chart`. The objects keep their UIDs, there is no
   gap, and a value the chart sets (an Agent's image, a Standard's ref) is
   updated in place. A field a person added that the chart doesn't set (an
   Agent's `env`) is kept; a field the chart sets is the chart's, as now. The
   first upgrade to this chart adopts the existing hook objects (server-side
   apply takes the fields over), so no migration is needed. The seed's role
   gains `get;list;create;patch` on Agents, Frameworks, Standards and
   GitProviders in the project namespaces. An object the chart stops
   rendering is left in place, as a hook object is today. Why not keep the
   hooks with no delete policy: Helm then fails the second upgrade on
   "already exists". Why not make them ordinary release objects: they would
   be applied before the CRDs they need on install, and a Kind check that
   they come up in order is a cluster-only test.
3. **A sandbox waits out a Gitea restart.** The runner's retry of a clone or
   push that couldn't reach the Git host grows from about a minute to about
   five (5s, 15s, 40s, 60s, 90s, 90s), which covers a Gitea pod restarting
   on a pulled image. A run that still couldn't reach the host ends as
   interrupted (the infrastructure failed it, not the agent), so it takes
   one of the round's free reruns (`MaxReruns`) and costs no round.
4. **A Brief has `spec.paused`, and it reaches every Crew it made.** While it
   is true, the Brief sets `spec.paused: true` on each Crew carrying its
   label, starts no new round (a new prompt waits) and tends no failed issue;
   a planner Task already started runs to its end, as a Crew's Tasks do. The
   Brief marks each Crew it paused with the annotation
   `theos.factory.io/paused-by: brief`; set back to false, it unpauses only the
   Crews carrying that mark and removes it, so a Crew a person paused by hand
   stays paused. This is unlike `parallel`, which the Brief owns outright: a
   person pausing one app of a Brief is a fair thing to want, and the
   reporter did exactly that.
5. **A paused Brief says what is still running.** Its condition
   `Paused=True` names the Tasks still running in its Crews and its planner
   Task ("Paused; 5 Tasks still running: cel-lens-core-181, ..."), and once
   none is, "Paused; nothing is running". A person upgrading waits for that
   line, not for a moment that may never come.
6. **The UI pauses and resumes a Brief.** The Brief's card gets a Pause and
   a Resume button that patch `spec.paused`, and shows the `Paused`
   condition's message. A paused Crew's badge already shows.
7. **The guide gets an upgrade section.** `docs/getting-started.md`
   "Upgrading": `helm upgrade` with the same values; running Tasks carry on
   (what survives and why, in a few lines); what changes at once (new Agent
   values apply from the next run, a lower `maxRounds` applies to running
   Tasks, Standard update pull requests follow a new template tag); how to
   pause a Brief or a Crew and see when nothing runs; that hand edits of the
   chart's Agents and catalog are kept only where the chart doesn't set the
   field.

## Steps

Each step is its own issue and pull request, and leaves main working.

1. **The seed applies the Agents, catalog and GitProviders in place**
   (#681, decision 2). The chart's ConfigMap, the seed's apply and its RBAC, the
   hook annotations gone, `dist/chart/README.md` and the design's table.
   Tier 1/2: `make validate-yaml` renders the chart and the ConfigMap's
   objects validate against the CRDs; a chart unit test (or the render
   check) that no Agent, Framework, Standard or GitProvider is a hook any
   more. `make seed-local`: the seed applies the ConfigMap twice and the
   second apply keeps every UID and changes an Agent's image in place.
2. **A sandbox waits out a Gitea restart** (#682, decision 3). Tier 1: the
   retry delays add up to about five minutes, and a run whose Git host stayed
   unreachable reports itself interrupted; `internal/model` table test that
   such a run takes a free rerun. envtest: a Task whose run ended
   unreachable reruns the round without counting it.
3. **A Brief pauses and resumes its Crews** (#683, decisions 4 and 5). API field,
   CRD, reconciler, condition, design. envtest: pausing pauses every
   labeled Crew and marks it, starts no round on a new prompt, a Crew paused
   by hand stays paused on resume, resume unpauses and unmarks the others,
   and the condition counts running Tasks down to "nothing is running".
4. **The UI pauses and resumes a Brief** (#684, decision 6). Vitest: the buttons
   patch `spec.paused`, and the card shows the condition's message. Depends
   on step 3.
5. **The guide's upgrade section** (#685, decision 7). Depends on steps 1, 2
   and 3, so it describes what ships.

## Not done here

A Gitea Actions job killed by a runner pod's restart shows as a failed
check, which costs the Task a fix round; whether Gitea marks such a job
failed or cancelled is something only a real runner shows, so it waits for a
human Kind run of an upgrade with a check in flight, which would also show
the whole upgrade leaving a running Task alone. Since #1074 such a job, cancelled or
cut off, is rerun at no round instead. Pausing the whole factory
(every Brief and Crew in every project) is a loop over the Briefs; if it is
wanted, it is its own issue.
