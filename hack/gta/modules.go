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

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"
)

// What a change to go.mod or go.sum affects (#127,
// docs/plans/done/module-selection.md). gta knows nothing of the two files: a
// change to them alone selects no package there. Every package is built from
// them, but a package is built from the modules it imports and no others, so
// a module whose version changed reaches the packages that depend on one of
// its packages, in their own code or in their tests, and those alone. Only a
// line that every package is built by, as go and toolchain, reaches them all.

// modFiles returns the module's own go.mod and go.sum among files (absolute
// paths), and the other files, which are gta's to place.
func modFiles(root string, files []string) (mod, rest []string) {
	for _, f := range files {
		if f == filepath.Join(root, "go.mod") || f == filepath.Join(root, "go.sum") {
			mod = append(mod, f)
		} else {
			rest = append(rest, f)
		}
	}
	return mod, rest
}

// moduleChanges compares go.mod and go.sum in the working directory with
// those of the git revision base. It returns the modules whose packages are
// no longer what they were, or in all the reason every package is affected.
func moduleChanges(base string) (all string, modules map[string]bool, err error) {
	if base == "" {
		return "go.mod or go.sum changed, and no -base says what they were", nil, nil
	}
	oldMod, found, err := atRevision(base, "go.mod")
	if err != nil {
		return "", nil, err
	}
	if !found {
		return "go.mod is new", nil, nil
	}
	oldSum, _, err := atRevision(base, "go.sum")
	if err != nil {
		return "", nil, err
	}
	newMod, err := os.ReadFile("go.mod")
	if err != nil {
		return "", nil, err
	}
	// A module without dependencies has no go.sum.
	newSum, err := os.ReadFile("go.sum")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", nil, err
	}
	return diffModules(oldMod, newMod, oldSum, newSum)
}

// atRevision returns the file name of the working directory as the git
// revision rev has it; found is false when rev has no such file.
func atRevision(rev, name string) (data []byte, found bool, err error) {
	//nolint:gosec // The caller names the revision; git gets it as one argument.
	if err := exec.Command("git", "rev-parse", "--verify", "--quiet", rev+"^{commit}").Run(); err != nil {
		return nil, false, fmt.Errorf("%s is no commit: %w", rev, err)
	}
	object := rev + ":./" + name
	//nolint:gosec // As above.
	if err := exec.Command("git", "cat-file", "-e", object).Run(); err != nil {
		return nil, false, nil
	}
	//nolint:gosec // As above.
	data, err = exec.Command("git", "show", object).Output()
	if err != nil {
		return nil, false, fmt.Errorf("git show %s: %w", object, err)
	}
	return data, true, nil
}

// diffModules says what differs between two versions of go.mod and go.sum:
// the modules a changed require, replace or exclude line names, and those
// whose checksum in go.sum changed. A retract or tool line builds no package,
// and neither does a comment (// indirect). Any other changed line (module,
// go, toolchain, godebug, ignore, one this code doesn't know) is one every
// package is built by: all names it.
func diffModules(oldMod, newMod, oldSum, newSum []byte) (all string, modules map[string]bool, err error) {
	before, err := modLines(oldMod)
	if err != nil {
		return "", nil, fmt.Errorf("go.mod as it was: %w", err)
	}
	after, err := modLines(newMod)
	if err != nil {
		return "", nil, err
	}
	modules = map[string]bool{}
	for _, key := range differing(before, after) {
		tokens := before[key]
		if tokens == nil {
			tokens = after[key]
		}
		switch tokens[0] {
		case "require", "replace", "exclude":
			if len(tokens) < 2 {
				return "a " + tokens[0] + " line of go.mod changed", nil, nil
			}
			modules[strings.Trim(tokens[1], "\"`")] = true
		case "retract", "tool":
		default:
			return "the " + tokens[0] + " line of go.mod changed, which every package is built by", nil, nil
		}
	}
	for _, key := range differing(sumLines(oldSum), sumLines(newSum)) {
		modules[strings.Fields(key)[0]] = true
	}
	return "", modules, nil
}

