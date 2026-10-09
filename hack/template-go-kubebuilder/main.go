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
// Framework's files have their placeholders turned into template fields, three
// edits follow (addComputePatch, addDistIgnore and addReleaseNote), and they replace everything
// in the output folder except its .c4factory/.
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

// rules sorts the paths `kubebuilder init` writes (docs/plans/kubebuilder-operators.md,
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

// addComputePatch is one of the three edits besides the substitutions: it makes
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

// readme is the README the release section is replaced in.
const readme = "README.md"

// The section of kubebuilder's README that addReleaseNote replaces, from its
// heading up to the next one: it applies dist/install.yaml from a raw URL in
// the repository, where /dist/ is ignored.
const (
	bundleHeading = "### By providing a bundle with all YAML files\n"
	helmHeading   = "### By providing a Helm Chart\n"
)

// releaseNote says what go-operator-standard's `make dist` writes and that the
// manager image in install.yaml is a placeholder to replace
// (docs/plans/kubebuilder-operators.md, decision 8). It is template text: the
// README is a .tmpl once the substitutions name the Container.
const releaseNote = `### From a release

` + "`make dist VERSION=<tag>`" + ` writes the release files into ` + "`dist/`" + `: the manager
binaries ` + "`{{ .Container }}_<os>_<arch>`" + ` for Linux, macOS and Windows,
` + "`install.yaml`" + ` (the CRDs, RBAC and the manager Deployment, from
` + "`make build-installer`" + `) and ` + "`SHA256SUMS`" + `. A version tag's release pipeline,
such as the factory's gitea-release, attaches them to the tag's release.

No image is built or pushed: ` + "`install.yaml`" + ` names the manager image
` + "`controller:<tag>`" + `, which no registry has. Before applying it, build and push
the image from a checkout of the tag, and set it in ` + "`install.yaml`" + `:

` + "```sh" + `
make docker-build docker-push IMG=<some-registry>/{{ .Container }}:<tag>
sed -i 's|image: controller:<tag>|image: <some-registry>/{{ .Container }}:<tag>|' install.yaml
kubectl apply -f install.yaml
` + "```" + `

Or, in that checkout, ` + "`make build-installer IMG=<some-registry>/{{ .Container }}:<tag>`" + `
writes ` + "`dist/install.yaml`" + ` with the image set.

`

// addReleaseNote is the third edit besides the substitutions: it replaces the
// README's bundle section with releaseNote. p and data are transform's result.
func addReleaseNote(p string, data []byte) (string, []byte, error) {
	start := bytes.Index(data, []byte(bundleHeading))
	end := bytes.Index(data, []byte(helmHeading))
	if start < 0 || end < start {
		return "", nil, fmt.Errorf("%s: no %q section before %q; place the release note by hand in addReleaseNote",
			p, strings.TrimSpace(bundleHeading), strings.TrimSpace(helmHeading))
	}
	if !strings.HasSuffix(p, ".tmpl") {
		p += ".tmpl"
		data = []byte(escaper.Replace(string(data)))
		start = bytes.Index(data, []byte(bundleHeading))
		end = bytes.Index(data, []byte(helmHeading))
	}
	out := append([]byte{}, data[:start]...)
	out = append(out, releaseNote...)
	return p, append(out, data[end:]...), nil
}

// file is one file of the generated template.
type file struct {
	data       []byte
	executable bool
}

// frameworkFiles reads what `kubebuilder init` wrote into dir and returns the
// Framework's template files by path, failing on every path no rule matches.
func frameworkFiles(dir string) (map[string]file, error) {
	out := map[string]file{}
	var errs []error
	err := filepath.WalkDir(dir, func(full string, d fs.DirEntry, err error) error {
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
		data, err := os.ReadFile(full)
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
			name, data, err = addReleaseNote(name, data)
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
	if _, ok := out[readme+".tmpl"]; !ok && len(errs) == 0 {
		errs = append(errs, fmt.Errorf("%s: kubebuilder no longer writes it; the release note has nowhere to go", readme))
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
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
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
