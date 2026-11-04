package cloudformation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	cfn "github.com/aws/aws-sdk-go-v2/service/cloudformation"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	"github.com/go-logr/logr"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// capabilities are what a CDK stack's change set acknowledges, as cdk
// deploy does.
var capabilities = []types.Capability{types.CapabilityCapabilityIam, types.CapabilityCapabilityNamedIam,
	types.CapabilityCapabilityAutoExpand}

// shipStack takes the next step of shipping s's stack: done when the stack
// runs or deploys s.Version, or its change set was empty, so the next stack
// is due.
func (d *Deployer) shipStack(ctx context.Context, ses *session, fs files, stack model.CDKStack,
	stacks int, s ports.Shipment) (done bool, err error) {
	env := ses.env(stack)
	st, err := describeStack(ctx, ses.clients.CloudFormation, stack.StackName)
	if err != nil {
		return false, err
	}
	if st != nil {
		tags := stackTags(*st)
		if model.CloudFormationStackTaken(tags, s.App) {
			return false, &StackTakenError{Stack: stack.StackName, App: s.App, Owner: tags[model.CloudFormationTagApp]}
		}
		status := string(st.StackStatus)
		if model.CloudFormationStackBusy(status) {
			return false, nil
		}
		if st.StackStatus == types.StackStatusRollbackComplete {
			return false, fmt.Errorf("stack %s failed its first create and is %s, which CloudFormation can't "+
				"update: delete the stack and the factory creates it again (%s)", stack.StackName, status,
				aws.ToString(st.StackStatusReason))
		}
		facts, err := stackFacts(ctx, ses.clients.CloudFormation, *st)
		if err != nil {
			return false, err
		}
		run := model.CloudFormationDeployState([]model.CloudFormationStackFacts{facts}, s.Version)
		if run.Version == s.Version && (run.Phase == model.DeployPhaseDeployed ||
			run.Phase == model.DeployPhaseDeploying) {
			return true, d.protect(ctx, ses, env, stack, st)
		}
	}
	given, image, err := imageParameter(ses.target.Parameters, stack, env, s)
	if err != nil {
		return false, err
	}
	params, missing := model.CDKChangeSetParameters(stack.Parameters, given)
	if len(missing) > 0 {
		return false, &ports.Error{Kind: ports.ErrPrerequisiteMissing,
			Cause: fmt.Errorf("stack %s needs the parameters %s, which have no default: give them in the "+
				"deploy target's parameters", stack.StackName, strings.Join(missing, ", "))}
	}
	if err := publish(ctx, ses, fs, stack, env); err != nil {
		return false, err
	}
	if image != nil {
		if err := publishImage(ctx, ses, s, *image, env.Region); err != nil {
			return false, err
		}
	}
	deploy, err := ses.assume(ctx, env.Substitute(stack.AssumeRoleArn), env.Region)
	if err != nil {
		return false, err
	}
	in, err := changeSetInput(ses.target, stack, env, st, params, stacks, s)
	if err != nil {
		return false, err
	}
	log := logr.FromContextOrDiscard(ctx).WithValues("stack", stack.StackName, "changeSet", *in.ChangeSetName)
	state, id, err := d.changeSet(ctx, deploy.CloudFormation, in)
	switch {
	case err != nil:
		return false, err
	case state == changeSetEmpty:
		log.Info("Deleted an empty change set: the stack runs the version")
		return true, nil
	case state == changeSetPending:
		return false, nil
	}
	token, err := model.CDKClientRequestToken(s.Version, tokenSuffix(id))
	if err != nil {
		return false, err
	}
	if _, err := deploy.CloudFormation.ExecuteChangeSet(ctx, &cfn.ExecuteChangeSetInput{ChangeSetName: aws.String(id),
		StackName: aws.String(stack.StackName), ClientRequestToken: aws.String(token)}); err != nil {
		return false, portError(fmt.Errorf("executing change set %s of stack %s: %w", *in.ChangeSetName,
			stack.StackName, err))
	}
	log.Info("Executed change set", "version", s.Version, "token", token)
	return false, d.protect(ctx, ses, env, stack, st)
}

