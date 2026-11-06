package awsenv

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/pricing"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"

	"github.com/JairJosafath/theos/internal/model"
)

// fakeAWS is an in-memory AWS of accounts, each with EC2 machines,
// subnets and security groups per region, and a Price List. A caller is
// found from the config's access key. A machine runs RunInstances' metadata
// options; a terminated one shuts down, then is terminated on its next read.
type fakeAWS struct {
	mu   sync.Mutex
	now  func() time.Time
	keys map[string]string // access key → account
	seq  int
	// instances, oldest first, and their account and region.
	instances []*fakeInstance
	groups    []*fakeGroup
	subnets   map[string]string // account/region/subnet → VPC
	// prices are the on-demand USD an hour by region/instance type.
	prices map[string]string
	// healthy are the machines whose theos-env answers, by public address.
	healthy map[string]bool
	// metadataPending keeps a turned-off metadata service pending until
	// the test applies it.
	metadataPending bool
	// launchError is RunInstances' answer, when set.
	launchError error
	launches    []ec2.RunInstancesInput
	calls       []string
	priceReads  int
	down        bool
}

type fakeInstance struct {
	account, region, userData string
	inst                      types.Instance
}

type fakeGroup struct {
	account, region string
	sg              types.SecurityGroup
}

func newFakeAWS(now func() time.Time) *fakeAWS {
	return &fakeAWS{now: now, keys: map[string]string{}, subnets: map[string]string{}, prices: map[string]string{},
		healthy: map[string]bool{}}
}

// account adds an account whose user's access key is key, with a subnet in
// region and the price of instanceType there.
func (f *fakeAWS) account(key, id, region, subnet, instanceType, usd string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys[key] = id
	f.subnets[id+"/"+region+"/"+subnet] = "vpc-0123456789abcdef0"
	f.prices[region+"/"+instanceType] = usd
}

func (f *fakeAWS) clients(cfg aws.Config) Clients {
	return Clients{EC2: &fakeEC2{f, cfg}, STS: &fakeSTS{f, cfg}, Pricing: &fakePricing{f, cfg}}
}

func apiErr(code, msg string) error { return &smithy.GenericAPIError{Code: code, Message: msg} }

// caller is the account cfg's credential is of; f is locked.
func (f *fakeAWS) caller(cfg aws.Config, call string) (string, error) {
	f.calls = append(f.calls, call)
	if f.down {
		return "", errors.New("dial tcp: lookup ec2.eu-west-1.amazonaws.com: no such host")
	}
	creds, err := cfg.Credentials.Retrieve(context.Background())
	if err != nil {
		return "", err
	}
	account, ok := f.keys[creds.AccessKeyID]
	if !ok {
		return "", apiErr("AuthFailure", "AWS was not able to validate the provided access credentials")
	}
	return account, nil
}

// machine is the machine of lease id; f is locked.
func (f *fakeAWS) machine(uid, id string) *fakeInstance {
	for _, i := range f.instances {
		if tag(i.inst.Tags, TagLease) == id && tag(i.inst.Tags, TagEnvironmentUID) == uid &&
			i.inst.State.Name != types.InstanceStateNameTerminated &&
			i.inst.State.Name != types.InstanceStateNameShuttingDown {
			return i
		}
	}
	return nil
}

// ready makes lease id's machine run with a public address, its theos-env
// answering.
func (f *fakeAWS) ready(uid, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i := f.machine(uid, id); i != nil {
		i.inst.State = &types.InstanceState{Name: types.InstanceStateNameRunning}
		i.inst.PublicIpAddress = aws.String(fmt.Sprintf("203.0.113.%d", 1+slices.Index(f.instances, i)%250))
		f.healthy[aws.ToString(i.inst.PublicIpAddress)] = true
	}
}

// run makes lease id's machine run with a public address, its theos-env
// not answering yet.
func (f *fakeAWS) run(uid, id string) {
	f.ready(uid, id)
	f.mu.Lock()
	defer f.mu.Unlock()
	if i := f.machine(uid, id); i != nil {
		f.healthy[aws.ToString(i.inst.PublicIpAddress)] = false
	}
}

// answer makes the theos-env of lease id's machine answer.
func (f *fakeAWS) answer(uid, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i := f.machine(uid, id); i != nil {
		f.healthy[aws.ToString(i.inst.PublicIpAddress)] = true
	}
}

