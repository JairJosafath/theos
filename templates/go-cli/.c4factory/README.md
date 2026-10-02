# go-cli (Framework)

A Go 1.26 command-line program: a testable `main`, packages under `internal/`,
and a version set at build time.

- **Supports:** `cli`
- **Layout:** Go's [module layout](https://go.dev/doc/modules/layout) for a
  command with supporting packages: `main.go` at the root, packages under
  `internal/`. Templates can't name directories after the Container, so there
  is no `cmd/<name>/`; the module path ends in the Container's name, so
  `go build` and `go install` name the binary after it anyway.
- **Writes:** `go.mod`, `main.go`, `main_test.go`, `internal/doc.go`, `README.md`,
  `.gitignore`
- **Module:** `c4factory.local/<project>/<container>`. Rename it with
  `go mod edit -module` once the code has a real home.
- **Command:** `run(args, stdout, stderr) int` holds the program and returns the
  exit code (0 OK, 2 usage); `main` only calls it. `--version` prints
  `<container> <version>`, where `version` is set with
  `-ldflags "-X main.version=..."` and is `dev` otherwise.
- **Standard:** a Standard brings the `Makefile`; go-standard's builds `.` into
  `bin/<container>` and cross-compiles `dist/`. Other Standards must do the same,
  since the binary's name and `main.version` are the Framework's contract.
- **Devcontainer:** `.c4factory/devcontainer.json` uses `c4factory/go:1.26`, the
  same Go and Node toolchain CI and agents use, with the Go extension set to
  gofumpt and golangci-lint v2.
- **Every `.go` file is a `.tmpl`**, even without template expressions: C4
  Factory is a Go module too, and a plain `.go` file here would join it and
  break its build.

## How a template works

Everything in this repository is copied into the new Container repository at the
same path, except `.c4factory/`, which is input for the factory. Files ending in
`.tmpl` are rendered with Go `text/template` (missing keys are errors) and lose the
suffix; a `.tmpl` that renders to only whitespace is left out. Other files are copied
byte for byte. Paths are never rendered, only contents.

Data: `.Project`, `.SoftwareSystem`, `.Container`, `.DisplayName`, `.Type`, the chosen
`.Framework`, `.Standard`, `.Compute` and `.Deployment` (empty when none is chosen),
and `.Connectors` (sorted names). Functions: `has`, `snake`, `upper`, `lower`, `dart`,
plus the `text/template` builtins.

Each path belongs to exactly one layer; `.devcontainer/**` and `.c4factory.yaml`
belong to the operator. Release a change by tagging a new version and bumping it in
`templates/versions.txt` of C4 Factory; a tag is never moved.
