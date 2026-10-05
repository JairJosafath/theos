# hello (Framework)

The smallest possible Container: a shell script that prints `hello, world`. It
exists for the end-to-end tests, which need a Container to scaffold, check and
change without a language toolchain.

- **Supports:** `cli`
- **Writes:** `hello.sh`, `README.md`
- **Standard:** `hello-standard` brings the `Makefile` and the CI workflow.

## How a template works

Everything in this repository is copied into the new Container repository at the
same path, except `.c4factory/`, which is input for the factory. Files ending in
`.tmpl` are rendered with Go `text/template` (missing keys are errors) and lose the
suffix. Other files are copied byte for byte.
