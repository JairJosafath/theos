// Package awsenv is the aws member of a TestEnvironment: one EC2 machine per
// lease in the user's AWS account, Linux or GPU, from the environment's AMI
// (docs/plans/test-environments.md, T5, T9 to T11). The machine gets no
// instance role and no SSH key, starts in the environment's subnet with a
// public address under a security group of the operator's own (theos-env's
// port in; DNS and public HTTPS out), and has its metadata service turned
// off before the lease is Ready. Its user data downloads theos-env, serves
// it on HTTPS's port with a certificate derived from the lease's token
// (model.TestEnvCertificate), which the run pins, and shuts the machine
// down, which terminates it, at the lease's deadline. Machines are tagged
// with the lease, its holder and the environment, found by tag after a
// restart, and terminated on Return. A lease costs the on-demand price,
// from the Price List, for the time it is held.
package awsenv

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// Tags on each machine, its volumes and its network interface, and on the
// environment's security group.
const (
	// TagLease is the lease's ID.
	TagLease = "theos.factory.io/lease"
	// TagHolder names the Task whose run holds the lease.
	TagHolder = "theos.factory.io/lease-holder"
	// TagEnvironment is the TestEnvironment's namespace/name, for people.
	TagEnvironment = "theos.factory.io/test-environment"
	// TagEnvironmentUID is the TestEnvironment's UID, which Leases lists
	// by, so two factories that share an account and an environment's name
	// never return each other's machines.
	TagEnvironmentUID = "theos.factory.io/test-environment-uid"
	// TagDeadline is when the lease ends at the latest, RFC 3339.
	TagDeadline = "theos.factory.io/lease-deadline"
	// TagPrice is the on-demand price the lease was taken at, in millionths
	// of a US dollar an hour.
	TagPrice = "theos.factory.io/price-micro-usd-per-hour"
	// TagKey is the public key theos-env serves with (model.TestEnvKeyPin),
	// which the operator checks before it turns the metadata service off.
	TagKey = "theos.factory.io/theos-env-key"
)

// minimumBilled is the least EC2 bills a Linux machine for.
const minimumBilled = time.Minute

// liveStates are the states of a machine that holds its place.
var liveStates = []string{string(types.InstanceStateNamePending), string(types.InstanceStateNameRunning),
	string(types.InstanceStateNameStopping), string(types.InstanceStateNameStopped)}

// Probe asks theos-env at endpoint whether it answers, as the lease's own:
// with the key keyPin names (TagKey).
type Probe func(ctx context.Context, endpoint, keyPin string) error

// Provider leases EC2 machines with each environment's own credential.
type Provider struct {
	clients NewClients
	// toolsURL is where a machine downloads theos-env, ${arch} its
	// architecture (amd64, arm64), with a SHA256SUMS next to it.
	toolsURL string
	// runnerURL is where a machine that runs a CI job downloads the CI
	// runner, as toolsURL (ci.go).
	runnerURL string
	probe     Probe
	now       func() time.Time

	mu sync.Mutex
	// prices are the on-demand prices read, by region and instance type.
	prices map[string]int64
}

var (
	_ ports.TestEnvironments           = (*Provider)(nil)
	_ ports.TestEnvironmentCredentials = (*Provider)(nil)
	_ ports.TestEnvironmentReleaser    = (*Provider)(nil)
)

// New returns the aws member's provider, on AWS when clients is nil, whose
// machines download theos-env from toolsURL.
func New(clients NewClients, toolsURL string) *Provider {
	if clients == nil {
		clients = AWSClients
	}
	return &Provider{clients: clients, toolsURL: toolsURL, probe: PinnedProbe, now: time.Now}
}

// session opens env's credential, without calling AWS.
func (p *Provider) session(env ports.Environment) (*session, error) {
	if env.AWS == nil {
		return nil, fmt.Errorf("test environment %s/%s has no aws member", env.Namespace, env.Name)
	}
	cfg, err := config(env)
	if err != nil {
		return nil, err
	}
	return &session{env: env, clients: p.clients(cfg)}, nil
}

