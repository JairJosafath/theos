package awsenv

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/JairJosafath/theos/internal/model"
)

// The machine's fence (T5): its security group, the operator's own, lets
// in theos-env's port from anywhere, where the lease's token and pinned
// certificate guard it, and lets out only DNS and HTTPS to public
// addresses, as the local fence does. Amazon's own DNS resolver is never
// filtered by a security group.

// rule is one rule of a security group: a protocol, its ports, and one peer
// (ipv4:<cidr>, ipv6:<cidr>, group:<id> or prefix:<id>).
type rule struct {
	protocol string
	from, to int32
	peer     string
}

// anywhere is every IPv4 address, as a rule's peer.
const anywhere = "ipv4:0.0.0.0/0"

// tcp is the protocol theos-env, HTTPS and DNS over TCP take.
const tcp = "tcp"

// FenceRules are the ingress and egress rules of an environment's security
// group.
func FenceRules() (ingress, egress []rule) {
	https := int32(model.TestEnvRemotePort)
	ingress = []rule{{tcp, https, https, anywhere}}
	for _, cidr := range model.PublicRanges() {
		egress = append(egress, rule{tcp, 443, 443, "ipv4:" + cidr})
	}
	egress = append(egress, rule{"udp", 53, 53, anywhere}, rule{tcp, 53, 53, anywhere})
	return ingress, egress
}

// groupName is the name of env's security group in its VPC.
func (s *session) groupName() string {
	return "theos-env-" + s.env.Namespace + "-" + s.env.Name + "-" + s.env.UID
}

// fence returns the environment's security group in its subnet's VPC, made
// when missing and set to FenceRules: a rule added by hand is revoked, one
// taken away added again.
func (s *session) fence(ctx context.Context) (string, error) {
	subnets, err := s.clients.EC2.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{SubnetIds: []string{s.env.AWS.SubnetID}})
	if err != nil {
		return "", portError(err)
	}
	if len(subnets.Subnets) == 0 || aws.ToString(subnets.Subnets[0].VpcId) == "" {
		return "", fmt.Errorf("subnet %s is in no VPC", s.env.AWS.SubnetID)
	}
	vpc := aws.ToString(subnets.Subnets[0].VpcId)
	groups, err := s.groups(ctx, vpc)
	if err != nil {
		return "", err
	}
	var group types.SecurityGroup
	if len(groups) > 0 {
		group = groups[0]
	} else {
		created, err := s.clients.EC2.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
			GroupName: aws.String(s.groupName()), VpcId: aws.String(vpc),
			Description: aws.String("theos test environment " + s.env.Namespace + "/" + s.env.Name +
				": theos-env's port in, DNS and public HTTPS out"),
			TagSpecifications: []types.TagSpecification{{ResourceType: types.ResourceTypeSecurityGroup, Tags: []types.Tag{
				{Key: aws.String("Name"), Value: aws.String(s.groupName())},
				{Key: aws.String(TagEnvironment), Value: aws.String(s.env.Namespace + "/" + s.env.Name)},
				{Key: aws.String(TagEnvironmentUID), Value: aws.String(s.env.UID)}}}},
		})
		if err != nil {
			return "", portError(err)
		}
		// A new group lets everything out; read it back to revoke that.
		read, err := s.clients.EC2.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
			GroupIds: []string{aws.ToString(created.GroupId)}})
		if err != nil {
			return "", portError(err)
		}
		if len(read.SecurityGroups) == 0 {
			return "", fmt.Errorf("security group %s is gone", aws.ToString(created.GroupId))
		}
		group = read.SecurityGroups[0]
	}
	return aws.ToString(group.GroupId), s.syncRules(ctx, group)
}

