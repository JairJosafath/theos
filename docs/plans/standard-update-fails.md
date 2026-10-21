# A Standard update whose check fails gets fixed by the Crew

GitHub issue #1447. Agent: otter-1447. Written 2026-10-20.

## Goal

A Standard update that a Container's own code fails is fixed by the
Container's Crew, told what is wrong, and then merges by itself. The case
that raised it: `aws-standard` v1.17.0 (#1267) makes `infra/standard.test.ts`
refuse a backup selection by tag. A Container scaffolded from an earlier
`aws-*` Framework still selects by tag in `infra/backups.ts`, a Framework
file the update never touches, so the update's CI fails, the Container shows
`StandardUpdated=False`, `ChecksFailed`, and nothing else happens.

Done when every step below is merged: the failing test names the fix, the
operator files an issue for the Crew when an update's CI fails, and an update
whose CI failed runs again once the repository's main moves.

## Where we stand

- `model.NextStandardStep` stops at `ChecksFailed` while the update's commit
  has failed CI: "the update merges once a re-run passes, or close the pull
  request to drop it". Nobody is told, and a re-run of the same commit fails
  the same way.
- The update's branch is the old main plus the Standard's difference. A fix
  the Crew merges into main never reaches it: CI on the pull request runs on
  its head commit, and the operator drops an update only when main conflicts
  with it.
- The operator already files work for a Crew in its repository: a release of
  B files `Bump B to vX`, `kind/bump`, `status/ready`, in A (`crew_bump.go`).
- The check's message for this case, `selects by ListOfTags, which reaches
  every stack of the Container`, says what is wrong but not what to write.

## Decisions, and why

1. **The operator files an issue, not a Task, and only on failure** (option
   1 of #1447, through the issue loop). An issue in the Container's
   repository is how the operator already hands a Crew work (bumps), the Crew's
   agents pick ready issues by themselves, and it stays readable to the owner.
   It is filed for any failed check of a Standard update, not only this test:
   the next breaking check needs no new code. The update itself stays the
   operator's alone (design.md: no agent takes part in it); the fix lands on
   main through the Crew's ordinary pull request.
2. **One issue per target ref.** Title `Standard <name> <ref> fails this
   repository's check`, labels `kind/bug` and `status/ready`, in the
   Container's next unprepared release as a bump is. The body links the
   update's pull request and the failed run, says the fix goes to main in its
   own pull request (never to the update's branch, which the operator would
   then stop as pushed to), and that the update runs again by itself once
   main moves. Its number is kept in `status.standard` so a re-plan or a
   restart never files it twice; a newer target ref files its own.
3. **An update whose CI failed is planned again when main moves.** When the
   update stopped at `ChecksFailed` and main's head is no longer the commit
   the update's branch started from, the operator closes the pull request and
   plans again from the new main, as it does for one that conflicts with
   main. That is how the Crew's fix meets the new Standard. While main stays
   put, nothing changes: the failure stays reported, and the issue stays
   open.
4. **The failing check says what to write.** Rules the Standard tightens
   name the fix in their message, since the agent fixing it reads the
   message first. For #1267: `select the stack's tables with
   BackupResource.fromArn(table.tableArn) in infra/backups.ts, as the
   aws-* Frameworks since #1267 do`. This is an `aws-standard` patch release
   and a Template `ref` bump; no operator change.
5. **Not chosen.** Release notes alone (option 2) leave every owner to find
   and fix each Container by hand. Holding the update (option 3) leaves the
   production backup reaching the development stack's tables, the bug #1267
   fixed. A Standard that ships migration notes for the operator to copy into
   the issue may come later, when a second breaking check wants more than its
   message can say.

## Steps

Each step is a GitHub issue that names #1447 and lands on its own with
`make check`.

| Step | Issue | Depends on | Done when |
| --- | --- | --- | --- |
| 1. This plan and the design change | #1447 (`Refs`) | | landed in `docs/plans/` and `docs/design.md` |
| 2. The backup-selection fault names the fix | #1490 | | `aws-standard` patch release whose `selects by ListOfTags` and `is none of the stack's tables` faults name `BackupResource.fromArn(table.tableArn)` in `infra/backups.ts`; the Standard's test of its own rule asserts the message; the Template ref bumped |
| 3. A failed update runs again when main moves | #1489 | | `model.NextStandardStep` drops an update at `ChecksFailed` whose base (main's head when it was planned, recorded in a new `status.standard.update.base`) is no longer main's head, and the next pass plans it from the new main; `internal/model` table cases (#1447) for main moved and main put; envtest on the faked git provider: a failed update, a commit to main, a new pull request |
| 4. A failed update files an issue for the Crew | #1491 | #1489 | on `ChecksFailed` the operator files decision 2's issue once per target ref, recorded in `status.standard`; title and body built and table-tested in `internal/model`; envtest: one issue after two passes and a re-plan, a second one for a newer ref |