// CheckCredentials reads the credential's identity, refuses an account
// other than the environment's, and reads the instance type's price, which
// the budget needs.
func (p *Provider) CheckCredentials(ctx context.Context, env ports.Environment) error {
	s, err := p.session(env)
	if err != nil {
		return err
	}
	if err := s.whoAmI(ctx); err != nil {
		return err
	}
	_, err = p.price(ctx, s)
	return err
}

// Lease returns the machine of req's ID, moved on as far as it goes, or
// launches one. A launch AWS answers with a failure of its own (no capacity,
// no such AMI or subnet) is a Failed lease with AWS's message; a refused
// credential or an AWS that can't be reached is the error.
func (p *Provider) Lease(ctx context.Context, req ports.LeaseRequest) (ports.Lease, error) {
	return p.lease(ctx, req, nil)
}

// lease is Lease of a machine that serves theos-env, or, with runner, one
// that runs that CI runner (LeaseRunner).
func (p *Provider) lease(ctx context.Context, req ports.LeaseRequest, runner *ports.JobRunner) (ports.Lease, error) {
	if !model.LeaseTokenSafe(req.Token) {
		return ports.Lease{}, errors.New("a lease's token must be letters, digits, '-', '_' and '.'")
	}
	certPEM, _, err := model.TestEnvCertificate(req.Token)
	if err != nil {
		return ports.Lease{}, err
	}
	s, err := p.session(req.Env)
	if err != nil {
		return ports.Lease{}, err
	}
	inst, err := s.find(ctx, req.ID)
	if err != nil {
		return ports.Lease{}, err
	}
	if inst == nil {
		if inst, err = p.launch(ctx, s, req, runner); err != nil {
			if portKind(err) {
				return ports.Lease{}, err
			}
			return ports.Lease{ID: req.ID, Holder: req.Holder, State: model.LeaseFailed,
				Message: "launching the machine: " + err.Error()}, nil
		}
	}
	l, err := p.advance(ctx, s, inst)
	if err != nil {
		return ports.Lease{}, err
	}
	l.CA = certPEM
	return l, nil
}

// launch starts req's machine, one that runs runner when it is set: the
// account checked, its price read and its security group made to the fence.
func (p *Provider) launch(ctx context.Context, s *session, req ports.LeaseRequest,
	runner *ports.JobRunner) (*types.Instance, error) {
	if p.toolsURL == "" {
		return nil, errors.New("no address to download theos-env from: set the operator's --test-environment-tools-url")
	}
	if err := s.whoAmI(ctx); err != nil {
		return nil, err
	}
	price, err := p.price(ctx, s)
	if err != nil {
		return nil, err
	}
	group, err := s.fence(ctx)
	if err != nil {
		return nil, err
	}
	in, err := p.launchInput(req, runner, group, price)
	if err != nil {
		return nil, err
	}
	out, err := s.clients.EC2.RunInstances(ctx, in)
	if err != nil {
		return nil, portError(err)
	}
	if len(out.Instances) == 0 {
		return nil, errors.New("EC2 started no machine")
	}
	return &out.Instances[0], nil
}

