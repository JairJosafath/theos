# The Crew tags a release when it is done

**Goal:** Releases reach users without a person at every release boundary: when a
release's work is merged, the operator tags it and closes its milestone. Asked for
by the user on 3 October 2026; GitHub issue #78.

**Why:** The Crews work through a release's issues on their own, but a person
still finds the last merge commit, pushes the tag as the operator's Git user,
checks the release run and closes the milestone. Sessions can't take that over
(auto mode refuses Gitea writes), and agents must not: the operator's Git user is
the only one allowed to push `v*` tags (`protectRepository`, `model.ReleaseTags`),
and a tag is the one write that reaches users without review.

## The rule

- A **release** is a milestone with a due date and a `vMAJOR.MINOR.PATCH` title.
  Releases go in due-date order, title breaking a tie, as the Crew already works
  them.
- A release is **done** when its milestone has no open issue (of any status,
  `status/needs-human` included: that release waits for a person's call) and no
  active Task works on one of its issues.
- A done release is tagged only once every earlier open release is tagged and
  closed, so tags go out in order. A later release that is done waits, and the
  Crew's message says for which release.
- The **tag commit** is the merge commit of the release's most recently merged
  issue: among the milestone's closed issues, the one whose merged Task completed
  last. Issues closed by hand, without a merged Task, don't count. A done release
  with no merged issue is left to a person, and the Crew says so.
- **Later work in the tag.** If a Task on an issue of a later release merged
  before that commit, the tag would carry the later release's work (an issue
  handed back to a person and fixed after the Crew moved on, or an urgent issue).
  The Crew leaves that release to a person and names the issue.
- **Never move a tag.** If the tag exists at the same commit, the Crew only
  closes the milestone. At another commit, it reports the clash and leaves tag
  and milestone alone.
- **Off by default.** `spec.tagReleases: true` on the Crew turns this on; a Crew
  without it behaves as today.
- **Reporting.** Each release gets an Event and the log line
  `released <tag> at <sha>`; `status.lastRelease` names the tag, the commit and
  the time. A release left to a person is a Warning Event and named in the
  Crew's message, once per release and reason.

Checked against the server's history (`docs/results-v4.md` at tag
`archive/product-coupled`): v0.2.0 was held while #99 was `needs-human`; once a
person moved #99 and #120 to v0.3.0 it was done, and its newest merge is
`694db57`, the tag the user chose. v0.3.0's tag goes at `1cedb81`, #99's merge.

## Settled questions

- **Where the merge commit comes from.** The Task records it on merge:
  `status.mergeCommit`, read from the pull request's `merge_commit_sha` after the
  squash-merge (`GetPullRequest` gains `MergeCommit`). `status.commit` is the
  branch head, not the commit on main. Standard-change Tasks, merged by
  `ForceMergePullRequest`, record it too. A Task that reached `Merged` without
  one (merged before this change, or the read failed) gets it read again on its
  next reconcile. Until it has one, its release waits and the Crew says so,
  unless a later merged Task of the same issue has one.
- **Which merge is newest.** The merged Task's `status.completedAt`; ties break
  on the Task's name. Tasks are only ordered within one Container, whose `main`
  takes squash merges one at a time, so merge order is completion order.
  A deleted Task's merge is lost to the Crew, so Merged Tasks of an open release
  must be kept until it is tagged.
- **Which issues belong to a release.** Gitea is the truth: the milestone's open
  and closed issues, read each poll, pull requests left out. A Task's own record
  of its issue's milestone would go stale when a person moves an issue.
- **How the tag is made.** Gitea's `POST /repos/{owner}/{repo}/tags` with
  `target` the commit and a message (an annotated tag), as the operator's user,
  whom the tag protection allows. It starts the release workflow like a pushed
  tag: in step 5's run on Gitea 1.27.3 the API tag queued
  `release.yaml@refs/tags/v0.1.0` with event `push`
  (`docs/screenshots/release-tags-hello.md`), so no go-git push is needed.
- **A paused Crew still tags.** `spec.paused` stops it picking up issues; a
  release whose work is already merged still goes out.
- **Warnings once.** A held release sets the Crew's `Released` condition False
  with the reason; the Crew emits its Warning only when the reason or message
  changes, and also remembers what it said, since the cached Crew can lag behind
  the status it just wrote.
- **Merge commits read late.** A Merged Task without one reads it again each
  minute for an hour after it completed, then each hour.
- **Known limit.** The Crew tags and closes before it writes its status; if that
  write fails, the milestone is closed and `status.lastRelease` misses that
  release. The Event still names it.
- **Version files.** The release job's `make release-version` refuses a tag that
  differs from `package.json`; that check stays in the release job. The Crew
  doesn't watch the release run (a follow-up, below; #116,
  `docs/plans/done/release-runs.md`).

## Steps

Each step is its own pull request to main that says `Part of #78`, and leaves
main working; tagging stays off until step 4 adds the switch, and no Crew turns
it on until step 5.

1. This plan.
2. `internal/model/release.go`: `ReleaseFacts` (open release milestones with
   their open and closed issue numbers, the Crew's Tasks with issue, completion
   time and merge commit, existing tags) and `NextRelease`, which returns the
   release to tag and its commit, the one to close only, or why the first
   release can't go out. Table tests first, including the server's v0.2.0 and
   v0.3.0 cases and every rule above.
3. `internal/gitprovider`: `PullRequest.MergeCommit`; `ListMilestones` (open
   ones, with ID, title and due date); `MilestoneIssues` (open and closed issue
   numbers, no pull requests); `GetTag` (the commit a tag names, or not found);
   `CreateTag`; `CloseMilestone`. Tests with the fake Gitea.
4. API and controllers: `TaskStatus.MergeCommit` set on merge;
   `CrewSpec.TagReleases`, `CrewStatus.LastRelease`; the Crew controller reads
   the facts when the switch is on, tags and closes, logs, records and emits
   Events. Envtest for a Crew with and without the switch. `make manifests
   generate`.
5. `docs/design.md` Crew section, next to "Releases go one at a time". `make
   check`, `make deploy`, then a test Container whose Standard has a release job,
   a milestone with one small issue, and its Crew with `tagReleases: true`.
   Evidence in `docs/screenshots/`.

## Done when

- `make check` passes.
- In the Kind cluster, the operator tags a release of a test Container whose
  Standard has a release job and closes its milestone with no person acting, and
  the release run attaches its files.
- The Crew's status, the Event and the Gitea tag are saved in
  `docs/screenshots/`.
- A Crew without the switch behaves as today.

## Out of scope: follow-ups

- The Crew watches the release run and reports a failure.
- The Crew files the "prepare vX" issue (version and changelog) for each release.
- When a Container releases, the Crew files a bump issue in the Containers that
  bundle it.
