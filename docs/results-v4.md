# Results: issues, crews and roles (v4)

What happened when agents built CEL Lens on C4 Factory from a backlog of Gitea
issues, what it taught us about the factory, and what changed because of it. The
design is [superpowers/specs/2026-10-02-issue-driven-factory-design.md](superpowers/specs/2026-10-02-issue-driven-factory-design.md);
evidence is in `docs/screenshots/v4-*`.

## The product, and why it suits agents

CEL Lens gives CEL inside Kubernetes YAML the support a language deserves: the
errors the API server would report, at the right line and column, completion and
hover, offline. It is two Containers joined by LSP: a Go language server (also a
CI linter) and a VS Code extension. Unlike v2's mobile apps, every behaviour can
be tested headless: Go tests against Kubernetes' own CEL libraries, protocol
tests, grammar tests and a packaging check.

Before writing issues, a spike checked the risky part: Kubernetes' own type
checker (`validating.TypeChecker`) checks a policy against Deployment's schema
from a local OpenAPI file, offline, with the API server's messages and positions;
completion works by compiling the receiver and listing its type's fields. The
issues carry those findings as notes.

## The issue loop, proven with stand-ins

A throwaway Container with a stand-in Crew ran the whole loop in Kind
([evidence](screenshots/v4-issue-loop.txt)): the Crew handed an issue without
acceptance criteria back with `status/needs-human`, started the ready one, waited
for the dependent one until the first closed, and each Task went through two
rounds (the stand-in asks for changes once). Gitea shows it as a person would
follow it: the issue labelled in progress and assigned to the agent bot, a
comment naming the Agents, a pull request by the agent that closes the issue, a
"request changes" review and an approval naming the reviewer Agent, and the
issue closed with a comment.

## Platform findings, and what changed

| Finding | How it showed | Change |
| --- | --- | --- |
| Every pull request comment since v2 failed: round feedback, approvals | Probing Gitea's issue API with the operator's token: "token does not have at least one of required scope(s), required=[write:issue]". The toolbox's pull requests had zero comments; the failure was only logged | The seed's tokens get `write:issue`; a reseed replaces tokens whose scopes differ |
| The operator opened pull requests as itself, so Gitea refused its reviews | The first stand-in run: "approve your own pull is not allowed", and no reviews on the pull request | Pull requests are opened with the Container's agent bot token. The fake Git host in envtest refuses self-reviews now, so the specs catch it |
| A freshly scaffolded Container never read its CI result | cel-language-server showed no checks while its run had passed in 38 s | Finishing a scaffold schedules the first read. The envtest spec used to nudge the Container with an annotation, which hid it |
| Compiling the Kubernetes CEL libraries cold takes about 7 minutes and 2.9 GB (half for `-race`); every agent sandbox starts cold | Measured in the toolchain image with a program importing the libraries | The toolchain image ships the build cache warm (image 5.8 GB). CI on a scaffolded repository took 38 s |
| A usage limit would cost a round, so a Crew would burn its rounds and hand back every issue in turn | Found while planning a run of many hours | `unavailable` results keep the round; the Task waits until the reported reset (or 15 minutes) and tries the same step again |
| Loading a 6 GB image into Kind restarted Gitea | Gitea's restart count while `make runner-go` loaded its image | Open: build and load the big images rarely, never while Tasks run |
| Local agent worktrees inside the repository broke `controller-gen` and Docker builds | I/O errors from a worktree's `node_modules` | `.claude/` is ignored by Git and every image's build context |

## The CEL Lens run

To be filled in as the Crews work through the backlog.