// advance moves a running machine's lease on: once theos-env answers there
// with the lease's key, its metadata service is turned off, and the lease is
// Ready once that applied. Nothing of the agents' runs reaches the machine
// before, since the run gets the endpoint only when the lease is Ready (T5).
func (p *Provider) advance(ctx context.Context, s *session, inst *types.Instance) (ports.Lease, error) {
	now := p.now()
	l := leaseOf(inst, now)
	md := inst.MetadataOptions
	if l.State != model.LeasePending || inst.State == nil || inst.State.Name != types.InstanceStateNameRunning ||
		aws.ToString(inst.PublicIpAddress) == "" || md != nil && md.HttpEndpoint == types.InstanceMetadataEndpointStateDisabled {
		return l, nil
	}
	if err := p.probe(ctx, endpoint(inst), tag(inst.Tags, TagKey)); err != nil {
		l.Message = "theos-env doesn't answer on the machine yet: " + err.Error()
		return l, nil
	}
	out, err := s.clients.EC2.ModifyInstanceMetadataOptions(ctx, &ec2.ModifyInstanceMetadataOptionsInput{
		InstanceId: inst.InstanceId, HttpEndpoint: types.InstanceMetadataEndpointStateDisabled})
	if err != nil {
		return ports.Lease{}, portError(err)
	}
	if out.InstanceMetadataOptions != nil {
		inst.MetadataOptions = out.InstanceMetadataOptions
	} else {
		inst.MetadataOptions = &types.InstanceMetadataOptionsResponse{
			HttpEndpoint: types.InstanceMetadataEndpointStateDisabled, State: types.InstanceMetadataOptionsStatePending}
	}
	return leaseOf(inst, now), nil
}

// Get reads lease id's machine back, moved on as far as it goes.
func (p *Provider) Get(ctx context.Context, env ports.Environment, id string) (ports.Lease, error) {
	s, err := p.session(env)
	if err != nil {
		return ports.Lease{}, err
	}
	inst, err := s.find(ctx, id)
	if err != nil {
		return ports.Lease{}, err
	}
	if inst == nil {
		return ports.Lease{}, ports.ErrLeaseNotFound
	}
	return p.advance(ctx, s, inst)
}

// Return terminates lease id's machines.
func (p *Provider) Return(ctx context.Context, env ports.Environment, id string) error {
	s, err := p.session(env)
	if err != nil {
		return err
	}
	insts, err := s.instances(ctx, id)
	if err != nil {
		return err
	}
	return s.terminate(ctx, insts)
}

// Leases lists the environment's live machines by their tags, one lease per
// ID, the oldest machine of an ID's.
func (p *Provider) Leases(ctx context.Context, env ports.Environment) ([]ports.Lease, error) {
	s, err := p.session(env)
	if err != nil {
		return nil, err
	}
	insts, err := s.instances(ctx, "")
	if err != nil {
		return nil, err
	}
	now := p.now()
	seen := map[string]bool{}
	out := make([]ports.Lease, 0, len(insts))
	for i := range insts {
		id := tag(insts[i].Tags, TagLease)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, leaseOf(&insts[i], now))
	}
	slices.SortFunc(out, func(a, b ports.Lease) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// Release deletes the environment's security group, once no machine is in
// it any more: AWS refuses while one shuts down, and Release is asked again.
func (p *Provider) Release(ctx context.Context, env ports.Environment) error {
	s, err := p.session(env)
	if err != nil {
		return err
	}
	groups, err := s.groups(ctx, "")
	if err != nil {
		return err
	}
	for _, g := range groups {
		if _, err := s.clients.EC2.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{GroupId: g.GroupId}); err != nil &&
			apiCode(err) != "InvalidGroup.NotFound" {
			return portError(err)
		}
	}
	return nil
}

// find is lease id's machine: the oldest live one tagged with it, the others
// a lost answer of RunInstances left terminated; nil when there is none.
func (s *session) find(ctx context.Context, id string) (*types.Instance, error) {
	insts, err := s.instances(ctx, id)
	if err != nil || len(insts) == 0 {
		return nil, err
	}
	if err := s.terminate(ctx, insts[1:]); err != nil {
		return nil, err
	}
	return &insts[0], nil
}

