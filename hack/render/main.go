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

// Command render renders template layers from templates/ into a folder, the way
// the operator scaffolds a Container, so a template author can run the result:
//
//	go run ./hack/render -out /tmp/app -type cli \
//	  Framework=go-cli Standard=go-standard Deployment=gitea-release
//
// The Container's spec.operations is the factory's defaults unless the
// -retention-days, -copy-to-region, -rpo, -rto or -alert-email flags set a
// field, as an AWS Framework renders it into infra/operations.ts.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/scaffold"
)

func main() {
	if err := run(os.Args[0], os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, errFlags) {
			// The flag package already printed the error and the usage.
			os.Exit(2)
		}
		if errors.Is(err, errUsage) {
			fmt.Fprintln(os.Stderr, "usage: render -out DIR Side=template ...")
			os.Exit(2)
		}
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "render:", err)
		os.Exit(1)
	}
}

var (
	errUsage = errors.New("usage")
	// errFlags is a flag that did not parse; the flag set has printed why.
	errFlags = errors.New("bad flags")
)

// run renders what args ask for and prints its summary line to stdout. name
// heads the usage text the flag set prints to stderr.
func run(name string, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	out := flags.String("out", "", "folder to write the repository into (must not exist)")
	root := flags.String("templates", "templates", "folder with the template repositories")
	var in scaffold.Input
	flags.StringVar(&in.Project, "project", "webshop", "Project name, written to .theos.yaml")
	flags.StringVar(&in.SoftwareSystem, "system", "shop", "Software System name, written to .theos.yaml")
	flags.StringVar(&in.Container, "container", "sample-app", "Container name")
	flags.StringVar(&in.DisplayName, "display-name", "Sample App", "Container display name")
	flags.StringVar(&in.Type, "type", "cli", "Container type")
	flags.StringVar(&in.Checks, "checks", "", "when CI runs the full check: EveryChange (the default) or BeforeRelease")
	flags.StringVar(&in.NpmScope, "npm-scope", "@theos", "the npm scope the Container's package is named under")
	ci := flags.String("ci", model.CIGiteaActions, "the CI system the repository runs")
	// The Container's spec.operations, each left out when empty (-retention-days -1).
	var ops model.OperationsChoices
	retention := flags.Int("retention-days", -1, "spec.operations.backups.retentionDays; -1 leaves it out")
	flags.StringVar(&ops.CopyToRegion, "copy-to-region", "", "spec.operations.backups.copyToRegion")
	flags.StringVar(&ops.RPO, "rpo", "", "spec.operations.recovery.rpo")
	flags.StringVar(&ops.RTO, "rto", "", "spec.operations.recovery.rto")
	flags.StringVar(&ops.Email, "alert-email", "", "spec.operations.alerts.email")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return err
		}
		return errFlags
	}
	if *out == "" || flags.NArg() == 0 {
		return errUsage
	}
	var err error
	if in.CI, err = model.CIByName(*ci); err != nil {
		return err
	}
	if *retention < -1 || *retention > int(model.MaxRetentionDays) {
		return fmt.Errorf("-retention-days %d: 0 to %d, or -1 to leave it out", *retention, model.MaxRetentionDays)
	}
	if *retention >= 0 {
		days := int32(*retention)
		ops.RetentionDays = &days
	}
	if problems := model.OperationsProblems(ops); len(problems) > 0 {
		return fmt.Errorf("spec.operations: %s", strings.Join(problems, "; "))
	}
	in.Operations = model.ResolveOperations(ops)

	layers := make([]scaffold.Layer, 0, flags.NArg())
	for _, arg := range flags.Args() {
		side, name, ok := strings.Cut(arg, "=")
		if !ok {
			return fmt.Errorf("%q: want Side=template", arg)
		}
		files, err := scaffold.ReadTree(os.DirFS(filepath.Join(*root, name)))
		if err != nil {
			return err
		}
		if side == model.SideDomain {
			// A Domain's folder is <name>-domain; the layer and the input name
			// the Domain itself (templates_test.go, domainSuffix).
			name = strings.TrimSuffix(name, "-domain")
			in.Domains = append(in.Domains, name)
		}
		layers = append(layers, scaffold.Layer{Side: side, Name: name, Ref: "local", Files: files})
		switch side {
		case "Framework":
			in.Framework = name
		case "Standard":
			in.Standard = name
		case "Compute":
			in.Compute = name
		case "Deployment":
			in.Deployment = name
		case "Connector":
			in.Connectors = append(in.Connectors, name)
		}
	}
	result, err := scaffold.Build(in, layers, scaffold.Options{})
	if err != nil {
		return err
	}
	for _, p := range result.Files.Paths() {
		f := result.Files[p]
		mode := os.FileMode(0o644)
		if f.Executable {
			mode = 0o755
		}
		dst := filepath.Join(*out, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { //nolint:gosec // a rendered repository to read
			return err
		}
		if err := os.WriteFile(dst, f.Data, mode); err != nil { //nolint:gosec // a rendered repository to read
			return err
		}
	}
	_, err = fmt.Fprintf(stdout, "Rendered %d files from %s into %s\n",
		len(result.Files), strings.Join(result.ScaffoldedFrom, ", "), *out)
	return err
}
