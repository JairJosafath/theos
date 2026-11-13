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

// Command gta prints the packages of this module that a set of changed files
// affects, as github.com/digitalocean/gta selects them: the packages the
// files lie in, and every package that depends on one, through its tests too.
// make test and make check test, vet and lint only those
// (hack/test-packages.sh, hack/check.sh; docs/plans/done/selective-check.md).
//
//	go run ./hack/gta [-changed-files FILE] [-base REVISION]
//
// FILE holds the changed files, one a line, relative to the working directory
// or absolute; without it, or with "-", they are read from stdin. The command
// runs in the module's root and prints a line per affected package that still
// exists, sorted by import path:
//
//	changed IMPORTPATH     a changed file lies in the package
//	dependent IMPORTPATH   the package, or its tests, depend on a changed one
//	module IMPORTPATH      the package, or its tests, are built from a module
//	                       whose version go.mod or go.sum changed
//
// gta knows nothing of go.mod and go.sum: a change to them alone selects no
// package there. Here, when one of the two is among the files, they are
// compared with those of the git revision -base names (modules.go), and
// before the packages one of these lines is printed:
//
//	modules MODULE...      the changed modules the module's packages use
//	all REASON             every package is affected: the go or toolchain
//	                       line changed, or the two files can't be compared
//
// It is gta's library and not its command, for these reasons:
//
//   - The command loads every package of every module in the build list
//     ("..."), which this module's go.mod can't do ("go: updates to go.mod
//     needed"). Here gta loads this module's packages alone ("./...").
//   - The command's git differ sees commits only. The callers list the files
//     themselves: commits since the merge base, staged, unstaged and untracked.
//   - gta marks a package changed by a Go file, a file it embeds or a file
//     below its testdata. Tests here also read other files beside them by a
//     relative path, so a package owns every file in its directory and below,
//     down to the next package (the owner type below). A file below testdata
//     stays gta's: it selects the package alone, whose tests read it.
//   - gta (v0.2.0) records what a package's tests import only when the
//     package has Go files besides its tests. A package of tests alone, as
//     test/chart, is never a dependent there; testOnlyDependents adds it.
//
// gta's default traversal is kept: a package whose tests alone import a
// changed one counts as changed for its own dependents too. Its narrower mode
// (-test-transitive=false) misses a test that imports a dependent of the
// changed package.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"go/build"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/digitalocean/gta"
)

func main() {
	changedFiles := flag.String("changed-files", "-", "file listing the changed files, one a line; - reads stdin")
	base := flag.String("base", "", "the git revision a changed go.mod or go.sum is compared with")
	flag.Parse()
	if err := run(*changedFiles, *base, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "gta:", err)
		os.Exit(1)
	}
}

func run(changedFiles, base string, stdin io.Reader, stdout io.Writer) error {
	in := stdin
	if changedFiles != "-" {
		f, err := os.Open(changedFiles) //nolint:gosec // The caller names the list of changed files.
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		in = f
	}
	files, err := readFiles(in)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}
	lines, err := affected(files, base)
	if err != nil {
		return err
	}
	for _, l := range lines {
		if _, err := fmt.Fprintln(stdout, l); err != nil {
			return err
		}
	}
	return nil
}

// readFiles reads one path a line and makes each absolute, as gta's file
// differ wants them. Empty lines are skipped.
func readFiles(r io.Reader) ([]string, error) {
	var files []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.TrimSpace(sc.Text())
		if f == "" {
			continue
		}
		abs, err := filepath.Abs(f)
		if err != nil {
			return nil, err
		}
		files = append(files, abs)
	}
	return files, sc.Err()
}

// affected returns a "changed PKG", "dependent PKG" or "module PKG" line for
// each package of the module in the working directory that the changed files
// (absolute paths) affect and that still exists, sorted by import path. gta
// says which packages the files affect; go.mod and go.sum are compared with
// those of the git revision base, and put a "modules ..." line first, or
// make an "all REASON" line the only one.
func affected(files []string, base string) ([]string, error) {
	root, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	mod, files := modFiles(root, files)
	var modules map[string]bool
	if len(mod) > 0 {
		var all string
		if all, modules, err = moduleChanges(base); err != nil {
			return nil, err
		}
		if all != "" {
			return []string{"all " + all}, nil
		}
	}
	kinds := map[string]string{}
	if len(files) > 0 {
		if kinds, err = selected(root, files); err != nil {
			return nil, err
		}
	}
	var head []string
	if len(modules) > 0 {
		users, used, err := moduleUsers(modules)
		if err != nil {
			return nil, err
		}
		for _, p := range users {
			if _, ok := kinds[p]; !ok {
				kinds[p] = "module"
			}
		}
		if len(used) > 0 {
			head = []string{"modules " + strings.Join(used, " ")}
		}
	}
	lines := make([]string, 0, len(kinds))
	for p, kind := range kinds {
		lines = append(lines, kind+" "+p)
	}
	slices.SortFunc(lines, func(a, b string) int {
		return strings.Compare(a[strings.Index(a, " "):], b[strings.Index(b, " "):])
	})
	return append(head, lines...), nil
}

