# flutter-standard (Standard)

How Flutter code in a C4 Factory Container is written and checked: strict lints,
the agent guide, offline style digests and the `make check` contract CI runs.

- **Supports:** `mobile-app`; Frameworks: `flutter-app`
- **Writes:** `analysis_options.yaml`, `AGENTS.md`, `docs/style/`, `Makefile`,
  `.editorconfig`, `.gitea/workflows/check.yaml`
- **Lints:** `very_good_analysis` 11.0.0 plus strict-casts, strict-inference and
  strict-raw-types; `flutter analyze --fatal-infos` makes every finding fail. The
  Framework must list `very_good_analysis` in `dev_dependencies`.
- **Check contract:** `make check` = `dart format` check, `flutter analyze`,
  `flutter test`. `.gitea/workflows/check.yaml` runs it on pull requests and on
  `main` on a runner labelled `flutter`, under the status context `check / check`,
  which the protected `main` requires (`model.CheckContexts`).
- **Style digests:** `docs/style/` distils the LLVM, Chromium, Google, Mozilla,
  Linux kernel, C++ Core, Rust API and GNU guides into cross-language principles,
  plus Effective Dart, the Flutter style and architecture guides, Android core app
  quality and Flutter accessibility. Every rule is marked `[lint]` or `[review]`;
  `docs/style/SOURCES.md` lists the sources with dates.
- **App values stay out:** the Standard doesn't decide whether an app is offline;
  the SoftwareSystem's needs, which agents get with every Task, do.

## How a template works

Everything in this repository is copied into the new Container repository at the
same path, except `.c4factory/`, which is input for the factory. Files ending in
`.tmpl` are rendered with Go `text/template` (missing keys are errors) and lose the
suffix. Other files, such as the workflow with its `${{ }}` expressions, are copied
byte for byte.