// groups are the environment's security groups, in vpc when it is set.
func (s *session) groups(ctx context.Context, vpc string) ([]types.SecurityGroup, error) {
	filters := []types.Filter{{Name: aws.String("tag:" + TagEnvironmentUID), Values: []string{s.env.UID}}}
	if vpc != "" {
		filters = append(filters, types.Filter{Name: aws.String("vpc-id"), Values: []string{vpc}})
	}
	var out []types.SecurityGroup
	in := &ec2.DescribeSecurityGroupsInput{Filters: filters}
	for {
		page, err := s.clients.EC2.DescribeSecurityGroups(ctx, in)
		if err != nil {
			return nil, portError(err)
		}
		out = append(out, page.SecurityGroups...)
		if aws.ToString(page.NextToken) == "" {
			break
		}
		in.NextToken = page.NextToken
	}
	slices.SortFunc(out, func(a, b types.SecurityGroup) int {
		return strings.Compare(aws.ToString(a.GroupId), aws.ToString(b.GroupId))
	})
	return out, nil
}

// syncRules sets group's rules to FenceRules: revokes what it has more,
// then authorizes what it lacks.
func (s *session) syncRules(ctx context.Context, group types.SecurityGroup) error {
	id := group.GroupId
	wantIn, wantOut := FenceRules()
	extraIn, missingIn := diffRules(flatten(group.IpPermissions), wantIn)
	extraOut, missingOut := diffRules(flatten(group.IpPermissionsEgress), wantOut)
	if len(extraIn) > 0 {
		if _, err := s.clients.EC2.RevokeSecurityGroupIngress(ctx, &ec2.RevokeSecurityGroupIngressInput{
			GroupId: id, IpPermissions: permissions(extraIn)}); err != nil {
			return portError(err)
		}
	}
	if len(extraOut) > 0 {
		if _, err := s.clients.EC2.RevokeSecurityGroupEgress(ctx, &ec2.RevokeSecurityGroupEgressInput{
			GroupId: id, IpPermissions: permissions(extraOut)}); err != nil {
			return portError(err)
		}
	}
	if len(missingIn) > 0 {
		if _, err := s.clients.EC2.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
			GroupId: id, IpPermissions: permissions(missingIn)}); err != nil && !duplicate(err) {
			return portError(err)
		}
	}
	if len(missingOut) > 0 {
		if _, err := s.clients.EC2.AuthorizeSecurityGroupEgress(ctx, &ec2.AuthorizeSecurityGroupEgressInput{
			GroupId: id, IpPermissions: permissions(missingOut)}); err != nil && !duplicate(err) {
			return portError(err)
		}
	}
	return nil
}

// duplicate says err is AWS's answer to a rule the group has already, as
// two operators racing would see it.
func duplicate(err error) bool { return apiCode(err) == "InvalidPermission.Duplicate" }

// diffRules are the rules of have that want lacks, and those of want that
// have lacks.
func diffRules(have, want []rule) (extra, missing []rule) {
	for _, r := range have {
		if !slices.Contains(want, r) {
			extra = append(extra, r)
		}
	}
	for _, r := range want {
		if !slices.Contains(have, r) {
			missing = append(missing, r)
		}
	}
	return extra, missing
}

// ports of a permission, -1 for none (all traffic).
func portOf(p *int32) int32 {
	if p == nil {
		return -1
	}
	return *p
}

// flatten makes perms one rule per peer.
func flatten(perms []types.IpPermission) []rule {
	var out []rule
	for _, p := range perms {
		r := rule{protocol: aws.ToString(p.IpProtocol), from: portOf(p.FromPort), to: portOf(p.ToPort)}
		if r.protocol == "-1" {
			r.from, r.to = -1, -1
		}
		for _, x := range p.IpRanges {
			r.peer = "ipv4:" + aws.ToString(x.CidrIp)
			out = append(out, r)
		}
		for _, x := range p.Ipv6Ranges {
			r.peer = "ipv6:" + aws.ToString(x.CidrIpv6)
			out = append(out, r)
		}
		for _, x := range p.UserIdGroupPairs {
			r.peer = "group:" + aws.ToString(x.GroupId)
			out = append(out, r)
		}
		for _, x := range p.PrefixListIds {
			r.peer = "prefix:" + aws.ToString(x.PrefixListId)
			out = append(out, r)
		}
	}
	return out
}

