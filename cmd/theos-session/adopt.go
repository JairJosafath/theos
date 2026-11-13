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
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/scaffold"
)

const adoptUsage = `theos-session adopt reads a repository you already have and proposes the model
that has the factory adopt it (docs/plans/done/adopt-existing.md). It applies
nothing and talks to no cluster.

Usage:
  theos-session adopt --project <namespace> --git-provider <name> [flags] > model.yaml

It prints the model as YAML: a SoftwareSystem, the Container that adopts the
repository with the check command and the CI checks it found, and a Crew that
holds every change for a person. On standard error it says what to do next,
what it could not tell, and the .theos.yaml to commit to the repository's
main first: that file is how the repository's owner says the project may work
there.

Flags:
`

// adoptFlags are what a person says of the repository they bring.
type adoptFlags struct {
	dir, project, system, name, gitProvider string
	writeIdentity                           bool
}

// kubeName is a name the API server takes for a Container: a DNS label.
var kubeName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// adoptMain runs theos-session adopt with args, and returns its exit code.
func adoptMain(args []string, stdout, stderr io.Writer) int {
	var f adoptFlags
	set := flag.NewFlagSet("adopt", flag.ContinueOnError)
	set.SetOutput(stderr)
	set.StringVar(&f.dir, "dir", ".", "The repository's clone")
	set.StringVar(&f.project, "project", "", "The project's namespace (required)")
	set.StringVar(&f.gitProvider, "git-provider", "",
		"The project's GitProvider, as kubectl -n <project> get gitproviders names it (required)")
	set.StringVar(&f.name, "name", "", "The Container's name, which is the repository's (default: the clone's folder)")
	set.StringVar(&f.system, "system", "", "The SoftwareSystem it belongs to (default: the project's name)")
	set.BoolVar(&f.writeIdentity, "write-identity", false,
		"Write .theos.yaml into the clone, for you to commit and push to main")
	set.Usage = func() {
		_, _ = io.WriteString(stderr, adoptUsage)
		set.PrintDefaults()
	}
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if err := adopt(f, stdout, stderr); err != nil {
		_, _ = io.WriteString(stderr, "theos-session adopt: "+err.Error()+"\n")
		return 1
	}
	return 0
}

// adopt reads the repository at f.dir and prints its proposal: the model on
// stdout, and what to do with it on stderr.
func adopt(f adoptFlags, stdout, stderr io.Writer) error {
	dir, err := filepath.Abs(f.dir)
	if err != nil {
		return err
	}
	if f.name == "" {
		f.name = strings.ToLower(filepath.Base(dir))
	}
	if f.system == "" {
		f.system = f.project
	}
	for _, named := range []struct{ flag, value string }{{"--project", f.project},
		{"--git-provider", f.gitProvider}, {"--name", f.name}, {"--system", f.system}} {
		switch {
		case named.value == "":
			return fmt.Errorf("%s is required", named.flag)
		case !kubeName.MatchString(named.value):
			return fmt.Errorf("%s %q is no name the cluster takes: lower-case letters, digits and -, at most 63",
				named.flag, named.value)
		}
	}
	paths, err := repoFiles(dir)
	if err != nil {
		return err
	}
	read := map[string]string{}
	for _, p := range model.AdoptionReads(paths) {
		data, err := os.ReadFile(filepath.Join(dir, p)) //nolint:gosec // a path of the person's own clone
		if err != nil {
			return err
		}
		read[p] = string(data)
	}
	finding := model.DetectRepository(paths, read)
	manifests, identity := model.AdoptionProposal(model.AdoptionInput{Project: f.project, System: f.system,
		Container: f.name, GitProvider: f.gitProvider}, finding)
	if _, err := io.WriteString(stdout, manifests); err != nil {
		return err
	}

	var steps strings.Builder
	fmt.Fprintf(&steps, "Read %d files of %s.\n", len(paths), dir)
	for _, note := range finding.Notes {
		fmt.Fprintln(&steps, "Look at this:", note)
	}
	fmt.Fprintln(&steps, "\nNext:")
	if f.writeIdentity {
		identityPath := filepath.Join(dir, scaffold.IdentityPath)
		if err := os.WriteFile(identityPath, []byte(identity), 0o644); err != nil { //nolint:gosec // a file of the repository
			return err
		}
		fmt.Fprintf(&steps, "1. Commit %s, which this wrote, and push it to main.\n", scaffold.IdentityPath)
	} else {
		fmt.Fprintf(&steps, "1. Commit this as %s on the repository's main (or run again with --write-identity):\n\n",
			scaffold.IdentityPath)
		for line := range strings.SplitSeq(strings.TrimSuffix(identity, "\n"), "\n") {
			fmt.Fprintln(&steps, "     "+line)
		}
		fmt.Fprintln(&steps)
	}
	fmt.Fprintf(&steps, "2. Have the repository in the organization GitProvider %s names, as %s.\n", f.gitProvider,
		f.name)
	fmt.Fprintln(&steps, "3. Read the model above, change what is wrong, and apply it: kubectl apply -f model.yaml")
	fmt.Fprintf(&steps, "4. kubectl -n %s get container %s "+
		"-o jsonpath='{.status.conditions[?(@.type==\"Protected\")].message}'\n"+
		"   says whether main reports the checks the model names, or which it reports instead. When it reports\n"+
		"   none, the repository's workflows don't run on this Git host: each job's runs-on must be a label of\n"+
		"   the project's CI runners, which GitProvider %s lists in its spec.ci.\n", f.project, f.name, f.gitProvider)
	_, err = io.WriteString(stderr, steps.String())
	return err
}

// repoFiles lists the files of the repository at dir, relative to it: the
// ones Git tracks, or every file but Git's own when dir is no clone.
func repoFiles(dir string) ([]string, error) {
	out, err := exec.Command("git", "-C", dir, "ls-files", "-z").Output() //nolint:gosec // the person's own clone
	if err == nil && len(out) > 0 {
		return strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00"), nil
	}
	var paths []string
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("%s has no files: give the repository's clone with --dir", dir)
	}
	return paths, nil
}