// applyMetadata applies lease id's metadata options.
func (f *fakeAWS) applyMetadata(uid, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i := f.machine(uid, id); i != nil {
		i.inst.MetadataOptions.State = types.InstanceMetadataOptionsStateApplied
	}
}

// stop stops lease id's machine, as a machine that failed is.
func (f *fakeAWS) stop(uid, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i := f.machine(uid, id); i != nil {
		i.inst.State = &types.InstanceState{Name: types.InstanceStateNameStopped}
		i.inst.StateReason = &types.StateReason{Code: aws.String("Server.InternalError"),
			Message: aws.String("Server.InternalError: Internal error on launch")}
	}
}

// live counts the machines of environment uid that hold their place.
func (f *fakeAWS) live(uid string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, i := range f.instances {
		if tag(i.inst.Tags, TagEnvironmentUID) == uid && slices.Contains(liveStates, string(i.inst.State.Name)) {
			n++
		}
	}
	return n
}

// count counts the calls named call.
func (f *fakeAWS) count(call string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == call {
			n++
		}
	}
	return n
}

var tokenInUserData = regexp.MustCompile(`THEOS_ENV_TOKEN=([A-Za-z0-9._-]+)`)

// probe is theos-env answering on the fake's machines: at a healthy
// machine's address, with the key its user data's token makes.
func (f *fakeAWS) probe(_ context.Context, endpoint, keyPin string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, i := range f.instances {
		ip := aws.ToString(i.inst.PublicIpAddress)
		if ip == "" || endpoint != "https://"+ip+":443" {
			continue
		}
		if !f.healthy[ip] {
			return errors.New("connection refused")
		}
		m := tokenInUserData.FindStringSubmatch(i.userData)
		if m == nil {
			return errors.New("the machine's user data starts no theos-env")
		}
		if pin, _ := model.TestEnvKeyPin(m[1]); pin != keyPin {
			return errors.New("the server's certificate isn't the lease's")
		}
		return nil
	}
	return errors.New("no route to host")
}

// matches says whether inst passes filters.
func matches(tags []types.Tag, state string, vpc string, filters []types.Filter) bool {
	for _, flt := range filters {
		name := aws.ToString(flt.Name)
		var have string
		switch {
		case strings.HasPrefix(name, "tag:"):
			have = tag(tags, strings.TrimPrefix(name, "tag:"))
			if have == "" {
				return false
			}
		case name == "instance-state-name":
			have = state
		case name == "vpc-id":
			have = vpc
		default:
			panic("the fake knows no filter " + name)
		}
		if !slices.Contains(flt.Values, have) {
			return false
		}
	}
	return true
}

type fakeEC2 struct {
	f   *fakeAWS
	cfg aws.Config
}

func (c *fakeEC2) begin(call string) (string, func(), error) {
	c.f.mu.Lock()
	account, err := c.f.caller(c.cfg, call)
	if err != nil {
		c.f.mu.Unlock()
		return "", nil, err
	}
	return account, c.f.mu.Unlock, nil
}

func (c *fakeEC2) RunInstances(_ context.Context, in *ec2.RunInstancesInput,
	_ ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	account, done, err := c.begin("RunInstances")
	if err != nil {
		return nil, err
	}
	defer done()
	f := c.f
	f.launches = append(f.launches, *in)
	if f.launchError != nil {
		return nil, f.launchError
	}
	ni := in.NetworkInterfaces[0]
	if f.subnets[account+"/"+c.cfg.Region+"/"+aws.ToString(ni.SubnetId)] == "" {
		return nil, apiErr("InvalidSubnetID.NotFound", "The subnet ID does not exist")
	}
	userData, _ := base64.StdEncoding.DecodeString(aws.ToString(in.UserData))
	f.seq++
	var tags []types.Tag
	for _, spec := range in.TagSpecifications {
		if spec.ResourceType == types.ResourceTypeInstance {
			tags = spec.Tags
		}
	}
	md := in.MetadataOptions
	inst := types.Instance{InstanceId: aws.String(fmt.Sprintf("i-%017x", f.seq)), ImageId: in.ImageId,
		InstanceType: in.InstanceType, LaunchTime: aws.Time(f.now()), SubnetId: ni.SubnetId, Tags: tags,
		State: &types.InstanceState{Name: types.InstanceStateNamePending},
		MetadataOptions: &types.InstanceMetadataOptionsResponse{HttpEndpoint: md.HttpEndpoint, HttpTokens: md.HttpTokens,
			HttpPutResponseHopLimit: md.HttpPutResponseHopLimit, InstanceMetadataTags: md.InstanceMetadataTags,
			State: types.InstanceMetadataOptionsStateApplied}}
	for _, g := range ni.Groups {
		inst.SecurityGroups = append(inst.SecurityGroups, types.GroupIdentifier{GroupId: aws.String(g)})
	}
	f.instances = append(f.instances, &fakeInstance{account: account, region: c.cfg.Region, userData: string(userData),
		inst: inst})
	return &ec2.RunInstancesOutput{Instances: []types.Instance{inst}}, nil
}