// imageParameter is given, a target's parameters, with ImageUri the copy of
// s's image in the bootstrap's ECR repository when stack runs the release's
// image; image is where that copy goes, nil for a stack that runs none. A
// shipment without an image is ErrArtifactMissing.
func imageParameter(given map[string]string, stack model.CDKStack, env model.CDKEnv,
	s ports.Shipment) (map[string]string, *model.CDKImageCopy, error) {
	if !model.CDKTakesImage(stack.Parameters) {
		return given, nil, nil
	}
	if s.Image == "" || s.CopyImage == nil {
		return nil, nil, &ports.Error{Kind: ports.ErrArtifactMissing,
			Cause: fmt.Errorf("stack %s runs the release's image, and release %s of %s has none: its release "+
				"builds and pushes it", stack.StackName, s.Version, s.App)}
	}
	q, err := model.CDKQualifier(stack.BootstrapVersionParameter)
	if err != nil {
		return nil, nil, fmt.Errorf("stack %s: %w", stack.StackName, err)
	}
	cp, err := env.ImageCopy(s.Image, q)
	if err != nil {
		return nil, nil, fmt.Errorf("release %s of %s: %w", s.Version, s.App, err)
	}
	out := make(map[string]string, len(given)+1)
	maps.Copy(out, given)
	out[model.CDKImageURIParameter] = cp.URI
	return out, &cp, nil
}

// changeSetInput is the change set that ships s's stack: CREATE or UPDATE
// as the stack exists, its template from the published asset, run as the
// cfn-exec role, with params and the assembly's tags and the factory's,
// stacks the number of stacks of the assembly.
func changeSetInput(t ports.Target, stack model.CDKStack, env model.CDKEnv, st *types.Stack,
	params []model.CDKParameterValue, stacks int, s ports.Shipment) (*cfn.CreateChangeSetInput, error) {
	name, err := model.CDKChangeSetName(s.Version)
	if err != nil {
		return nil, err
	}
	bucket, key, err := env.S3Object(stack.TemplateObjectURL)
	if err != nil {
		return nil, fmt.Errorf("stack %s: %w", stack.StackName, err)
	}
	typ := types.ChangeSetTypeUpdate
	if st == nil || st.StackStatus == types.StackStatusReviewInProgress {
		typ = types.ChangeSetTypeCreate
	}
	in := &cfn.CreateChangeSetInput{StackName: aws.String(stack.StackName), ChangeSetName: aws.String(name),
		ChangeSetType: typ, TemplateURL: aws.String(env.TemplateURL(bucket, key)), Capabilities: capabilities,
		Description: aws.String(fmt.Sprintf("theos ships %s %s", s.App, s.Version))}
	if stack.ExecutionRoleArn != "" {
		in.RoleARN = aws.String(env.Substitute(stack.ExecutionRoleArn))
	}
	for _, p := range params {
		in.Parameters = append(in.Parameters, types.Parameter{ParameterKey: aws.String(p.Name),
			ParameterValue: aws.String(p.Value)})
	}
	tags := model.CloudFormationStackTags(stack.Tags, s.App, t.Tier, s.Version, stacks)
	for _, k := range slices.Sorted(maps.Keys(tags)) {
		in.Tags = append(in.Tags, types.Tag{Key: aws.String(k), Value: aws.String(tags[k])})
	}
	return in, nil
}

// protect turns the stack's termination protection on when the assembly
// asks for it and st, the stack as read before its change set, didn't have
// it (nil: the change set created it), as cdk deploy does. It never turns
// it off.
func (d *Deployer) protect(ctx context.Context, ses *session, env model.CDKEnv, stack model.CDKStack,
	st *types.Stack) error {
	if !stack.TerminationProtection || st != nil && aws.ToBool(st.EnableTerminationProtection) {
		return nil
	}
	deploy, err := ses.assume(ctx, env.Substitute(stack.AssumeRoleArn), env.Region)
	if err != nil {
		return err
	}
	if _, err := deploy.CloudFormation.UpdateTerminationProtection(ctx, &cfn.UpdateTerminationProtectionInput{
		StackName: aws.String(stack.StackName), EnableTerminationProtection: aws.Bool(true)}); err != nil {
		return portError(fmt.Errorf("protecting stack %s: %w", stack.StackName, err))
	}
	logr.FromContextOrDiscard(ctx).Info("Turned termination protection on", "stack", stack.StackName)
	return nil
}

