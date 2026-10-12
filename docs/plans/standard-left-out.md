# A Standard's fixes reach every Container: merge, flag and offer the files left out

Issue [#880](https://github.com/JairJosafath/c4factory/issues/880). Agent: otter-880.

## Goal

A fix shipped in a Standard reaches every Container it is meant for. Today a
Standard update leaves out any file the repository changed since its render
(`status.standard.leftOut`), says so only in the update's pull request and a
True `StandardUpdated` condition, and expects a person to bring it up to date
by hand. gray-lenovo's engine app, cel-lens-core, kept the old
`.gitea/workflows/check.yaml` of go-standard v1.1.6 this way and nobody was
told: the skip for changes that touch no code (#600) never reached it, and a
CHANGELOG-only change still ran the full 18-minute check.

Done when: a file both the Standard and the repository changed is merged line
by line into the update when the changes don't overlap; one whose changes
overlap is flagged where the person looks (the Work area's *Needs a look*, the
Brief that made the Container, `kubectl get container`), stays flagged across
later updates until it is settled, and the person settles it in one step:
take the Standard's version, which the operator writes through a pull request
of its own, or keep the repository's.

## Decisions

Taken by otter-880 in a headless session; the user can revisit them on the
pull request.

- **Merge by lines first, as the operator.** Most files a repository changed
  and the Standard changed too were changed in different places: a Standard
  change added a step to `check.yaml`, the Standard's fix added a
  `paths-ignore`. `scaffold.PlanUpdate` does a three-way merge (base: the
  render the repository's copy came from; ours: the repository's copy; theirs:
  the new render), the way `git merge-file` does. A clean merge is written in
  the update's pull request like any other path and named in its body and
  commit as "merged with the repository's changes", so the reviewer of record
  (the person reading the pull request) sees it. Only overlapping changes, a
  binary file, or a file one side removed and the other changed are left out.
  Still no agent: the merge is a pure function in `internal/scaffold`, with no
  Kubernetes and no Git host, table-tested; the update still merges only once
  CI passes with every check `main` requires. The diff is our own (a line
  LCS, in `internal/scaffold`), not a new dependency, so the layering stays.
- **A left-out file stays left out until it is settled.** Today `leftOut` is
  replaced by each update, and a later ref that doesn't change the file drops
  it from the list although the repository still holds the old version. From
  now on `status.standard.behind` lists each left-out path with the ref its
  repository copy was last brought to (the merge base). Each update carries
  every entry forward and merges it against that base: a path the repository
  now holds as the new render, or that merges cleanly, leaves the list. The
  old `leftOut` stays, filled with the same paths, so nothing that reads it
  breaks.
- **Flagged as waiting on a person, where the person looks.** While
  `behind` lists a path and no update is in flight, `StandardUpdated` is False
  with reason `FilesBehind`, its message naming the files, the ref, and the
  two answers. The Container's Warning Event `StandardFilesBehind` says it
  once per ref. The UI's *Needs a look* lists the Container ("has Standard
  files left out of an update"), and its page names the files in place of
  "bring them up to date by hand". The Brief that made the Container lists it
  in `status.needsYou`, kind `Container`, so it shows in the Brief's card and
  `NeedsYou` condition; the choice the Brief would make is *take*, the why
  "the Standard's fix (name, ref) reaches the app; the repository's own lines
  that overlap it are listed in the pull request and can be added back". It is
  answered on the Container, not by a prompt, as a request is answered in
  `spec.releases`. In no autopilot mode does the Brief take it by itself: the
  repository's change may itself be a reviewed Standard change.
- **The person answers with one annotation on the Container.**
  `c4.factory.io/standard-take: <ref>` takes the Standard's version of every
  path in `behind`; `c4.factory.io/standard-keep: <ref>` keeps the
  repository's. The value is the Standard ref the answer was given for; an
  answer for another ref (the Standard moved on since) is ignored and the item
  stays. The Kubernetes API is the truth and agents never reach it, so no
  agent can answer for the person; the UI offers both as one-click actions with
  `ui.actions.enabled` (an allowlisted merge patch of the annotations, like
  `c4.factory.io/retry`) and shows the `kubectl annotate` lines otherwise.
- **Take is the operator's pull request, merged like an update.** The
  operator opens "Bring `<paths>` up to date with `<standard>` `<ref>`" on
  branch `standard-take/<standard>-<ref>`: each file is the three-way merge
  with every overlapping hunk resolved to the Standard's side, so the
  repository's other changes stay; its body shows, per file, the repository's
  lines it drops. It merges as admin once CI passes, through the same gated
  merge as an update, and `behind` empties. Closing it leaves the files
  behind and flagged; one take or update is in flight per Container at a time.
- **Keep is recorded.** `status.standard.kept` lists each kept path with the
  ref it was kept at; it leaves *Needs a look*. A later ref that changes the
  path again plans it like any other: merged when clean, flagged again when not.
- **The design changes** in `docs/design.md`, "A Standard's fixes reach its
  Containers", and in *Needs you*'s list of Container items.

## Steps

Each is its own issue and pull request, and leaves `main` working.

1. **Three-way line merge in Standard updates (#890).** `internal/scaffold`: a line
   diff and `Merge3(base, ours, theirs)` returning the merged content or the
   conflicting hunks; `PlanUpdate` writes clean merges and reports them
   (`Update.Merged`), leaving out only conflicts, binary files and
   remove-versus-change. Body and commit message name the merged paths.
   Table tests: disjoint edits, overlapping edits, edits at the file's ends,
   both sides the same edit, CRLF and missing final newline kept, binary.
2. **Left-out files are tracked and flagged (#891).** API: `status.standard.behind`
   (path, ref) and `kept`; `leftOut` kept in step. The update plans every
   `behind` path against its own base; `StandardUpdated` False `FilesBehind`;
   Event `StandardFilesBehind`. UI: *Needs a look* item, Container page and
   lifecycle text. Envtest: a left-out file flags the Container and stays
   flagged through a later ref that doesn't change it; a clean merge clears it.
   Built by otter-891: each entry also keeps the pull request that last left
   it out (`pullRequestURL`), so *Needs a look* links it once the update is
   merged and gone from status. A `leftOut` path written before `behind` was
   kept is taken as behind at the Container's ref, as the planner treated it
   until now. The Event goes out when the condition's message changes, which
   names the ref, so once per ref.
3. **The Brief lists its apps' left-out files (#892).** `status.needsYou` gets a
   `Container` item per flagged Container the Brief made, with the choice and
   why above; the UI's Brief card shows it with the two answers. Envtest.
   Built by otter-892: the item is listed in every autopilot mode, as a
   request waiting in Ask is, from the Containers labeled with the Brief whose
   `StandardUpdated` is `FilesBehind`; the Brief's Container watch passes a
   change of that condition's message. Its recommend names the take
   annotation; the card links every Container item to the Container's page
   and says it is settled there, not in a prompt; the buttons come with step 4.
4. **Take or keep from one annotation (#893).** The two annotations, the take pull
   request (merged like an update), `kept`, and the UI actions (server
   allowlist, ClusterRole unchanged, buttons on *Needs a look* and the
   Container page). Envtest with the fake Git host: take opens and merges the
   pull request and clears `behind`; keep records `kept`; a stale ref is
   ignored; closing the take pull request leaves the item.
   Built by otter-893: the operator removes an answer once it acted on or
   ignored it, so a person can take again after closing a take; an answer
   waits while a take is in flight, and is ignored while an update to another
   ref is. A take is `status.standard.update` with `take: true`, followed by
   the same steps as an update; one that conflicts with main is opened again.

Kind would show the whole loop on a real Gitea and runner; no step waits for
it. Bringing cel-lens-core's `check.yaml` up to date now is not part of this
plan: once step 4 lands, its owners answer it with one annotation.
