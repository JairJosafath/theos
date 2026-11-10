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
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/contract"
)

const (
	macType = "mac2.metal"
	// macHostPrice is a mac2 dedicated host's on-demand price an hour, as
	// the Price List writes it.
	macHostPrice = "0.6500000000"
	// macDay is a mac2 host's 24 hours, in thousandths of a dollar.
	macDay = 15_600
)

// mac is a new EC2 Mac environment in account, with a key of its own, its
// hosts kept for keepHours (0: the default).
func (w *world) mac(keepHours int) ports.Environment {
	env := w.gpu()
	env.Name = "ios"
	env.AWS.InstanceType, env.AWS.KeepHostHours = macType, keepHours
	w.aws.mu.Lock()
	w.aws.hostPrices[region+"/mac2"] = macHostPrice
	w.aws.mu.Unlock()
	return env
}

// The contract every provider meets (T6), on a fake EC2 with a Mac's
// dedicated hosts.
func TestMacTestEnvironmentsContract(t *testing.T) {
	w := newWorld()
	contract.TestEnvironments(t, contract.TestEnvironmentsEnv{
		Provider:    w.p,
		Environment: func(*testing.T) ports.Environment { return w.mac(0) },
		Places:      func(_ *testing.T, env ports.Environment) int { return w.aws.live(env.UID) },
		Ready:       func(_ *testing.T, env ports.Environment, id string) { w.aws.ready(env.UID, id) },
		Fail:        func(_ *testing.T, env ports.Environment, id string) { w.aws.stop(env.UID, id) },
	})
}

// A Mac machine is the Linux machine's fenced call placed on its host, and
// tagged with the host, the end of its kept time and what of it the lease
// carries (T11).
func TestMacLaunchInputIsPlacedOnTheHost(t *testing.T) {
	req := request(newWorld().mac(0), "add-share-sheet-r1-implement")
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	in, err := MacLaunchInput(req, "sg-0123456789abcdef0", "h-0123456789abcdef0", 650_000, 24*time.Hour,
		now.Add(24*time.Hour), toolsURL, now)
	if err != nil {
		t.Fatal(err)
	}
	linux, err := LaunchInput(req, "sg-0123456789abcdef0", 650_000, toolsURL, now)
	if err != nil {
		t.Fatal(err)
	}
	if p := in.Placement; p == nil || p.Tenancy != types.TenancyHost || aws.ToString(p.HostId) != "h-0123456789abcdef0" {
		t.Errorf("placement %+v, want the host", in.Placement)
	}
	if in.IamInstanceProfile != nil || in.KeyName != nil || len(in.NetworkInterfaces) != 1 ||
		aws.ToString(in.UserData) != aws.ToString(linux.UserData) ||
		in.MetadataOptions.HttpTokens != types.HttpTokensStateRequired ||
		in.InstanceInitiatedShutdownBehavior != types.ShutdownBehaviorTerminate {
		t.Error("a Mac machine isn't fenced as a Linux one is")
	}
	for _, spec := range in.TagSpecifications {
		for k, v := range map[string]string{TagHost: "h-0123456789abcdef0", TagHostKeptUntil: "2026-11-07T09:00:00Z",
			TagHostUpfront: "24h0m0s", TagLease: req.ID, TagPrice: "650000"} {
			if got := tag(spec.Tags, k); got != v {
				t.Errorf("%s tag %s = %q, want %q", spec.ResourceType, k, got, v)
			}
		}
	}
	if tag(linux.TagSpecifications[0].Tags, TagHost) != "" {
		t.Error("placing a Mac machine changed the Linux call")
	}
}

