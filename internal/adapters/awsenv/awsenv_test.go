package awsenv

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/pricing"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
)

const (
	toolsURL = "https://github.com/JairJosafath/theos/releases/download/v0.31.0/theos-env-linux-${arch}"
	account  = "123456789012"
	region   = "eu-west-1"
	subnet   = "subnet-0123456789abcdef0"
	gpuType  = "g5.xlarge"
	// gpuPrice is g5.xlarge's on-demand price an hour, as the Price List
	// writes it.
	gpuPrice = "1.0060000000"
)

// clock is a test's time, moved by hand.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// world is a fake AWS, a provider on it and the test's clock.
type world struct {
	aws   *fakeAWS
	p     *Provider
	clock *clock
	envs  int
}

func newWorld() *world {
	c := &clock{t: time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)}
	f := newFakeAWS(c.now)
	p := New(f.clients, toolsURL)
	p.probe, p.now = f.probe, c.now
	return &world{aws: f, p: p, clock: c}
}

// provider is another provider on the same AWS: the operator after a
// restart.
func (w *world) provider() *Provider {
	p := New(w.aws.clients, toolsURL)
	p.probe, p.now = w.aws.probe, w.clock.now
	return p
}

// gpu is a new GPU environment in account, with a key of its own.
func (w *world) gpu() ports.Environment {
	w.envs++
	key := rand.Text()
	w.aws.account(key, account, region, subnet, gpuType, gpuPrice)
	return ports.Environment{Namespace: "apps", Name: "gpu", UID: fmt.Sprintf("0b6f3c1e-%04d", w.envs),
		Member: model.TestEnvMemberAWS,
		AWS: &ports.AWSEnvironment{Region: region, Account: account, InstanceType: gpuType,
			ImageID: "ami-0123456789abcdef0", SubnetID: subnet},
		Secret: map[string][]byte{"accessKeyId": []byte(key), "secretAccessKey": []byte(rand.Text())}}
}

func request(env ports.Environment, run string) ports.LeaseRequest {
	return ports.LeaseRequest{ID: model.LeaseID(env.Name, run), Env: env, Holder: "add-shadows", Token: rand.Text(),
		Deadline: 45 * time.Minute}
}

func (w *world) lease(t *testing.T, req ports.LeaseRequest) ports.Lease {
	t.Helper()
	l, err := w.p.Lease(context.Background(), req)
	if err != nil {
		t.Fatalf("Lease = %v", err)
	}
	return l
}

func (w *world) get(t *testing.T, env ports.Environment, id string) ports.Lease {
	t.Helper()
	l, err := w.p.Get(context.Background(), env, id)
	if err != nil {
		t.Fatalf("Get = %v", err)
	}
	return l
}

// The contract every provider meets (T6), on a fake EC2: a lease is one
// machine however often it is asked for, Ready with an HTTPS endpoint once
// it runs and theos-env answers, Failed once it stopped; Return terminates
// it, and Leases finds the environment's machines by tag.
func TestTestEnvironmentsContract(t *testing.T) {
	w := newWorld()
	contract.TestEnvironments(t, contract.TestEnvironmentsEnv{
		Provider:    w.p,
		Environment: func(*testing.T) ports.Environment { return w.gpu() },
		Places:      func(_ *testing.T, env ports.Environment) int { return w.aws.live(env.UID) },
		Ready:       func(_ *testing.T, env ports.Environment, id string) { w.aws.ready(env.UID, id) },
		Fail:        func(_ *testing.T, env ports.Environment, id string) { w.aws.stop(env.UID, id) },
	})
}

