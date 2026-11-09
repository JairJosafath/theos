# Contributing

theos is built by people and by agents, under the same rules. This page takes
you from an idea or a fork to a merged pull request. Everyone follows the
[Code of Conduct](CODE_OF_CONDUCT.md); [GOVERNANCE.md](GOVERNANCE.md) says who
decides what.

## Ask for something or report a problem

[Open an issue](https://github.com/JairJosafath/theos/issues/new/choose) with
the form that fits: a bug, a feature or improvement, or a catalog template.
Say what happened and what you expected, or what you want and why. You don't
need to say how to build it: designing is our job. You are asked to explain
only when what you want is unclear, and the issue says what happens to it.

Vulnerabilities go to [SECURITY.md](SECURITY.md), never to an issue.

Not sure whether theos does what you need?
[docs/comparison.md](docs/comparison.md) says what it does and doesn't do yet.

## Change something

1. **Start from an issue.** Pick an open one, or file yours first, so the
   design is agreed before the code is written. A typo or a broken link
   needs no issue.
2. **Fork and set up.** `./dev setup --lite` downloads Go, Node and the other
   tools into `.dev/` in the checkout and needs no Docker and no cluster
   ([DEVELOPMENT.md](DEVELOPMENT.md)). Linux and WSL work today; macOS is
   #54.
3. **Read the rules.** [AGENTS.md](AGENTS.md) is short and holds for people
   too: the layering, the files that are generated and never edited, and
   that [docs/design.md](docs/design.md) is the spec. A change in behaviour
   changes the design in the same pull request. Work that spans more than
   one package starts with a short plan in `docs/plans/`.
4. **Branch** from an up-to-date main: `<type>/<N>-<slug>`, such as
   `fix/123-crew-stalls`.
5. **Commit** as [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/)
   with the issue: `fix(#123): a Crew with no reviewer says so`. The types
   are `feat`, `fix`, `docs`, `test`, `chore`, `style` and `refactor`.
   Before 1.0 a `feat` bumps the minor version and a `fix` the patch
   ([ROADMAP.md](ROADMAP.md)).
6. **Run `make check`** before you push. It regenerates the CRDs and the
   DeepCopy code, formats, vets and lints, runs the unit and envtest suites,
   validates the YAML and runs the UI's checks. No cluster is needed, and
   nobody is asked to run one: `make e2e` is a maintainer's tool.
7. **Open one small pull request to main per issue.** The template asks for
   `Closes #N`, what changed, the evidence (the end of `make check`'s
   output) and what you didn't test. Report honestly: a failed check or a
   skipped step goes in the pull request, with the output.

## What happens to a pull request

- The checks in [.github/required-checks](.github/required-checks) run. A
  pull request from a fork waits for a maintainer to let its first run
  start.
- [CODEOWNERS](.github/CODEOWNERS) asks the owner for a review. We aim for a first
  answer within 5 business days: approved, changes asked for with the
  reason, or declined with the reason.
- A change to the fence (what agents can reach and change; CODEOWNERS lists
  the paths) is the owner's to approve, and takes longer.
- It merges with a merge commit once the checks pass and it is approved.
  Fixes ship as a patch release soon after ([ROADMAP.md](ROADMAP.md),
  *Releases are a train*).

## Tests and warnings

- New behaviour comes with tests, and a fixed bug with the test that would
  have caught it, naming the issue (`#123`) in the spec or table case. Test
  each rule at the lowest tier that can see it ([AGENTS.md](AGENTS.md),
  *Test the rules where they live*).
- `make check` fails on a warning, so fix the warning instead of silencing
  it; a `//nolint` says why on the same line.
- [docs/openssf.md](docs/openssf.md) checks the project against the
  OpenSSF Best Practices passing criteria.

## Agents

An agent working an issue, in the factory's own Crew or in a session a person
starts, follows this page and [AGENTS.md](AGENTS.md). Its commits say which
agent wrote them, in a trailer. It never merges its own work on its own word,
never touches a cluster, and never changes the paths CODEOWNERS marks as the
fence.

## License

theos is licensed under the [Apache License 2.0](LICENSE). What you
contribute is licensed the same way: inbound is outbound, as section 5 of
the license says, and there is no contributor agreement to sign. Sign your
commits off (`git commit -s`) to say you have the right to contribute them,
the [Developer Certificate of Origin](https://developercertificate.org/).
Every new Go file starts with the header in `hack/boilerplate.go.txt`, with
the year for `YEAR`; `make check` fails on one without it. The files under
`templates/` become other people's applications and carry the license their
Project chooses.
