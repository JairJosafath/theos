---
title: Map positions inside YAML scalars
labels: status/ready, kind/feature
milestone: M1 Diagnostics
---
## Context

CEL reports errors at an offset in the expression text, such as column 12 of
`object.spec.replica <= 5`. In YAML that text may be a block scalar, a folded
scalar or a quoted string with escapes, so offset 12 of the value is not simply
column 12 of the line. Errors must land on the right character in the editor
(see the system's needs).

## Goal

A package (suggested: `internal/position`) that, given the file bytes and a
scalar `yaml.Node`, maps every offset in the decoded value back to a byte offset
in the file; and a line index that turns a byte offset into an LSP position
(zero-based line, and character in UTF-16 code units, as LSP 3.17 requires) and
back.

## Acceptance criteria

- [ ] Plain, single-quoted, double-quoted, literal (`|`, `|-`, `|+`, with and without indentation indicators) and folded (`>`, `>-`) scalars map correctly.
- [ ] Round trip: for every offset of the decoded value, the mapped file offset holds the same character, except where YAML changed it: an escape (`\n`, `\"`, `é`, `''`) maps to the escape's first byte, and a folded line break maps to the break.
- [ ] Table tests per style, plus a property test that generates expressions, writes them in each style and checks the round trip.
- [ ] The line index converts byte offsets to UTF-16 positions and back, tested with non-ASCII text (`é`, `日本`, an emoji) before and inside an expression.
- [ ] Mapping a value costs no more than a pass over the scalar's source lines (no per-offset rescans); a benchmark shows it.

## Out of scope

Finding the expressions (issue "Find the CEL expressions in Kubernetes YAML");
what cel-go's error locations count in (issue "Check ValidatingAdmissionPolicy
expressions" deals with that).

## Notes

- `yaml.Node` gives the scalar's start line and column and its `Style`; the
  source span of a block scalar runs to the last line indented deeper than its
  key.
- YAML spec 1.2, chapter 8 (block scalars) and 7.3 (flow scalars) define folding
  and escapes.
