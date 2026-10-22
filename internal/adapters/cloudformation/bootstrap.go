// Package cloudformation is the cloudFormation member of ports.Deployer: it
// deploys a CDK assembly through CloudFormation (docs/plans/aws-deploy.md).
package cloudformation

import (
	"cmp"
	"context"
	"embed"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	cfn "github.com/aws/aws-sdk-go-v2/service/cloudformation"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/go-logr/logr"
	"go.yaml.in/yaml/v3"

	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/ports"
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

// templateParameters are the names of the parameters a CloudFormation YAML
// template declares. A yaml.Node decode takes the template's !Ref-style
// tags as they are.
func templateParameters(body string) (map[string]bool, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
		return nil, fmt.Errorf("parsing the bootstrap template: %w", err)
	}
	out := map[string]bool{}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return out, nil
	}
	top := doc.Content[0].Content
	for i := 0; i+1 < len(top); i += 2 {
		if top[i].Value != "Parameters" || top[i+1].Kind != yaml.MappingNode {
			continue
		}
		params := top[i+1].Content
		for j := 0; j+1 < len(params); j += 2 {
			out[params[j].Value] = true
		}
	}
	return out, nil
}

// The bootstrap's stack, its default version parameter, and the parameter
// naming the policies the cfn-exec role gets.
const (
	toolkitStack            = "CDKToolkit"
	defaultBootstrapParam   = "/cdk-bootstrap/hnb659fds/version"
	executionPoliciesParam  = "CloudFormationExecutionPolicies"
	bootstrapChangeSetStart = "c4factory-bootstrap-v"
)

// bootstrap makes sure the account and the target's region run the CDK
// bootstrap the assembly needs (docs/plans/aws-deploy.md, decision 5):
// ready when its version parameter is high enough; else it creates or
// updates CDKToolkit from the embedded template with the credential itself
// and is not ready until that finishes. A credential that may not, or a
// CDKToolkit that failed, is ErrPrerequisiteMissing with the command its
// owner runs.
func (d *Deployer) bootstrap(ctx context.Context, ses *session, a model.CDKAssembly) (ready bool, err error) {
	need := a.BootstrapVersion
	if need == 0 {
		return true, nil
	}
	param := cmp.Or(a.BootstrapVersionParameter, defaultBootstrapParam)
	have, err := bootstrapVersion(ctx, ses.clients.SSM, param)
	if err != nil || have >= need {
		return err == nil, err
	}
	command := fmt.Sprintf("cdk bootstrap aws://%s/%s", ses.id.Account, ses.cfg.Region)
	missing := func(format string, args ...any) error {
		return &ports.Error{Kind: ports.ErrPrerequisiteMissing,
			Cause: fmt.Errorf("the CDK bootstrap of aws://%s/%s is version %d, the release needs %d: %s; "+
				"have the account's owner run %s", ses.id.Account, ses.cfg.Region, have, need,
				fmt.Sprintf(format, args...), command)}
	}
	body, version, err := bootstrapTemplate()
	if err != nil {
		return false, err
	}
	if version < need {
		return false, missing("the operator carries bootstrap version %d", version)
	}
	c := ses.clients.CloudFormation
	st, err := describeStack(ctx, c, toolkitStack)
	if err != nil {
		return false, err
	}
	in := &cfn.CreateChangeSetInput{StackName: aws.String(toolkitStack),
		ChangeSetName: aws.String(bootstrapChangeSetStart + strconv.Itoa(version)),
		ChangeSetType: types.ChangeSetTypeCreate, TemplateBody: aws.String(body),
		Capabilities: []types.Capability{types.CapabilityCapabilityIam, types.CapabilityCapabilityNamedIam},
		Description:  aws.String(fmt.Sprintf("c4factory bootstraps CDK version %d", version))}
	policies := true
	if st != nil {
		status := string(st.StackStatus)
		switch {
		case model.CloudFormationStackBusy(status):
			return false, nil
		// UPDATE_ROLLBACK_COMPLETE runs the version before a failed update
		// and takes an update; a failed first create, a failed rollback or
		// a deleted stack takes a person.
		case st.StackStatus == types.StackStatusRollbackComplete || strings.HasSuffix(status, "_FAILED") ||
			strings.HasPrefix(status, "DELETE_"):
			return false, missing("stack %s is %s (%s)", toolkitStack, status, aws.ToString(st.StackStatusReason))
		case st.StackStatus != types.StackStatusReviewInProgress:
			in.ChangeSetType = types.ChangeSetTypeUpdate
			declared, err := templateParameters(body)
			if err != nil {
				return false, err
			}
			// An update keeps what the stack was given, a scoped
			// execution policy above all, of the parameters the template
			// still declares: CloudFormation refuses one it doesn't.
			for _, p := range st.Parameters {
				key := aws.ToString(p.ParameterKey)
				if !declared[key] {
					continue
				}
				in.Parameters = append(in.Parameters, types.Parameter{ParameterKey: p.ParameterKey,
					UsePreviousValue: aws.Bool(true)})
				policies = policies && key != executionPoliciesParam
			}
		}
	}
	if policies {
		in.Parameters = append(in.Parameters, types.Parameter{ParameterKey: aws.String(executionPoliciesParam),
			ParameterValue: aws.String("arn:" + ses.id.Partition + ":iam::aws:policy/AdministratorAccess")})
	}
	state, id, err := d.changeSet(ctx, c, in)
	switch {
	case errors.Is(err, ports.ErrCredentialRefused):
		return false, missing("the credential may not bootstrap: %v", err)
	case err != nil && !errors.Is(err, ports.ErrUnreachable):
		return false, missing("%v", err)
	case err != nil:
		return false, err
	case state == changeSetEmpty:
		return false, missing("stack %s runs the embedded template, yet %s says %d", toolkitStack, param, have)
	case state == changeSetPending:
		return false, nil
	}
	if _, err := c.ExecuteChangeSet(ctx, &cfn.ExecuteChangeSetInput{ChangeSetName: aws.String(id),
		StackName: aws.String(toolkitStack)}); err != nil {
		if err = portError(err); errors.Is(err, ports.ErrCredentialRefused) {
			return false, missing("the credential may not bootstrap: %v", err)
		}
		return false, err
	}
	logr.FromContextOrDiscard(ctx).Info("Bootstrapping CDK", "type", in.ChangeSetType, "version", version,
		"had", have)
	return false, nil
}

// bootstrapVersion reads the bootstrap's version parameter, 0 when there is
// none.
func bootstrapVersion(ctx context.Context, c SSMAPI, name string) (int, error) {
	out, err := c.GetParameter(ctx, &ssm.GetParameterInput{Name: aws.String(name)})
	if apiCode(err) == "ParameterNotFound" {
		return 0, nil
	}
	if err != nil {
		return 0, portError(fmt.Errorf("reading the CDK bootstrap version %s: %w", name, err))
	}
	if out.Parameter == nil {
		return 0, nil
	}
	v, err := strconv.Atoi(strings.TrimSpace(aws.ToString(out.Parameter.Value)))
	if err != nil {
		return 0, fmt.Errorf("the CDK bootstrap version %s is %q, not a number", name,
			aws.ToString(out.Parameter.Value))
	}
	return v, nil
}
