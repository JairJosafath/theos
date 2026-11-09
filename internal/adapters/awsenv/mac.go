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

package awsenv

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	pricingtypes "github.com/aws/aws-sdk-go-v2/service/pricing/types"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// An EC2 Mac environment (docs/plans/done/test-environments.md, T11, step 9): a
// Mac machine runs on a dedicated host, which EC2 bills for 24 hours at
// least from its allocation and scrubs after each machine. The provider
// allocates a host for a lease only when every host it keeps for the
// environment is busy, keeps each for keepHostHours from its allocation
// (model.KeepHost), places the lease's machine on a free one, and releases
// a host past its kept time once no machine is on it. The lease whose
// machine is the first on a host carries the host's kept hours as its cost
// from its start; a lease placed on a host kept costs nothing until the
// host's kept time is over (model.HostLeaseBilled).

// Tags on a Mac machine and on its host.
const (
	// TagHost is the dedicated host a Mac machine is placed on.
	TagHost = "theos.factory.io/host"
	// TagHostKeptUntil is when the host's kept time ends, RFC 3339: on the
	// host, and on each machine placed on it.
	TagHostKeptUntil = "theos.factory.io/host-kept-until"
	// TagHostUpfront is the host's kept time a Mac machine's lease carries
	// from its start, a Go duration: the host's kept hours for the first
	// machine on it, 0s for the others.
	TagHostUpfront = "theos.factory.io/host-upfront"
	// TagHostClaimed is the lease whose machine carries the host's kept
	// hours, set on the host once that machine started.
	TagHostClaimed = "theos.factory.io/host-claimed-by"
)

// hostScrubMessage is a Mac lease's message while the host it waits for is
// scrubbed.
const hostScrubMessage = "the Mac host %s is being scrubbed after its last machine, as EC2 does before a host " +
	"takes another; the lease waits for it rather than allocating a host for 24 hours more"

// keptHostStates are the states of a host that holds its place: EC2 bills
// it.
var keptHostStates = []string{string(types.AllocationStateAvailable), string(types.AllocationStatePending),
	string(types.AllocationStateUnderAssessment)}

// hostWait is why a Mac lease waits before its machine starts: a host kept
// is being scrubbed.
type hostWait struct{ message string }

func (w *hostWait) Error() string { return w.message }

// launchMac starts req's Mac machine on a host of the environment's: a
// free one kept, else a new one, unless one kept is being scrubbed, which
// the lease waits for (*hostWait).
func (p *Provider) launchMac(ctx context.Context, s *session, req ports.LeaseRequest, group string,
	microUSDPerHour int64) (*types.Instance, error) {
	if !strings.Contains(p.toolsURL, "${os}") && !strings.Contains(p.toolsURL, "darwin") {
		return nil, fmt.Errorf("the theos-env download address %q names no ${os}: a Mac needs theos-env's darwin "+
			"build, so set the operator's --test-environment-tools-url to one with ${os}", p.toolsURL)
	}
	now := p.now()
	zone, err := s.subnetZone(ctx)
	if err != nil {
		return nil, err
	}
	hosts, err := s.hosts(ctx)
	if err != nil {
		return nil, err
	}
	var free, scrubbing *types.Host
	for i := range hosts {
		h := &hosts[i]
		if aws.ToString(h.AvailabilityZone) != zone || !hostTakes(h, s.env.AWS.InstanceType) ||
			!now.Before(keptUntil(h)) {
			continue // in another zone, of another type, or past its kept time
		}
		switch {
		case len(h.Instances) > 0:
		case h.State == types.AllocationStateAvailable:
			if free == nil {
				free = h
			}
		case scrubbing == nil:
			scrubbing = h
		}
	}
	keep := model.KeepHost(s.env.AWS.KeepHostHours)
	switch {
	case free == nil && scrubbing != nil:
		return nil, &hostWait{fmt.Sprintf(hostScrubMessage, aws.ToString(scrubbing.HostId))}
	case free == nil:
		if free, err = s.allocateHost(ctx, req, zone, microUSDPerHour, now.Add(keep)); err != nil {
			return nil, err
		}
	}
	upfront := time.Duration(0)
	first := tag(free.Tags, TagHostClaimed) == ""
	if first {
		upfront = keep
	}
	in, err := MacLaunchInput(req, group, aws.ToString(free.HostId), microUSDPerHour, upfront, keptUntil(free),
		p.toolsURL, now)
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
	if first {
		// Best effort: a host left unclaimed has its kept hours counted
		// again by the next machine on it, which counts more, never less.
		_, _ = s.clients.EC2.CreateTags(ctx, &ec2.CreateTagsInput{Resources: []string{aws.ToString(free.HostId)},
			Tags: []types.Tag{{Key: aws.String(TagHostClaimed), Value: aws.String(req.ID)}}})
	}
	return &out.Instances[0], nil
}