// permissions are rules as AWS takes them, one permission per protocol and
// ports.
func permissions(rules []rule) []types.IpPermission {
	type ports struct {
		protocol string
		from, to int32
	}
	var order []ports
	by := map[ports]*types.IpPermission{}
	for _, r := range rules {
		k := ports{r.protocol, r.from, r.to}
		p, ok := by[k]
		if !ok {
			p = &types.IpPermission{IpProtocol: aws.String(r.protocol)}
			if r.from >= 0 {
				p.FromPort, p.ToPort = aws.Int32(r.from), aws.Int32(r.to)
			}
			by[k] = p
			order = append(order, k)
		}
		kind, value, _ := strings.Cut(r.peer, ":")
		switch kind {
		case "ipv4":
			p.IpRanges = append(p.IpRanges, types.IpRange{CidrIp: aws.String(value)})
		case "ipv6":
			p.Ipv6Ranges = append(p.Ipv6Ranges, types.Ipv6Range{CidrIpv6: aws.String(value)})
		case "group":
			p.UserIdGroupPairs = append(p.UserIdGroupPairs, types.UserIdGroupPair{GroupId: aws.String(value)})
		case "prefix":
			p.PrefixListIds = append(p.PrefixListIds, types.PrefixListId{PrefixListId: aws.String(value)})
		}
	}
	out := make([]types.IpPermission, 0, len(order))
	for _, k := range order {
		out = append(out, *by[k])
	}
	return out
}

// UserData is the script a machine runs at its first boot (T11): shut down,
// which terminates it, at the lease's deadline; download theos-env for its
// architecture from toolsURL (${arch} amd64 or arm64), checked against the
// SHA256SUMS next to it; and serve the lease's runs on HTTPS's port with the
// key its token makes, under systemd when it has systemd-run.
func UserData(toolsURL, token string, deadline time.Duration) (string, error) {
	if !strings.HasPrefix(toolsURL, "https://") || strings.ContainsAny(toolsURL, "'\n\r\\") {
		return "", fmt.Errorf("the theos-env download address %q must be https:// with no quote, newline or backslash",
			toolsURL)
	}
	if !model.LeaseTokenSafe(token) {
		return "", errors.New("a lease's token must be letters, digits, '-', '_' and '.'")
	}
	// Single-quoted, so the shell expands nothing of the address but $arch.
	url := "'" + toolsURL + "'"
	if before, after, ok := strings.Cut(toolsURL, "${arch}"); ok {
		url = "'" + before + `'"$arch"'` + after + "'"
	}
	minutes := int64(math.Ceil(deadline.Minutes()))
	serve := "/usr/local/bin/theos-env serve --listen :" + strconv.Itoa(model.TestEnvRemotePort) + " --work /work --tls"
	r := strings.NewReplacer("@MINUTES@", strconv.FormatInt(minutes, 10), "@URL@", url, "@TOKEN@", token,
		"@SERVE@", serve)
	return r.Replace(userDataScript), nil
}

const userDataScript = `#!/bin/sh
# A theos test environment's machine (docs/plans/test-environments.md, T11):
# it serves one lease's runs with theos-env over HTTPS and shuts down, which
# terminates it, at the lease's deadline.
set -eu
shutdown -h +@MINUTES@ || true
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*)
	echo "theos-env: no build for $(uname -m)" >&2
	exit 1
	;;
esac
url=@URL@
name=${url##*/}
dir=$(mktemp -d)
curl -fsSL --retry 5 -o "$dir/$name" "$url"
curl -fsSL --retry 5 -o "$dir/SHA256SUMS" "${url%/*}/SHA256SUMS"
(cd "$dir" && grep " \*\{0,1\}$name\$" SHA256SUMS | sha256sum -c -)
install -m 0755 "$dir/$name" /usr/local/bin/theos-env
mkdir -p /work
if command -v systemd-run >/dev/null 2>&1; then
	systemd-run --unit=theos-env --property=Restart=on-failure --setenv=THEOS_ENV_TOKEN=@TOKEN@ @SERVE@
else
	THEOS_ENV_TOKEN=@TOKEN@ nohup @SERVE@ >/var/log/theos-env.log 2>&1 &
fi
`
