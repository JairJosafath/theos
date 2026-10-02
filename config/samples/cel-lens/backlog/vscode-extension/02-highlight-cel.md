---
title: Highlight CEL inside YAML
labels: status/ready, kind/feature
milestone: M3 VS Code
---
## Context

In YAML, CEL is just a string: everything is the same colour. Highlighting it
makes policies readable at a glance, and works even before the server starts.

## Goal

A TextMate grammar for CEL, injected into YAML for the values of the keys that
hold CEL in Kubernetes kinds: `expression`, `messageExpression`,
`valueExpression` and `rule`.

## Acceptance criteria

- [ ] A grammar `source.cel` covers CEL's literals (strings with escapes, raw and triple-quoted strings, bytes, numbers incl. unsigned and hex, `true`, `false`, `null`), operators, `in`, field selection, function calls, macros (`has`, `all`, `exists`, `exists_one`, `map`, `filter`) and `//` line comments.
- [ ] An injection grammar applies `source.cel` to plain, quoted, literal (`|`) and folded (`>`) values of the four keys, and nowhere else; other YAML highlighting stays as it was.
- [ ] Grammar tests with `vscode-tmgrammar-test` cover each kind of token and each scalar style; they run in `make check` through the test script.
- [ ] The README says which keys are highlighted, and that the server decides what really is CEL.

## Out of scope

Semantic tokens from the server.

## Notes

- CEL's syntax: https://github.com/google/cel-spec/blob/master/doc/langdef.md#syntax
- Injection grammars: https://code.visualstudio.com/api/language-extensions/syntax-highlight-guide#injection-grammars
