package model

import (
	"testing"
	"time"
)

func TestTestEnvMac(t *testing.T) {
	for typ, want := range map[string]bool{"mac2.metal": true, "mac1.metal": true, "mac2-m2pro.metal": true,
		"g5.xlarge": false, "m7i.large": false, "": false} {
		if got := TestEnvMac(typ); got != want {
			t.Errorf("TestEnvMac(%q) = %v, want %v", typ, got, want)
		}
	}
	for typ, want := range map[string]string{"mac2.metal": "mac2", "mac2-m2pro.metal": "mac2-m2pro", "mac1.metal": "mac1"} {
		if got := MacHostFamily(typ); got != want {
			t.Errorf("MacHostFamily(%q) = %q, want %q", typ, got, want)
		}
	}
}

// CI runs on an aws machine per job, never on an EC2 Mac nor Device Farm
// (steps 8 to 10).
func TestTestEnvRemoteCI(t *testing.T) {
	for _, tt := range []struct {
		member, instanceType string
		want                 bool
	}{
		{TestEnvMemberAWS, "g5.xlarge", true}, {TestEnvMemberAWS, "mac2.metal", false},
		{TestEnvMemberDeviceFarm, "", false}, {TestEnvMemberLocal, "", false},
	} {
		if got := TestEnvRemoteCI(tt.member, tt.instanceType); got != tt.want {
			t.Errorf("TestEnvRemoteCI(%q, %q) = %v, want %v", tt.member, tt.instanceType, got, tt.want)
		}
	}
}

func TestKeepHost(t *testing.T) {
	for hours, want := range map[int]time.Duration{0: 24 * time.Hour, 24: 24 * time.Hour, 48: 48 * time.Hour,
		12: 24 * time.Hour, -1: 24 * time.Hour} {
		if got := KeepHost(hours); got != want {
			t.Errorf("KeepHost(%d) = %v, want %v", hours, got, want)
		}
	}
}

// A Mac host's hours are each counted once: the lease that allocated it
// carries its kept time from the start, a lease placed on it after costs
// nothing until the kept time is over, and a lease held past it pays for
// its time past it (T11).
func TestHostLeaseBilled(t *testing.T) {
	allocated := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	paidUntil := allocated.Add(24 * time.Hour)
	at := func(h float64) time.Time { return allocated.Add(time.Duration(h * float64(time.Hour))) }
	for _, tt := range []struct {
		name         string
		upfront      time.Duration
		started, now time.Time
		want         time.Duration
	}{
		{"the allocating lease at once", 24 * time.Hour, allocated, at(0), 24 * time.Hour},
		{"the allocating lease an hour on", 24 * time.Hour, allocated, at(1), 24 * time.Hour},
		{"the allocating lease past the kept time", 24 * time.Hour, allocated, at(26), 26 * time.Hour},
		{"a later lease within the kept time", 0, at(3), at(5), 0},
		{"a later lease held past the kept time", 0, at(23), at(25.5), 90 * time.Minute},
		{"a lease started past the kept time", 0, at(30), at(31), time.Hour},
		{"a clock before the start", 0, at(30), at(29), 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := HostLeaseBilled(tt.upfront, tt.started, paidUntil, tt.now); got != tt.want {
				t.Errorf("HostLeaseBilled = %v, want %v", got, tt.want)
			}
		})
	}
	// mac2.metal's $0.65 an hour, counted at its 24-hour minimum.
	if got := LeaseCostMilliUSD(650_000, HostLeaseBilled(24*time.Hour, allocated, paidUntil, at(0.5)), 0); got != 15_600 {
		t.Errorf("a new host's lease costs %d thousandths, want 24 hours' 15600", got)
	}
}
