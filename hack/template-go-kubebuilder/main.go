// Command template-go-kubebuilder regenerates the go-kubebuilder Framework,
// templates/go-kubebuilder, from a pinned `kubebuilder init`:
//
//	make template-go-kubebuilder
//	go run ./hack/template-go-kubebuilder -kubebuilder .dev/make/bin/kubebuilder-v4.16.0
//
// It runs `kubebuilder init` with placeholder names in a temporary folder and
// sorts every path it writes into the Framework, the Standard (go-operator-standard
// writes its own) or dropped; a path no rule matches fails the run, so a
// kubebuilder bump that writes a new path is a decision, not a surprise. The
// Framework's files have their placeholders turned into template fields, edits
// follow (addComputePatch, addDistIgnore and addCrossBuild), the README is the
// factory's own (readmeFor), and they replace everything in the output folder
// except its .c4factory/.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// The names `kubebuilder init` runs with; replacements turns them into
// template fields.
const (
	placeholderProject   = "placeholder-project"
	placeholderContainer = "placeholder-container"
	placeholderDomain    = placeholderProject + ".c4factory.local"
	placeholderRepo      = "c4factory.local/" + placeholderProject + "/" + placeholderContainer
)

// initArgs are the arguments of `kubebuilder init`.
var initArgs = []string{
	"init", "--plugins", "go/v4",
	"--domain", placeholderDomain,
	"--repo", placeholderRepo,
	"--project-name", placeholderContainer,
}

// owner is who a path `kubebuilder init` writes belongs to.
type owner int

const (
	framework owner = iota // written into templates/go-kubebuilder
	standard               // left out: go-operator-standard writes its own
	dropped                // left out: nothing writes it
)

func (o owner) String() string {
	return [...]string{"Framework", "Standard", "dropped"}[o]
}

// rules sorts the paths `kubebuilder init` writes (docs/plans/done/kubebuilder-operators.md,
// decision 3). A pattern ending in "/" matches every path under that folder;
// any other pattern matches one path.
var rules = []struct {
	pattern string
	owner   owner
}{
	{"go.mod", framework},
	{"go.sum", framework},
	{"cmd/", framework},
	{"PROJECT", framework},
	{"config/", framework},
	{"hack/boilerplate.go.txt", framework},
	{"test/e2e/", framework},
	{"test/utils/", framework},
	{"Dockerfile", framework},
	{".dockerignore", framework},
	{".gitignore", framework},
	{"README.md", framework},

	{"Makefile", standard},
	{".golangci.yml", standard},
	{"AGENTS.md", standard},

	{".devcontainer/", dropped},
	{".github/", dropped},
	{".custom-gcl.yml", dropped},
}

// classify says who owns the slash-separated path p, and fails on a path no
// rule matches.
func classify(p string) (owner, error) {
	for _, r := range rules {
		if p == r.pattern || strings.HasSuffix(r.pattern, "/") && strings.HasPrefix(p, r.pattern) {
			return r.owner, nil
		}
	}
	return 0, fmt.Errorf("%s: no rule says who owns it; add one to rules", p)
}

// replacements turn the placeholders into template fields, longest first.
// The leader election ID in cmd/main.go is an FNV hash of the repo, prefixed
// to the domain.
var replacements = []struct {
	re  *regexp.Regexp
	new string
}{
	{regexp.MustCompile(regexp.QuoteMeta(placeholderRepo)), "c4factory.local/{{ .Project }}/{{ .Container }}"},
	{
		regexp.MustCompile(`[0-9a-f]{8}\.` + regexp.QuoteMeta(placeholderDomain)),
		"{{ .Container }}.{{ .Project }}.c4factory.local",
	},
	{regexp.MustCompile(placeholderProject), "{{ .Project }}"},
	{regexp.MustCompile(placeholderContainer), "{{ .Container }}"},
}

// copyright is the year kubebuilder writes into the license headers. It is
// fixed so the regeneration gives the same tree in any year.
var copyright = regexp.MustCompile(`Copyright 20[0-9]{2}\.`)

const fixedCopyright = "Copyright 2026."

// escaper escapes a file's own template delimiters, such as the go-template
// strings of test/e2e, in one pass so they render as themselves.
var escaper = strings.NewReplacer("{{", `{{"{{"}}`, "}}", `{{"}}"}}`)

