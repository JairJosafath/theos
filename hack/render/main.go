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
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

func main() {
	out := flag.String("out", "", "folder to write the repository into (must not exist)")
	root := flag.String("templates", "templates", "folder with the template repositories")
	in := scaffold.Input{Project: "webshop", SoftwareSystem: "shop"}
	flag.StringVar(&in.Container, "container", "sample-app", "Container name")
	flag.StringVar(&in.DisplayName, "display-name", "Sample App", "Container display name")
	flag.StringVar(&in.Type, "type", "cli", "Container type")
	flag.StringVar(&in.Checks, "checks", "", "when CI runs the full check: EveryChange (the default) or BeforeRelease")
	flag.StringVar(&in.NpmScope, "npm-scope", "@c4factory", "the npm scope the Container's package is named under")
	ci := flag.String("ci", model.CIGiteaActions, "the CI system the repository runs")
	// The Container's spec.operations, each left out when empty (-retention-days -1).
	var ops model.OperationsChoices
	retention := flag.Int("retention-days", -1, "spec.operations.backups.retentionDays; -1 leaves it out")
	flag.StringVar(&ops.CopyToRegion, "copy-to-region", "", "spec.operations.backups.copyToRegion")
	flag.StringVar(&ops.RPO, "rpo", "", "spec.operations.recovery.rpo")
	flag.StringVar(&ops.RTO, "rto", "", "spec.operations.recovery.rto")
	flag.StringVar(&ops.Email, "alert-email", "", "spec.operations.alerts.email")
	flag.Parse()
	if *out == "" || flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: render -out DIR Side=template ...")
		os.Exit(2)
	}
	var err error
	if in.CI, err = model.CIByName(*ci); err != nil {
		fail(err)
	}
	if *retention < -1 || *retention > int(model.MaxRetentionDays) {
		fail(fmt.Errorf("-retention-days %d: 0 to %d, or -1 to leave it out", *retention, model.MaxRetentionDays))
	}
	if *retention >= 0 {
		days := int32(*retention)
		ops.RetentionDays = &days
	}
	if problems := model.OperationsProblems(ops); len(problems) > 0 {
		fail(fmt.Errorf("spec.operations: %s", strings.Join(problems, "; ")))
	}
	in.Operations = model.ResolveOperations(ops)

	layers := make([]scaffold.Layer, 0, flag.NArg())
	for _, arg := range flag.Args() {
		side, name, ok := strings.Cut(arg, "=")
		if !ok {
			fail(fmt.Errorf("%q: want Side=template", arg))
		}
		files, err := scaffold.ReadTree(os.DirFS(filepath.Join(*root, name)))
		if err != nil {
			fail(err)
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
		fail(err)
	}
	for _, p := range result.Files.Paths() {
		f := result.Files[p]
		mode := os.FileMode(0o644)
		if f.Executable {
			mode = 0o755
		}
		dst := filepath.Join(*out, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil { //nolint:gosec // a rendered repository to read
			fail(err)
		}
		if err := os.WriteFile(dst, f.Data, mode); err != nil {
			fail(err)
		}
	}
	fmt.Printf("Rendered %d files from %s into %s\n", len(result.Files), strings.Join(result.ScaffoldedFrom, ", "), *out)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "render:", err)
	os.Exit(1)
}
