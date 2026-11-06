package model

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"time"
)

// Test environments (docs/plans/test-environments.md): a named set of tools,
// such as browser, a run leases from a provider while it works, and runs
// commands in through theos-env. These are the rules the operator, the
// providers and theos-env share.

// The provider members of a TestEnvironment, by field name.
const (
	TestEnvMemberLocal = "local"
)

// TestEnvPort is the port theos-env serve listens on in every environment;
// the fences open it from the project's sandboxes and CI runner pods alone
// (T5).
const TestEnvPort = 7070

// TestEnvComponent is the app.kubernetes.io/component of a local
// environment's pods, which the project's theos-test-environment policy
// selects.
const TestEnvComponent = "test-environment"

// TestEnvNetworkPolicy is the NetworkPolicy that fences a project's local
// environment pods (T5).
const TestEnvNetworkPolicy = "theos-test-environment"

// TestEnvMaxTree is how many bytes of files `theos-env run` sends at most:
// the working tree's tracked and untracked, not ignored, files (T4).
const TestEnvMaxTree = 512 << 20

// The default limits of a TestEnvironment (T10), as its CRD defaults them.
const (
	DefaultMaxLeases       = 2
	DefaultMaxLeaseMinutes = 180
)

// The states of a lease (T6).
const (
	LeasePending = "Pending"
	LeaseReady   = "Ready"
	LeaseFailed  = "Failed"
	LeaseEnded   = "Ended"
)

// The reasons of a TestEnvironment's Ready condition.
const (
	TestEnvReady          = "Ready"
	TestEnvImageNotPinned = "ImageNotPinned"
	TestEnvFenceMissing   = "FenceMissing"
)

// The suffixes of the variables a run gets per leased environment
// (TestEnvVar): its endpoint, its lease's token and, for an endpoint over
// public HTTPS, the PEM certificate it is pinned to.
const (
	TestEnvURL   = "URL"
	TestEnvToken = "TOKEN"
	TestEnvCA    = "CA"
)

// TestEnvVar is the variable of environment name's suffix in a sandbox:
// THEOS_ENV_<NAME>_<SUFFIX>, the name upper-cased and its dashes made
// underscores (THEOS_ENV_BROWSER_URL).
func TestEnvVar(name, suffix string) string {
	return "THEOS_ENV_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_" + suffix
}

// pinnedDigest and pinnedTag are an image reference that ends in a digest,
// or in a tag (the last path segment's, so a registry's port is no tag).
var (
	pinnedDigest = regexp.MustCompile(`@sha256:[0-9a-f]{64}$`)
	pinnedTag    = regexp.MustCompile(`^[^@]+:[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)
)

// ImagePinned reports whether image names a digest, or a tag other than
// latest: what a TestEnvironment's CEL rule lets through (T7).
func ImagePinned(image string) bool {
	if pinnedDigest.MatchString(image) {
		return true
	}
	return pinnedTag.MatchString(image) && !strings.HasSuffix(image, ":latest")
}

// LeaseID is the ID of the lease of environment env for run, the sandbox
// that holds it: the environment's name and a hash of both, so a second
// Lease for the same run finds the first and two runs never share one. It is
// a DNS label, a pod's name for the local provider.
func LeaseID(env, run string) string {
	sum := sha256.Sum256([]byte(run + "/" + env))
	return env + "-" + hex.EncodeToString(sum[:])[:10]
}

// LeaseDeadline is how long a lease lasts at most: the environment's
// maxLeaseMinutes, DefaultMaxLeaseMinutes when unset.
func LeaseDeadline(maxLeaseMinutes int) time.Duration {
	if maxLeaseMinutes <= 0 {
		maxLeaseMinutes = DefaultMaxLeaseMinutes
	}
	return time.Duration(maxLeaseMinutes) * time.Minute
}

// OrphanLease reports whether a lease the provider holds is the factory's
// no more: its holder doesn't list it, and it is older than grace, so a
// lease taken but not yet recorded by its holder is kept.
func OrphanLease(listed bool, started, now time.Time, grace time.Duration) bool {
	return !listed && !started.IsZero() && now.Sub(started) >= grace
}
