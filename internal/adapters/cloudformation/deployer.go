package cloudformation

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cfn "github.com/aws/aws-sdk-go-v2/service/cloudformation"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation/types"

	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/ports"
)

// maxAssemblyFile bounds what the member reads of a manifest, template or
// asset manifest into memory.
const maxAssemblyFile = 16 << 20

// Deployer ships a release's CDK assembly to an AWS account through
// CloudFormation. CloudFormation runs the deploy, so Ship starts a stack's
// change set and returns; each later call takes the next stack.
type Deployer struct {
	clients NewClients
	// polls bounds the DescribeChangeSet calls waiting for a change set to
	// be ready, delay between them; sleep waits.
	polls int
	delay time.Duration
	sleep func(ctx context.Context, d time.Duration) error
}

var _ ports.Deployer = (*Deployer)(nil)

// New returns the cloudFormation member's Deployer, on AWS when clients is
// nil.
func New(clients NewClients) *Deployer {
	if clients == nil {
		clients = AWSClients
	}
	return &Deployer{clients: clients, polls: 15, delay: 2 * time.Second, sleep: sleep}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// session opens the target's credential, without calling AWS.
func (d *Deployer) session(t ports.Target) (*session, error) {
	cfg, err := targetConfig(t)
	if err != nil {
		return nil, err
	}
	return &session{target: t, cfg: cfg, clients: d.clients(cfg), newC: d.clients}, nil
}

// Check reads the credential's identity and refuses an account other than
// the target's account setting, naming both.
func (d *Deployer) Check(ctx context.Context, t ports.Target) error {
	s, err := d.session(t)
	if err != nil {
		return err
	}
	return s.whoAmI(ctx)
}

// Ship takes the next step of shipping the tier's assembly of s: the
// bootstrap when the account lacks it, else the first stack, in the
// assembly's order, that doesn't run or deploy s.Version yet: publish its
// assets, then create and execute its change set. A stack CloudFormation is
// working on waits; nothing is ever deleted but an empty change set.
func (d *Deployer) Ship(ctx context.Context, t ports.Target, s ports.Shipment) error {
	if len(s.Files) == 0 {
		return &ports.Error{Kind: ports.ErrArtifactMissing,
			Cause: fmt.Errorf("release %s of %s has no files: CloudFormation ships its cloud assembly", s.Version,
				s.App)}
	}
	files := releaseFiles(s.Files)
	assembly, err := model.ParseCDKAssembly(t.Tier, func(name string) ([]byte, error) {
		return files.read(ctx, name)
	})
	if err != nil {
		if errors.Is(err, ports.ErrArtifactMissing) {
			return &ports.Error{Kind: ports.ErrArtifactMissing, Cause: err}
		}
		return fmt.Errorf("release %s of %s: %w", s.Version, s.App, err)
	}
	ses, err := d.session(t)
	if err != nil {
		return err
	}
	if err := ses.whoAmI(ctx); err != nil {
		return err
	}
	ready, err := d.bootstrap(ctx, ses, assembly)
	if err != nil || !ready {
		return err
	}
	for _, stack := range assembly.Stacks {
		done, err := d.shipStack(ctx, ses, files, stack, s)
		if err != nil || !done {
			return err
		}
	}
	return nil
}

// files are a release's files by name.
type files map[string]ports.File

func releaseFiles(fs []ports.File) files {
	out := make(files, len(fs))
	for _, f := range fs {
		out[f.Name] = f
	}
	return out
}

// get is the release file name, ErrArtifactMissing when the release lacks it.
func (fs files) get(name string) (ports.File, error) {
	f, ok := fs[name]
	if !ok {
		return ports.File{}, &ports.Error{Kind: ports.ErrArtifactMissing,
			Cause: fmt.Errorf("the release lacks %s, a file of its cloud assembly", name)}
	}
	return f, nil
}

// read reads the release file name whole.
func (fs files) read(ctx context.Context, name string) ([]byte, error) {
	f, err := fs.get(name)
	if err != nil {
		return nil, err
	}
	body, _, err := f.Open(ctx)
	if err != nil {
		return nil, fmt.Errorf("opening release file %s: %w", name, err)
	}
	defer func() { _ = body.Close() }()
	var b bytes.Buffer
	if _, err := io.Copy(&b, io.LimitReader(body, maxAssemblyFile+1)); err != nil {
		return nil, fmt.Errorf("reading release file %s: %w", name, err)
	}
	if b.Len() > maxAssemblyFile {
		return nil, fmt.Errorf("release file %s is over %d bytes", name, maxAssemblyFile)
	}
	return b.Bytes(), nil
}

// State reports the app's stacks at the target, the tier's: what
// model.CloudFormationDeployState makes of each stack's status and newest
// stack-level event. CloudFormation runs one version, so version is ignored.
func (d *Deployer) State(ctx context.Context, t ports.Target, app, _ string) (ports.DeployState, error) {
	ses, err := d.session(t)
	if err != nil {
		return ports.DeployState{}, err
	}
	stacks, err := appStacks(ctx, ses.clients.CloudFormation, app, t.Tier)
	if err != nil {
		return ports.DeployState{}, err
	}
	facts := make([]model.CloudFormationStackFacts, 0, len(stacks))
	for _, st := range stacks {
		f, err := stackFacts(ctx, ses.clients.CloudFormation, st)
		if err != nil {
			return ports.DeployState{}, err
		}
		facts = append(facts, f)
	}
	run := model.CloudFormationDeployState(facts)
	return ports.DeployState{Version: run.Version, Phase: run.Phase, Health: run.Health, URL: run.URL,
		Reason: run.Reason, Message: run.Message}, nil
}

// appStacks are the stacks tagged for app at tier, oldest first: the
// assembly's order, as the factory created them in it.
func appStacks(ctx context.Context, c CloudFormationAPI, app, tier string) ([]types.Stack, error) {
	var out []types.Stack
	var next *string
	for {
		page, err := c.DescribeStacks(ctx, &cfn.DescribeStacksInput{NextToken: next})
		if err != nil {
			return nil, portError(err)
		}
		for _, st := range page.Stacks {
			tags := stackTags(st)
			if tags[model.CloudFormationTagApp] != app {
				continue
			}
			if tt, ok := tags[model.CloudFormationTagTier]; ok && tt != tier {
				continue
			}
			out = append(out, st)
		}
		if next = page.NextToken; aws.ToString(next) == "" {
			break
		}
	}
	slices.SortStableFunc(out, func(a, b types.Stack) int {
		return cmp.Or(aws.ToTime(a.CreationTime).Compare(aws.ToTime(b.CreationTime)),
			cmp.Compare(aws.ToString(a.StackName), aws.ToString(b.StackName)))
	})
	return out, nil
}

// describeStack is the stack name, nil when there is none.
func describeStack(ctx context.Context, c CloudFormationAPI, name string) (*types.Stack, error) {
	out, err := c.DescribeStacks(ctx, &cfn.DescribeStacksInput{StackName: aws.String(name)})
	if stackMissing(err) {
		return nil, nil
	}
	if err != nil {
		return nil, portError(err)
	}
	for i := range out.Stacks {
		if aws.ToString(out.Stacks[i].StackName) == name {
			return &out.Stacks[i], nil
		}
	}
	return nil, nil
}

// maxEventPages bounds the pages of events read for a stack-level one.
const maxEventPages = 5

// stackFacts are what the model decides a stack's deploy on: its status,
// tags, outputs, parameters and the token of its newest stack-level event.
func stackFacts(ctx context.Context, c CloudFormationAPI, st types.Stack) (model.CloudFormationStackFacts, error) {
	name := aws.ToString(st.StackName)
	f := model.CloudFormationStackFacts{Name: name, Status: string(st.StackStatus),
		StatusReason: aws.ToString(st.StackStatusReason), VersionTag: stackTags(st)[model.CloudFormationTagVersion],
		Outputs: map[string]string{}, Parameters: map[string]string{}}
	for _, o := range st.Outputs {
		f.Outputs[aws.ToString(o.OutputKey)] = aws.ToString(o.OutputValue)
	}
	for _, p := range st.Parameters {
		f.Parameters[aws.ToString(p.ParameterKey)] = aws.ToString(p.ParameterValue)
	}
	id := cmp.Or(aws.ToString(st.StackId), name)
	var next *string
	for range maxEventPages {
		page, err := c.DescribeStackEvents(ctx, &cfn.DescribeStackEventsInput{StackName: aws.String(id),
			NextToken: next})
		if err != nil {
			return f, portError(err)
		}
		// Events come newest first.
		for _, e := range page.StackEvents {
			if aws.ToString(e.LogicalResourceId) == name && aws.ToString(e.ResourceType) == stackResourceType {
				f.EventToken = aws.ToString(e.ClientRequestToken)
				return f, nil
			}
		}
		if next = page.NextToken; aws.ToString(next) == "" {
			break
		}
	}
	return f, nil
}

const stackResourceType = "AWS::CloudFormation::Stack"

func stackTags(st types.Stack) map[string]string {
	out := make(map[string]string, len(st.Tags))
	for _, t := range st.Tags {
		out[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	return out
}
