package model

import (
	"strings"
	"time"
)

// An EC2 Mac test environment (docs/plans/test-environments.md, T11, step
// 9): a Mac instance runs on a dedicated host, which EC2 bills for 24 hours
// at least from its allocation, so the aws provider keeps a host across
// leases for keepHostHours and counts the host, not the instance, against
// the budget.

// MacHostMinimum is the least EC2 bills a Mac's dedicated host for, from its
// allocation; it can't be released before.
const MacHostMinimum = 24 * time.Hour

// DefaultKeepHostHours is how long a Mac host is kept from its allocation
// when the environment doesn't say: its minimum.
const DefaultKeepHostHours = 24

// TestEnvMac reports whether instanceType is an EC2 Mac, such as mac2.metal
// or mac2-m2pro.metal, which runs on a dedicated host.
func TestEnvMac(instanceType string) bool { return strings.HasPrefix(instanceType, "mac") }

// MacHostFamily is the host type a Mac instance type runs on, as AWS
// prices a dedicated host: the type's family, such as mac2-m2 for
// mac2-m2.metal.
func MacHostFamily(instanceType string) string {
	family, _, _ := strings.Cut(instanceType, ".")
	return family
}

// KeepHost is how long a Mac host is kept from its allocation: hours, else
// DefaultKeepHostHours, never less than MacHostMinimum.
func KeepHost(hours int) time.Duration {
	if hours <= 0 {
		hours = DefaultKeepHostHours
	}
	return max(time.Duration(hours)*time.Hour, MacHostMinimum)
}

// HostLeaseBilled is what a lease on a kept Mac host is billed for at now
// (T10, T11): upfront, the host's kept time, which the lease that allocated
// the host carries from its start (0 for a lease placed on a host already
// kept), and the time the lease is held past paidUntil, the end of the
// host's kept time, when the host bills by the hour. Each hour of the host
// is counted once, by the lease that allocated it or by one held past its
// kept time, so a budget never counts less than EC2 bills for the host.
func HostLeaseBilled(upfront time.Duration, started, paidUntil, now time.Time) time.Duration {
	from := started
	if paidUntil.After(from) {
		from = paidUntil
	}
	return max(upfront, 0) + max(now.Sub(from), 0)
}
