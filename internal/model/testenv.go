package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
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

// EnvRunnerLabel is the CI job label a test environment's runner pool takes,
// env-<name>, which a workflow names in runs-on (T8).
func EnvRunnerLabel(name string) string { return "env-" + name }

// ReasonLabelClash is the Project's CIRunners reason when a GitProvider's ci
// block names a label a test environment's pool takes (T8).
const ReasonLabelClash = "LabelClash"

// LabelEnvPrefix starts the label an issue asks for a test environment
// with, env/browser (T2).
const LabelEnvPrefix = "env/"

// ReasonWaitingForEnvironment is a Task's Progressing reason while its run
// waits for a test environment; the message starts with why: one of
// EnvNotInProject, EnvAllLeased, EnvOverBudget, EnvPending and EnvFailed. A
// wait costs no try and no round (T2, T3, T10).
const ReasonWaitingForEnvironment = "WaitingForEnvironment"

// Why a run waits for a test environment, and the Container condition's
// reasons.
const (
	// EnvNotInProject: the issue asks for one the project lacks.
	EnvNotInProject = "NotInProject"
	// EnvAllLeased: the environment has maxLeases live leases.
	EnvAllLeased = "AllLeased"
	// EnvOverBudget: the environment's leases spent the day's budget.
	EnvOverBudget = "OverBudget"
	// EnvPending: its lease isn't ready yet.
	EnvPending = "Pending"
	// EnvFailed: its lease failed; the run takes another.
	EnvFailed = "Failed"
	// EnvPresent is the Container condition's reason when the project has
	// every environment its Standard and Framework ask for.
	EnvPresent = "Present"
)

// LeasesEnvironments reports whether a run of role leases test environments:
// the implementer's and the verifier's, never a spec writer's, the
// reviewer's, the triager's or the planner's (T3).
func LeasesEnvironments(role string, spec bool) bool {
	return !spec && (role == RoleImplementer || role == RoleVerifier)
}

// EnvironmentsLabelled are the environments an issue's env/<name> labels ask
// for, sorted.
func EnvironmentsLabelled(labels []string) []string {
	var out []string
	for _, l := range labels {
		if name, ok := strings.CutPrefix(l, LabelEnvPrefix); ok && name != "" {
			out = append(out, name)
		}
	}
	return sortedUnique(out)
}

// RunEnvironments is which test environments a run gets (T2).
type RunEnvironments struct {
	// Get are the environments the run leases, sorted.
	Get []string
	// Missing are the ones its issue asks for that the project lacks: the
	// run waits for them.
	Missing []string
	// Skipped are the ones its Standard or Framework asks for that the
	// project lacks: the run goes on without them.
	Skipped []string
}

// EnvironmentsOf is what a run gets of required, the environments its
// issue's labels ask for, and asked, those its Standard and Framework list,
// when the project has project.
func EnvironmentsOf(required, asked, project []string) RunEnvironments {
	var out RunEnvironments
	for _, name := range sortedUnique(slices.Concat(required, asked)) {
		switch {
		case slices.Contains(project, name):
			out.Get = append(out.Get, name)
		case slices.Contains(required, name):
			out.Missing = append(out.Missing, name)
		default:
			out.Skipped = append(out.Skipped, name)
		}
	}
	return out
}

// LeaseMayStart says why a new lease of an environment may not start, or ""
// when it may: with a budget, budgetMilliUSD above 0, spent at or past it,
// no lease starts (EnvOverBudget); at maxLeases live leases a run waits
// (EnvAllLeased). Leases held finish their run (T10).
func LeaseMayStart(live, maxLeases int, spentMilliUSD, budgetMilliUSD int64) string {
	if maxLeases <= 0 {
		maxLeases = DefaultMaxLeases
	}
	switch {
	case budgetMilliUSD > 0 && spentMilliUSD >= budgetMilliUSD:
		return EnvOverBudget
	case live >= maxLeases:
		return EnvAllLeased
	}
	return ""
}

// EnvironmentWaitMessage is a waiting Task's message: why, first, then what
// and the detail.
func EnvironmentWaitMessage(why string, names []string, detail string) string {
	var what string
	switch why {
	case EnvNotInProject:
		what = fmt.Sprintf("the issue asks for %s, which the project has no TestEnvironment for; "+
			"it starts once one is added, or the label is taken off", envLabels(names))
	case EnvAllLeased:
		what = fmt.Sprintf("every lease of %s is taken; it starts when one is returned", joinAnd(names))
	case EnvOverBudget:
		what = fmt.Sprintf("%s spent its budget for today; no lease starts until tomorrow, or a higher budget",
			joinAnd(names))
	case EnvPending:
		what = fmt.Sprintf("waiting for the lease of %s to be ready", joinAnd(names))
	case EnvFailed:
		what = fmt.Sprintf("the lease of %s failed, so the run takes another", joinAnd(names))
	}
	if detail != "" {
		what += ": " + detail
	}
	return why + ": " + what
}

// MissingEnvironmentsMessage is the Container's TestEnvironments condition
// message for the environments its Standard and Framework ask for that the
// project lacks.
func MissingEnvironmentsMessage(names []string) string {
	return fmt.Sprintf("the project has no TestEnvironment %s, which its Standard or Framework asks for; "+
		"its runs go on without it", joinAnd(names))
}

// envLabels names environments as the labels that ask for them.
func envLabels(names []string) string {
	labels := make([]string, len(names))
	for i, n := range names {
		labels[i] = "`" + LabelEnvPrefix + n + "`"
	}
	return joinAnd(labels)
}
