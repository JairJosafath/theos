# Contributing

Work on C4 Factory runs through GitHub issues. Agents work them through the
relay, and the maintainer looks after the project
([GOVERNANCE.md](GOVERNANCE.md)). Everyone follows the
[Code of Conduct](CODE_OF_CONDUCT.md).

## Ask for something or report a problem

File an issue with the *user report* template
(`.github/ISSUE_TEMPLATE/user-report.yml`). Say what happened and what you
expected. You don't need to say how to build it: designing is our job. The
maintainer answers, asks you to explain only when what you want is unclear,
and keeps you informed.

Vulnerabilities go to [SECURITY.md](SECURITY.md), never to an issue.

## Work on an issue

- [AGENTS.md](AGENTS.md) has the rules. [DEVELOPMENT.md](DEVELOPMENT.md)
  sets up a device, and [docs/dev/testenv.md](docs/dev/testenv.md) covers
  tests and the relay.
- Take the issue with `hack/lib/github.sh take N`, which names you and the
  branch (`<type>/<N>-<slug>`, from an up-to-date main).
- Commits follow
  [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/) as
  `type(N): summary`, with an `Agent: name` trailer.
- Open one small pull request to main per issue, whose body says `Closes #N`
  and shows the evidence. Merge it with a merge commit once `make check`
  passes.
- Report honestly: a failed check or a skipped step goes in the pull request,
  with the output.