// hasPlaceholder says whether data names the placeholder project or container.
func hasPlaceholder(data []byte) bool {
	return bytes.Contains(data, []byte(placeholderProject)) || bytes.Contains(data, []byte(placeholderContainer))
}

// transform turns one Framework file into its template file: the year fixed,
// and, for a .go file or one that has a placeholder, its own delimiters
// escaped, the placeholders made template fields and ".tmpl" added to its name.
// Other files keep their bytes. No "placeholder" may remain.
func transform(p string, data []byte) (string, []byte, error) {
	data = copyright.ReplaceAll(data, []byte(fixedCopyright))
	if path.Ext(p) == ".go" || hasPlaceholder(data) {
		s := escaper.Replace(string(data))
		for _, r := range replacements {
			s = r.re.ReplaceAllLiteralString(s, r.new)
		}
		p, data = p+".tmpl", []byte(s)
	}
	if bytes.Contains(data, []byte("placeholder")) {
		return "", nil, fmt.Errorf("%s: still says \"placeholder\" after the substitutions", p)
	}
	return p, data, nil
}

// managerKustomization is the kustomization the compute patch is added to.
const managerKustomization = "config/manager/kustomization.yaml"

// computePatch lists a Compute layer's config/manager/compute.yaml, a
// strategic-merge patch on the manager Deployment, when a Compute is chosen.
const computePatch = `{{- if .Compute }}
patches:
- path: compute.yaml
{{- end }}
`

// addComputePatch is one of the edits besides the substitutions: it makes
// config/manager/kustomization.yaml a template that applies the Compute's
// patch. p and data are transform's result.
func addComputePatch(p string, data []byte) (string, []byte, error) {
	if bytes.Contains(data, []byte("patches:")) {
		return "", nil, fmt.Errorf("%s: already has patches; merge the compute patch by hand in addComputePatch", p)
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		data = append(data, '\n')
	}
	if !strings.HasSuffix(p, ".tmpl") {
		p += ".tmpl"
		data = []byte(escaper.Replace(string(data)))
	}
	return p, append(data, computePatch...), nil
}

// gitignore is the ignore file the release output is added to.
const gitignore = ".gitignore"

// distIgnore ignores go-operator-standard's release output: its `make dist`
// writes dist/, and the Standard can't write .gitignore, the Framework's.
const distIgnore = `
# Release output: make dist
/dist/
`

// addDistIgnore is another edit besides the substitutions: it appends
// distIgnore to .gitignore. p and data are transform's result.
func addDistIgnore(p string, data []byte) (string, []byte, error) {
	if bytes.Contains(data, []byte("/dist/")) {
		return "", nil, fmt.Errorf("%s: already ignores /dist/; drop addDistIgnore", p)
	}
	if len(data) > 0 && !bytes.HasSuffix(data, []byte("\n")) {
		data = append(data, '\n')
	}
	return p, append(data, distIgnore...), nil
}

// dockerfile is the Dockerfile the builder stage is pinned to $BUILDPLATFORM in.
const dockerfile = "Dockerfile"

// The builder stage's FROM, as kubebuilder writes it and as the release
// builds it: the image runner builds linux/amd64 and linux/arm64 with no
// emulation, so a stage that RUNs must be FROM --platform=$BUILDPLATFORM
// (docs/plans/images.md, decision 3); kubebuilder's go build already takes
// TARGETOS and TARGETARCH.
const (
	builderFrom      = "FROM ${BASE_IMAGE} AS builder\n"
	crossBuilderFrom = "FROM --platform=$BUILDPLATFORM ${BASE_IMAGE} AS builder\n"
)

// addCrossBuild is another edit besides the substitutions: it builds the
// Dockerfile's builder stage on $BUILDPLATFORM (#1458). p and data are
// transform's result.
func addCrossBuild(p string, data []byte) (string, []byte, error) {
	if bytes.Contains(data, []byte(crossBuilderFrom)) {
		return "", nil, fmt.Errorf("%s: already builds on $BUILDPLATFORM; drop addCrossBuild", p)
	}
	if !bytes.Contains(data, []byte(builderFrom)) {
		return "", nil, fmt.Errorf("%s: no %q line; place --platform=$BUILDPLATFORM by hand in addCrossBuild",
			p, strings.TrimSpace(builderFrom))
	}
	return p, bytes.Replace(data, []byte(builderFrom), []byte(crossBuilderFrom), 1), nil
}

