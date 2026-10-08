# A release whose tag carries later work goes out anyway

**Goal:** One handed-back issue no longer turns every later release into a tag
a person makes. When a done release's tag would carry a later release's merged
work, the Crew tags it anyway and moves that work into its milestone, so
milestones match what each tag holds and the later releases go on by
themselves. GitHub issue #603; the maintainer decided the direction there.

## Why it chained

An issue handed back to a person doesn't hold its release, so the Crew went on
with the next release's work. When the handed-back issue was settled and its
release prepared, the release's newest merge came after the later release's
merges, and the Crew left the tag to a person (`HoldsLaterWork`). The next
release could then only be prepared after that tag, by which time the release
after it had merged work too, and so on.

## The rule

- **Tag anyway.** A done release is tagged at its newest merge even when a later
  release's issue merged before it. `HoldsLaterWork` is gone.
- **Move what the tag carries.** Each closed issue of a later release whose
  newest merge by a Task came before the tagged one moves into the tagged
  release's milestone, with a comment on it naming both releases and the tag.
  An open issue stays where it is: its work isn't done, and its release carries
  the rest. A later release's `kind/release` issue stays too.
- **The tag comes first.** The Crew tags, moves the issues, closes the later
  milestones left empty, and closes the release's milestone last. A poll that
  fails half way finds the tag at the commit and moves only the rest; a tag
  already there (made on a failed poll, or by a person) still moves them before
  the milestone closes. A tag at another commit is still a person's
  (`TagClash`), and moves nothing. A comment goes before its move, so a failed
  one is made again (a repeat at worst). Known limit: a failure between a later
  release's last move and closing its milestone, right after it, leaves that
  release empty, and a person closes it (`NoMergedIssue` says so).
- **A later release left with no issue closes.** When every issue of a later
  release moved, its milestone closes untagged with the tag, and its version is
  skipped: nothing is left for it to hold, and an empty release would wait for a
  person (`NoMergedIssue`) and hold every release after it. A milestone that was
  empty before (made ahead of its issues) is left alone.
- **The changelog says so.** The prepare issue lists the later releases' closed
  issues already merged under the release it prepares, so its changelog covers
  what the tag will carry. Later work that merges while the prepare issue is
  open is carried and moved too, but its changelog may miss it, as it was
  written from main before; the moved issue's comment says only that the tag
  carries it.
- **Reporting.** The `Released` Event and condition say which issues moved from
  which release, and which releases closed empty; each move is the log line
  `Moved an issue into the release`.

## Steps

One pull request: the design change, `internal/model` (`NextRelease`,
`ReleaseStep.Moved` and `Emptied`), and the Crew's release step.

## Done when

- An `internal/model` table test: `NextRelease` for a done release whose newest
  merge came after a later release's merges says tag, with the later issues to
  move, instead of `HoldsLaterWork`; open issues stay; a release left empty
  closes; a tag already at the commit still moves.
- An envtest: the Crew tags v0.1.3 there, moves v0.2.0's merged issues into
  v0.1.3's milestone with a comment each, and later prepares and tags v0.2.0
  without a person.
- `make check` passes.
