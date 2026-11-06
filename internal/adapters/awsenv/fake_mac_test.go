package awsenv

import (
	"context"
	"fmt"
	"slices"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/JairJosafath/theos/internal/model"
)

// The fake EC2's dedicated hosts, as EC2 keeps a Mac's: one machine on a
// host at a time, in the host's zone; a host whose machine terminated is
// scrubbed (pending) until the test says it is done; and a host can't be
// released within 24 hours of its allocation, nor with a machine on it.

type fakeHost struct {
	account, region string
	host            types.Host
	// allocated counts AllocateHosts calls this host answered, a client
	// token's repeat included.
	allocated int
}

// place is the host in, a RunInstances call, puts its machine on: nil for a
// machine on shared tenancy; f is locked.
func (f *fakeAWS) place(account, region, subnet string, in *ec2.RunInstancesInput) (*fakeHost, error) {
	if in.Placement == nil || in.Placement.Tenancy != types.TenancyHost {
		if len(in.InstanceType) >= 3 && in.InstanceType[:3] == "mac" {
			return nil, apiErr("InvalidParameterCombination", "A Mac instance must be launched on a dedicated host")
		}
		return nil, nil
	}
	h := f.host(account, aws.ToString(in.Placement.HostId))
	switch {
	case h == nil:
		return nil, apiErr("InvalidHostID.NotFound", "The host ID does not exist")
	case aws.ToString(h.host.AvailabilityZone) != region+"a" || subnet == "":
		return nil, apiErr("InvalidParameterValue", "The host is in another Availability Zone than the subnet")
	case h.host.State != types.AllocationStateAvailable || len(f.onHost(h)) > 0:
		return nil, apiErr("InsufficientHostCapacity", "There is not enough capacity on the host to launch the instance")
	case aws.ToString(h.host.HostProperties.InstanceType) != string(in.InstanceType):
		return nil, apiErr("InvalidParameterValue", "The host takes "+aws.ToString(h.host.HostProperties.InstanceType))
	}
	return h, nil
}

// host is account's host id; f is locked.
func (f *fakeAWS) host(account, id string) *fakeHost {
	for _, h := range f.hosts {
		if h.account == account && aws.ToString(h.host.HostId) == id {
			return h
		}
	}
	return nil
}

// onHost are the machines on h that aren't terminated; f is locked.
func (f *fakeAWS) onHost(h *fakeHost) []types.HostInstance {
	var out []types.HostInstance
	for _, i := range f.instances {
		if p := i.inst.Placement; p != nil && aws.ToString(p.HostId) == aws.ToString(h.host.HostId) &&
			i.inst.State.Name != types.InstanceStateNameTerminated &&
			i.inst.State.Name != types.InstanceStateNameShuttingDown {
			out = append(out, types.HostInstance{InstanceId: i.inst.InstanceId, InstanceType: aws.String(string(i.inst.InstanceType))})
		}
	}
	return out
}

// scrub starts scrubbing the host i is on, as EC2 does once a Mac machine
// terminates; f is locked.
func (f *fakeAWS) scrub(i *fakeInstance) {
	if p := i.inst.Placement; p != nil {
		if h := f.host(i.account, aws.ToString(p.HostId)); h != nil {
			h.host.State = types.AllocationStatePending
		}
	}
}

// scrubbed ends the scrubbing of every host of environment uid.
func (f *fakeAWS) scrubbed(uid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, h := range f.hosts {
		if tag(h.host.Tags, TagEnvironmentUID) == uid && h.host.State == types.AllocationStatePending {
			h.host.State = types.AllocationStateAvailable
		}
	}
}

// liveHosts are the hosts of environment uid EC2 bills.
func (f *fakeAWS) liveHosts(uid string) []types.Host {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []types.Host
	for _, h := range f.hosts {
		if tag(h.host.Tags, TagEnvironmentUID) == uid && h.host.State != types.AllocationStateReleased {
			out = append(out, h.host)
		}
	}
	return out
}

