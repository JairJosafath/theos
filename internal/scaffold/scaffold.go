// Package scaffold turns pinned template layers into the file tree of a new
// Container repository. It renders the layers in a fixed order, fails when two
// layers write the same path, assembles .devcontainer/ and writes the identity file.
//
// It is pure: files in, files out. It must not import Kubernetes,
// controller-runtime, net/http or the api package; see docs/architecture.md.
package scaffold

import (
	"bytes"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"
	"text/template"

	"github.com/JairJosafath/c4factory/internal/model"
)

// File is one file in a repository tree.
type File struct {
	Data       []byte
	Executable bool
}

// Tree maps slash-separated paths to files.
type Tree map[string]File

// Paths returns the paths in the tree, sorted.
func (t Tree) Paths() []string {
	return slices.Sorted(maps.Keys(t))
}

// ReadTree reads every file under fsys into a Tree, such as a template folder
// through os.DirFS. A file with any execute bit set is executable.
func ReadTree(fsys fs.FS) (Tree, error) {
	tree := Tree{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		tree[p] = File{Data: data, Executable: info.Mode()&0o111 != 0}
		return nil
	})
	return tree, err
}

// Input is the data every template receives.
type Input struct {
	Project        string
	SoftwareSystem string
	Container      string
	DisplayName    string
	Type           string
	// Framework, Standard, Compute and Deployment name the chosen catalog entries.
	// Standard, Compute and Deployment are empty when the Container doesn't pick one.
	Framework  string
	Standard   string
	Compute    string
	Deployment string
	// Connectors are the unique Connector names, sorted.
	Connectors []string
	// Checks is when CI runs the full check: EveryChange or BeforeRelease, as
	// model.EffectiveChecks decides. Empty reads as EveryChange.
	Checks string
}

// Layer is one catalog entry's template repository at a pinned ref.
type Layer struct {
	Side  string
	Name  string
	Ref   string
	Files Tree
}

// Pinned returns the layer as name@ref.
func (l Layer) Pinned() string { return l.Name + "@" + l.Ref }

// Options configure the parts of the scaffold the operator owns.
type Options struct {
	// Features are added to devcontainer.json, keyed by feature id.
	Features map[string]any
	// DevcontainerFiles are written under .devcontainer/, such as a local feature.
	DevcontainerFiles Tree
}

// Result is a rendered repository.
type Result struct {
	Files Tree
	// ScaffoldedFrom lists the layers as name@ref, in render order.
	ScaffoldedFrom []string
}

const (
	// metaDir holds a template's input for the factory. It is never copied.
	metaDir = ".c4factory/"
	// templateSuffix marks files rendered with text/template.
	templateSuffix = ".tmpl"
	// IdentityPath is the identity file the Session client reads.
	IdentityPath = ".c4factory.yaml"
	// devcontainerDir is written by the operator, never by a layer.
	devcontainerDir = ".devcontainer/"
	// operatorOwner names the operator in conflict messages.
	operatorOwner = "the operator"
)

var sideOrder = map[string]int{
	model.SideFramework:  0,
	model.SideStandard:   1,
	model.SideCompute:    2,
	model.SideDeployment: 3,
	model.SideConnector:  4,
}

// ConflictError reports a path written by two owners.
type ConflictError struct {
	Path   string
	Owners [2]string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s and %s both write %s", e.Owners[0], e.Owners[1], e.Path)
}

// TemplateError reports a template that failed to render.
type TemplateError struct {
	Layer string
	Path  string
	Err   error
}

func (e *TemplateError) Error() string {
	return fmt.Sprintf("%s: %s: %v", e.Layer, e.Path, e.Err)
}

func (e *TemplateError) Unwrap() error { return e.Err }

// Build renders the layers into one repository tree. Layers must come in the
// fixed order Framework, Standard, Compute, Deployment, then each Connector, with exactly one
// Framework, and the Connector layers must match in.Connectors.
func Build(in Input, layers []Layer, opts Options) (Result, error) {
	if err := checkLayers(in, layers); err != nil {
		return Result{}, err
	}

	out := Tree{}
	owner := map[string]string{}
	result := Result{Files: out}
	for _, layer := range layers {
		files, err := RenderLayer(in, layer)
		if err != nil {
			return Result{}, err
		}
		for _, p := range files.Paths() {
			if first, taken := owner[p]; taken {
				return Result{}, &ConflictError{Path: p, Owners: [2]string{first, layer.Name}}
			}
			owner[p] = layer.Name
			out[p] = files[p]
		}
		result.ScaffoldedFrom = append(result.ScaffoldedFrom, layer.Pinned())
	}

	devcontainer, err := assembleDevcontainer(in, layers, opts)
	if err != nil {
		return Result{}, err
	}
	maps.Copy(out, devcontainer)

	identity, err := Identity{
		Project:        in.Project,
		SoftwareSystem: in.SoftwareSystem,
		Container:      in.Container,
		Type:           in.Type,
		ScaffoldedFrom: result.ScaffoldedFrom,
	}.Marshal()
	if err != nil {
		return Result{}, err
	}
	out[IdentityPath] = File{Data: identity}
	return result, nil
}

