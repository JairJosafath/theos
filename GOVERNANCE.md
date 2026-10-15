# Governance

C4 Factory has one person, the owner, and a team of agents that do the work.
This document says who decides what. Agents use it to run the project day to
day, so the owner only steps in when they choose to. It adapts the CNCF
project template's
[maintainer governance](https://github.com/cncf/project-template/blob/main/GOVERNANCE-maintainer.md)
to a single owner, as
[Minimum Viable Governance](https://github.com/github/MVG) does. Everyone in
the project, people and agents alike, follows the
[Code of Conduct](CODE_OF_CONDUCT.md).

## Roles

| Role | Who | Does |
| --- | --- | --- |
| Owner | one person, listed in [MAINTAINERS.md](MAINTAINERS.md); the user in a session, the human where a rule is about a cluster | Sets the project's direction, holds everything [reserved](#reserved-to-the-owner) below, and has the final say on any decision. |
| Maintainer | the relay's maintainer session (`hack/relay.sh --maintain`), one at a time | Runs the project for the owner: escalations, triage, decisions the design leaves open, review of merged work, reporters, hygiene and releases. It decides; it doesn't build. Triage is included: a triage session (`hack/relay.sh --triage`, the `triage` skill) readies sound proposed issues within minutes of their filing, on the maintainer's behalf. |
| Agents | relay sessions, and sessions the owner starts | Build: one issue each, as [AGENTS.md](AGENTS.md) says. They escalate to the maintainer. |
| Reporters | anyone who files an issue (`source/user`) | Say what they want. They get an answer and are kept informed. |

## What the maintainer decides

The maintainer decides these without asking the owner, following the
[`maintainer` skill](.claude/skills/maintainer/SKILL.md):

- **Design and direction**, within the project's goals and
  [docs/design.md](docs/design.md). It chooses an approach, records it with
  its reasons on the issue, and readies the issue. The session that builds it
  writes the plan or the change to the design.
- **Triage** (the `triage` skill): status, kind, priority, duplicates,
  splitting work, and closing an issue the project won't do, with the reason.
- **Corrections:** when an agent breaks a rule, the maintainer marks the wrong
  comment outdated, posts the right one and restores the right status. When a
  rule, prompt or doc caused the mistake, it files the fix as an issue.
- **Reporters:** it answers them, keeps them informed, and asks them only to
  explain what they want, never how to build it.
- **Review:** of merged pull requests. Each defect it finds becomes an issue.
- **Releases:** tagging, release notes and checking the release, as
  [docs/dev/testenv.md](docs/dev/testenv.md#checking-a-release)
  says.

The maintainer changes no code or docs and commits nothing. Changes go through
issues that the relay works. The one exception is a release's version bump:
the maintainer runs `hack/release.sh`, which makes and merges only that
pull request and tags it.

## Reserved to the owner

Only the owner decides these. The maintainer brings each to the owner with the
choice it would make and why. The list is in the *Needs you* section of the
pinned *Maintainer report* issue, which is the one place the owner reads.

- Credentials, access, accounts and secrets.
- What agents may run: the permission rules in `.claude/settings.json`. Today
  they allow exactly `hack/lib/github.sh land` and `hack/release.sh`, the two
  commands the owner already gave the relay, and no agent widens them.
- Money: anything that costs more, such as more agents, a bigger model or a
  paid service.
- Licensing, trademarks, this governance and the Code of Conduct.
- Irreversible acts: deleting a repository or a branch someone needs,
  rewriting main's history, force-pushing, and changing a repository's
  settings or visibility.
- Code of Conduct reports and security reports, as
  [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) and [SECURITY.md](SECURITY.md) say.
- Reversing a decision the owner made.
- A direction the maintainer is unsure of. It still sends the choice it would
  make.

## How decisions are made

- **Openly, with reasons, where the work is:** on the issue. A design
  decision ends up in a plan in `docs/plans/` or a change to
  `docs/design.md`.
- **The design doc is the spec** ([AGENTS.md](AGENTS.md)). A decision that
  changes it says so.
- **The owner's word wins.** An owner comment on an issue is binding: one
  without a relay marker such as `<!-- relay-maintainer -->` or an
  `Agent: name` line. The maintainer and agents follow the owner's newest word
  and never reverse it.
- **No votes.** With one person, lazy consensus becomes: the maintainer
  decides and records it, and the owner may overrule at any time.

## Escalation

Agents escalate to the maintainer, and only the maintainer escalates to the
owner:

1. An agent stuck on something it may not decide sets
   `status/needs-maintainer` and says what it needs.
2. The maintainer decides it and moves the issue on. If the item is reserved
   to the owner, it sets `status/needs-human` instead, with its recommendation,
   and lists the item under *Needs you*.
3. The owner answers on the issue whenever they choose.

An agent never sends the owner a list of open questions. Designing is our job
([AGENTS.md](AGENTS.md)).

## The owner stays in control

- Overrule any decision by commenting on its issue.
- Stop the maintainer with `WATCH_MAINTAIN=0`, or stop the whole relay with
  `touch .relay/stop`; resume it by starting the watch again
  (`hack/relay-watch.sh`), which removes `.relay/stop`
  ([docs/dev/testenv.md](docs/dev/testenv.md)).
- Read the pinned *Maintainer report* when they choose: its *Updated* line says
  when the maintainer last swept. The relay never waits on the owner.
- Only the owner changes this document, [MAINTAINERS.md](MAINTAINERS.md) and
  the Code of Conduct. The maintainer may propose a change under *Needs you*.
