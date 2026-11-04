# Contributing

Work on theos runs through GitHub issues. Agents work them through the
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
- [ROADMAP.md](ROADMAP.md) says how releases go out, which outcomes the
  milestones track, and what 1.0 means.

## Tests and warnings

- New behaviour comes with tests, and a fixed bug with the test that would
  have caught it. Test each rule at the lowest tier that can see it
  ([AGENTS.md](AGENTS.md), *Test the rules where they live*).
- `make check` runs the tests, `go vet`, golangci-lint and the UI's checks. A
  warning fails it, so fix the warning instead of silencing it; a
  `//nolint` says why on the same line.
- [docs/openssf.md](docs/openssf.md) checks the project against the
  OpenSSF Best Practices passing criteria.

## License

theos is proprietary; all rights are reserved ([LICENSE](LICENSE)). By
contributing, you agree that the owner may use, license and relicense your
contribution under any terms, including an open-source license later. Every
new Go file starts with the header in `hack/boilerplate.go.txt`.