func (c *fakeEC2) DescribeInstances(_ context.Context, in *ec2.DescribeInstancesInput,
	_ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	account, done, err := c.begin("DescribeInstances")
	if err != nil {
		return nil, err
	}
	defer done()
	out := &ec2.DescribeInstancesOutput{}
	for _, i := range c.f.instances {
		if i.account != account || i.region != c.cfg.Region {
			continue
		}
		if i.inst.State.Name == types.InstanceStateNameShuttingDown {
			i.inst.State = &types.InstanceState{Name: types.InstanceStateNameTerminated}
		}
		if matches(i.inst.Tags, string(i.inst.State.Name), "", in.Filters) {
			// One reservation per page, as a lister must page through.
			out.Reservations = append(out.Reservations, types.Reservation{Instances: []types.Instance{i.inst}})
		}
	}
	return out, nil
}

func (c *fakeEC2) TerminateInstances(_ context.Context, in *ec2.TerminateInstancesInput,
	_ ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	account, done, err := c.begin("TerminateInstances")
	if err != nil {
		return nil, err
	}
	defer done()
	for _, id := range in.InstanceIds {
		for _, i := range c.f.instances {
			if i.account == account && aws.ToString(i.inst.InstanceId) == id &&
				i.inst.State.Name != types.InstanceStateNameTerminated {
				i.inst.State = &types.InstanceState{Name: types.InstanceStateNameShuttingDown}
			}
		}
	}
	return &ec2.TerminateInstancesOutput{}, nil
}

func (c *fakeEC2) ModifyInstanceMetadataOptions(_ context.Context, in *ec2.ModifyInstanceMetadataOptionsInput,
	_ ...func(*ec2.Options)) (*ec2.ModifyInstanceMetadataOptionsOutput, error) {
	account, done, err := c.begin("ModifyInstanceMetadataOptions")
	if err != nil {
		return nil, err
	}
	defer done()
	for _, i := range c.f.instances {
		if i.account == account && aws.ToString(i.inst.InstanceId) == aws.ToString(in.InstanceId) {
			md := i.inst.MetadataOptions
			md.HttpEndpoint, md.State = in.HttpEndpoint, types.InstanceMetadataOptionsStateApplied
			if c.f.metadataPending {
				md.State = types.InstanceMetadataOptionsStatePending
			}
			copied := *md
			return &ec2.ModifyInstanceMetadataOptionsOutput{InstanceId: in.InstanceId, InstanceMetadataOptions: &copied}, nil
		}
	}
	return nil, apiErr("InvalidInstanceID.NotFound", "The instance ID does not exist")
}

func (c *fakeEC2) DescribeSubnets(_ context.Context, in *ec2.DescribeSubnetsInput,
	_ ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	account, done, err := c.begin("DescribeSubnets")
	if err != nil {
		return nil, err
	}
	defer done()
	out := &ec2.DescribeSubnetsOutput{}
	for _, id := range in.SubnetIds {
		vpc := c.f.subnets[account+"/"+c.cfg.Region+"/"+id]
		if vpc == "" {
			return nil, apiErr("InvalidSubnetID.NotFound", "The subnet ID '"+id+"' does not exist")
		}
		out.Subnets = append(out.Subnets, types.Subnet{SubnetId: aws.String(id), VpcId: aws.String(vpc)})
	}
	return out, nil
}

// group is security group id of account; f is locked.
func (c *fakeEC2) group(account string, id *string) (*fakeGroup, error) {
	for _, g := range c.f.groups {
		if g.account == account && aws.ToString(g.sg.GroupId) == aws.ToString(id) {
			return g, nil
		}
	}
	return nil, apiErr("InvalidGroup.NotFound", "The security group does not exist")
}

