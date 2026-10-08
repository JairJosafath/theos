# ts-standard (Standard)

How a TypeScript VS Code extension, React web app or Node command-line
program in a C4 Factory Container is written, checked and reviewed: typescript-eslint's strictest configs, Prettier, the agent
guide and role guides, offline style digests, issue and pull request templates,
and the `make check` contract CI runs.

- **Supports:** `extension`, `static-site` and `cli`; Frameworks:
  `vscode-extension`, `react-vite` and `node-cli`
- **Per Framework:** every file but the workflow, `.editorconfig` and
  `.prettierrc.json` renders `.Framework`: the lint rules,
  the `Makefile`'s build and release targets, `AGENTS.md`'s review rules and
  the role guides' notes. `docs/style/vscode.md` is written only for
  `vscode-extension`, `docs/style/react.md` only for `react-vite`,
  `docs/style/node.md` only for `node-cli`. What a `vscode-extension`
  Container gets is the same as in v1.1.3, what a `react-vite` one gets the
  same as in v1.2.0.
- **Writes:** `eslint.config.mjs`, `.prettierrc.json`, `.prettierignore`,
  `.editorconfig`, `Makefile`, `AGENTS.md`, `docs/style/`,
  `docs/agents/implementer.md`, `docs/agents/reviewer.md`,
  `.gitea/workflows/check.yaml`, `.gitea/ISSUE_TEMPLATE/task.md`,
  `.gitea/PULL_REQUEST_TEMPLATE.md`
- **Lints:** ESLint 10 with `@eslint/js` recommended, typescript-eslint
  `strictTypeChecked` and `stylisticTypeChecked` (typed, through the project
  service), plus exhaustive switches, explicit module boundary types,
  `prefer-readonly`, sort compare functions, `no-console`, `eqeqeq` and
  `curly`; unused disable comments are errors. For `react-vite`,
  `eslint-plugin-react-hooks`' recommended preset too (the Rules of Hooks and
  the React Compiler's checks of the Rules of React), with `exhaustive-deps`
  an error, and Node's globals and `node:` imports refused in `src/`,
  which runs in the browser. For `node-cli`, `process.exit` refused, so
  output flushes. Prettier formats everything but
  the platform's files and the lockfile.
- **Check contract:** `make check` = `npm ci`, `prettier --check`,
  `eslint --max-warnings 0`, `tsc --noEmit`, `vitest run`, and `vsce package`
  (`vscode-extension`), `vite build` (`react-vite`) or `npm pack` and a run
  of the packed program's `--version` (`node-cli`) into a temporary
  directory, so a packaging or build problem fails CI.
  `.gitea/workflows/check.yaml` runs it on pull requests and on `main` on a
  runner labelled `go`, under the status context `check / check`, which the
  protected `main` requires (`model.CheckContexts`). npm's cache goes to the
  runner's memory-backed `/tmp`.
  A change to only `CHANGELOG.md`, `docs/` or images (`.png`, `.jpg`, `.gif`,
  `.svg`) skips the steps after the checkout and passes, so the context still
  reports; any other file, `README.md` included, or a first push runs them.
- **Releases:** `make dist VERSION=v1.2.3` packages
  `dist/<container>-1.2.3.vsix` and fails when the tag doesn't match
  `package.json`'s `version`; gitea-release attaches it to the release. For
  `react-vite`, `make dist` builds the site into `dist/`, which a Deployment
  ships. For `node-cli`, `make dist VERSION=v1.2.3` packs
  `dist/<container>-1.2.3.tgz` with `npm pack`, with the same tag check;
  `npm install --global` installs it.
- **Framework contract:** the Framework owns `package.json`, which must list
  this Standard's tools with exact versions (`eslint`, `@eslint/js`,
  `typescript-eslint`, `prettier`, `typescript`, `vitest`, `@vscode/vsce`) and a
  `vscode:prepublish` script that writes the production bundle; it also owns
  `tsconfig.json` (with `vitest.config.mts` in its `include`) and
  `esbuild.mjs`. vscode-extension does all of this. react-vite lists `eslint`,
  `@eslint/js`, `typescript-eslint`, `eslint-plugin-react-hooks`, `prettier`,
  `typescript`, `vitest` and `vite`, and owns `tsconfig.json` (with
  `vite.config.ts` in its `include`) and `vite.config.ts`. node-cli lists
  `eslint`, `@eslint/js`, `typescript-eslint`, `prettier`, `typescript`,
  `vitest` and `esbuild`, a `prepack` script that writes the bundle `bin`
  points at, and owns `tsconfig.json` (with `vitest.config.mts` in its
  `include`), `vitest.config.mts` and `esbuild.mjs`.
- **Role guides:** `docs/agents/implementer.md` and `docs/agents/reviewer.md`,
  which the factory gives its implementer and reviewer Agents, read from `main`.
- **Issues:** `.gitea/ISSUE_TEMPLATE/task.md` has the sections the factory needs;
  an issue without an unchecked item under "Acceptance criteria" is never picked up.
- **Style digests:** `docs/style/` distils the Google TypeScript Style Guide, the
  TypeScript handbook's do's and don'ts, typescript-eslint, plus cross-language
  principles; for `vscode-extension`, VS Code's extension and UX guidelines,
  activation events, bundling, the language server guide and LSP 3.17; for
  `react-vite`, the Rules of React, react.dev's guides on state and effects,
  Vite's guides, WCAG 2.2 and Testing Library; for `node-cli`, Node's `util`,
  `process` and `fs` docs, the Command Line Interface Guidelines, GNU's
  command-line standards, npm's `package.json` and `npm pack`, esbuild's API
  and NO_COLOR. Every rule is marked `[lint]` or
  `[review]`; `docs/style/SOURCES.md` lists the sources with dates.
- **Protect:** list `AGENTS.md`, `Makefile`, `eslint.config.mjs`,
  `.prettierrc.json`, `.prettierignore`, `.editorconfig`, `docs/style/` and
  `docs/agents/` in the catalog entry's `protectedPaths`, plus the Framework's
  `tsconfig.json` and `vitest.config.mts` (`vite.config.ts` for `react-vite`),
  which decide what the type check and
  tests cover; `.gitea/` is always protected.

## How a template works

Everything in this repository is copied into the new Container repository at the
same path, except `.c4factory/`, which is input for the factory. Files ending in
`.tmpl` are rendered with Go `text/template` (missing keys are errors) and lose the
suffix. Other files, such as the workflow with its `${{ }}` expressions, are copied
byte for byte.