// One host is allocated, in the subnet's zone, and reused across leases: a
// lease waits while the host is scrubbed after the last machine, rather
// than allocating another for 24 hours more (T11, step 9).
func TestMacHostIsReusedAcrossLeases(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	env := w.mac(0)
	first := request(env, "add-share-sheet-r1-implement")
	if l := w.lease(t, first); l.State != model.LeasePending || l.Started.IsZero() {
		t.Fatalf("Lease = %+v, want a machine starting", l)
	}
	hosts := w.aws.liveHosts(env.UID)
	if len(hosts) != 1 || aws.ToString(hosts[0].AvailabilityZone) != region+"a" ||
		aws.ToString(hosts[0].HostProperties.InstanceType) != macType || hosts[0].AutoPlacement != types.AutoPlacementOff {
		t.Fatalf("hosts %+v, want one mac2.metal host in the subnet's zone, placed on by ID alone", hosts)
	}
	host := aws.ToString(hosts[0].HostId)
	if got := tag(hosts[0].Tags, TagHostClaimed); got != first.ID {
		t.Errorf("host claimed by %q, want the first lease", got)
	}
	m := w.aws.machine(env.UID, first.ID)
	if m.inst.Placement == nil || aws.ToString(m.inst.Placement.HostId) != host {
		t.Fatalf("the machine is on %+v, want host %s", m.inst.Placement, host)
	}
	w.aws.ready(env.UID, first.ID)
	if l := w.get(t, env, first.ID); l.State != model.LeaseReady {
		t.Fatalf("Get = %+v, want Ready", l)
	}
	if err := w.p.Return(ctx, env, first.ID); err != nil {
		t.Fatal(err)
	}

	second := request(env, "add-share-sheet-r1-verify")
	l := w.lease(t, second)
	if l.State != model.LeasePending || !strings.Contains(l.Message, "being scrubbed") ||
		!strings.Contains(l.Message, host) {
		t.Errorf("Lease while the host is scrubbed = %+v, want Pending, waiting for host %s", l, host)
	}
	if n := w.aws.count("RunInstances"); n != 1 {
		t.Errorf("RunInstances called %d times, want once: the second lease waits", n)
	}
	w.aws.scrubbed(env.UID)
	w.lease(t, second)
	m = w.aws.machine(env.UID, second.ID)
	if m == nil || aws.ToString(m.inst.Placement.HostId) != host {
		t.Fatalf("the second machine is on %+v, want the first's host %s", m, host)
	}
	if n := w.aws.count("AllocateHosts"); n != 1 {
		t.Errorf("AllocateHosts called %d times, want once: one host across the leases", n)
	}
	if got := tag(m.inst.Tags, TagHostUpfront); got != "0s" {
		t.Errorf("the second machine carries %s of the host, want none: the first carries its 24 hours", got)
	}
}

// A lease allocates a host when every host kept is busy, and the oldest
// free one is taken after.
func TestMacAllocatesAHostWhenEveryHostIsBusy(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	env := w.mac(0)
	a, b := request(env, "add-share-sheet-r1-implement"), request(env, "fix-dark-mode-r1-implement")
	w.lease(t, a)
	w.clock.add(time.Minute)
	w.lease(t, b)
	hosts := w.aws.liveHosts(env.UID)
	if len(hosts) != 2 {
		t.Fatalf("hosts %d, want two: the first is busy", len(hosts))
	}
	for _, id := range []string{a.ID, b.ID} {
		if err := w.p.Return(ctx, env, id); err != nil {
			t.Fatal(err)
		}
	}
	w.aws.scrubbed(env.UID)
	c := request(env, "add-widget-r1-implement")
	w.lease(t, c)
	if got := aws.ToString(w.aws.machine(env.UID, c.ID).inst.Placement.HostId); got != aws.ToString(hosts[0].HostId) {
		t.Errorf("the third machine is on %s, want the oldest free host %s", got, aws.ToString(hosts[0].HostId))
	}
	if n := w.aws.count("AllocateHosts"); n != 2 {
		t.Errorf("AllocateHosts called %d times, want twice", n)
	}
}

