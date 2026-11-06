package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Remote test environments in CI (docs/plans/test-environments.md, step 8):
// a remote TestEnvironment with a ci block runs its project's CI jobs
// labelled env-<name>. For each such job that waits, within the
// environment's limits, the operator registers a runner for one job to the
// GitProvider's owner organization and leases a machine that runs it; the
// lease's holder is that runner, and the lease is returned once no job
// needs it.

// RemoteRunnerDir is where a remote environment's machine keeps the CI
// runner's files and runs it: the CI adapter's registration names its
// paths under it.
const RemoteRunnerDir = "/var/lib/theos-runner"

// CIStartGrace is how long a CI lease's machine may take to start its
// runner while a job waits: boot, download the runner and theos-env, and
// have its metadata service turned off. One that hasn't called the CI host
// by then is returned.
const CIStartGrace = 20 * time.Minute

// ciHolderPrefix starts a CI lease's holder; a Task's name never holds a
// slash.
const ciHolderPrefix = "ci/"

// CIHolder is the holder of a CI lease: the runner runnerID registered to
// GitProvider provider's owner organization, ci/<provider>/<runnerID>.
func CIHolder(provider string, runnerID int64) string {
	return ciHolderPrefix + provider + "/" + strconv.FormatInt(runnerID, 10)
}

// ParseCIHolder is CIHolder backwards; ok is false for a holder that is no
// CI lease's, such as a Task's name.
func ParseCIHolder(holder string) (provider string, runnerID int64, ok bool) {
	rest, ok := strings.CutPrefix(holder, ciHolderPrefix)
	if !ok {
		return "", 0, false
	}
	i := strings.LastIndexByte(rest, '/')
	if i <= 0 {
		return "", 0, false
	}
	id, err := strconv.ParseInt(rest[i+1:], 10, 64)
	if err != nil || id <= 0 {
		return "", 0, false
	}
	return rest[:i], id, true
}

// CILeaseID is the ID of the k-th CI lease taken for job of GitProvider
// provider on the environment env, whose UID is uid: <env>-ci- and a hash,
// so two environments, providers or namespaces never share one, and the
// runner registered for it is named by it. A runner takes any job of its
// label, so the job a lease was taken for may run elsewhere and wait no
// more while another waits; k counts on past the leases taken for it
// already.
func CILeaseID(env, uid, provider string, job int64, k int) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s/%s/%d/%d", uid, provider, job, k))
	return env + "-ci-" + hex.EncodeToString(sum[:])[:10]
}

// CIRunnerState is what the CI host says of a CI lease's runner.
type CIRunnerState struct {
	// Registered is whether the host lists it: an ephemeral runner goes when
	// its one job ends.
	Registered bool
	// Online is whether it called the host lately, and Busy whether it runs
	// a job.
	Online, Busy bool
}

// CILeaseHeld reports whether a CI lease's runner still holds it: while it
// runs its job; once it is gone, its job ended, no; with no job of its
// label waiting, no, since there is nothing left for it to take; while it
// is online, waiting to take one, yes; and while it hasn't called the host
// yet, only for CIStartGrace from the lease's start, age.
func CILeaseHeld(runner CIRunnerState, waiting bool, age time.Duration) bool {
	switch {
	case runner.Busy:
		return true
	case !runner.Registered, !waiting:
		return false
	case runner.Online:
		return true
	}
	return age < CIStartGrace
}

// CIRunnerLabel is the label, with its schema, a CI lease's runner takes
// jobs for: env-<name>, run on the machine itself.
func CIRunnerLabel(env string) string { return EnvRunnerLabel(env) + ":host" }

// RemoteRunnerAddress is where a CI runner outside the cluster, a remote
// environment's machine, reaches the Git host: publicURL, else the
// GitProvider's url, when it is one such a machine reaches, https:// on
// port 443, the one port its fence lets out, at a public host; "" and why
// otherwise. The chart's Git host is in the cluster, so remote CI is off
// until a GitProvider gives its public address.
func RemoteRunnerAddress(gitURL, publicURL string) (string, string) {
	address, given := publicURL, "publicURL"
	if address == "" {
		address, given = gitURL, "url"
	}
	why := remoteAddressProblem(address)
	if why == "" {
		return strings.TrimSuffix(address, "/"), ""
	}
	if publicURL == "" {
		why = "its ci block has no publicURL, and its url " + why
	} else {
		why = "its " + given + " " + why
	}
	return "", why
}

// remoteScheme is the one scheme a remote machine's fence lets out.
const remoteScheme = "https"

// remoteAddressProblem says why a machine outside the cluster can't reach
// address, "" when it can.
func remoteAddressProblem(address string) string {
	u, err := url.Parse(address)
	if err != nil || u.Host == "" {
		return fmt.Sprintf("%q is no address", address)
	}
	host := u.Hostname()
	ip, err := netip.ParseAddr(host)
	isIP := err == nil
	_, inCluster := ClusterNamespace(host)
	switch {
	case isIP && (!ip.Is4() || ip.IsLoopback() || ip.IsUnspecified() || PrivateBlock(ip.String()+"/32")):
		return fmt.Sprintf("%s is no public IPv4 address, the only kind a remote machine's fence lets out to", address)
	case !isIP && (inCluster || !strings.Contains(host, ".") || strings.HasSuffix(host, ".local")):
		return fmt.Sprintf("%s is inside the cluster, where a remote machine can't reach it", address)
	case u.Scheme != remoteScheme:
		return fmt.Sprintf("%s is not https://, the one scheme a remote machine's fence lets out", address)
	case u.Port() != "" && u.Port() != "443":
		return fmt.Sprintf("%s is on port %s; a remote machine's fence lets out port 443 alone", address, u.Port())
	}
	return ""
}

// The reasons of a remote TestEnvironment's CI condition, which it has
// while its ci block is set; besides these, ReasonLabelClash, and
// TestEnvUnreachable when a Git host doesn't answer.
const (
	// TestEnvCIServing: it leases a machine for each waiting job, within its
	// limits (True).
	TestEnvCIServing = "Serving"
	// TestEnvCINoGitProvider: no GitProvider in the namespace has a ci block
	// that keeps runners, whose owner organization the machines' runners
	// would register to.
	TestEnvCINoGitProvider = "NoGitProvider"
	// TestEnvCINoPublicAddress: no GitProvider gives an address a machine
	// outside the cluster reaches (RemoteRunnerAddress).
	TestEnvCINoPublicAddress = "NoPublicAddress"
	// TestEnvCIUnsupported: the provider can start no runner, or the Git
	// host can't register one for one job.
	TestEnvCIUnsupported = "Unsupported"
	// TestEnvCIOverBudget: the day's budget is spent, so no machine starts
	// for a job until tomorrow, or a higher budget.
	TestEnvCIOverBudget = "OverBudget"
)
