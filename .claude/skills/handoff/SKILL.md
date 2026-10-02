---
name: handoff
description: Write the in-flight state of the current work to docs/handoffs/ so a fresh session can pick it up. Use when the session's context is getting large (around 140k tokens), when work stops mid-way, or when the user asks to hand off, pause, wrap up or continue later.
---

# Handoff

A handoff is one Markdown file about one piece of work in flight. It is dead once
that work lands: the commit that finishes the work deletes it. Standing facts
(true next month) go in `AGENTS.md`, `docs/` or memory instead.

## Write it

1. **Path:** `docs/handoffs/YYYY-MM-DD-<slug>.md`. Durable on purpose: the next
   session may start hours later.
2. **Sections**, short:
   - **Goal:** the outcome, in two sentences, and who asked for it.
   - **Where it stands:** done (with commit SHAs), in progress, not started; the
     branch and whether it is pushed.
   - **Live thread:** what you were in the middle of, what you learned that is
     written nowhere else, dead ends and why.
   - **Decisions:** made, and still open with your recommendation.
   - **Next steps:** numbered and concrete; the first doable in minutes.
   - **State outside git:** clusters, paused or running things, background
     processes, ports; where secrets live, never their values.
   - **References:** spec, plan, results, issues, pull requests, by path or URL.
     Point to them; never copy them.
   - **For the next session:** the skills and docs to reach for.
3. Keep it under about 150 lines. Redact secrets.
4. Commit it (`docs: hand off <slug>`) and push, unless told not to.
5. Give the user the path and the line to start the next session with:
   `Read docs/handoffs/<file> and continue.`

## Pick one up

Read the file, check "Where it stands" against `git log` and the cluster, then do
the first next step. Delete the file in the commit that finishes its work.
