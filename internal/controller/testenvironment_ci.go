/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// A remote TestEnvironment with a ci block runs the project's CI jobs
// labelled env-<name> (docs/plans/done/test-environments.md, step 8): for each
// such job that waits, within the environment's limits and budget, which it
// shares with the runs, the operator registers a runner for one job to the
// GitProvider's owner organization, its registration token never leaving
// the operator, and leases a machine that runs it. The lease's holder is
// that runner (model.CIHolder), and the lease is returned, and the runner
// deleted, once no job needs it (model.CILeaseHeld).

// ciRecheck is how soon a remote environment with CI work looks again: a
// job that waits, or a CI lease that starts.
const ciRecheck = 15 * time.Second

// ciHost is the CI of a GitProvider whose jobs a remote environment's
// machines run: its runners, and the jobs a runner for one job registers
// for.
type ciHost interface {
	ports.OwnerRunners
	ports.JobRunners
}

// ciTarget is one GitProvider with a ci block that keeps runners, as a
// remote environment's CI step found it.
type ciTarget struct {
	// name is the GitProvider's.
	name string
	// address is where its machines' runners reach the Git host, and why
	// says why it serves no job when it doesn't (no public address, a label
	// clash, a host with no runner for one job), with its reason.
	address, why, reason string
	host                 ciHost
	// registered are the owner organization's runners by ID, and waiting
	// its jobs that wait for env-<name>; unknown is true when the host
	// didn't say, so its leases are kept.
	registered map[int64]ports.Runner
	waiting    []ports.WaitingJob
	unknown    bool
}

// serves reports whether the environment leases machines for t's jobs.
func (t *ciTarget) serves() bool { return t.why == "" && !t.unknown }

// ciWork is what a remote environment's CI step found: the label its
// machines take, and each GitProvider with a ci block that keeps runners.
type ciWork struct {
	label   string
	targets map[string]*ciTarget
}

// ciWorkFor reads the GitProviders whose CI env's machines would serve, and
// their runners and waiting jobs: for a remote environment with a ci block,
// or one that still holds a CI lease. nil for any other.
func (r *TestEnvironmentReconciler) ciWorkFor(ctx context.Context, env *c4v1beta1.TestEnvironment,
	leases []ports.Lease) (*ciWork, error) {
	instanceType := ""
	if env.Spec.AWS != nil {
		instanceType = env.Spec.AWS.InstanceType
	}
	if !model.TestEnvRemoteCI(testEnvMember(env), instanceType) {
		return nil, nil
	}
	ciHeld := slices.ContainsFunc(leases, func(l ports.Lease) bool {
		_, _, ok := model.ParseCIHolder(l.Holder)
		return ok
	})
	if env.Spec.CI == nil && !ciHeld {
		return nil, nil
	}
	var providers c4v1beta1.GitProviderList
	if err := r.List(ctx, &providers, client.InNamespace(env.Namespace)); err != nil {
		return nil, err
	}
	log := logf.FromContext(ctx)
	work := &ciWork{label: model.EnvRunnerLabel(env.Name), targets: map[string]*ciTarget{}}
	for _, p := range providers.Items {
		pool := runnerPool(&p)
		if pool == nil {
			continue
		}
		t := &ciTarget{name: p.Name}
		work.targets[p.Name] = t
		address, why := model.RemoteRunnerAddress(p.Spec.URL, pool.PublicURL)
		switch {
		case slices.ContainsFunc(pool.Runners, func(c c4v1beta1.CIRunnerSpec) bool {
			return c.Label == work.label
		}):
			t.why, t.reason = "its ci block names label "+work.label+" too", model.ReasonLabelClash
		case address == "":
			t.why, t.reason = why, model.TestEnvCINoPublicAddress
		}
		t.address = address
		provider, err := providerFor(ctx, r.Client, r.reader(), r.Git, env.Namespace, p.Name)
		if err != nil {
			t.unknown = true
			log.Info("Could not read a GitProvider's CI for the test environment's CI jobs; its leases are kept",
				"gitProvider", p.Name, "error", err.Error())
			continue
		}
		host, ok := provider.(ciHost)
		if !ok {
			// No runners to read: its CI leases are returned.
			t.why, t.reason = "its Git host can't register a runner for one job", model.TestEnvCIUnsupported
			continue
		}
		t.host = host
		if err := t.read(ctx, work.label); err != nil {
			t.unknown = true
			log.Info("Could not read the CI jobs of a GitProvider; its leases are kept", "gitProvider", p.Name,
				"error", err.Error())
		}
	}
	return work, nil
}

