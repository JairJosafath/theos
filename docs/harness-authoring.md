# Harness authoring

A harness is an agent tool that plays the lead runtime's roles: Claude Code
today, Codex as the worked example, and any other. The content is theos's
(`internal/method`: the lead's playbook, each role's boundaries, the work
order); the format is the harness's. Adding one touches no engine code.
[plans/lead-runtime.md](plans/lead-runtime.md), decision 7, says why.

## Three levels

| Level | What a harness has | Code |
| --- | --- | --- |
| 0 | any MCP client: it starts `theos mcp` and plays one role per session, the work order as its prompt | none |
| 1 | the lead's files for a project repository (`harness.LeadFiles`): the playbook, a subagent per Agent a Crew names, the MCP config | a package under `internal/harness/` |
| 2 | headless in an operator's sandbox: theos-agent runs it for a Task on a cluster (`internal/agentrunner`'s `Harness`, `harness.go`) | `internal/agentrunner/`; folding it into `internal/harness` is #58's |

## The interface

`internal/harness` has the interface and its optional parts. A caller asks
for a part by interface, never for a harness by name.

```go
type Harness interface {
	Name() string               // the value theos agents -harness takes
	Capabilities() Capabilities // subagents, parallel, a model per role, MCP
}

// LeadFiles renders the lead's files from method's content, in the
// harness's own formats. Each file carries harness.Generated where its
// format takes a comment.
type LeadFiles interface {
	Render(p method.Project) ([]File, error)
}

// RoleGrants is a harness that enforces each role's tools. One that only
// instructs a role has none: its reviewer's boundary is its own word.
type RoleGrants interface {
	Grants(role string) Grants // Spawn, Write, Shell
}
```

A level-1 harness is a package with a `Harness` type that implements
`Harness` and `LeadFiles`, and `RoleGrants` when the tool enforces tools
per agent. Add it to `nocluster.Harnesses` (`internal/nocluster/agents.go`),
with the file whose presence says a project uses it, and `theos agents
-harness <name>` writes its files.

## The conformance suite

```go
func TestMyHarness(t *testing.T) { harnesstest.Run(t, myharness.Harness{}) }
```

`internal/harness/harnesstest` checks what every harness must keep, on a
project of one Crew with every role: an implementer, a reviewer, a
triager, a spec writer and a verifier (#337):

- files at local paths of the project repository, each once, the same at
  every render, each with the generated marker but a JSON file;
- the lead's playbook whole in one file;
- each role's boundaries whole in a file that names its subagent, and its
  model where the harness runs each role on its own; the boundaries name
  the Container's `AGENTS.md` and `theos heartbeat`;
- `theos mcp` started where the harness speaks MCP;
- with `RoleGrants`: no role that starts agents; a reviewer and a verifier
  that write nothing, the verifier able to run the build; a triager that
  neither writes nor runs a command; an implementer and a spec writer that
  write.

A harness that drops the reviewer's boundary, by its grants or by leaving
its boundaries out, fails the suite.

## Claude Code, the reference

`internal/harness/claudecode`:

- `CLAUDE.md`, one line importing `AGENTS.md`, the playbook;
- `.claude/agents/theos-<agent>.md`: YAML frontmatter with `name`,
  `description`, the Agent's `model` and the role's `tools`, then the
  role's boundaries. The reviewer has `Read, Grep, Glob, Bash` and
  `mcp__theos__*`, with no `Agent`, `Edit` or `Write`;
- `.mcp.json`: `theos mcp` over stdio, with no marker, as JSON has no
  comments.

It has `RoleGrants`: Claude Code enforces a subagent's `tools`.

## Codex, the worked example

`internal/harness/codex`, from Codex's documentation of 2026-11:

- `AGENTS.md`, the playbook: Codex reads `AGENTS.md` from the project root
  down to its working directory;
- `.codex/agents/theos-<agent>.toml`, a custom agent: `name`,
  `description`, `model`, and the role's boundaries as
  `developer_instructions`;
- `.codex/config.toml`: `[mcp_servers.theos]` with `command = "theos"` and
  `args = ["mcp"]`. Codex loads a project's `.codex/config.toml` only once
  the project is trusted.

Codex sets no tools per custom agent, and an agent file can't set its own
sandbox, so the Codex harness has no `RoleGrants`: its reviewer's boundary
is its instructions alone. A person who wants it held runs the review
session read-only (`sandbox_mode = "read-only"` in a config of its own).

## What a harness never does

- It decides nothing: the engine decides every step, and the content of
  every file is `internal/method`'s.
- It writes nothing into a Container repository: a Container's
  `CLAUDE.md`, `.claude/` and `.mcp.json` are its Standard's protected
  paths.
- It gives no role a merge: a person merges.
