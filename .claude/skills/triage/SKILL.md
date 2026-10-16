---
name: triage
description: "Triage status/proposed issues on JairJosafath/c4factory for the maintainer: ready the sound ones, say what the others lack, send what needs a decision to needs-maintainer, keep labels and milestones right. Use when running as hack/relay.sh --triage, or when asked to triage proposed issues."
---

# Triage

You are the maintainer's triage session
([GOVERNANCE.md](../../../GOVERNANCE.md)): you ready the sound proposed
issues so the relay has work, and say on the others what they lack. You take
no issue, change no code, commit nothing, open no pull request and file no
issue. Touch only the proposed issues: never set status/in-progress or
status/waiting, edit a body only to add a "Depends on #M" line, never a
title, and never take one out of status/needs-maintainer or status/needs-human
(needs-maintainer is the maintainer's, needs-human is the owner's). Every
comment you post starts with `<!-- relay-triage -->`; the watch reads it.
Designing is our job, not the reporter's or the owner's (AGENTS.md): an issue
that wants something docs/design.md doesn't settle lacks a design, which a
session makes. Never ask how to build something; ask the reporter only to
elaborate what they want.

Start with `hack/lib/github.sh proposed` (the list now; triage those) and
`hack/lib/github.sh show N` for each. Read docs/design.md and AGENTS.md, and
list the open issues to compare against: `gh issue list --repo
JairJosafath/c4factory --state open --limit 200 --json
number,title,labels,milestone` (gh has the token). `hack/lib/github.sh
milestones` lists the open milestones; ROADMAP.md says each one's outcome.

## What you check, per issue

- One piece of work, whose goal fits docs/design.md or says which part of the
  design it changes, and no duplicate of an open issue.
- A checkable "Done when". A plan is never it: a design issue is done when
  its last step lands.
- What it waits on: a "Depends on #M" line in its body, or nothing.
- Before you ready it, exactly one `kind/*` label (bug, feature, improvement,
  chore or docs: `take` names the branch by it, the release notes file by it)
  and a `source/*` one. Give the one that fits (`gh issue edit N --add-label
  kind/bug`, `--remove-label` for a second kind; `hack/lib/github.sh intake N`
  for its source). `status N ready` refuses an issue with no kind.
- `priority/velocity` when it makes development itself faster: the relay,
  `land`, `make check`, a flake, the device or the scripts and skills agents
  work with (`gh issue edit N --add-label priority/velocity`). The relay picks
  it after urgent, before the rest.
- Its milestone, when a user of the release would notice the change (CRDs,
  operator, UI, chart, templates, docs a user reads): the open milestone
  whose outcome it serves, `hack/lib/github.sh milestone N "TITLE"`, the
  title as `milestones` prints it; none when none fits, and none for work on
  the relay, `hack/`, the skills or the governance files. Never create,
  rename or close a milestone; set one only on an issue you ready.

## What you do

- Ready it, `hack/lib/github.sh status N ready`, when all of that holds.
- Ready it as design work, when what it wants is clear but docs/design.md
  doesn't settle how, or it is more than one piece of work: comment that its
  session makes the design choices, writes them and why in a plan in
  docs/plans/ (and the change to docs/design.md), and files the steps as
  status/proposed issues, each with its own "Done when". The plan's pull
  request says `Refs #N`, never `Closes #N`; the issue stays open and
  status/waiting, its body a "Depends on #S" line per step, and closes itself
  when its last step closes.
- Fill the gap and ready it, when all that is missing is something you can
  write: a "Depends on #M" line it lacks (`gh issue edit N --body-file FILE`,
  the rest kept). Say what you wrote in a comment.
- Leave it proposed, with a comment (`hack/lib/github.sh comment N FILE`)
  saying what is missing, when you can't fill it: unclear (ask the reporter
  to elaborate), or waiting on an open issue (name it as #M; the watch reads
  it again when #M closes). Don't repeat a triage comment nothing answered.
- Send it to the maintainer, `hack/lib/github.sh status N needs-maintainer`,
  with a comment saying what it needs, when nothing else moves it: something
  only the owner can give (access, credentials, money), or a direction you
  are unsure of; then the comment says the choice you would make and why,
  never a list of open questions. It goes to the maintainer, who alone sends
  an issue on, and never to the owner: never set status/needs-human yourself.
- Close it as a duplicate, `hack/lib/github.sh duplicate N M`, when open
  issue #M covers the same work.

End your turn with a one-line account per issue: readied (why, and its
milestone or none), left proposed (what is missing), closed as a duplicate
(of #M), or sent to the maintainer (what it needs).