// instances are the environment's live machines, oldest first: lease id's
// alone when id is set.
func (s *session) instances(ctx context.Context, id string) ([]types.Instance, error) {
	filters := []types.Filter{
		{Name: aws.String("tag:" + TagEnvironmentUID), Values: []string{s.env.UID}},
		{Name: aws.String("instance-state-name"), Values: liveStates},
	}
	if id != "" {
		filters = append(filters, types.Filter{Name: aws.String("tag:" + TagLease), Values: []string{id}})
	}
	var out []types.Instance
	in := &ec2.DescribeInstancesInput{Filters: filters}
	for {
		page, err := s.clients.EC2.DescribeInstances(ctx, in)
		if err != nil {
			return nil, portError(err)
		}
		for _, r := range page.Reservations {
			out = append(out, r.Instances...)
		}
		if aws.ToString(page.NextToken) == "" {
			break
		}
		in.NextToken = page.NextToken
	}
	slices.SortStableFunc(out, func(a, b types.Instance) int {
		if c := aws.ToTime(a.LaunchTime).Compare(aws.ToTime(b.LaunchTime)); c != 0 {
			return c
		}
		return strings.Compare(aws.ToString(a.InstanceId), aws.ToString(b.InstanceId))
	})
	return out, nil
}

// terminate ends insts; one that is gone is no error.
func (s *session) terminate(ctx context.Context, insts []types.Instance) error {
	if len(insts) == 0 {
		return nil
	}
	ids := make([]string, 0, len(insts))
	for _, i := range insts {
		ids = append(ids, aws.ToString(i.InstanceId))
	}
	_, err := s.clients.EC2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: ids})
	if err != nil && apiCode(err) != "InvalidInstanceID.NotFound" {
		return portError(err)
	}
	return nil
}

// endpoint is where theos-env answers on inst.
func endpoint(inst *types.Instance) string {
	return "https://" + aws.ToString(inst.PublicIpAddress) + ":" + strconv.Itoa(model.TestEnvRemotePort)
}

// tag is the value of tags' key, "" when it has none.
func tag(tags []types.Tag, key string) string {
	for _, t := range tags {
		if aws.ToString(t.Key) == key {
			return aws.ToString(t.Value)
		}
	}
	return ""
}

// leaseOf is what a machine says of its lease, from EC2's account of it
// alone: Ready once it runs with a public address and its metadata service
// is off, which the provider turns off only once theos-env answered with
// the lease's key; Failed past its deadline or once it stopped; Ended once
// it shuts down. Its cost is the price it was launched at for the time it
// has run.
func leaseOf(inst *types.Instance, now time.Time) ports.Lease {
	l := ports.Lease{ID: tag(inst.Tags, TagLease), Holder: tag(inst.Tags, TagHolder),
		Started: aws.ToTime(inst.LaunchTime), State: model.LeasePending}
	var state types.InstanceStateName
	if inst.State != nil {
		state = inst.State.Name
	}
	if state != types.InstanceStateNameShuttingDown && state != types.InstanceStateNameTerminated {
		price, _ := strconv.ParseInt(tag(inst.Tags, TagPrice), 10, 64)
		l.CostMilliUSD = model.LeaseCostMilliUSD(price, now.Sub(l.Started), minimumBilled)
	}
	deadline, err := time.Parse(time.RFC3339, tag(inst.Tags, TagDeadline))
	passed := err == nil && !now.Before(deadline)
	md := inst.MetadataOptions
	switch {
	case state == types.InstanceStateNameShuttingDown || state == types.InstanceStateNameTerminated:
		l.State, l.Message = model.LeaseEnded, "the machine "+string(state)+reasonOf(inst)
	case state == types.InstanceStateNameStopping || state == types.InstanceStateNameStopped:
		l.State, l.Message = model.LeaseFailed, "the machine stopped"+reasonOf(inst)
	case passed:
		l.State, l.Message = model.LeaseFailed, "the lease passed its deadline"
	case state != types.InstanceStateNameRunning:
		l.Message = "the machine is starting"
	case aws.ToString(inst.PublicIpAddress) == "":
		l.Message = "the machine has no public address yet; its subnet must route to the internet"
	case md == nil || md.HttpEndpoint != types.InstanceMetadataEndpointStateDisabled:
		l.Message = "waiting for theos-env to answer on the machine"
	case md.State != types.InstanceMetadataOptionsStateApplied:
		l.Message = "turning the machine's metadata service off"
	default:
		l.State, l.Endpoint = model.LeaseReady, endpoint(inst)
	}
	return l
}