// differing returns the keys that only one of the two maps holds, sorted.
func differing[V any](a, b map[string]V) []string {
	var keys []string
	for k := range a {
		if _, ok := b[k]; !ok {
			keys = append(keys, k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}

// modLines returns the lines of a go.mod, each as its tokens with the verb
// first, a line of a block as a line of its own, keyed by the tokens joined.
// Comments are no tokens.
func modLines(data []byte) (map[string][]string, error) {
	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil, err
	}
	lines := map[string][]string{}
	add := func(tokens []string) {
		if len(tokens) > 0 {
			lines[strings.Join(tokens, " ")] = tokens
		}
	}
	for _, stmt := range f.Syntax.Stmt {
		switch s := stmt.(type) {
		case *modfile.Line:
			add(s.Token)
		case *modfile.LineBlock:
			for _, l := range s.Line {
				add(append(slices.Clone(s.Token), l.Token...))
			}
		}
	}
	return lines, nil
}

// sumLines returns the lines of a go.sum that vouch for a module's files:
// "MODULE VERSION HASH". Those for its go.mod alone ("MODULE VERSION/go.mod
// HASH") are left out: they vouch for what decides the versions, and the
// versions a package is built from are go.mod's require lines.
func sumLines(data []byte) map[string]bool {
	lines := map[string]bool{}
	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		if len(fields) == 3 && !strings.HasSuffix(fields[1], "/go.mod") {
			lines[strings.Join(fields, " ")] = true
		}
	}
	return lines
}

// listed is a package as go list -test -deps prints it: one of the module, of
// a dependency or of the standard library, or the variant of one that a
// package's tests build ("P [Q.test]", "Q.test"), which ForTest names.
type listed struct {
	ImportPath string
	ForTest    string
	Imports    []string
	Module     *struct {
		Path string
		Main bool
	}
}

// moduleUsers returns the packages of the module in the working directory
// that depend on a package of one of modules, in their own code or in their
// tests, and the modules among them that some package here uses, both
// sorted. go list fails on a go.mod or go.sum that doesn't hold what the
// packages need, and so does this.
func moduleUsers(modules map[string]bool) (users, used []string, err error) {
	var stderr bytes.Buffer
	cmd := exec.Command("go", "list", "-test", "-deps", "-json=ImportPath,ForTest,Imports,Module", "./...")
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("go list: %w: %s", err, lastLine(stderr.String()))
	}
	pkgs := map[string]listed{}
	for dec := json.NewDecoder(bytes.NewReader(out)); ; {
		var p listed
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, nil, fmt.Errorf("go list: %w", err)
		}
		pkgs[p.ImportPath] = p
	}

	// reaches: whether the package, or one it imports, is of a changed module.
	memo := map[string]bool{}
	var reaches func(path string) bool
	reaches = func(path string) bool {
		if r, ok := memo[path]; ok {
			return r
		}
		p := pkgs[path]
		r := p.Module != nil && modules[p.Module.Path]
		for _, imp := range p.Imports {
			r = reaches(imp) || r
		}
		memo[path] = r
		return r
	}

	userSet, usedSet := map[string]bool{}, map[string]bool{}
	for path, p := range pkgs {
		if p.Module == nil {
			continue
		}
		if modules[p.Module.Path] {
			usedSet[p.Module.Path] = true
		}
		if !p.Module.Main || !reaches(path) {
			continue
		}
		// The package a variant is built for: P for "P [P.test]", for a
		// package rebuilt for P's tests and for the test binary "P.test".
		switch pkg := strings.TrimSuffix(path, ".test"); {
		case p.ForTest != "":
			userSet[p.ForTest] = true
		case pkg != path && isModulePackage(pkgs, pkg):
			userSet[pkg] = true
		default:
			userSet[path] = true
		}
	}
	return slices.Sorted(maps.Keys(userSet)), slices.Sorted(maps.Keys(usedSet)), nil
}

// isModulePackage says whether path is a package of the main module among
// those listed.
func isModulePackage(pkgs map[string]listed, path string) bool {
	p, ok := pkgs[path]
	return ok && p.Module != nil && p.Module.Main
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
