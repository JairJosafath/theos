# hello-html-standard (Standard)

The smallest possible Standard, for the end-to-end tests: the agents' role
guides for a `hello-html` page. `hello-html` brings its own check, so this
Standard writes no `Makefile` and no workflow; it exists so the e2e suite has a
Standard whose bump it can carry to main.

- **Supports:** `static-site`; Frameworks: `hello-html`
- **Writes:** `docs/agents/implementer.md`, `docs/agents/reviewer.md`
- **Check contract:** `hello-html`'s `make check`, which greps `index.html` for
  `hello, world`.

## How a template works

Everything in this repository is copied into the new Container repository at the
same path, except `.theos/`, which is input for the factory. Files ending in
`.tmpl` are rendered with Go `text/template` (missing keys are errors) and lose the
suffix. Other files are copied byte for byte.
