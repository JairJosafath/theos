# Open-source launch, and the open alternative to Warp's factory

Parent issues: #45 (the launch) and #46 (the comparison). Written 2026-11-09
in a session the owner ran, with their words for it: "Create issues to improve
theos, we are moving towards opensourcing it. Keep in mind that we want to
compete with warp.dev that also has software factories."

## Goal

Two outcomes, in this order:

1. **The repository can go public.** A person who has never seen theos gets
   from the public README to a running factory, and from a fork to a merged
   pull request, with nothing but what the repository says. Nothing in the
   tree or its history is the owner's alone, and nothing a stranger's pull
   request runs can reach the owner's machines.
2. **theos is the open choice against Warp.** Where Warp's factory is ahead
   the gap has an issue and closes; where theos leads, the README says so
   with evidence.

## Where we stand (2026-11-09, main at 0d49b3a)

The factory runs the whole loop, triage to monitor, on a Kind cluster, and
develops itself. What was found for the launch is on #45, and against Warp
on #46 and in [docs/comparison.md](../comparison.md). In short:

- Proprietary `LICENSE`; contributor docs about the retired relay; one issue
  form, for "devices"; self-hosted runners that a fork's pull request would
  use; links to the private `c4factory`; no tag or release in this
  repository; only `SHA256SUMS` signed; a first run that takes a cluster, a
  1,410-line guide and a Claude token.
- Against Warp: Gitea only, Claude Code only, Matrix only, adoption as a
  footnote, no Prometheus metrics, a person decides at the spec and the
  deploy and nowhere between, nothing runs on a timer, and "monitor" is the
  deploy's health alone.

## Decisions, and why

1. **The licence is the owner's choice; the plan recommends Apache-2.0**
   (#47 has the reasons) and changes no licence text itself. Everything else
   in the launch is written so it holds under any OSI licence.
2. **The history is published if a full scan is clean** (#52), because the
   plans and the design cite it; otherwise the public repository starts from
   one commit. Rewriting or dropping history is the owner's act.
3. **Numbers from c4factory become plain text,** `c4factory #1094`, not
   links: the old repository stays private as the archive, so a link is a
   404 for every reader but the owner (#56).
4. **A fork's pull request runs on GitHub's hosted runners, always** (#49).
   The owner's devices never run a stranger's code; the cost is a slower
   first check for outside contributors.
5. **"Device" stays in the code and the history, and leaves the pages a
   newcomer reads first** (#55): README, CONTRIBUTING and the issue forms
   say "install" or "user".
6. **GitHub as a Git host comes first among the features** (#57). It is the
   gap that decides whether a team can try theos at all, and it makes the
   host ports the first with two real adapters. Harnesses (#58) are second:
   an open factory that runs one vendor's agent is not open where it counts.
7. **The comparison is a page in the repository, with sources and a date**
   (#65), rechecked at each minor release. It claims nothing about Warp that
   Warp's own pages don't say.
8. **theos does not become a hosted product to match Warp's first-run
   time.** It closes that gap with a one-command local run on the stand-in
   harness (#53) and with GitHub in place (#57), and keeps "everything runs
   on your cluster" as the difference.

## Steps

Each step is its own issue and lands on its own with `make check`. The
owner's acts are listed under *For the owner*.

### The launch (#45)

| Step | Issue | Needs | Done when (short) |
| --- | --- | --- | --- |
| Issue forms, pull request template, CODEOWNERS | #48 | | the forms and files exist and intake still labels |
| Docs without private links and logins | #56 | | no c4factory link outside history; a check keeps it so |
| Contributor docs without the relay | #55 | | CONTRIBUTING takes a fork to a merge; ROADMAP is current |
| Fork pull requests on hosted runners | #49 | | the `runs-on` rule, checked by `hack/check-workflows.sh` |
| A release from this repository | #50 | | a tag, the images, the chart and the GitHub release |
| The licence | #47 | the owner | the text, the headers, the manifests |
| History and secrets scan | #52 | | the report, the list for the owner |
| Signed images, SBOM, provenance, scanning | #51 | #50 | `cosign verify` in the guide; Scorecard, CodeQL, updates |
| A first run in ten minutes | #53 | #50 | `hack/quickstart.sh`; README's first screen |
| macOS for `./dev` | #54 | | `./dev setup --lite` and `make check` on a Mac |

### The comparison (#46)

| Step | Issue | Needs | Done when (short) |
| --- | --- | --- | --- |
| docs/comparison.md | #65 | | the page, linked from README |
| Prometheus metrics and a dashboard | #59 | | the series, the chart's dashboard |
| GitHub as a Git host | #57 | | the adapter passes the host contracts |
| Harnesses and models | #58 | the owner (protectedPaths) | the interface, Codex, Gemini CLI, OpenCode |
| Approval rules on a Crew | #62 | | `spec.approval`, a person's review merges |
| Recurring Tasks | #63 | | `spec.schedules` files issues on time |
| Bring your own repository | #60 | better with #57 | `adopt` proposes a model; no Standard needed |
| Slack, Linear, Jira | #61 | | a second ChatHost, two more IssueTrackers |
| Alerts file bugs | #64 | | `AlertSource`, Alertmanager and Sentry |
| The factory's own numbers, published | #66 | #59 helps | the report with each release |

## For the owner

Only the owner does these (GOVERNANCE.md, "Reserved to the owner"). The
choice we would make is with each.

1. **Name the licence** on #47. Apache-2.0.
2. **Read the scan's report** on #52 and choose: publish the history, or a
   fresh root commit.
3. **Make the repository public, then the GHCR packages** (each package has
   its own visibility).
4. **Settings when public:** private vulnerability reporting on;
   Discussions on, for questions; Actions' "Require approval for all outside
   collaborators"; the main ruleset's required checks as
   `.github/required-checks` lists them.
5. **Register on bestpractices.dev** (docs/openssf.md, "For the owner").
6. **`GOVERNANCE.md`:** the text proposed on #55.
7. **Go-ahead for the protected paths** in #58 (`internal/agentrunner/`,
   `internal/modelproxy/`).

## Done when

Every step's issue is closed or decided against with the reason on it, the
repository is public under an OSI licence with a release a stranger
installed from README's first screen, and [docs/comparison.md](../comparison.md)
has no "not yet" in theos's column for something Warp ships.
