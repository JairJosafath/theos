# A template in a subdirectory of its repository (#35)

**Goal.** A catalog entry's `template` can name a directory of its
repository, `template.path`, so a Domain (or a Framework, Standard, ...) can
live in a product's own repository beside its model and `CLAUDE.md`, without
a repository of its own and without the files beside it reaching the
template.

## Decisions

1. **`path` is on `TemplateSource`,** so every catalog kind with a template
   (Framework, Standard, Compute, Deployment, Connector, Domain) takes it.
   Optional, empty is the root, so every existing entry is unchanged.
2. **One spelling per directory.** CEL refuses an absolute path, a `.` or
   `..` segment, and a trailing or doubled `/`. A name that only starts
   with dots (`..hidden`) is a name, and allowed.
3. **The fetch reads the subtree.** `FetchTemplate` takes the directory and
   returns its files by paths relative to it, through `scaffold.Tree.Sub`,
   which the Git adapter and the controllers' fake share. Everything after
   the fetch (rendering, the layer conflict check, the Domain checks) sees
   only the subtree, so `DomainNotMarkdown` and `DomainNoReadme` never count
   a file outside it.
4. **A missing directory is permanent.** `ports.ErrTemplatePathNotFound`,
   reason `TemplatePathNotFound`, names the repository, the path and the
   ref. It fails the scaffold and the adoption check like `UnpinnedRef`,
   stops a Standard update (its old/new renders and the renders of files
   behind), and stops an operations update, where `UnpinnedRef` is still
   retried as a Git error, as it was. Git keeps no empty directory, so a
   directory with no file below it is missing, and so is a path that names
   a file.
5. **The path is where a template lives, not a version.** A Standard update
   reads every ref at the Standard's current path. A template moved within
   its repository is a new Standard: a bump that changes the path too
   stops with `TemplatePathNotFound` at the old ref. Recording the path per
   ref in `status.standard` would let a move be a bump; nobody has asked
   for that.
6. **What is recorded stays `name@ref`.** `status.scaffoldedFrom` and
   `status.standard` don't carry the path; the UI's catalog pages show it,
   as "In directory", when it is set, and the UI explains the new reason.

## Steps

All done in one change:

- [x] `TemplateSource.path` with its CEL rules; CRDs and the chart's CRDs
  regenerated (`make manifests`).
- [x] `scaffold.Tree.Sub`, the Git adapter and the fake honour it; every
  `FetchTemplate` call passes the entry's path: the scaffold, the adoption
  check, the Standard update's renders and the operations update.
- [x] `ReasonTemplatePathNotFound`, handled where each fetch fails.
- [x] The UI's catalog and Domain pages show the path; the reason has its
  description and next step.
- [x] docs/design.md, "The model" and "Standards and the catalog".

## Done when

Tests naming #35 pass: `TestTreeSub` (tier 1), `TestFetchTemplatePath`
against a real Git repository, the envtests that scaffold a Container from a
Domain in a subdirectory of a repository with non-Markdown files and a
`CLAUDE.md`, fail one whose path the ref doesn't have, and refuse each bad
path at admission, and the UI's page and next-step tests. `make check`
passes. Evidence: [../../screenshots/35-template-path-check.txt](../../screenshots/35-template-path-check.txt).