// A host is kept for keepHostHours from its allocation and released after,
// once no machine is on it, on the poll that lists the leases; a lease past
// a host's kept time allocates another (T11, step 9).
func TestMacHostIsKeptForKeepHostHoursThenReleased(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	env := w.mac(48)
	first := request(env, "add-share-sheet-r1-implement")
	w.lease(t, first)
	host := aws.ToString(w.aws.liveHosts(env.UID)[0].HostId)
	if got := tag(w.aws.liveHosts(env.UID)[0].Tags, TagHostKeptUntil); got != "2026-11-08T09:00:00Z" {
		t.Errorf("host kept until %s, want 48 hours from its allocation", got)
	}
	if err := w.p.Return(ctx, env, first.ID); err != nil {
		t.Fatal(err)
	}
	w.aws.scrubbed(env.UID)
	w.clock.add(30 * time.Hour)
	if _, err := w.p.Leases(ctx, env); err != nil {
		t.Fatal(err)
	}
	if hosts := w.aws.liveHosts(env.UID); len(hosts) != 1 {
		t.Fatalf("hosts %d at 30 hours, want the one kept", len(hosts))
	}

	// A lease held past the host's kept time keeps it until it returns.
	late := request(env, "add-share-sheet-r2-implement")
	w.lease(t, late)
	if got := aws.ToString(w.aws.machine(env.UID, late.ID).inst.Placement.HostId); got != host {
		t.Fatalf("a lease within the kept time is on %s, want the host kept %s", got, host)
	}
	w.clock.add(20 * time.Hour)
	if _, err := w.p.Leases(ctx, env); err != nil {
		t.Fatal(err)
	}
	if hosts := w.aws.liveHosts(env.UID); len(hosts) != 1 || hosts[0].State == types.AllocationStateReleased {
		t.Fatalf("hosts %+v past the kept time with a machine on it, want it kept", hosts)
	}
	if err := w.p.Return(ctx, env, late.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.p.Leases(ctx, env); err != nil {
		t.Fatal(err)
	}
	if hosts := w.aws.liveHosts(env.UID); len(hosts) != 0 {
		t.Errorf("hosts %d past the kept time with no machine on it, want it released", len(hosts))
	}

	next := request(env, "add-widget-r1-implement")
	w.lease(t, next)
	if got := aws.ToString(w.aws.machine(env.UID, next.ID).inst.Placement.HostId); got == host || got == "" {
		t.Errorf("a lease after the host's release is on %q, want a new host", got)
	}
}

// A host is counted at its kept hours by the lease whose machine is the
// first on it, from its start; a lease on a host kept costs nothing until
// the kept time is over, and its time past it after (T10, T11).
func TestMacLeaseCostCountsTheHostsMinimum(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	env := w.mac(0)
	if err := w.p.CheckCredentials(ctx, env); err != nil {
		t.Fatalf("CheckCredentials = %v: it reads the host's price", err)
	}
	first := request(env, "add-share-sheet-r1-implement")
	if l := w.lease(t, first); l.CostMilliUSD != macDay {
		t.Errorf("cost at launch %d, want the host's 24 hours, %d", l.CostMilliUSD, macDay)
	}
	w.clock.add(time.Hour)
	if l := w.get(t, env, first.ID); l.CostMilliUSD != macDay {
		t.Errorf("cost after an hour %d, want the host's 24 hours, %d", l.CostMilliUSD, macDay)
	}
	if err := w.p.Return(ctx, env, first.ID); err != nil {
		t.Fatal(err)
	}
	w.aws.scrubbed(env.UID)
	w.clock.add(22 * time.Hour) // 23 hours from the allocation
	second := request(env, "add-share-sheet-r1-verify")
	if l := w.lease(t, second); l.CostMilliUSD != 0 {
		t.Errorf("cost of a lease on the host kept %d, want 0", l.CostMilliUSD)
	}
	w.clock.add(150 * time.Minute) // 90 minutes past the kept time
	leases, err := w.p.Leases(ctx, env)
	if err != nil || len(leases) != 1 || leases[0].CostMilliUSD != 975 {
		t.Errorf("Leases = %+v, %v; want the lease at the host's 90 minutes past its kept time, 975", leases, err)
	}
	if w.aws.priceReads != 1 {
		t.Errorf("the Price List was read %d times, want once", w.aws.priceReads)
	}
	other := w.mac(0)
	w.aws.mu.Lock()
	delete(w.aws.hostPrices, region+"/mac2")
	w.aws.mu.Unlock()
	other.AWS.InstanceType = "mac2-m2pro.metal"
	if l := w.lease(t, request(other, "add-share-sheet-r1-implement")); l.State != model.LeaseFailed ||
		!strings.Contains(l.Message, "no on-demand price for a mac2-m2pro dedicated host") {
		t.Errorf("Lease of a Mac with no host price = %+v, want Failed, saying so", l)
	}
}

// A Mac needs theos-env's darwin build: a download address that names no
// ${os} fails the lease before any host is allocated.
func TestMacNeedsTheDarwinBuild(t *testing.T) {
	w := newWorld()
	w.p.toolsURL = "https://github.com/JairJosafath/theos/releases/download/v0.31.0/theos-env-linux-${arch}"
	env := w.mac(0)
	l := w.lease(t, request(env, "add-share-sheet-r1-implement"))
	if l.State != model.LeaseFailed || !strings.Contains(l.Message, "names no ${os}") {
		t.Errorf("Lease = %+v, want Failed: the address has no darwin build", l)
	}
	if n := w.aws.count("AllocateHosts"); n != 0 {
		t.Errorf("AllocateHosts called %d times", n)
	}
	w.p.toolsURL = "https://downloads.example.com/theos-env-darwin-${arch}"
	if l := w.lease(t, request(env, "add-share-sheet-r1-implement")); l.State != model.LeasePending {
		t.Errorf("Lease with a darwin address = %+v, want a machine starting", l)
	}
}

// Deleting a Mac environment releases its hosts, which EC2 refuses within
// 24 hours of their allocation: Release says when, and is asked again.
func TestReleaseReleasesTheMacHosts(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	env := w.mac(0)
	req := request(env, "add-share-sheet-r1-implement")
	w.lease(t, req)
	if err := w.p.Return(ctx, env, req.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.p.Leases(ctx, env); err != nil { // the machine is terminated on this read
		t.Fatal(err)
	}
	err := w.p.Release(ctx, env)
	if err == nil || !strings.Contains(err.Error(), "24 hours") {
		t.Fatalf("Release within the host's 24 hours = %v, want it refused, saying why", err)
	}
	if len(w.aws.groups) != 1 {
		t.Error("the security group went while a host is kept")
	}
	w.clock.add(24 * time.Hour)
	if err := w.p.Release(ctx, env); err != nil {
		t.Fatalf("Release after 24 hours = %v", err)
	}
	if hosts := w.aws.liveHosts(env.UID); len(hosts) != 0 || len(w.aws.groups) != 0 {
		t.Errorf("hosts %d, groups %d after Release, want none", len(hosts), len(w.aws.groups))
	}
}

// A Mac machine's lease, read back after a restart, keeps its cost: the
// host's tags are on the machine.
func TestMacLeaseCostSurvivesARestart(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	env := w.mac(0)
	req := request(env, "add-share-sheet-r1-implement")
	w.lease(t, req)
	leases, err := w.provider().Leases(ctx, env)
	if err != nil || len(leases) != 1 || leases[0].CostMilliUSD != macDay {
		t.Errorf("Leases after a restart = %+v, %v; want the lease at the host's 24 hours", leases, err)
	}
	for _, key := range []string{"accessKeyId", "secretAccessKey"} {
		if m := w.aws.machine(env.UID, req.ID); strings.Contains(m.userData, string(env.Secret[key])) {
			t.Errorf("the Mac's user data holds the credential's %s", key)
		}
	}
}
