# Sources

Every source read to write `docs/style/`. The TypeScript, VS Code and LSP
sources were read on 2026-10-02. The cross-language sources were read on
2026-09-30 for another Standard, whose `principles.md` this one adapts; they were
not read again. Version or date as shown on the page; "live" when the page shows
none.

## TypeScript

| Source                                                                                                                     | Version / date                                                             |
| -------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------- |
| Google TypeScript Style Guide — https://google.github.io/styleguide/tsguide.html                                           | live                                                                       |
| TypeScript handbook, Do's and Don'ts — https://www.typescriptlang.org/docs/handbook/declaration-files/do-s-and-don-ts.html | live                                                                       |
| typescript-eslint shared configs — https://typescript-eslint.io/users/configs/                                             | live; `strict-type-checked` is not semver-stable, so the version is pinned |

## VS Code and LSP

| Source                                                                                                                                                            | Version / date                                     |
| ----------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------- |
| UX guidelines overview — https://code.visualstudio.com/api/ux-guidelines/overview (linked from https://code.visualstudio.com/api/references/extension-guidelines) | live                                               |
| UX guidelines, notifications — https://code.visualstudio.com/api/ux-guidelines/notifications                                                                      | live                                               |
| UX guidelines, status bar — https://code.visualstudio.com/api/ux-guidelines/status-bar                                                                            | live                                               |
| UX guidelines, settings — https://code.visualstudio.com/api/ux-guidelines/settings                                                                                | live                                               |
| UX guidelines, command palette — https://code.visualstudio.com/api/ux-guidelines/command-palette                                                                  | live                                               |
| Activation events — https://code.visualstudio.com/api/references/activation-events                                                                                | live                                               |
| Bundling extensions — https://code.visualstudio.com/api/working-with-extensions/bundling-extension                                                                | live                                               |
| Language server extension guide — https://code.visualstudio.com/api/language-extensions/language-server-extension-guide                                           | live                                               |
| Language Server Protocol — https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/                                            | **3.17**                                           |
| vscode-languageclient — https://www.npmjs.com/package/vscode-languageclient                                                                                       | 10.1.2, `engines.vscode` ^1.91.0 (npm, 2026-10-02) |

## Tools

The versions pinned in the Framework's `package.json` and `package-lock.json`:
TypeScript 6.0.3 (typescript-eslint 8.71.0 supports `<6.1.0`, so not 7.x),
ESLint 10.11.0, @eslint/js 10.0.1, typescript-eslint 8.71.0, Prettier 3.9.9,
vitest 5.0.3, esbuild 0.28.2, @vscode/vsce 4.0.0, @types/vscode 1.100.0,
@types/node 24.19.1. Every ESLint rule this digest names was checked to be on
in the resolved config (`eslint --print-config`).

## Cross-language (read 2026-09-30)

| Source                                                                                                             | Version / date |
| ------------------------------------------------------------------------------------------------------------------ | -------------- |
| LLVM Coding Standards — https://llvm.org/docs/CodingStandards.html                                                 | live           |
| Chromium style guide index — https://chromium.googlesource.com/chromium/src/+/HEAD/styleguide/styleguide.md        | HEAD           |
| Google style guides index — https://google.github.io/styleguide/                                                   | live           |
| Mozilla C++ coding style — https://firefox-source-docs.mozilla.org/code-quality/coding-style/coding_style_cpp.html | live           |
| Linux kernel coding style — https://docs.kernel.org/process/coding-style.html                                      | live           |
| C++ Core Guidelines — https://isocpp.github.io/CppCoreGuidelines/CppCoreGuidelines                                 | Jun 14, 2026   |
| Rust API Guidelines, naming — https://rust-lang.github.io/api-guidelines/naming.html                               | live           |
| GNU Coding Standards — https://www.gnu.org/prep/standards/standards.html                                           | April 27, 2026 |
