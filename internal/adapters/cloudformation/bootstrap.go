// Package cloudformation is the cloudFormation member of ports.Deployer: it
// deploys a CDK assembly through CloudFormation (docs/plans/aws-deploy.md).
package cloudformation

import (
	"embed"
	"fmt"
	"regexp"
	"strconv"
)

// bootstrapTemplates holds the CDK bootstrap template of the pinned aws-cdk
// CLI, bootstrap-template-v<N>.yaml, N its bootstrap version; refresh it with
// hack/cdk-bootstrap-template.sh.
//
//go:embed bootstrap-template-v*.yaml
var bootstrapTemplates embed.FS

var bootstrapName = regexp.MustCompile(`^bootstrap-template-v(\d+)\.yaml$`)

// bootstrapTemplate returns the embedded bootstrap template body and its
// bootstrap version. The version comes from the file name because the YAML's
// !Ref-style tags are not worth a parser here.
func bootstrapTemplate() (body string, version int, err error) {
	entries, err := bootstrapTemplates.ReadDir(".")
	if err != nil {
		return "", 0, err
	}
	for _, e := range entries {
		m := bootstrapName.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		version, err = strconv.Atoi(m[1])
		if err != nil {
			return "", 0, err
		}
		b, err := bootstrapTemplates.ReadFile(e.Name())
		if err != nil {
			return "", 0, err
		}
		return string(b), version, nil
	}
	return "", 0, fmt.Errorf("no bootstrap-template-v<N>.yaml embedded")
}