// readme is the README the factory writes in place of kubebuilder's, whose
// overview and description are left as // TODO(user) (#1477).
const readme = "README.md"

// goDirective is go.mod's go line, the Go version the module needs.
var goDirective = regexp.MustCompile(`(?m)^go ([0-9][0-9.]*)\s*$`)

// goVersion returns the Go version go.mod's go line names.
func goVersion(gomod []byte) (string, error) {
	m := goDirective.FindSubmatch(gomod)
	if m == nil {
		return "", errors.New("go.mod: no go line; the README can't say which Go to install")
	}
	return string(m[1]), nil
}

// releaseNote says what go-operator-standard's `make dist` writes and that a
// release names the manager image in install.yaml by digest, where the
// factory pushes it (docs/plans/images.md, decision 6).
const releaseNote = `## Release

` + "`make dist VERSION=<tag> IMG=<image>`" + ` writes the release files into ` + "`dist/`" + `: the
manager binaries ` + "`{{ .Container }}_<os>_<arch>`" + ` for Linux, macOS and Windows,
` + "`install.yaml`" + ` (the CRDs, RBAC and the manager Deployment, from
` + "`make build-installer`" + `, naming the manager image ` + "`IMG`" + `) and ` + "`SHA256SUMS`" + `.

A version tag's release pipeline, such as the factory's gitea-release, builds
the image from the Dockerfile first, attaches it and those files to the tag's
release, and names the image in ` + "`install.yaml`" + ` by digest on the Git host's
container registry, where the factory pushes it; applying ` + "`install.yaml`" + ` as
attached works:

` + "```sh" + `
kubectl apply -f install.yaml
` + "```" + `

For another registry, build and push the image from a checkout of the tag,
and write ` + "`install.yaml`" + ` with it set:

` + "```sh" + `
make docker-build docker-push IMG=<some-registry>/{{ .Container }}:<tag>
make build-installer IMG=<some-registry>/{{ .Container }}:<tag>
kubectl apply -f dist/install.yaml
` + "```" + `

Without ` + "`IMG`" + `, ` + "`make dist`" + ` names ` + "`controller:<tag>`" + `, which no registry has.
`

// readmeTemplate is the README template, GOVERSION standing for go.mod's Go
// version. It says what the operator is, what to add first, how to check,
// run and release it; the make targets are the Standard's, so without one it
// names the go commands instead.
const readmeTemplate = `# {{ if .DisplayName }}{{ .DisplayName }}{{ else }}{{ .Container }}{{ end }}
{{ if .Description }}
{{ .Description }}
{{ end }}
The ` + "`{{ .Container }}`" + ` Kubernetes operator of {{ .SoftwareSystem }}, in project
{{ .Project }}: a kubebuilder ` + "`go/v4`" + ` project, scaffolded by C4 Factory. It
needs Go GOVERSION or later (` + "`go.mod`" + `'s ` + "`go`" + ` line); the devcontainer has it,
with kubebuilder, controller-gen, kustomize and the envtest binaries.

## Add an API first

The manager in ` + "`cmd/main.go`" + ` runs no controller yet. Add the first kind and
its controller, then regenerate the CRDs, RBAC and DeepCopy methods:

` + "```sh" + `
kubebuilder create api --group <group> --version v1alpha1 --kind <Kind>
{{- if .Standard }}
make manifests generate
{{- else }}
controller-gen rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases
controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./..."
{{- end }}
` + "```" + `

The kind's API group is ` + "`<group>.{{ .Project }}.c4factory.local`" + `. Its types go in
` + "`api/v1alpha1/`" + `, its reconciler in ` + "`internal/controller/`" + `; the files under
` + "`config/crd/bases/`" + `, ` + "`config/rbac/role.yaml`" + ` and ` + "`zz_generated.*.go`" + ` are
generated, never edited.

## Develop

` + "```sh" + `
{{- if .Standard }}
make check           # what CI runs: tidy, generated files, format, vet, lint, test
make test            # the unit and envtest tests
make help            # every target
{{- else }}
go build ./...
go test ./...
{{- end }}
` + "```" + `
{{- if .Standard }}

Read ` + "`AGENTS.md`" + ` before changing anything: it is the rulebook, for people and agents.
{{- end }}

## Run against a cluster

With ` + "`~/.kube/config`" + ` naming a cluster you may change, install the CRDs
and run the manager from your machine:

` + "```sh" + `
{{- if .Standard }}
make install run
{{- else }}
kubectl apply -k config/crd
go run ./cmd
{{- end }}
` + "```" + `
{{- if .Standard }}

` + "`make uninstall`" + ` removes the CRDs again. ` + "`make test-e2e`" + ` deploys the manager
into a new Kind cluster, checks it runs and serves its metrics (` + "`test/e2e`" + `),
and deletes the cluster.

` + releaseNote + `{{- end }}
`