// read reads t's runners and the jobs that wait for label.
func (t *ciTarget) read(ctx context.Context, label string) error {
	runners, err := t.host.Runners(ctx)
	if err != nil {
		return err
	}
	t.registered = make(map[int64]ports.Runner, len(runners))
	for _, runner := range runners {
		t.registered[runner.ID] = runner
	}
	t.waiting, err = t.host.WaitingJobs(ctx, []string{label})
	return err
}

// held reports whether CI lease l's runner still holds it
// (model.CILeaseHeld); a GitProvider that is gone, or keeps no runners any
// more, holds none, and one whose host didn't answer keeps its leases.
func (w *ciWork) held(l ports.Lease, now time.Time) bool {
	provider, id, ok := model.ParseCIHolder(l.Holder)
	if !ok || w == nil {
		return false
	}
	t := w.targets[provider]
	switch {
	case t == nil || t.host == nil && !t.unknown:
		return false
	case t.unknown:
		return true
	}
	runner, registered := t.registered[id]
	return model.CILeaseHeld(model.CIRunnerState{Registered: registered, Online: runner.Online, Busy: runner.Busy},
		len(t.waiting) > 0, now.Sub(l.Started))
}

// returnLease returns lease l of environment, and deletes the runner a CI
// lease's machine runs first, so the CI gives it no job while it goes and
// its credential dies with the machine.
func (r *TestEnvironmentReconciler) returnLease(ctx context.Context, provider ports.TestEnvironments,
	environment ports.Environment, l ports.Lease, work *ciWork) error {
	if gp, id, ok := model.ParseCIHolder(l.Holder); ok && work != nil {
		if t := work.targets[gp]; t != nil && t.host != nil {
			if err := t.host.DeleteRunner(ctx, id); err != nil {
				return err
			}
		}
	}
	return provider.Return(ctx, environment, l.ID)
}