func (c *fakeEC2) DescribeSecurityGroups(_ context.Context, in *ec2.DescribeSecurityGroupsInput,
	_ ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
	account, done, err := c.begin("DescribeSecurityGroups")
	if err != nil {
		return nil, err
	}
	defer done()
	out := &ec2.DescribeSecurityGroupsOutput{}
	for _, g := range c.f.groups {
		if g.account != account || g.region != c.cfg.Region {
			continue
		}
		if len(in.GroupIds) > 0 && !slices.Contains(in.GroupIds, aws.ToString(g.sg.GroupId)) {
			continue
		}
		if matches(g.sg.Tags, "", aws.ToString(g.sg.VpcId), in.Filters) {
			out.SecurityGroups = append(out.SecurityGroups, g.sg)
		}
	}
	return out, nil
}

func (c *fakeEC2) CreateSecurityGroup(_ context.Context, in *ec2.CreateSecurityGroupInput,
	_ ...func(*ec2.Options)) (*ec2.CreateSecurityGroupOutput, error) {
	account, done, err := c.begin("CreateSecurityGroup")
	if err != nil {
		return nil, err
	}
	defer done()
	f := c.f
	for _, g := range f.groups {
		if g.account == account && aws.ToString(g.sg.VpcId) == aws.ToString(in.VpcId) &&
			aws.ToString(g.sg.GroupName) == aws.ToString(in.GroupName) {
			return nil, apiErr("InvalidGroup.Duplicate", "The security group already exists")
		}
	}
	f.seq++
	g := &fakeGroup{account: account, region: c.cfg.Region, sg: types.SecurityGroup{
		GroupId: aws.String(fmt.Sprintf("sg-%017x", f.seq)), GroupName: in.GroupName, VpcId: in.VpcId,
		Description: in.Description, Tags: in.TagSpecifications[0].Tags,
		// A new group lets everything out.
		IpPermissionsEgress: []types.IpPermission{{IpProtocol: aws.String("-1"),
			IpRanges: []types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}}}}}}
	f.groups = append(f.groups, g)
	return &ec2.CreateSecurityGroupOutput{GroupId: g.sg.GroupId}, nil
}

func (c *fakeEC2) DeleteSecurityGroup(_ context.Context, in *ec2.DeleteSecurityGroupInput,
	_ ...func(*ec2.Options)) (*ec2.DeleteSecurityGroupOutput, error) {
	account, done, err := c.begin("DeleteSecurityGroup")
	if err != nil {
		return nil, err
	}
	defer done()
	g, err := c.group(account, in.GroupId)
	if err != nil {
		return nil, err
	}
	for _, i := range c.f.instances {
		if i.inst.State.Name != types.InstanceStateNameTerminated && slices.ContainsFunc(i.inst.SecurityGroups,
			func(id types.GroupIdentifier) bool { return aws.ToString(id.GroupId) == aws.ToString(g.sg.GroupId) }) {
			return nil, apiErr("DependencyViolation", "resource "+aws.ToString(g.sg.GroupId)+" has a dependent object")
		}
	}
	c.f.groups = slices.DeleteFunc(c.f.groups, func(x *fakeGroup) bool { return x == g })
	return &ec2.DeleteSecurityGroupOutput{}, nil
}

// change adds or removes perms of a group's rules.
func change(rules []types.IpPermission, perms []types.IpPermission, add bool) ([]types.IpPermission, error) {
	have := flatten(rules)
	for _, r := range flatten(perms) {
		switch i := slices.Index(have, r); {
		case add && i >= 0:
			return nil, apiErr("InvalidPermission.Duplicate", "the specified rule already exists")
		case add:
			have = append(have, r)
		case i < 0:
			return nil, apiErr("InvalidPermission.NotFound", "the specified rule does not exist")
		default:
			have = slices.Delete(have, i, i+1)
		}
	}
	return permissions(have), nil
}

func (c *fakeEC2) rules(call string, id *string, perms []types.IpPermission, egress, add bool) error {
	account, done, err := c.begin(call)
	if err != nil {
		return err
	}
	defer done()
	g, err := c.group(account, id)
	if err != nil {
		return err
	}
	if egress {
		g.sg.IpPermissionsEgress, err = change(g.sg.IpPermissionsEgress, perms, add)
	} else {
		g.sg.IpPermissions, err = change(g.sg.IpPermissions, perms, add)
	}
	return err
}