func checkLayers(in Input, layers []Layer) error {
	frameworks := 0
	var connectors []string
	last := -1
	for _, layer := range layers {
		order, ok := sideOrder[layer.Side]
		if !ok {
			return fmt.Errorf("layer %s has unknown side %q", layer.Name, layer.Side)
		}
		if order < last || (order == last && layer.Side != model.SideConnector) {
			return fmt.Errorf("layer %s (%s) is out of order: want Framework, Standard, Compute, Deployment, then Connectors",
				layer.Name, layer.Side)
		}
		last = order
		switch layer.Side {
		case model.SideFramework:
			frameworks++
		case model.SideConnector:
			connectors = append(connectors, layer.Name)
		}
	}
	if frameworks != 1 {
		return fmt.Errorf("want exactly one Framework layer, got %d", frameworks)
	}
	if !slices.Equal(connectors, in.Connectors) {
		return fmt.Errorf("connector layers %v don't match input connectors %v", connectors, in.Connectors)
	}
	return nil
}

func isOperatorPath(p string) bool {
	return p == IdentityPath || strings.HasPrefix(p, devcontainerDir)
}

// renderLayer returns the files a layer adds to the repository: everything except
// .c4factory/, with .tmpl files rendered and whitespace-only results left out.
func renderLayer(in Input, layer Layer) (Tree, error) {
	out := Tree{}
	for _, p := range layer.Files.Paths() {
		if err := checkPath(p); err != nil {
			return nil, &TemplateError{Layer: layer.Name, Path: p, Err: err}
		}
		if strings.HasPrefix(p, metaDir) {
			continue
		}
		f := layer.Files[p]
		target, data, keep, err := renderFile(in, p, f.Data)
		if err == nil {
			err = checkPath(target)
		}
		if err != nil {
			return nil, &TemplateError{Layer: layer.Name, Path: p, Err: err}
		}
		if !keep {
			continue
		}
		if _, dup := out[target]; dup {
			return nil, &ConflictError{Path: target, Owners: [2]string{layer.Name, layer.Name}}
		}
		out[target] = File{Data: data, Executable: f.Executable}
	}
	return out, nil
}

// renderFile renders one file. keep is false for a template that renders to only
// whitespace, which is how templates leave a file out.
func renderFile(in Input, p string, data []byte) (target string, out []byte, keep bool, err error) {
	if !strings.HasSuffix(p, templateSuffix) {
		return p, data, true, nil
	}
	target = strings.TrimSuffix(p, templateSuffix)
	tmpl, err := template.New(p).Funcs(funcs).Option("missingkey=error").Parse(string(data))
	if err != nil {
		return "", nil, false, err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, in); err != nil {
		return "", nil, false, err
	}
	if len(bytes.TrimSpace(buf.Bytes())) == 0 {
		return target, nil, false, nil
	}
	return target, buf.Bytes(), true, nil
}

// metaFile returns a layer's .c4factory/<name> input, rendered when it is a template.
func metaFile(in Input, layer Layer, name string) ([]byte, bool, error) {
	for _, p := range []string{metaDir + name, metaDir + name + templateSuffix} {
		f, ok := layer.Files[p]
		if !ok {
			continue
		}
		_, data, keep, err := renderFile(in, p, f.Data)
		if err != nil {
			return nil, false, &TemplateError{Layer: layer.Name, Path: p, Err: err}
		}
		return data, keep, nil
	}
	return nil, false, nil
}

func checkPath(p string) error {
	if p == "" || strings.HasPrefix(p, "/") || path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../") {
		return fmt.Errorf("path must be relative and clean")
	}
	// Git refuses a .git entry at any depth, in any case.
	if slices.ContainsFunc(strings.Split(p, "/"), func(part string) bool { return strings.EqualFold(part, ".git") }) {
		return fmt.Errorf("templates can't write .git")
	}
	return nil
}

var funcs = template.FuncMap{
	"has":   slices.Contains[[]string],
	"snake": func(s string) string { return strings.ReplaceAll(s, "-", "_") },
	"upper": strings.ToUpper,
	"lower": strings.ToLower,
	"dart":  dartString,
}

// dartString quotes s as a Dart string literal the way Effective Dart and the
// formatter and linter want it: single quotes, or double quotes when s holds a
// single quote and no double one; raw when that avoids escapes; else with
// backslashes, dollars and the quote escaped.
func dartString(s string) string {
	quote := "'"
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		quote = `"`
	}
	// A raw string when only backslashes or dollars would need escaping
	// (use_raw_strings).
	if strings.ContainsAny(s, `\\$`) && !strings.Contains(s, quote) &&
		!strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 }) {
		return "r" + quote + s + quote
	}
	var b strings.Builder
	b.WriteString(quote)
	for _, r := range s {
		switch {
		case r == '\\' || r == '$' || string(r) == quote:
			b.WriteRune('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r < 0x20:
			fmt.Fprintf(&b, `\u{%x}`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString(quote)
	return b.String()
}