// serveCI moves the environment's CI leases on, returns the failed ones,
// and leases a machine for each job of the project's that waits for
// env-<name> and has none starting for it, within the environment's
// limits; it sets the CI condition and returns the live leases, and
// whether to look again soon.
func (r *TestEnvironmentReconciler) serveCI(ctx context.Context, env *c4v1beta1.TestEnvironment,
	status *c4v1beta1.TestEnvironmentStatus, environment ports.Environment, provider ports.TestEnvironments,
	work *ciWork, live []ports.Lease) ([]ports.Lease, bool, error) {
	log := logf.FromContext(ctx)
	soon, failed := false, ""
	kept := live[:0:0]
	for _, l := range live {
		if _, _, ok := model.ParseCIHolder(l.Holder); !ok {
			kept = append(kept, l)
			continue
		}
		if l.State == model.LeasePending {
			// Get moves it on: the metadata service off once theos-env answers.
			got, err := provider.Get(ctx, environment, l.ID)
			switch {
			case errors.Is(err, ports.ErrLeaseNotFound):
				continue
			case err != nil:
				return nil, false, err
			}
			l, soon = got, true
		}
		if l.State == model.LeaseFailed || l.State == model.LeaseEnded {
			if err := r.returnLease(ctx, provider, environment, l, work); err != nil {
				return nil, false, err
			}
			log.Info("Returned a CI job's lease that ended", "lease", l.ID, "state", l.State, "message", l.Message)
			if l.State == model.LeaseFailed {
				failed = l.Message
			}
			continue
		}
		kept = append(kept, l)
	}
	live = kept
	if env.Spec.CI == nil {
		apimeta.RemoveStatusCondition(&status.Conditions, c4v1beta1.ConditionTestEnvironmentCI)
		return live, soon, nil
	}
	runners, ok := provider.(ports.TestEnvironmentRunners)
	if !ok {
		r.setCI(env, status, metav1.ConditionFalse, model.TestEnvCIUnsupported,
			"the "+testEnvMember(env)+" provider can't lease a machine that runs a CI job")
		return live, soon, nil
	}
	if why := runners.RunnerMissing(); why != "" {
		r.setCI(env, status, metav1.ConditionFalse, model.TestEnvCIUnsupported, why)
		return live, soon, nil
	}
	var serving, problems []string
	reason := model.TestEnvCINoGitProvider
	waiting := 0
	var blocked string
	for _, name := range sortedTargets(work) {
		t := work.targets[name]
		if !t.serves() {
			switch {
			case t.why != "":
				problems = append(problems, name+": "+t.why)
				reason = t.reason
			case t.unknown:
				problems = append(problems, name+": its Git host didn't answer; it is asked again on the next poll")
				reason = model.TestEnvUnreachable
			}
			continue
		}
		serving = append(serving, name)
		waiting += len(t.waiting)
		if blocked != "" {
			continue
		}
		var err error
		if live, blocked, err = r.leaseForJobs(ctx, env, environment, runners, t, live); err != nil {
			return nil, false, err
		}
	}
	soon = soon || waiting > 0
	switch {
	case len(serving) == 0 && len(problems) == 0:
		r.setCI(env, status, metav1.ConditionFalse, model.TestEnvCINoGitProvider,
			"no GitProvider in the namespace has a ci block that keeps runners, whose organization a machine's "+
				"runner would register to")
	case len(serving) == 0:
		r.setCI(env, status, metav1.ConditionFalse, reason, "no GitProvider's jobs run here: "+
			strings.Join(problems, "; "))
	case blocked == model.EnvOverBudget:
		r.setCI(env, status, metav1.ConditionFalse, model.TestEnvCIOverBudget, fmt.Sprintf(
			"%d jobs labelled %s wait, and the day's budget is spent: no machine starts until tomorrow, or a "+
				"higher budget", waiting, work.label))
	default:
		message := fmt.Sprintf("a machine is leased for each job labelled %s of GitProvider %s that waits",
			work.label, strings.Join(serving, ", "))
		if blocked == model.EnvAllLeased {
			message += fmt.Sprintf("; %d wait, and every lease is taken", waiting)
		}
		if len(problems) > 0 {
			message += "; not of " + strings.Join(problems, "; ")
		}
		if failed != "" {
			// A launch AWS refuses comes back Failed at once, and the job's
			// next pass takes another.
			message += "; the last machine failed, so the job takes another: " + failed
		}
		r.setCI(env, status, metav1.ConditionTrue, model.TestEnvCIServing, message)
	}
	return live, soon, nil
}