// hostTakes reports whether h takes a machine of instanceType.
func hostTakes(h *types.Host, instanceType string) bool {
	if h.HostProperties == nil {
		return true
	}
	if t := aws.ToString(h.HostProperties.InstanceType); t != "" {
		return t == instanceType
	}
	return aws.ToString(h.HostProperties.InstanceFamily) == model.MacHostFamily(instanceType)
}

// keptUntil is when h's kept time ends: its tag, else its allocation's
// minimum, the zero time when it says neither.
func keptUntil(h *types.Host) time.Time {
	if t, err := time.Parse(time.RFC3339, tag(h.Tags, TagHostKeptUntil)); err == nil {
		return t
	}
	if h.AllocationTime != nil {
		return h.AllocationTime.Add(model.MacHostMinimum)
	}
	return time.Time{}
}

// subnetZone is the availability zone of the environment's subnet, where
// its hosts are allocated: a host takes machines in its own zone alone.
func (s *session) subnetZone(ctx context.Context) (string, error) {
	out, err := s.clients.EC2.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{SubnetIds: []string{s.env.AWS.SubnetID}})
	if err != nil {
		return "", portError(err)
	}
	if len(out.Subnets) == 0 || aws.ToString(out.Subnets[0].AvailabilityZone) == "" {
		return "", fmt.Errorf("subnet %s is in no availability zone", s.env.AWS.SubnetID)
	}
	return aws.ToString(out.Subnets[0].AvailabilityZone), nil
}

// allocateHost allocates a host for req's machine in zone, kept until
// keptUntil, tagged with the environment: one host, placed on by name
// alone, idempotent on the lease so a lost answer allocates none more.
func (s *session) allocateHost(ctx context.Context, req ports.LeaseRequest, zone string, microUSDPerHour int64,
	keptUntil time.Time) (*types.Host, error) {
	env := s.env
	out, err := s.clients.EC2.AllocateHosts(ctx, &ec2.AllocateHostsInput{
		AvailabilityZone: aws.String(zone), InstanceType: aws.String(env.AWS.InstanceType), Quantity: aws.Int32(1),
		AutoPlacement: types.AutoPlacementOff, HostRecovery: types.HostRecoveryOff,
		ClientToken: aws.String(hostToken(env.UID, req.ID)),
		TagSpecifications: []types.TagSpecification{{ResourceType: types.ResourceTypeDedicatedHost, Tags: []types.Tag{
			{Key: aws.String("Name"), Value: aws.String("theos-env " + env.Namespace + "/" + env.Name)},
			{Key: aws.String(TagEnvironment), Value: aws.String(env.Namespace + "/" + env.Name)},
			{Key: aws.String(TagEnvironmentUID), Value: aws.String(env.UID)},
			{Key: aws.String(TagHostKeptUntil), Value: aws.String(keptUntil.UTC().Format(time.RFC3339))},
			{Key: aws.String(TagPrice), Value: aws.String(fmt.Sprint(microUSDPerHour))},
		}}},
	})
	if err != nil {
		return nil, portError(err)
	}
	if len(out.HostIds) == 0 {
		return nil, errors.New("EC2 allocated no host")
	}
	read, err := s.clients.EC2.DescribeHosts(ctx, &ec2.DescribeHostsInput{HostIds: out.HostIds[:1]})
	if err != nil {
		return nil, portError(err)
	}
	if len(read.Hosts) == 0 {
		return nil, fmt.Errorf("host %s is gone", out.HostIds[0])
	}
	return &read.Hosts[0], nil
}

// hostToken is AllocateHosts' idempotency token for lease id of
// environment uid: EC2 takes 64 characters, and a token another
// environment used with other tags is refused.
func hostToken(uid, id string) string {
	sum := sha256.Sum256([]byte(uid + "/" + id))
	return "theos-" + hex.EncodeToString(sum[:16])
}

// hosts are the environment's hosts EC2 bills, oldest first.
func (s *session) hosts(ctx context.Context) ([]types.Host, error) {
	in := &ec2.DescribeHostsInput{Filter: []types.Filter{
		{Name: aws.String("tag:" + TagEnvironmentUID), Values: []string{s.env.UID}},
		{Name: aws.String("state"), Values: keptHostStates},
	}}
	var out []types.Host
	for {
		page, err := s.clients.EC2.DescribeHosts(ctx, in)
		if err != nil {
			return nil, portError(err)
		}
		out = append(out, page.Hosts...)
		if aws.ToString(page.NextToken) == "" {
			break
		}
		in.NextToken = page.NextToken
	}
	slices.SortStableFunc(out, func(a, b types.Host) int {
		if c := aws.ToTime(a.AllocationTime).Compare(aws.ToTime(b.AllocationTime)); c != 0 {
			return c
		}
		return strings.Compare(aws.ToString(a.HostId), aws.ToString(b.HostId))
	})
	return out, nil
}

