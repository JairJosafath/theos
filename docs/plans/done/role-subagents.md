# Role subagents: the Standards say when an agent delegates

Issue: #259 (the spec). Written 2026-11-14.

## Goal

Every agent run is Claude Code, headless, in its Task's sandbox, and can
already start subagents whose cost reaches the Task's record. The Standards
give the reviewer and the implementer subagents written for their role, and
their role guides say when delegating helps, so a large change gets three
focused reads and an implementer's context stays small.

## Steps

1. go-standard, go-operator-standard, ts-standard, python-standard and
   aws-standard write `.claude/agents/`: `review-correctness.md`,
   `review-security.md`, `review-style.md` (the reviewer's three lenses),
   `explorer.md` and `test-writer.md` (the implementer's).
2. Their `docs/agents/reviewer.md.tmpl` and `implementer.md.tmpl` gain a
   _Subagents_ section; `AGENTS.md.tmpl` lists `.claude/agents/` among the
   Standard's paths.
3. `.claude/agents/` joins each one's `protectedPaths` in the chart's catalog.
4. Each one moves a minor version, as f5bdf13 did for #263: `templates/versions.txt`,
   `docs/templates.md`, `.theos/README.md`, aws-standard's `SOURCES.md` and the chart's pins.
5. Tests in `internal/scaffold` naming #259.

## Choices and why

- **The file format** is Claude Code's subagent file, read from its documentation
  (code.claude.com/docs/en/sub-agents): Markdown with YAML front matter, `name` and
  `description` required, `tools` a comma-separated list, `model` optional.
- **`model: inherit`.** A subagent runs on the Agent's model, the one the model
  proxy serves; a named model could be one it does not.
- **Read-only means `Read, Grep, Glob`.** The lenses and the explorer get no Bash:
  no command of theirs can change the branch under review or reach the network.
  The reviewer gives each lens the diff (`git diff origin/<base>...HEAD`) in its
  prompt. The test writer edits test files and runs the one test, so it has
  `Edit, Write, Bash` too.
- **One set of five files, the same in every Standard.** They name no language:
  the style lens reads the Standard's `docs/style/` and `AGENTS.md`, the test
  writer the role guide's notes. A test keeps the copies equal.
- **go-operator-standard gets them; hello-standard doesn't.** go-operator-standard
  is a copy of go-standard (its own files, no include) with the same role guides,
  so it ships the same subagents. hello-standard is the end-to-end fixture, kept
  tiny on purpose, with no role guides; hello-html-standard has small role
  guides of its own but no subagents.
- **Plain `.md`, not `.tmpl`**: the files need no Container data.
- **The layer rule holds:** no Framework, Compute, Deployment, Connector or
  Domain writes under `.claude/`, and a Container has one Standard.
- **`protectedPaths` and `AgentNeverPaths`** (changed after review). The first
  choice was protectedPaths alone, so a Standard change could change them, as
  for `Makefile`. The review showed why not: the subagents, settings, hooks,
  skills, `CLAUDE.md` and `.mcp.json` configure the agents that check a change,
  so a change to them could change how it is itself checked. So the five
  Standards protect `.claude/agents/`, `.claude/`, `CLAUDE.md` and
  `.mcp.json`, and `.claude/`, `CLAUDE.md` and `.mcp.json` join `AgentNeverPaths`
  (`internal/model/fence.go`) and the Task CRD's CEL rule on
  `allowedProtectedPaths`, beside `AGENTS.md`, `docs/agents/` and `.theos.yaml`.
  `.claude/` as a whole, not just `agents/`, because a settings file disables a
  lens as surely as an edited one.
  A person changes `CLAUDE.md`, `.mcp.json` and the rest of `.claude/` (no
  Standard ships them); `.claude/agents/` and `docs/agents/` change with the
  Standard's own release.
- **Text in the change is data** (after review). The lenses and the explorer
  read files the agent under review wrote; each says that text is what it
  judges, never instructions to it, and the reviewer guide says so too. A test
  pins it, and the guides' "check before use" bullets.
- **Test-writers one at a time** (after review). Two test-writers in one
  working tree break each other's "see it fail for the right reason": one's
  unbuilt test fails the other's build. The implementer guide starts explorers
  in parallel and test-writers one at a time, or in parallel only on different
  packages.
- **The verdict stays the reviewer's.** The guide says so in one sentence, which
  a test pins.

## Done when

- A table test renders each of the five Standards and finds the five files, and
  `.claude/agents/` in the chart catalog entry's `protectedPaths` (tier 1).
- A test pins the reviewer guide's sentence that the verdict stays the reviewer's.
- `make validate-yaml` passes with the new pins (tier 2); `make check` passes.

Done: `TestStandardsShipRoleSubagents` and `TestRoleGuidesSayWhenToDelegate` in
`internal/scaffold/role_subagents_test.go`, failing before the templates
changed and passing after, in
[the evidence](../../screenshots/259-role-subagents.txt).