// launchInput is the RunInstances call of a lease of a new GPU environment,
// taken at 09:00 for 45 minutes.
func launchInput(t *testing.T) (ports.LeaseRequest, *ec2.RunInstancesInput) {
	t.Helper()
	req := request(newWorld().gpu(), "add-shadows-r1-implement")
	in, err := LaunchInput(req, "sg-0123456789abcdef0", 1_006_000, toolsURL,
		time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return req, in
}

// The machine is fenced in its RunInstances call (T5): no instance profile
// and no SSH key; one network interface, with a public address, in the
// environment's group alone; the metadata service IMDSv2 only, one hop, no
// tags, no IPv6, until the provider turns it off; shutting down terminates
// it.
func TestLaunchInputIsFenced(t *testing.T) {
	_, in := launchInput(t)
	if in.IamInstanceProfile != nil {
		t.Errorf("IamInstanceProfile %+v, want none: the machine gets no role", in.IamInstanceProfile)
	}
	if in.KeyName != nil || in.LaunchTemplate != nil {
		t.Errorf("KeyName %v, LaunchTemplate %v: want neither", in.KeyName, in.LaunchTemplate)
	}
	if len(in.SecurityGroupIds) != 0 || len(in.SecurityGroups) != 0 || in.SubnetId != nil {
		t.Error("the call names groups or a subnet outside its one network interface")
	}
	if len(in.NetworkInterfaces) != 1 {
		t.Fatalf("network interfaces %d, want 1", len(in.NetworkInterfaces))
	}
	ni := in.NetworkInterfaces[0]
	if aws.ToInt32(ni.DeviceIndex) != 0 || aws.ToString(ni.SubnetId) != subnet ||
		!slices.Equal(ni.Groups, []string{"sg-0123456789abcdef0"}) || !aws.ToBool(ni.AssociatePublicIpAddress) ||
		!aws.ToBool(ni.DeleteOnTermination) {
		t.Errorf("network interface %+v, want the subnet, the fence's group alone and a public address", ni)
	}
	md := in.MetadataOptions
	if md == nil || md.HttpTokens != types.HttpTokensStateRequired || aws.ToInt32(md.HttpPutResponseHopLimit) != 1 ||
		md.InstanceMetadataTags != types.InstanceMetadataTagsStateDisabled ||
		md.HttpProtocolIpv6 != types.InstanceMetadataProtocolStateDisabled ||
		md.HttpEndpoint != types.InstanceMetadataEndpointStateEnabled {
		t.Errorf("metadata options %+v, want IMDSv2 only, one hop, no tags, no IPv6, on for the user data alone", md)
	}
	if in.InstanceInitiatedShutdownBehavior != types.ShutdownBehaviorTerminate {
		t.Errorf("shutdown behaviour %q, want terminate: the deadline's shutdown ends the machine",
			in.InstanceInitiatedShutdownBehavior)
	}
	if aws.ToString(in.ImageId) != "ami-0123456789abcdef0" || in.InstanceType != gpuType ||
		aws.ToInt32(in.MinCount) != 1 || aws.ToInt32(in.MaxCount) != 1 {
		t.Errorf("image %s, type %s, count %d-%d: want one machine of the environment's", aws.ToString(in.ImageId),
			in.InstanceType, aws.ToInt32(in.MinCount), aws.ToInt32(in.MaxCount))
	}
}

// The machine, its volumes and its network interface are tagged with the
// lease, its holder, the environment, the deadline, the price and the key
// theos-env serves with; the user data holds the lease's token and none of
// the credential (T9, T11).
func TestLaunchInputTagsTheLease(t *testing.T) {
	req, in := launchInput(t)
	env := req.Env
	pin, _ := model.TestEnvKeyPin(req.Token)
	want := map[string]string{TagLease: req.ID, TagHolder: "add-shadows", TagEnvironment: "apps/gpu",
		TagEnvironmentUID: env.UID, TagDeadline: "2026-11-06T09:45:00Z", TagPrice: "1006000", TagKey: pin}
	resources := map[types.ResourceType]bool{}
	for _, spec := range in.TagSpecifications {
		resources[spec.ResourceType] = true
		for k, v := range want {
			if got := tag(spec.Tags, k); got != v {
				t.Errorf("%s tag %s = %q, want %q", spec.ResourceType, k, got, v)
			}
		}
	}
	if !resources[types.ResourceTypeInstance] || !resources[types.ResourceTypeVolume] ||
		!resources[types.ResourceTypeNetworkInterface] {
		t.Errorf("tagged %v, want the machine, its volumes and its network interface", resources)
	}
	data, err := base64.StdEncoding.DecodeString(aws.ToString(in.UserData))
	if err != nil || !strings.Contains(string(data), "THEOS_ENV_TOKEN="+req.Token) {
		t.Errorf("user data %q, want the script that serves the lease's token", data)
	}
	for _, key := range []string{"accessKeyId", "secretAccessKey"} {
		if strings.Contains(string(data), string(env.Secret[key])) {
			t.Errorf("the user data holds the credential's %s", key)
		}
	}
}

// The user data shuts the machine down at the deadline, installs theos-env
// for the machine's architecture after checking it against SHA256SUMS, and
// serves it with TLS on HTTPS's port; it takes no address the shell could
// read as more than one (T11).
func TestUserData(t *testing.T) {
	token := rand.Text()
	script, err := UserData(toolsURL, token, 44*time.Minute+30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"#!/bin/sh\n", "set -eu\n", "shutdown -h +45 ",
		"url='https://github.com/JairJosafath/theos/releases/download/v0.31.0/theos-env-linux-'\"$arch\"''\n",
		`"${url%/*}/SHA256SUMS"`, "sha256sum -c -", "install -m 0755 \"$dir/$name\" /usr/local/bin/theos-env",
		"--setenv=THEOS_ENV_TOKEN=" + token + " /usr/local/bin/theos-env serve --listen :443 --work /work --tls\n",
		"THEOS_ENV_TOKEN=" + token + " nohup /usr/local/bin/theos-env serve --listen :443 --work /work --tls",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("user data lacks %q:\n%s", want, script)
		}
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh to read the script with")
	}
	if out, err := exec.Command(sh, "-n", "-c", script).CombinedOutput(); err != nil { //nolint:gosec // the test's own script
		t.Errorf("sh -n: %v: %s", err, out)
	}
	// What the machine downloads, and the line of SHA256SUMS it checks it by.
	var lines []string
	for line := range strings.Lines(script) {
		if strings.HasPrefix(line, "url=") || strings.HasPrefix(line, "name=") ||
			strings.HasPrefix(line, "(cd \"$dir\" && grep") {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	probe := "arch=arm64\n" + lines[0] + "\n" + lines[1] + "\necho \"$url\"\necho \"${url%/*}/SHA256SUMS\"\n" +
		strings.Replace(strings.Replace(lines[2], `(cd "$dir" && `, "", 1), " | sha256sum -c -)", "", 1) + "\n"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte("0a1b  theos-env-linux-amd64\n"+
		"2c3d  theos-env-linux-arm64\n4e5f  theos-session-linux-arm64\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(sh, "-c", probe) //nolint:gosec // the test's own script
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if want := "https://github.com/JairJosafath/theos/releases/download/v0.31.0/theos-env-linux-arm64\n" +
		"https://github.com/JairJosafath/theos/releases/download/v0.31.0/SHA256SUMS\n" +
		"2c3d  theos-env-linux-arm64\n"; err != nil || string(out) != want {
		t.Errorf("the script's download lines give %q, %v; want %q", out, err, want)
	}
	for _, bad := range []struct{ url, token string }{
		{"http://downloads.example.com/theos-env-linux-${arch}", token},
		{"https://example.com/it's/theos-env-${arch}", token},
		{"https://example.com/theos-env\nreboot", token},
		{toolsURL, "$(reboot)"},
		{toolsURL, ""},
	} {
		if _, err := UserData(bad.url, bad.token, time.Hour); err == nil {
			t.Errorf("UserData(%q, %q) succeeded", bad.url, bad.token)
		}
	}
}

// The environment's security group is made in its subnet's VPC with the
// fence's rules alone: theos-env's port in; DNS, and HTTPS to public
// addresses, out; the new group's allow-all egress revoked. A rule added by
// hand is revoked on the next launch (T5).
func TestSecurityGroupIsTheFence(t *testing.T) {
	w := newWorld()
	env := w.gpu()
	w.lease(t, request(env, "add-shadows-r1-implement"))
	if len(w.aws.groups) != 1 {
		t.Fatalf("security groups %d, want 1", len(w.aws.groups))
	}
	g := w.aws.groups[0]
	ingress, egress := FenceRules()
	check := func(t *testing.T) {
		t.Helper()
		if got := flatten(g.sg.IpPermissions); !sameRules(got, ingress) {
			t.Errorf("ingress %v, want %v", got, ingress)
		}
		if got := flatten(g.sg.IpPermissionsEgress); !sameRules(got, egress) {
			t.Errorf("egress %v, want %v", got, egress)
		}
	}
	check(t)
	if aws.ToString(g.sg.VpcId) != "vpc-0123456789abcdef0" || tag(g.sg.Tags, TagEnvironmentUID) != env.UID {
		t.Errorf("group in %s tagged %v, want the subnet's VPC and the environment", aws.ToString(g.sg.VpcId), g.sg.Tags)
	}
	if !slices.Contains(ingress, rule{"tcp", 443, 443, "ipv4:0.0.0.0/0"}) || len(ingress) != 1 {
		t.Errorf("ingress %v, want theos-env's port alone", ingress)
	}
	for _, r := range egress {
		if r.protocol == "tcp" && r.from == 443 && slices.ContainsFunc(model.PrivateRanges(), func(p string) bool {
			return r.peer == "ipv4:"+p
		}) {
			t.Errorf("egress %v reaches a private range", r)
		}
		if r.from != 443 && r.from != 53 || r.from != r.to {
			t.Errorf("egress %v is neither HTTPS nor DNS", r)
		}
	}

	// A group someone widened is narrowed again.
	g.sg.IpPermissions = append(g.sg.IpPermissions, types.IpPermission{IpProtocol: aws.String("tcp"),
		FromPort: aws.Int32(22), ToPort: aws.Int32(22), IpRanges: []types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}}})
	g.sg.IpPermissionsEgress = append(g.sg.IpPermissionsEgress, types.IpPermission{IpProtocol: aws.String("-1"),
		UserIdGroupPairs: []types.UserIdGroupPair{{GroupId: aws.String("sg-0aaaaaaaaaaaaaaaa")}}})
	w.lease(t, request(env, "add-shadows-r1-verify"))
	check(t)
	if len(w.aws.groups) != 1 {
		t.Errorf("security groups %d after a second launch, want the one", len(w.aws.groups))
	}
}

func sameRules(a, b []rule) bool {
	if len(a) != len(b) {
		return false
	}
	for _, r := range a {
		if !slices.Contains(b, r) {
			return false
		}
	}
	return true
}

// The metadata service goes off only once theos-env answers with the
// lease's key, and the lease is Ready only once that applied: no run reaches
// a machine whose metadata service is on (T5).
func TestMetadataServiceIsOffBeforeReady(t *testing.T) {
	w := newWorld()
	env := w.gpu()
	req := request(env, "add-shadows-r1-implement")
	if l := w.lease(t, req); l.State != model.LeasePending || l.Endpoint != "" {
		t.Fatalf("Lease = %+v, want Pending with no endpoint", l)
	}
	w.aws.metadataPending = true
	w.aws.run(env.UID, req.ID)
	l := w.get(t, env, req.ID)
	if l.State != model.LeasePending || !strings.Contains(l.Message, "theos-env doesn't answer") {
		t.Errorf("Get = %+v, want Pending until theos-env answers", l)
	}
	if n := w.aws.count("ModifyInstanceMetadataOptions"); n != 0 {
		t.Errorf("the metadata service was turned off %d times before theos-env answered", n)
	}
	w.aws.answer(env.UID, req.ID)
	if l = w.get(t, env, req.ID); l.State != model.LeasePending || !strings.Contains(l.Message, "metadata service off") {
		t.Errorf("Get = %+v, want Pending while the metadata service goes off", l)
	}
	if w.aws.machine(env.UID, req.ID).inst.MetadataOptions.HttpEndpoint != types.InstanceMetadataEndpointStateDisabled {
		t.Error("the metadata service is still on once theos-env answered")
	}
	w.aws.applyMetadata(env.UID, req.ID)
	l, err := w.p.Lease(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if l.State != model.LeaseReady || !strings.HasPrefix(l.Endpoint, "https://203.0.113.") ||
		!strings.HasSuffix(l.Endpoint, ":443") {
		t.Errorf("Lease = %+v, want Ready at https://<public address>:443", l)
	}
	cert, _, _ := model.TestEnvCertificate(req.Token)
	if l.CA != cert {
		t.Error("the lease's CA is not the certificate its token makes, which the machine serves")
	}
	if n := w.aws.count("ModifyInstanceMetadataOptions"); n != 1 {
		t.Errorf("the metadata service was turned off %d times, want once", n)
	}

	// It is never turned off for a machine that answers with another key.
	other := request(env, "add-shadows-r1-verify")
	w.lease(t, other)
	w.aws.ready(env.UID, other.ID)
	w.aws.machine(env.UID, other.ID).userData = "THEOS_ENV_TOKEN=" + rand.Text()
	if l := w.get(t, env, other.ID); l.State != model.LeasePending || !strings.Contains(l.Message, "isn't the lease's") {
		t.Errorf("Get = %+v, want Pending: what answers isn't the lease's theos-env", l)
	}
}

// A lease costs the price it was launched at for the time it has run, a
// minute at least; the Price List is read once per type (T10).
func TestCostAccruesByTime(t *testing.T) {
	w := newWorld()
	env := w.gpu()
	req := request(env, "add-shadows-r1-implement")
	if l := w.lease(t, req); l.CostMilliUSD != 17 {
		t.Errorf("cost at launch %d, want a minute's 17 thousandths", l.CostMilliUSD)
	}
	w.clock.add(30 * time.Minute)
	if l := w.get(t, env, req.ID); l.CostMilliUSD != 503 {
		t.Errorf("cost after 30 minutes %d, want 503", l.CostMilliUSD)
	}
	w.clock.add(30 * time.Minute)
	leases, err := w.p.Leases(context.Background(), env)
	if err != nil || len(leases) != 1 || leases[0].CostMilliUSD != 1006 {
		t.Errorf("Leases after an hour = %+v, %v; want one lease that cost 1006", leases, err)
	}
	// A price the Price List raises later doesn't change a lease taken.
	w.aws.prices[region+"/"+gpuType] = "2.0000000000"
	if l := w.get(t, env, req.ID); l.CostMilliUSD != 1006 {
		t.Errorf("cost after the price changed %d, want the launch price's 1006", l.CostMilliUSD)
	}
	w.lease(t, request(env, "add-shadows-r1-verify"))
	if w.aws.priceReads != 1 {
		t.Errorf("the Price List was read %d times, want once", w.aws.priceReads)
	}
}

// A credential of another account, one AWS doesn't know, or a Secret that
// lacks a key is refused before any machine starts (T9); an AWS that can't
// be reached is unreachable.
func TestCredentials(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	good := w.gpu()
	if err := w.p.CheckCredentials(ctx, good); err != nil {
		t.Fatalf("CheckCredentials = %v", err)
	}
	wrongAccount := w.gpu()
	wrongAccount.AWS.Account = "210987654321"
	unknown := w.gpu()
	unknown.Secret = map[string][]byte{"accessKeyId": []byte(rand.Text()), "secretAccessKey": []byte(rand.Text())}
	noSecret := w.gpu()
	delete(noSecret.Secret, "secretAccessKey")
	for _, tt := range []struct {
		name string
		env  ports.Environment
		says string
	}{
		{"another account", wrongAccount, "is of AWS account 123456789012, the environment names account 210987654321"},
		{"a key AWS doesn't know", unknown, "InvalidClientTokenId"},
		{"a Secret without secretAccessKey", noSecret, "lacks secretAccessKey"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := w.p.CheckCredentials(ctx, tt.env)
			if !errors.Is(err, ports.ErrCredentialRefused) || !strings.Contains(err.Error(), tt.says) {
				t.Errorf("CheckCredentials = %v, want refused, saying %q", err, tt.says)
			}
			_, err = w.p.Lease(ctx, request(tt.env, "add-shadows-r1-implement"))
			if !errors.Is(err, ports.ErrCredentialRefused) {
				t.Errorf("Lease = %v, want refused", err)
			}
			for _, key := range []string{"accessKeyId", "secretAccessKey"} {
				if v := tt.env.Secret[key]; len(v) > 0 && err != nil && strings.Contains(err.Error(), string(v)) {
					t.Errorf("the error holds the credential's %s", key)
				}
			}
		})
	}
	if n := w.aws.count("RunInstances"); n != 0 {
		t.Errorf("RunInstances called %d times with a refused credential", n)
	}
	w.aws.down = true
	if err := w.p.CheckCredentials(ctx, good); !errors.Is(err, ports.ErrUnreachable) {
		t.Errorf("CheckCredentials with AWS down = %v, want unreachable", err)
	}
}

// After a restart the operator finds its machines by their tags; a second
// machine of one lease, which a lost RunInstances answer would leave, is
// terminated (T11).
func TestLeasesFindsMachinesByTag(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	env := w.gpu()
	req := request(env, "add-shadows-r1-implement")
	first := w.lease(t, req)

	restarted := w.provider()
	leases, err := restarted.Leases(ctx, env)
	if err != nil || len(leases) != 1 || leases[0].ID != req.ID || leases[0].Holder != "add-shadows" ||
		!leases[0].Started.Equal(first.Started) {
		t.Fatalf("Leases after a restart = %+v, %v; want the lease", leases, err)
	}

	in, err := LaunchInput(req, "sg-0123456789abcdef0", 1_006_000, toolsURL, w.clock.now())
	if err != nil {
		t.Fatal(err)
	}
	w.clock.add(time.Second)
	cfg, err := config(env)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.aws.clients(cfg).EC2.RunInstances(ctx, in); err != nil {
		t.Fatal(err)
	}
	if n := w.aws.live(env.UID); n != 2 {
		t.Fatalf("machines %d, want the lease's two", n)
	}
	if l, err := restarted.Lease(ctx, req); err != nil || !l.Started.Equal(first.Started) {
		t.Errorf("Lease = %+v, %v; want the first machine", l, err)
	}
	if n := w.aws.live(env.UID); n != 1 {
		t.Errorf("machines %d after Lease, want the second terminated", n)
	}
}

// A launch AWS refuses on its own grounds, such as no capacity, is a Failed
// lease with AWS's message, which the run waits on and takes again.
func TestLaunchFailureIsAFailedLease(t *testing.T) {
	w := newWorld()
	env := w.gpu()
	w.aws.launchError = apiErr("InsufficientInstanceCapacity",
		"We currently do not have sufficient g5.xlarge capacity in the Availability Zone you requested")
	req := request(env, "add-shadows-r1-implement")
	l := w.lease(t, req)
	if l.State != model.LeaseFailed || l.ID != req.ID || !strings.Contains(l.Message, "sufficient g5.xlarge capacity") {
		t.Errorf("Lease = %+v, want Failed with AWS's message", l)
	}
	if err := w.p.Return(context.Background(), env, req.ID); err != nil {
		t.Errorf("Return of a lease that never launched = %v", err)
	}
}

// A machine past its lease's deadline is Failed; it shuts itself down then
// too, which terminates it.
func TestPastTheDeadline(t *testing.T) {
	w := newWorld()
	env := w.gpu()
	req := request(env, "add-shadows-r1-implement")
	w.lease(t, req)
	w.aws.ready(env.UID, req.ID)
	if l := w.get(t, env, req.ID); l.State != model.LeaseReady {
		t.Fatalf("Get = %+v, want Ready", l)
	}
	w.clock.add(45 * time.Minute)
	if l := w.get(t, env, req.ID); l.State != model.LeaseFailed || !strings.Contains(l.Message, "deadline") {
		t.Errorf("Get past the deadline = %+v, want Failed", l)
	}
}

// Release deletes the environment's security group once no machine is in
// it; while one shuts down AWS refuses, and Release is asked again.
func TestReleaseDeletesTheGroup(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	env := w.gpu()
	req := request(env, "add-shadows-r1-implement")
	w.lease(t, req)
	if err := w.p.Return(ctx, env, req.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.p.Release(ctx, env); err == nil {
		t.Error("Release while the machine shuts down succeeded")
	}
	if _, err := w.p.Leases(ctx, env); err != nil { // the machine is terminated on this read
		t.Fatal(err)
	}
	if err := w.p.Release(ctx, env); err != nil {
		t.Fatalf("Release = %v", err)
	}
	if len(w.aws.groups) != 0 {
		t.Errorf("security groups %d after Release, want none", len(w.aws.groups))
	}
	if err := w.p.Release(ctx, env); err != nil {
		t.Errorf("a second Release = %v, want none", err)
	}
}

// What a machine says of its lease.
func TestLeaseOf(t *testing.T) {
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	on := &types.InstanceMetadataOptionsResponse{HttpEndpoint: types.InstanceMetadataEndpointStateEnabled,
		State: types.InstanceMetadataOptionsStateApplied}
	off := &types.InstanceMetadataOptionsResponse{HttpEndpoint: types.InstanceMetadataEndpointStateDisabled,
		State: types.InstanceMetadataOptionsStateApplied}
	turning := &types.InstanceMetadataOptionsResponse{HttpEndpoint: types.InstanceMetadataEndpointStateDisabled,
		State: types.InstanceMetadataOptionsStatePending}
	ip := aws.String("203.0.113.7")
	for _, tt := range []struct {
		name      string
		state     types.InstanceStateName
		ip        *string
		md        *types.InstanceMetadataOptionsResponse
		deadline  string
		want      string
		endpoint  string
		inMessage string
	}{
		{name: "starting", state: types.InstanceStateNamePending, want: model.LeasePending, inMessage: "starting"},
		{name: "no public address", state: types.InstanceStateNameRunning, md: on, want: model.LeasePending,
			inMessage: "no public address"},
		{name: "metadata on", state: types.InstanceStateNameRunning, ip: ip, md: on, want: model.LeasePending,
			inMessage: "theos-env to answer"},
		{name: "metadata going off", state: types.InstanceStateNameRunning, ip: ip, md: turning,
			want: model.LeasePending, inMessage: "metadata service off"},
		{name: "ready", state: types.InstanceStateNameRunning, ip: ip, md: off, want: model.LeaseReady,
			endpoint: "https://203.0.113.7:443"},
		{name: "past its deadline", state: types.InstanceStateNameRunning, ip: ip, md: off,
			deadline: "2026-11-06T10:00:00Z", want: model.LeaseFailed, inMessage: "deadline"},
		{name: "stopped", state: types.InstanceStateNameStopped, want: model.LeaseFailed, inMessage: "stopped"},
		{name: "shutting down", state: types.InstanceStateNameShuttingDown, want: model.LeaseEnded},
	} {
		t.Run(tt.name, func(t *testing.T) {
			deadline := tt.deadline
			if deadline == "" {
				deadline = "2026-11-06T12:00:00Z"
			}
			inst := &types.Instance{State: &types.InstanceState{Name: tt.state}, PublicIpAddress: tt.ip,
				MetadataOptions: tt.md, LaunchTime: aws.Time(now.Add(-time.Hour)),
				Tags: []types.Tag{{Key: aws.String(TagLease), Value: aws.String("gpu-0123456789")},
					{Key: aws.String(TagDeadline), Value: aws.String(deadline)},
					{Key: aws.String(TagPrice), Value: aws.String("1006000")}}}
			l := leaseOf(inst, now)
			if l.State != tt.want || l.Endpoint != tt.endpoint || l.ID != "gpu-0123456789" {
				t.Errorf("leaseOf = %+v, want %s at %q", l, tt.want, tt.endpoint)
			}
			if tt.inMessage != "" && !strings.Contains(l.Message, tt.inMessage) {
				t.Errorf("message %q, want it to say %q", l.Message, tt.inMessage)
			}
			if wantCost := int64(1006); tt.want != model.LeaseEnded && l.CostMilliUSD != wantCost {
				t.Errorf("cost %d, want an hour's %d", l.CostMilliUSD, wantCost)
			}
		})
	}
}

// The Price List's on-demand price, in millionths of a dollar, rounded up.
func TestOnDemandHourly(t *testing.T) {
	got, err := onDemandHourly(priceList(gpuType, gpuPrice))
	if err != nil || got != 1_006_000 {
		t.Errorf("onDemandHourly = %d, %v; want 1006000", got, err)
	}
	for in, want := range map[string]int64{"0.0416000000": 41_600, "12": 12_000_000, "0.0000001": 1, "3.": 3_000_000} {
		if got, err := microUSD(in); err != nil || got != want {
			t.Errorf("microUSD(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "-1", "1.2.3", "a", "1e3"} {
		if _, err := microUSD(bad); err == nil {
			t.Errorf("microUSD(%q) succeeded", bad)
		}
	}
	w := newWorld()
	env := w.gpu()
	env.AWS.InstanceType = "x9z.huge"
	if l := w.lease(t, request(env, "add-shadows-r1-implement")); l.State != model.LeaseFailed ||
		!strings.Contains(l.Message, "no on-demand Linux price for instance type x9z.huge") {
		t.Errorf("Lease of a type with no price = %+v, want Failed, saying so", l)
	}
}

// The probe takes an answer only from the lease's own theos-env: the
// certificate its token makes, whatever address it answers on.
func TestPinnedProbe(t *testing.T) {
	token := rand.Text()
	serve := func(t *testing.T, token string) string {
		t.Helper()
		certPEM, key, err := model.TestEnvCertificate(token)
		if err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode([]byte(certPEM))
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" {
				_, _ = w.Write([]byte("ok\n"))
				return
			}
			http.NotFound(w, r)
		}))
		srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{block.Bytes}, PrivateKey: key}}}
		srv.StartTLS()
		t.Cleanup(srv.Close)
		return srv.URL
	}
	pin, err := model.TestEnvKeyPin(token)
	if err != nil {
		t.Fatal(err)
	}
	if err := PinnedProbe(context.Background(), serve(t, token), pin); err != nil {
		t.Errorf("PinnedProbe of the lease's theos-env = %v", err)
	}
	if err := PinnedProbe(context.Background(), serve(t, rand.Text()), pin); err == nil {
		t.Error("PinnedProbe took a server with another certificate")
	}
	if err := PinnedProbe(context.Background(), serve(t, token), ""); err == nil {
		t.Error("PinnedProbe took a server with no key to check it by")
	}
}

// The Price List answers in us-east-1 for every region: the SDK's clients
// ask it there, and EC2 in the environment's region.
func TestAWSClientsAskThePriceListInUSEast1(t *testing.T) {
	c := AWSClients(aws.Config{Region: region})
	if got := c.Pricing.(*pricing.Client).Options().Region; got != pricingRegion {
		t.Errorf("the Price List's region %q, want %q", got, pricingRegion)
	}
	if got := c.EC2.(*ec2.Client).Options().Region; got != region {
		t.Errorf("EC2's region %q, want %q", got, region)
	}
}