// readmeFor is the README template for go.mod's Go version. It holds no
// "TODO(user)": kubebuilder's README is not used.
func readmeFor(goVersion string) []byte {
	return []byte(strings.ReplaceAll(readmeTemplate, "GOVERSION", goVersion))
}

// file is one file of the generated template.
type file struct {
	data       []byte
	executable bool
}

// frameworkFiles reads what `kubebuilder init` wrote into dir and returns the
// Framework's template files by path, failing on every path no rule matches.
func frameworkFiles(dir string) (map[string]file, error) {
	gomod, err := os.ReadFile(filepath.Join(dir, "go.mod")) //nolint:gosec // the go.mod kubebuilder wrote
	if err != nil {
		return nil, err
	}
	goVer, err := goVersion(gomod)
	if err != nil {
		return nil, err
	}
	out := map[string]file{}
	var errs []error
	err = filepath.WalkDir(dir, func(full string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, full)
		if err != nil {
			return err
		}
		p := filepath.ToSlash(rel)
		o, err := classify(p)
		if err != nil {
			errs = append(errs, err)
			return nil
		}
		if o != framework {
			return nil
		}
		data, err := os.ReadFile(full) //nolint:gosec // a file of the tree it walks
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		name, data, err := transform(p, data)
		if err == nil && p == managerKustomization {
			name, data, err = addComputePatch(name, data)
		}
		if err == nil && p == gitignore {
			name, data, err = addDistIgnore(name, data)
		}
		if err == nil && p == readme {
			name, data = readme+".tmpl", readmeFor(goVer)
		}
		if err == nil && p == dockerfile {
			name, data, err = addCrossBuild(name, data)
		}
		if err != nil {
			errs = append(errs, err)
			return nil
		}
		out[name] = file{data: data, executable: info.Mode()&0o111 != 0}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if _, ok := out[managerKustomization+".tmpl"]; !ok && len(errs) == 0 {
		errs = append(errs, fmt.Errorf("%s: kubebuilder no longer writes it; the compute patch has nowhere to go",
			managerKustomization))
	}
	_, plain := out[gitignore]
	_, tmpl := out[gitignore+".tmpl"]
	if !plain && !tmpl && len(errs) == 0 {
		errs = append(errs, fmt.Errorf("%s: kubebuilder no longer writes it; /dist/ has nowhere to be ignored",
			gitignore))
	}
	return out, errors.Join(errs...)
}

// write replaces everything in dir except .c4factory/ with files.
func write(dir string, files map[string]file) error {
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, e := range entries {
		if e.Name() == ".c4factory" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		f := files[p]
		mode := os.FileMode(0o644)
		if f.executable {
			mode = 0o755
		}
		dst := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { //nolint:gosec // a template tree to commit
			return err
		}
		if err := os.WriteFile(dst, f.data, mode); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	kubebuilder := flag.String("kubebuilder", "kubebuilder", "the kubebuilder binary to run")
	out := flag.String("out", "templates/go-kubebuilder", "the Framework's template folder to replace")
	flag.Parse()
	if err := run(*kubebuilder, *out); err != nil {
		fmt.Fprintln(os.Stderr, "template-go-kubebuilder:", err)
		os.Exit(1)
	}
}

func run(kubebuilder, out string) error {
	tmp, err := os.MkdirTemp("", "template-go-kubebuilder-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	cmd := exec.Command(kubebuilder, initArgs...)
	cmd.Dir = tmp
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kubebuilder %s: %w", strings.Join(initArgs, " "), err)
	}
	files, err := frameworkFiles(tmp)
	if err != nil {
		return err
	}
	if err := write(out, files); err != nil {
		return err
	}
	fmt.Printf("Wrote %d files into %s\n", len(files), out)
	return nil
}