// reasonOf is ": " and why EC2 says inst changed state, "" when it says
// nothing.
func reasonOf(inst *types.Instance) string {
	if r := inst.StateReason; r != nil && aws.ToString(r.Message) != "" {
		return ": " + aws.ToString(r.Message)
	}
	return ""
}

// LaunchInput is the RunInstances call of req's machine in security group
// group at microUSDPerHour, a pure function of its arguments, fenced (T5):
// no instance profile, so no role; no SSH key; one network interface in the
// environment's subnet, with a public address, in group alone, since a
// machine's groups add up and another could only widen the fence; the
// metadata service on only until the provider turns it off before the lease
// is Ready, and until then IMDSv2 only, one hop, no tags, no IPv6; shutting
// down terminates; and tagged with the lease. The metadata service is on at
// launch because the machine reads its user data from it: launched with it
// off, the machine never starts theos-env.
func LaunchInput(req ports.LeaseRequest, group string, microUSDPerHour int64, toolsURL string,
	now time.Time) (*ec2.RunInstancesInput, error) {
	env := req.Env
	deadline := req.Deadline
	if deadline <= 0 {
		deadline = model.LeaseDeadline(0)
	}
	userData, err := UserData(toolsURL, req.Token, deadline)
	if err != nil {
		return nil, err
	}
	pin, err := model.TestEnvKeyPin(req.Token)
	if err != nil {
		return nil, err
	}
	tags := []types.Tag{
		{Key: aws.String("Name"), Value: aws.String("theos-env " + env.Namespace + "/" + env.Name + " " + req.ID)},
		{Key: aws.String(TagLease), Value: aws.String(req.ID)},
		{Key: aws.String(TagHolder), Value: aws.String(req.Holder)},
		{Key: aws.String(TagEnvironment), Value: aws.String(env.Namespace + "/" + env.Name)},
		{Key: aws.String(TagEnvironmentUID), Value: aws.String(env.UID)},
		{Key: aws.String(TagDeadline), Value: aws.String(now.Add(deadline).UTC().Format(time.RFC3339))},
		{Key: aws.String(TagPrice), Value: aws.String(strconv.FormatInt(microUSDPerHour, 10))},
		{Key: aws.String(TagKey), Value: aws.String(pin)},
	}
	return &ec2.RunInstancesInput{
		ImageId:      aws.String(env.AWS.ImageID),
		InstanceType: types.InstanceType(env.AWS.InstanceType),
		MinCount:     aws.Int32(1), MaxCount: aws.Int32(1),
		UserData:                          aws.String(base64.StdEncoding.EncodeToString([]byte(userData))),
		InstanceInitiatedShutdownBehavior: types.ShutdownBehaviorTerminate,
		NetworkInterfaces: []types.InstanceNetworkInterfaceSpecification{{
			DeviceIndex: aws.Int32(0), SubnetId: aws.String(env.AWS.SubnetID), Groups: []string{group},
			AssociatePublicIpAddress: aws.Bool(true), DeleteOnTermination: aws.Bool(true)}},
		MetadataOptions: &types.InstanceMetadataOptionsRequest{
			HttpEndpoint:            types.InstanceMetadataEndpointStateEnabled,
			HttpTokens:              types.HttpTokensStateRequired,
			HttpPutResponseHopLimit: aws.Int32(1),
			InstanceMetadataTags:    types.InstanceMetadataTagsStateDisabled,
			HttpProtocolIpv6:        types.InstanceMetadataProtocolStateDisabled,
		},
		TagSpecifications: []types.TagSpecification{
			{ResourceType: types.ResourceTypeInstance, Tags: tags},
			{ResourceType: types.ResourceTypeVolume, Tags: tags},
			{ResourceType: types.ResourceTypeNetworkInterface, Tags: tags},
		},
	}, nil
}