// releaseHosts releases the environment's hosts that hold no machine: past
// their kept time alone unless all is set, as when the environment goes.
// It returns why a host EC2 refused to release stays, which is asked again;
// a refused credential or an AWS that can't be reached is its error.
func (s *session) releaseHosts(ctx context.Context, now time.Time, all bool) (string, error) {
	hosts, err := s.hosts(ctx)
	if err != nil {
		return "", err
	}
	var ids []string
	for i := range hosts {
		if len(hosts[i].Instances) == 0 && (all || !now.Before(keptUntil(&hosts[i]))) {
			ids = append(ids, aws.ToString(hosts[i].HostId))
		}
	}
	if len(ids) == 0 {
		return "", nil
	}
	out, err := s.clients.EC2.ReleaseHosts(ctx, &ec2.ReleaseHostsInput{HostIds: ids})
	if err != nil {
		if err = portError(err); portKind(err) {
			return "", err
		}
		return err.Error(), nil
	}
	var refused []string
	for _, u := range out.Unsuccessful {
		why := ""
		if u.Error != nil {
			why = aws.ToString(u.Error.Code) + ": " + aws.ToString(u.Error.Message)
		}
		refused = append(refused, aws.ToString(u.ResourceId)+" ("+why+")")
	}
	if len(refused) == 0 {
		return "", nil
	}
	return "EC2 doesn't release host " + strings.Join(refused, ", ") + " yet: a Mac host is billed 24 hours at " +
		"least from its allocation", nil
}

// MacLaunchInput is LaunchInput placed on Mac host hostID, a pure function
// of its arguments: the machine is on the host, and tagged with it, the end
// of its kept time and the kept time the lease carries from its start.
func MacLaunchInput(req ports.LeaseRequest, group, hostID string, microUSDPerHour int64, upfront time.Duration,
	keptUntil time.Time, toolsURL string, now time.Time) (*ec2.RunInstancesInput, error) {
	in, err := LaunchInput(req, group, microUSDPerHour, toolsURL, now)
	if err != nil {
		return nil, err
	}
	in.Placement = &types.Placement{Tenancy: types.TenancyHost, HostId: aws.String(hostID)}
	extra := []types.Tag{
		{Key: aws.String(TagHost), Value: aws.String(hostID)},
		{Key: aws.String(TagHostKeptUntil), Value: aws.String(keptUntil.UTC().Format(time.RFC3339))},
		{Key: aws.String(TagHostUpfront), Value: aws.String(upfront.String())},
	}
	for i := range in.TagSpecifications {
		in.TagSpecifications[i].Tags = append(slices.Clone(in.TagSpecifications[i].Tags), extra...)
	}
	return in, nil
}

// leaseCost is what a machine's lease cost by now at microUSDPerHour: a Mac
// machine's by its host's hours (model.HostLeaseBilled), any other's by
// the time held, a minute at least.
func leaseCost(tags []types.Tag, microUSDPerHour int64, started, now time.Time) int64 {
	until, err := time.Parse(time.RFC3339, tag(tags, TagHostKeptUntil))
	if err != nil {
		return model.LeaseCostMilliUSD(microUSDPerHour, now.Sub(started), minimumBilled)
	}
	upfront, _ := time.ParseDuration(tag(tags, TagHostUpfront))
	return model.LeaseCostMilliUSD(microUSDPerHour, model.HostLeaseBilled(upfront, started, until, now), 0)
}

// priceFilters are the Price List's filters of the environment's price: an
// EC2 Mac's dedicated host by its family, any other instance type on
// demand, Linux on shared tenancy.
func priceFilters(env *ports.AWSEnvironment) ([]pricingtypes.Filter, string) {
	match := func(field, value string) pricingtypes.Filter {
		return pricingtypes.Filter{Type: pricingtypes.FilterTypeTermMatch, Field: aws.String(field), Value: aws.String(value)}
	}
	if model.TestEnvMac(env.InstanceType) {
		family := model.MacHostFamily(env.InstanceType)
		return []pricingtypes.Filter{match("productFamily", "Dedicated Host"), match("instanceType", family),
				match("regionCode", env.Region)},
			fmt.Sprintf("no on-demand price for a %s dedicated host, which instance type %s runs on, in region %s",
				family, env.InstanceType, env.Region)
	}
	return []pricingtypes.Filter{
			match("instanceType", env.InstanceType), match("regionCode", env.Region),
			match("operatingSystem", "Linux"), match("tenancy", "Shared"), match("preInstalledSw", "NA"),
			match("capacitystatus", "Used"), match("licenseModel", "No License required"),
		}, fmt.Sprintf("no on-demand Linux price for instance type %s in region %s", env.InstanceType,
			env.Region)
}