func (c *fakeEC2) AuthorizeSecurityGroupIngress(_ context.Context, in *ec2.AuthorizeSecurityGroupIngressInput,
	_ ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupIngressOutput, error) {
	return &ec2.AuthorizeSecurityGroupIngressOutput{},
		c.rules("AuthorizeSecurityGroupIngress", in.GroupId, in.IpPermissions, false, true)
}

func (c *fakeEC2) RevokeSecurityGroupIngress(_ context.Context, in *ec2.RevokeSecurityGroupIngressInput,
	_ ...func(*ec2.Options)) (*ec2.RevokeSecurityGroupIngressOutput, error) {
	return &ec2.RevokeSecurityGroupIngressOutput{},
		c.rules("RevokeSecurityGroupIngress", in.GroupId, in.IpPermissions, false, false)
}

func (c *fakeEC2) AuthorizeSecurityGroupEgress(_ context.Context, in *ec2.AuthorizeSecurityGroupEgressInput,
	_ ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupEgressOutput, error) {
	return &ec2.AuthorizeSecurityGroupEgressOutput{},
		c.rules("AuthorizeSecurityGroupEgress", in.GroupId, in.IpPermissions, true, true)
}

func (c *fakeEC2) RevokeSecurityGroupEgress(_ context.Context, in *ec2.RevokeSecurityGroupEgressInput,
	_ ...func(*ec2.Options)) (*ec2.RevokeSecurityGroupEgressOutput, error) {
	return &ec2.RevokeSecurityGroupEgressOutput{},
		c.rules("RevokeSecurityGroupEgress", in.GroupId, in.IpPermissions, true, false)
}

type fakeSTS struct {
	f   *fakeAWS
	cfg aws.Config
}

func (c *fakeSTS) GetCallerIdentity(_ context.Context, _ *sts.GetCallerIdentityInput,
	_ ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	account, err := c.f.caller(c.cfg, "GetCallerIdentity")
	if err != nil {
		if apiCode(err) == "AuthFailure" {
			err = apiErr("InvalidClientTokenId", "The security token included in the request is invalid.")
		}
		return nil, err
	}
	return &sts.GetCallerIdentityOutput{Account: aws.String(account),
		Arn: aws.String("arn:aws:iam::" + account + ":user/theos")}, nil
}

type fakePricing struct {
	f   *fakeAWS
	cfg aws.Config
}

func (c *fakePricing) GetProducts(_ context.Context, in *pricing.GetProductsInput,
	_ ...func(*pricing.Options)) (*pricing.GetProductsOutput, error) {
	c.f.mu.Lock()
	defer c.f.mu.Unlock()
	if _, err := c.f.caller(c.cfg, "GetProducts"); err != nil {
		return nil, err
	}
	c.f.priceReads++
	fields := map[string]string{}
	for _, flt := range in.Filters {
		fields[aws.ToString(flt.Field)] = aws.ToString(flt.Value)
	}
	if fields["operatingSystem"] != "Linux" || fields["tenancy"] != "Shared" || fields["capacitystatus"] != "Used" {
		return &pricing.GetProductsOutput{}, nil
	}
	usd, ok := c.f.prices[fields["regionCode"]+"/"+fields["instanceType"]]
	if !ok {
		return &pricing.GetProductsOutput{}, nil
	}
	return &pricing.GetProductsOutput{PriceList: []string{priceList(fields["instanceType"], usd)}}, nil
}

// priceList is a Price List product of instanceType at usd an hour on
// demand, as AWS writes one.
func priceList(instanceType, usd string) string {
	return `{"product":{"productFamily":"Compute Instance","attributes":{"instanceType":"` + instanceType +
		`","operatingSystem":"Linux"},"sku":"SKU1"},"serviceCode":"AmazonEC2","terms":{"OnDemand":{"SKU1.JRTCKXETXF":` +
		`{"priceDimensions":{"SKU1.JRTCKXETXF.6YS6EN2CT7":{"unit":"Hrs","endRange":"Inf","description":"$` + usd +
		` per On Demand Linux ` + instanceType + ` Instance Hour","pricePerUnit":{"USD":"` + usd + `"}}},` +
		`"sku":"SKU1","effectiveDate":"2026-10-01T00:00:00Z","offerTermCode":"JRTCKXETXF"}}}}`
}
