# hello-html (Framework)

The smallest possible web page: an `index.html` that says `hello, world`. It
exists for the end-to-end tests, which need a Container to scaffold, check and
change without a language toolchain. It brings its own check, so it needs no
Standard.

- **Supports:** `static-site`
- **Writes:** `index.html`, `README.md`, `Makefile`, `.gitea/workflows/check.yaml`
- **Check contract:** `make check` greps `index.html` for `hello, world`.
  `.gitea/workflows/check.yaml` runs it on a runner labelled `base` under the
  status context `check / check`, which the protected `main` requires
  (Gitea Actions' check contexts, `model.CI` in `internal/model/ci.go`).
- **Standard:** none needed; a Standard that also writes `Makefile` or
  `.gitea/workflows/check.yaml` conflicts with it.

## How a template works

Everything in this repository is copied into the new Container repository at the
same path, except `.c4factory/`, which is input for the factory. Files ending in
`.tmpl` are rendered with Go `text/template` (missing keys are errors) and lose the
suffix. Other files, such as the workflow with its `${{ }}` expressions, are copied
byte for byte.