// tokenSuffix tells executions of one version apart: 8 hex characters of
// the change set's id, which is new each time.
func tokenSuffix(changeSetID string) string {
	sum := sha256.Sum256([]byte(changeSetID))
	return hex.EncodeToString(sum[:4])
}

type changeSetState int

const (
	// changeSetPending: not ready yet, or being executed; look again on the
	// next poll.
	changeSetPending changeSetState = iota
	// changeSetReady: created and available to execute.
	changeSetReady
	// changeSetEmpty: it changed nothing and was deleted.
	changeSetEmpty
)

// Reasons CloudFormation gives a change set that changes nothing.
var emptyReasons = []string{"didn't contain changes", "No updates are to be performed"}

// changeSet makes sure in's change set exists, creating it when it doesn't,
// and waits a bounded while for it to be ready, returning its id. An empty
// one is deleted; a failed one is deleted and its reason returned, so the
// next poll tries afresh.
func (d *Deployer) changeSet(ctx context.Context, c CloudFormationAPI, in *cfn.CreateChangeSetInput) (
	changeSetState, string, error) {
	desc := &cfn.DescribeChangeSetInput{ChangeSetName: in.ChangeSetName, StackName: in.StackName}
	name, stack := aws.ToString(in.ChangeSetName), aws.ToString(in.StackName)
	wait := false
	for i := range d.polls + 1 {
		if wait {
			if err := d.sleep(ctx, d.delay); err != nil {
				return changeSetPending, "", err
			}
		}
		wait = true
		out, err := c.DescribeChangeSet(ctx, desc)
		if code := apiCode(err); code == "ChangeSetNotFound" || stackMissing(err) {
			if i > 0 {
				return changeSetPending, "", nil
			}
			if _, err := c.CreateChangeSet(ctx, in); err != nil {
				return changeSetPending, "", portError(fmt.Errorf("creating change set %s of stack %s: %w", name,
					stack, err))
			}
			logr.FromContextOrDiscard(ctx).Info("Created change set", "stack", stack, "changeSet", name,
				"type", in.ChangeSetType)
			wait = false
			continue
		}
		if err != nil {
			return changeSetPending, "", portError(err)
		}
		id := aws.ToString(out.ChangeSetId)
		switch out.Status {
		case types.ChangeSetStatusCreateComplete:
			switch out.ExecutionStatus {
			case types.ExecutionStatusAvailable:
				return changeSetReady, id, nil
			case types.ExecutionStatusExecuteInProgress, types.ExecutionStatusExecuteComplete:
				return changeSetPending, id, nil
			}
			// Obsolete or failed to execute: start again on the next poll.
			return changeSetPending, id, deleteChangeSet(ctx, c, id, stack)
		case types.ChangeSetStatusFailed:
			reason := aws.ToString(out.StatusReason)
			if err := deleteChangeSet(ctx, c, id, stack); err != nil {
				return changeSetPending, id, err
			}
			for _, r := range emptyReasons {
				if strings.Contains(reason, r) {
					return changeSetEmpty, id, nil
				}
			}
			return changeSetPending, id, fmt.Errorf("change set %s of stack %s failed: %s", name, stack, reason)
		case types.ChangeSetStatusCreatePending, types.ChangeSetStatusCreateInProgress:
			continue
		}
		return changeSetPending, id, nil
	}
	return changeSetPending, "", nil
}

func deleteChangeSet(ctx context.Context, c CloudFormationAPI, id, stack string) error {
	if _, err := c.DeleteChangeSet(ctx, &cfn.DeleteChangeSetInput{ChangeSetName: aws.String(id),
		StackName: aws.String(stack)}); err != nil {
		return portError(fmt.Errorf("deleting change set %s of stack %s: %w", id, stack, err))
	}
	return nil
}