// leaseForJobs leases a machine for each job of t that waits and has no CI
// lease of t's starting or idle to take it, oldest first, while the
// environment's limits let a lease start; it returns the live leases and
// why one may not start, "" when none was refused.
func (r *TestEnvironmentReconciler) leaseForJobs(ctx context.Context, env *c4v1beta1.TestEnvironment,
	environment ports.Environment, runners ports.TestEnvironmentRunners, t *ciTarget,
	live []ports.Lease) ([]ports.Lease, string, error) {
	ids := map[string]bool{}
	ready := 0
	for _, l := range live {
		ids[l.ID] = true
		if gp, id, ok := model.ParseCIHolder(l.Holder); ok && gp == t.name && !t.registered[id].Busy {
			ready++ // starting, or online and waiting to take a job
		}
	}
	short := len(t.waiting) - ready
	for _, job := range t.waiting {
		if short <= 0 {
			break
		}
		// What today's leases cost, the ended ones as the environment last
		// counted them, and the live ones as the provider counts them now,
		// against the budget the runs share (T10).
		spent := spentToday(env.Status.SpentToday, env.Status.Leases, live, r.now()).MilliUSD
		if why := model.LeaseMayStart(liveCount(live), int(env.Spec.Limits.MaxLeases), spent,
			model.BudgetMilliUSD(env.Spec.Limits.BudgetUSDPerDay)); why != "" {
			return live, why, nil
		}
		id := ""
		for k := 0; id == "" || ids[id]; k++ {
			id = model.CILeaseID(env.Name, string(env.UID), t.name, job.ID, k)
		}
		lease, err := r.leaseForJob(ctx, env, environment, runners, t, id)
		if err != nil {
			return nil, "", err
		}
		logf.FromContext(ctx).Info("Leased a test environment for a CI job", "lease", id, "job", job.ID,
			"gitProvider", t.name, "holder", lease.Holder)
		ids[id] = true
		live = append(live, lease)
		short--
	}
	return live, "", nil
}

// leaseForJob registers a runner for one job, named id, to t's owner
// organization, at the address its machine reaches the host at, and
// leases machine id that runs it. A runner of that name left by a pass
// that registered and then failed to lease is deleted first; one that
// fails to lease is deleted after.
func (r *TestEnvironmentReconciler) leaseForJob(ctx context.Context, env *c4v1beta1.TestEnvironment,
	environment ports.Environment, runners ports.TestEnvironmentRunners, t *ciTarget, id string) (ports.Lease, error) {
	for _, left := range t.registered {
		if left.Name == id && !left.Busy {
			if err := t.host.DeleteRunner(ctx, left.ID); err != nil {
				return ports.Lease{}, err
			}
		}
	}
	deadline := model.LeaseDeadline(int(env.Spec.Limits.MaxLeaseMinutes))
	runner, err := t.host.RegisterJobRunner(ctx, ports.RunnerRegistration{Name: id,
		Labels: []string{model.CIRunnerLabel(env.Name)}, Address: t.address, Timeout: deadline})
	if err != nil {
		return ports.Lease{}, fmt.Errorf("registering a runner for a CI job with GitProvider %s: %w", t.name, err)
	}
	// The token reaches the machine alone: the operator probes theos-env by
	// its key, and no run uses a CI lease.
	lease, err := runners.LeaseRunner(ctx, ports.LeaseRequest{ID: id, Env: environment,
		Holder: model.CIHolder(t.name, runner.ID), Token: rand.Text(), Deadline: deadline}, runner)
	if err != nil {
		return ports.Lease{}, errors.Join(err, t.host.DeleteRunner(ctx, runner.ID))
	}
	return lease, nil
}

// liveCount counts the leases that hold their place.
func liveCount(leases []ports.Lease) int {
	n := 0
	for _, l := range leases {
		if l.State != model.LeaseEnded {
			n++
		}
	}
	return n
}

// sortedTargets are work's GitProviders in name order.
func sortedTargets(work *ciWork) []string {
	names := make([]string, 0, len(work.targets))
	for name := range work.targets {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// setCI sets env's CI condition.
func (r *TestEnvironmentReconciler) setCI(env *c4v1beta1.TestEnvironment, status *c4v1beta1.TestEnvironmentStatus,
	value metav1.ConditionStatus, reason, message string) {
	setCondition(&status.Conditions, env.Generation, c4v1beta1.ConditionTestEnvironmentCI, value, reason, message)
}

// recheck is when to look again: soon while CI work goes on, else on the
// poll.
func (r *TestEnvironmentReconciler) recheck(soon bool) time.Duration {
	if soon {
		return min(r.pollInterval(), ciRecheck, testRunRecheck)
	}
	return r.pollInterval()
}