// selected asks gta which packages of the module in root the changed files
// (absolute paths) affect, and returns each that still exists with its kind:
// changed or dependent.
func selected(root string, files []string) (map[string]string, error) {
	packager := owner{Packager: gta.NewPackager([]string{"./..."}, nil), root: root}
	g, err := gta.New(gta.SetDiffer(gta.NewFileDiffer(files)), gta.SetPackager(packager))
	if err != nil {
		return nil, err
	}
	pkgs, err := g.ChangedPackages()
	if err != nil {
		return nil, err
	}
	// kinds: each affected package that still exists. gta leaves Dir empty for
	// one that is gone: nothing of it is left to test.
	kinds := map[string]string{}
	for _, p := range pkgs.AllChanges {
		if p.Dir != "" {
			kinds[p.ImportPath] = "dependent"
		}
	}
	for _, p := range pkgs.Changes {
		if _, ok := kinds[p.ImportPath]; ok {
			kinds[p.ImportPath] = "changed"
		}
	}
	if len(kinds) > 0 {
		tests, err := testOnlyPackages()
		if err != nil {
			return nil, err
		}
		for _, p := range testOnlyDependents(tests, linked(packager, files, pkgs)) {
			if _, ok := kinds[p]; !ok {
				kinds[p] = "dependent"
			}
		}
	}
	return kinds, nil
}

// linked returns the affected packages whose code other than their tests the
// change reaches, which is what another package's test binary links: the
// dependents gta found, and the packages a changed file lies in unless that
// file is one of its tests.
func linked(packager owner, files []string, pkgs *gta.Packages) map[string]bool {
	set := map[string]bool{}
	for _, dependents := range pkgs.Dependencies {
		for _, p := range dependents {
			set[p.ImportPath] = true
		}
	}
	for _, f := range files {
		switch {
		case strings.HasSuffix(f, "_test.go"):
		case filepath.Ext(f) == ".go":
			if pkg, err := packager.PackageFromDir(filepath.Dir(f)); err == nil {
				set[pkg.ImportPath] = true
			}
		default:
			for _, p := range packager.EmbeddedBy(f) {
				set[p] = true
			}
		}
	}
	return set
}

// testOnlyPackages returns the packages of the module that hold tests and no
// other Go file, each with the packages its tests import.
func testOnlyPackages() (map[string][]string, error) {
	const format = `{{if not (or .GoFiles .CgoFiles)}}{{.ImportPath}}` +
		`{{range .TestImports}} {{.}}{{end}}{{range .XTestImports}} {{.}}{{end}}{{end}}`
	out, err := exec.Command("go", "list", "-e", "-f", format, "./...").Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	tests := map[string][]string{}
	for line := range strings.Lines(string(out)) {
		if fields := strings.Fields(line); len(fields) > 1 {
			tests[fields[0]] = fields[1:]
		}
	}
	return tests, nil
}

// testOnlyDependents returns the packages of tests alone that import a linked
// package. Nothing imports a package without Go files of its own, so those
// they import directly are enough: gta's set already holds every dependent of
// a changed package.
func testOnlyDependents(tests map[string][]string, linked map[string]bool) []string {
	var dependents []string
	for pkg, imports := range tests {
		if slices.ContainsFunc(imports, func(imp string) bool { return linked[imp] }) {
			dependents = append(dependents, pkg)
		}
	}
	return dependents
}

// owner is gta's packager for the module's own packages, with two changes.
//
// A directory counts as a package only when gta loaded it: the Go files of a
// nested module are in no package of this module, and gta fails on them
// ("... not found") instead of skipping them.
//
// A file that is no Go file counts as embedded by the package that owns it:
// the nearest package in its directory or above, inside the module. gta then
// marks that package changed, and its dependents with it.
type owner struct {
	gta.Packager
	root string
}

// PackageFromDir returns the package of dir, or a *build.NoGoError when the
// directory holds no package gta loaded.
func (o owner) PackageFromDir(dir string) (*gta.Package, error) {
	pkg, err := o.Packager.PackageFromDir(dir)
	if err != nil {
		return pkg, err
	}
	if _, err := o.PackageFromImport(pkg.ImportPath); err != nil {
		return pkg, &build.NoGoError{Dir: dir}
	}
	return pkg, nil
}

// EmbeddedBy returns the packages that embed file and, when it is no Go file,
// the package that owns it. A file below a testdata directory is left to gta,
// which selects the package it lies in and no dependent: only that package's
// tests read it.
func (o owner) EmbeddedBy(file string) []string {
	pkgs := o.Packager.EmbeddedBy(file)
	if filepath.Ext(file) == ".go" || belowTestdata(o.root, file) {
		return pkgs
	}
	if p := o.owning(filepath.Dir(file)); p != "" {
		pkgs = append(pkgs, p)
	}
	return pkgs
}

// belowTestdata says whether file lies below a directory named testdata
// inside root.
func belowTestdata(root, file string) bool {
	rel, err := filepath.Rel(root, filepath.Dir(file))
	if err != nil {
		return false
	}
	return slices.Contains(strings.Split(filepath.ToSlash(rel), "/"), "testdata")
}

// owning returns the import path of the nearest package in dir or above it,
// inside the module's root; "" when there is none.
func (o owner) owning(dir string) string {
	for {
		rel, err := filepath.Rel(o.root, dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return ""
		}
		// Any error means no package here: no Go files, or a directory that
		// is gone with the file.
		if pkg, err := o.PackageFromDir(dir); err == nil {
			return pkg.ImportPath
		}
		if rel == "." {
			return ""
		}
		dir = filepath.Dir(dir)
	}
}
