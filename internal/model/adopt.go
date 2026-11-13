/*
Copyright 2026 The theos Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package model

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Adopting a repository the factory never scaffolded starts with reading it
// (docs/plans/adopt-existing.md, step 3; #96). DetectRepository says, from a
// repository's file list and the few files it asks for, what the repository
// is and how it is checked; AdoptionProposal writes that as the model a
// person applies. The detector guesses nothing it can't read: what it can't
// tell is a note, and the proposal says where a person must look.

// Languages the detector tells, by the file at the repository's root that
// names each.
const (
	LanguageGo         = "Go"
	LanguageRust       = "Rust"
	LanguageTypeScript = "TypeScript"
	LanguageJavaScript = "JavaScript"
	LanguagePython     = "Python"
)

// Files the detector reads.
const (
	adoptMakefile    = "Makefile"
	adoptGoMod       = "go.mod"
	adoptCargo       = "Cargo.toml"
	adoptPackageJSON = "package.json"
	adoptPyproject   = "pyproject.toml"
	adoptDockerfile  = "Dockerfile"
)

// maxAdoptContexts is how many check contexts a Container names at most, as
// its CRD says.
const maxAdoptContexts = 8

// RepoFinding is what DetectRepository found in a repository.
type RepoFinding struct {
	// Language is the repository's language, one of the Language constants;
	// empty when no file at its root names one.
	Language string
	// Type is the Container type the repository looks like (service, cli,
	// static-site); empty when nothing says.
	Type string
	// CheckCommand is the command that checks the repository; empty when
	// its Makefile has a check target, which is the factory's default.
	// NoCheck means none was found at all.
	CheckCommand string
	NoCheck      bool
	// CheckContexts are the status checks its workflows report on a pull
	// request, as patterns a Container's checkContexts takes; empty when it
	// has no workflow that runs on one.
	CheckContexts []string
	// Notes say what the detector could not tell, and what a person should
	// look at before applying the proposal.
	Notes []string
}

// AdoptionReads returns the files among paths, a repository's files, that
// DetectRepository reads: its build files at the root and its workflows.
func AdoptionReads(paths []string) []string {
	root := []string{adoptMakefile, adoptGoMod, adoptCargo, adoptPackageJSON, adoptPyproject}
	var out []string
	for _, p := range paths {
		if slices.Contains(root, p) || adoptWorkflow(p) != "" {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// adoptWorkflow returns the workflow directory p is a workflow file of, or
// "" when it is none.
func adoptWorkflow(p string) string {
	for _, dir := range WorkflowDirs() {
		if rest, ok := strings.CutPrefix(p, dir); ok && !strings.Contains(rest, "/") &&
			(strings.HasSuffix(rest, ".yml") || strings.HasSuffix(rest, ".yaml")) {
			return dir
		}
	}
	return ""
}

// makeCheckTarget matches a Makefile's check target.
var makeCheckTarget = regexp.MustCompile(`(?m)^check\s*:`)

// DetectRepository reads a repository: paths are all its files, and read the
// contents of those AdoptionReads named.
func DetectRepository(paths []string, read map[string]string) RepoFinding {
	has := func(p string) bool { return slices.Contains(paths, p) }
	under := func(dir string) bool {
		return slices.ContainsFunc(paths, func(p string) bool { return strings.HasPrefix(p, dir) })
	}
	var f RepoFinding
	var also []string
	for _, lang := range []struct{ file, name string }{{adoptGoMod, LanguageGo}, {adoptCargo, LanguageRust},
		{adoptPackageJSON, LanguageJavaScript}, {adoptPyproject, LanguagePython}, {"setup.py", LanguagePython},
		{"requirements.txt", LanguagePython}} {
		name := lang.name
		if !has(lang.file) {
			continue
		}
		if name == LanguageJavaScript && has("tsconfig.json") {
			name = LanguageTypeScript
		}
		switch {
		case f.Language == "":
			f.Language = name
		case name != f.Language && !slices.Contains(also, name):
			also = append(also, name)
		}
	}
	if f.Language == "" {
		f.Notes = append(f.Notes, "No go.mod, Cargo.toml, package.json or pyproject.toml at the root: the "+
			"language is not told, and nothing depends on it.")
	}
	if len(also) > 0 {
		f.Notes = append(f.Notes, fmt.Sprintf("It also has %s at its root; the check below is %s's.",
			joinAnd(also), f.Language))
	}

	f.Type = adoptType(f.Language, has, under, read)
	if f.Type == "" {
		f.Notes = append(f.Notes, "Nothing says what kind of application this is (no Dockerfile, no command it "+
			"installs): the proposal says service; change spec.type to what it is.")
	}

	switch {
	case makeCheckTarget.MatchString(read[adoptMakefile]):
		// make check, the factory's default: nothing to name.
	default:
		f.CheckCommand = adoptCheck(f.Language, has, under, read)
		if f.CheckCommand == "" {
			f.NoCheck = true
			f.Notes = append(f.Notes, "No check was found (no Makefile with a check target, and no test command "+
				"the language's files name): name the command that checks a change in spec.checkCommand.")
		}
	}

	contexts, notes := adoptContexts(paths, read)
	f.CheckContexts = contexts
	f.Notes = append(f.Notes, notes...)
	return f
}

// adoptType says what kind of application the repository looks like.
func adoptType(language string, has func(string) bool, under func(string) bool, read map[string]string) string {
	docker := has(adoptDockerfile)
	switch language {
	case LanguageGo:
		if has("main.go") || under("cmd/") {
			return serviceOr(docker, TypeCLI)
		}
	case LanguageRust:
		if has("src/main.rs") || under("src/bin/") {
			return serviceOr(docker, TypeCLI)
		}
	case LanguageJavaScript, LanguageTypeScript:
		pkg := read[adoptPackageJSON]
		switch {
		case docker:
			return TypeService
		case strings.Contains(pkg, `"bin"`):
			return TypeCLI
		case strings.Contains(pkg, `"vite"`) || strings.Contains(pkg, `"react-scripts"`) ||
			strings.Contains(pkg, `"astro"`):
			return TypeStaticSite
		}
	case LanguagePython:
		py := read[adoptPyproject]
		switch {
		case docker:
			return TypeService
		case strings.Contains(py, "[project.scripts]"):
			return TypeCLI
		}
	}
	if docker {
		return TypeService
	}
	return ""
}

func serviceOr(docker bool, other string) string {
	if docker {
		return TypeService
	}
	return other
}

// adoptCheck is the check command the language's own files name, for a
// repository whose Makefile has no check target; "" when they name none.
func adoptCheck(language string, has func(string) bool, under func(string) bool, read map[string]string) string {
	switch language {
	case LanguageGo:
		return "go test ./..."
	case LanguageRust:
		return "cargo test"
	case LanguageJavaScript, LanguageTypeScript:
		var pkg struct {
			Scripts map[string]string `yaml:"scripts"`
		}
		// JSON is YAML: one parser for both.
		if yaml.Unmarshal([]byte(read[adoptPackageJSON]), &pkg) == nil && pkg.Scripts["test"] != "" {
			return "npm test"
		}
	case LanguagePython:
		if strings.Contains(read[adoptPyproject], "pytest") || under("tests/") || has("pytest.ini") {
			return "pytest"
		}
	}
	return ""
}

// adoptContexts reads the workflows a host runs (those of the first workflow
// directory that has any) and returns, as patterns, the checks the ones that
// run on a pull request report: "<workflow> / <job>*", which is the job on
// every event and for every matrix entry.
func adoptContexts(paths []string, read map[string]string) (contexts, notes []string) {
	var files []string
	for _, dir := range WorkflowDirs() {
		for _, p := range paths {
			if adoptWorkflow(p) == dir {
				files = append(files, p)
			}
		}
		if len(files) > 0 {
			break
		}
	}
	slices.Sort(files)
	if len(files) == 0 {
		return nil, []string{fmt.Sprintf("It has no workflow under %s: nothing checks a change, and no agent "+
			"works on a repository whose main requires no check. Add one first.",
			strings.Join(WorkflowDirs(), " or "))}
	}
	ran := false
	for _, file := range files {
		var wf struct {
			Name string    `yaml:"name"`
			On   yaml.Node `yaml:"on"`
			Jobs map[string]struct {
				Name string `yaml:"name"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal([]byte(read[file]), &wf); err != nil {
			notes = append(notes, fmt.Sprintf("%s could not be read as YAML: its checks are not in the proposal.", file))
			continue
		}
		if !runsOnPullRequest(&wf.On) {
			continue
		}
		ran = true
		if wf.Name == "" {
			notes = append(notes, fmt.Sprintf("%s has no name, so its checks are called after its file as the Git "+
				"host shows it: they are not in the proposal.", file))
			continue
		}
		ids := make([]string, 0, len(wf.Jobs))
		for id := range wf.Jobs {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			job := wf.Jobs[id].Name
			if job == "" || strings.Contains(job, "${{") {
				job = id
			}
			contexts = append(contexts, wf.Name+" / "+job+"*")
		}
	}
	if !ran {
		notes = append(notes, fmt.Sprintf("None of the workflows under %s runs on a pull request: nothing checks "+
			"a change before it merges. Add \"pull_request\" to the one that tests.", path.Dir(files[0])+"/"))
	}
	if len(contexts) > maxAdoptContexts {
		notes = append(notes, fmt.Sprintf("Its workflows report %d checks on a pull request and a Container names "+
			"at most %d: the proposal has the first %d; keep the ones a change must pass.", len(contexts),
			maxAdoptContexts, maxAdoptContexts))
		contexts = contexts[:maxAdoptContexts]
	}
	return contexts, notes
}

// runsOnPullRequest reports whether a workflow's "on" names pull_request: as
// a word, in a list, or as a key.
func runsOnPullRequest(on *yaml.Node) bool {
	const event = "pull_request"
	switch on.Kind {
	case yaml.ScalarNode:
		return on.Value == event
	case yaml.SequenceNode:
		return slices.ContainsFunc(on.Content, func(n *yaml.Node) bool { return n.Value == event })
	case yaml.MappingNode:
		for i := 0; i+1 < len(on.Content); i += 2 {
			if on.Content[i].Value == event {
				return true
			}
		}
	}
	return false
}

// AdoptionInput is what a person says of the repository they bring.
type AdoptionInput struct {
	// Project is the project's namespace, System its SoftwareSystem and
	// Container the Container's name, which is the repository's.
	Project, System, Container string
	// GitProvider names the project's GitProvider.
	GitProvider string
}

// AdoptionProposal writes what DetectRepository found as the model a person
// applies: manifests, the SoftwareSystem, the Container adopting the
// repository and its Crew, as YAML; and identity, the .theos.yaml to commit
// to the repository's main first, which is how its owner says the project
// may work there. The finding's notes are comments at the top.
func AdoptionProposal(in AdoptionInput, f RepoFinding) (manifests, identity string) {
	typ := f.Type
	if typ == "" {
		typ = TypeService
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s, a repository the factory adopts as it is: nothing is scaffolded or rewritten.\n",
		in.Container)
	if f.Language != "" {
		fmt.Fprintf(&b, "# Read from the repository: %s.\n", f.Language)
	}
	for _, note := range f.Notes {
		fmt.Fprintf(&b, "# Look at this: %s\n", note)
	}
	fmt.Fprintf(&b, `apiVersion: theos.factory.io/v1beta1
kind: SoftwareSystem
metadata:
  name: %[1]s
  namespace: %[2]s
spec:
  displayName: %[1]s
---
apiVersion: theos.factory.io/v1beta1
kind: Container
metadata:
  name: %[3]s
  namespace: %[2]s
spec:
  softwareSystemRef:
    name: %[1]s
  displayName: %[3]s
  type: %[4]s
  gitProviderRef:
    name: %[5]s
  adopt: true
`, in.System, in.Project, in.Container, typ, in.GitProvider)
	if f.CheckCommand != "" {
		fmt.Fprintf(&b, "  checkCommand: %s\n", f.CheckCommand)
	}
	if len(f.CheckContexts) > 0 {
		b.WriteString("  checkContexts:\n")
		for _, c := range f.CheckContexts {
			fmt.Fprintf(&b, "  - %q\n", c)
		}
	}
	fmt.Fprintf(&b, `---
# Start small: the Crew holds every change for a person. Loosen it rule by
# rule (docs/getting-started.md, "Start small").
apiVersion: theos.factory.io/v1beta1
kind: Crew
metadata:
  name: %[1]s
  namespace: %[2]s
spec:
  containerRef:
    name: %[1]s
  implementer:
    name: implementer
  reviewer:
    name: reviewer
  approval:
  - require: Person
`, in.Container, in.Project)
	identity = fmt.Sprintf("project: %s\nsoftwareSystem: %s\ncontainer: %s\ntype: %s\nscaffoldedFrom: []\n",
		in.Project, in.System, in.Container, typ)
	return b.String(), identity
}
