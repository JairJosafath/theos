# Show what the Standard changed in a file it left out

Issue [#975](https://github.com/JairJosafath/c4factory/issues/975). Agent: otter-975.

## Goal

When a Standard update leaves a file out ([standard-left-out.md](standard-left-out.md)),
the person chooses: take the Standard's version or keep the repository's. Today
nothing shows what the Standard changed in that file: the condition names the
file and the two refs, the update's pull request lists it under "Left out" and
carries only the files that merged. gray-lenovo's cel-lens-core had its
`Makefile` left out of go-standard v1.1.6 to v1.2.1; its owners would have to
find the template repository and diff two tags themselves, or choose blind.
Their Makefile holds their own targets, so they also can't tell what *take*
would drop.

Done when: wherever the factory asks to take or keep a left-out file, it shows
the Standard's change to that file between the file's ref and the Standard's
new one, and the repository's lines a take would drop: in the update's pull
request, and on the Container (`status.standard.behind`, the UI's Container
page) for an update that opens no pull request. A test of the update's pull
request body shows it.

## Decisions

Taken by otter-975 in a headless session; the user can revisit them on the
pull request.

- **The diff is of the Standard's renders, not of its template sources.** The
  operator already renders the Standard at each left-out file's ref (its base
  in `behind`) and at the new ref to plan the update. The diff between those
  two renders of the one path is exactly "what the Standard changed in this
  file", with the Container's names filled in, at the path the repository
  holds; a link to the template repository's compare view would show template
  sources (`text/template`, other paths, every file of the Standard) and needs
  a host that has one. No link is the main answer.
- **A unified diff, made by the planner.** `internal/scaffold` gets
  `StandardChange(base, next Tree, path) FileChange`: a unified diff (three
  lines of context, `@@` hunk headers, `--- <path> (<old ref>)` and
  `+++ <path> (<new ref>)`) built on the line LCS `merge.go` already has, no
  new dependency. A file only one render has shows as all added or all
  removed; a binary file (a NUL byte, as `Merge3` decides) or one that only
  changed its executable bit gets a one-line note instead of a diff. Pure and
  table-tested, like `Merge3`.
- **The take's cost is shown before the take.** `PlanTake` already lists the
  repository's lines a take drops (`TakenFile.Dropped`), for the take's own
  pull request. The left-out section runs it as a preview, so the person sees
  "taking it drops these lines of yours" next to the Standard's change, and
  that every other line the repository changed stays. That answers the
  reporter's fear that take drops their own targets.
- **In the update's pull request, per file.** `StandardUpdateBody`'s
  "Left out" section gets one `### <path>` per file: the ref it is at, the
  Standard's change in a ```` ```diff ```` block, the dropped lines, and the two
  `kubectl annotate` lines. A carried-over file is diffed from its own ref, not
  the update's `from`.
- **Bounded.** At most 200 diff lines and 100 dropped lines per file, cut at a
  line with "… N more lines not shown"; the whole body stays under 60,000
  characters (GitHub's limit is 65,536, Gitea's is larger), and a file past
  that says "too large to show here; on the Container's page". The numbers are
  constants beside `maxDroppedLines`.
- **On the Container too, because not every update has a pull request.** An
  update that leaves files out and has nothing else to write opens no pull
  request today, and a file carried through later updates may outlive the one
  that left it out. So each `status.standard.behind` entry gets `change`: the
  same diff and dropped lines, as text, cut at 4 KiB; at most 16 entries carry
  it and the rest say so, so the status grows by 64 KiB at most, far below an
  object's limit (a Container rarely has more than a handful behind). It is computed when the entry
  is written or its ref moves, never on each reconcile. The `FilesBehind`
  message stays short and adds "what the Standard changed in each is in
  `status.standard.behind` and on the Container's page". The CRDs are the
  truth: the UI reads it from there, it renders nothing itself.
- **The UI shows it where the choice is offered.** The Container page's
  left-out files, and the *Needs a look* item, show each file's change in a
  collapsed block beside the take and keep buttons. The Brief's `needsYou`
  item's why says the change is on the Container's page.
- **The design changes** in `docs/design.md`, "A Standard's fixes reach its
  Containers".

## Steps

Each is its own issue and pull request, and leaves `main` working.

1. **(#979) The update's pull request shows the Standard's change to each left-out
   file.** `scaffold.StandardChange` and the cut; a `model.LeftOutFile`
   (path, ref, diff, dropped, note) replacing the `leftOut []string` argument
   of `StandardUpdateBody`; the controller fills it from the renders it holds
   and `PlanTake` as a preview. Table tests of the diff (changed, added,
   removed, binary, mode only, CRLF, no final newline, the cuts) and of the
   body; the envtest that leaves `Makefile` out checks the pull request body
   carries the Standard's change and the dropped lines.
2. **(#980) The Container carries each left-out file's change.** API
   `status.standard.behind[].change` (`make manifests`), filled when an entry
   is written or its ref moves, including an update that opens no pull
   request; the 4 KiB and 16-entry bounds; the `FilesBehind` message points at
   it. Envtest: an update whose only change is left out records the change
   with no pull request; a carried-over entry keeps its change until its ref
   moves.
3. **(#981) The UI shows it beside take and keep.** Container page and *Needs a
   look*: a collapsed block per file; the Brief's `needsYou` why names the
   Container's page. UI tests with a fixture holding `change`. Depends on
   step 2.

Kind would show the loop on a real Gitea; no step waits for it.

Evidence: the tests the three steps added, rerun on main
([the run](../../screenshots/standard-left-out-diff.txt)).
