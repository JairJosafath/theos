# hello-standard (Standard)

The smallest possible quality contract, for the end-to-end tests: `make check`
runs `hello.sh` and expects `hello, world`.

- **Supports:** `cli`; Frameworks: `hello`
- **Writes:** `Makefile`, `.gitea/workflows/check.yaml`
- **Check contract:** `make check`, which the agents run after each round.
  `.gitea/workflows/check.yaml` runs `sh hello.sh` on a runner labelled `base`
  under the status context `check / check`, which the protected `main` requires
  (`model.CheckContexts`).
- **No role guides:** without `docs/agents/implementer.md` and `reviewer.md` the
  agents work from their prompts alone.

## How a template works

Everything in this repository is copied into the new Container repository at the
same path, except `.c4factory/`, which is input for the factory. Files ending in
`.tmpl` are rendered with Go `text/template` (missing keys are errors) and lose the
suffix. Other files, such as the workflow with its `${{ }}` expressions, are copied
byte for byte.