func (c *fakeEC2) AllocateHosts(_ context.Context, in *ec2.AllocateHostsInput,
	_ ...func(*ec2.Options)) (*ec2.AllocateHostsOutput, error) {
	account, done, err := c.begin("AllocateHosts")
	if err != nil {
		return nil, err
	}
	defer done()
	f := c.f
	if aws.ToInt32(in.Quantity) != 1 || in.AutoPlacement != types.AutoPlacementOff || aws.ToString(in.InstanceType) == "" {
		return nil, apiErr("InvalidParameterValue", "the fake allocates one host of a type, placed on by ID alone")
	}
	if aws.ToString(in.AvailabilityZone) != c.cfg.Region+"a" {
		return nil, apiErr("InvalidParameterValue", "Invalid availability zone: "+aws.ToString(in.AvailabilityZone))
	}
	for _, h := range f.hosts {
		if token := aws.ToString(in.ClientToken); h.account == account && token != "" &&
			aws.ToString(h.host.ClientToken) == token {
			if uid := tag(h.host.Tags, TagEnvironmentUID); !slices.ContainsFunc(in.TagSpecifications,
				func(s types.TagSpecification) bool { return tag(s.Tags, TagEnvironmentUID) == uid }) {
				return nil, apiErr("IdempotentParameterMismatch", "The client token was used with other parameters")
			}
			h.allocated++
			return &ec2.AllocateHostsOutput{HostIds: []string{aws.ToString(h.host.HostId)}}, nil
		}
	}
	f.seq++
	var tags []types.Tag
	for _, spec := range in.TagSpecifications {
		if spec.ResourceType == types.ResourceTypeDedicatedHost {
			tags = slices.Clone(spec.Tags)
		}
	}
	h := &fakeHost{account: account, region: c.cfg.Region, allocated: 1, host: types.Host{
		HostId: aws.String(fmt.Sprintf("h-%017x", f.seq)), AvailabilityZone: in.AvailabilityZone,
		AllocationTime: aws.Time(f.now()), State: types.AllocationStateAvailable, ClientToken: in.ClientToken,
		AutoPlacement: in.AutoPlacement, HostRecovery: in.HostRecovery, Tags: tags,
		HostProperties: &types.HostProperties{InstanceType: in.InstanceType}}}
	f.hosts = append(f.hosts, h)
	return &ec2.AllocateHostsOutput{HostIds: []string{aws.ToString(h.host.HostId)}}, nil
}

func (c *fakeEC2) DescribeHosts(_ context.Context, in *ec2.DescribeHostsInput,
	_ ...func(*ec2.Options)) (*ec2.DescribeHostsOutput, error) {
	account, done, err := c.begin("DescribeHosts")
	if err != nil {
		return nil, err
	}
	defer done()
	out := &ec2.DescribeHostsOutput{}
	for _, h := range c.f.hosts {
		if h.account != account || h.region != c.cfg.Region {
			continue
		}
		if len(in.HostIds) > 0 && !slices.Contains(in.HostIds, aws.ToString(h.host.HostId)) {
			continue
		}
		if hostMatches(h, in.Filter) {
			host := h.host
			host.Instances = c.f.onHost(h)
			out.Hosts = append(out.Hosts, host)
		}
	}
	return out, nil
}

// hostMatches says whether h passes filters, its state's among them.
func hostMatches(h *fakeHost, filters []types.Filter) bool {
	var tags []types.Filter
	for _, flt := range filters {
		if aws.ToString(flt.Name) != "state" {
			tags = append(tags, flt)
		} else if !slices.Contains(flt.Values, string(h.host.State)) {
			return false
		}
	}
	return matches(h.host.Tags, "", "", tags)
}

func (c *fakeEC2) ReleaseHosts(_ context.Context, in *ec2.ReleaseHostsInput,
	_ ...func(*ec2.Options)) (*ec2.ReleaseHostsOutput, error) {
	account, done, err := c.begin("ReleaseHosts")
	if err != nil {
		return nil, err
	}
	defer done()
	f := c.f
	out := &ec2.ReleaseHostsOutput{}
	refuse := func(id, code, msg string) {
		out.Unsuccessful = append(out.Unsuccessful, types.UnsuccessfulItem{ResourceId: aws.String(id),
			Error: &types.UnsuccessfulItemError{Code: aws.String(code), Message: aws.String(msg)}})
	}
	for _, id := range in.HostIds {
		h := f.host(account, id)
		switch {
		case h == nil || h.host.State == types.AllocationStateReleased:
			refuse(id, "Client.InvalidHost.NotFound", "The host does not exist")
		case len(f.onHost(h)) > 0:
			refuse(id, "Client.InvalidHost.Occupied", "The host has running instances")
		case f.now().Before(h.host.AllocationTime.Add(model.MacHostMinimum)):
			refuse(id, "Client.InvalidHost.ReleaseNotAllowed",
				"Mac Dedicated Hosts can't be released within 24 hours of their allocation")
		default:
			h.host.State, h.host.ReleaseTime = types.AllocationStateReleased, aws.Time(f.now())
			out.Successful = append(out.Successful, id)
		}
	}
	return out, nil
}

func (c *fakeEC2) CreateTags(_ context.Context, in *ec2.CreateTagsInput,
	_ ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	account, done, err := c.begin("CreateTags")
	if err != nil {
		return nil, err
	}
	defer done()
	for _, id := range in.Resources {
		h := c.f.host(account, id)
		if h == nil {
			return nil, apiErr("InvalidID", "The ID '"+id+"' is not valid")
		}
		for _, t := range in.Tags {
			h.host.Tags = slices.DeleteFunc(h.host.Tags, func(x types.Tag) bool { return aws.ToString(x.Key) == aws.ToString(t.Key) })
			h.host.Tags = append(h.host.Tags, t)
		}
	}
	return &ec2.CreateTagsOutput{}, nil
}
