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
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/agentrunner"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// A Task's runs lease the test environments they need before their sandbox
// is made and return them when they end or are suspended
// (docs/plans/test-environments.md, T2 and T3).

// runEnvironment is a Ready lease a run's sandbox gets: its endpoint, and
// its token by the Task's token Secret's key.
type runEnvironment struct {
	name, description string
	lease             ports.Lease
	tokenSecret       string
}

// environmentWait is why a run waits for its environments: the Task's
// Progressing message.
type environmentWait struct{ message string }

// environmentRecheck is how often a Task waiting for a test environment
// looks again.
func (r *TaskReconciler) environmentRecheck() time.Duration {
	if r.EnvironmentRecheck > 0 {
		return r.EnvironmentRecheck
	}
	return 15 * time.Second
}

// envTokenSecret is the Secret, owned by the Task, that holds the token of
// each lease its runs took, by lease ID: a sandbox reads its own lease's
// key alone, so one run can't use another's environment (T5).
func envTokenSecret(task *c4v1beta1.Task) string {
	return task.Name + "-test-environments"
}

// leaseEnvironments takes the leases a run of role, in sandbox, needs: those
// its issue's env/<name> labels ask for, which it waits for when the
// project lacks one, and those its Container's Standard and Framework list,
// which it goes on without. It returns the Ready leases, or why the run
// waits: one isn't in the project, every lease of one is taken, one spent its
// day's budget or its provider refuses its credential, or one is
// starting or failed. The leases are recorded in status.environments as
// they are taken, so a run never takes one twice and the Task returns each.
func (r *TaskReconciler) leaseEnvironments(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	container *c4v1beta1.Container, role, sandbox string, labels []string) ([]runEnvironment, *environmentWait, error) {
	if !model.LeasesEnvironments(role, isSpec(task)) {
		return nil, nil, nil
	}
	required := model.EnvironmentsLabelled(labels)
	asked, err := askedEnvironments(ctx, r.Client, container)
	if err != nil || len(required)+len(asked) == 0 {
		return nil, nil, err
	}
	var list c4v1beta1.TestEnvironmentList
	if err := r.List(ctx, &list, client.InNamespace(task.Namespace)); err != nil {
		return nil, nil, err
	}
	byName := map[string]*c4v1beta1.TestEnvironment{}
	names := make([]string, 0, len(list.Items))
	for i := range list.Items {
		byName[list.Items[i].Name] = &list.Items[i]
		names = append(names, list.Items[i].Name)
	}
	envs := model.EnvironmentsOf(required, asked, names)
	if len(envs.Missing) > 0 {
		return nil, &environmentWait{model.EnvironmentWaitMessage(model.EnvNotInProject, envs.Missing, "")}, nil
	}
	var ready []runEnvironment
	var waits []string
	for _, name := range envs.Get {
		env := byName[name]
		got, wait, err := r.leaseEnvironment(ctx, task, status, env, sandbox)
		if err != nil {
			return nil, nil, err
		}
		if wait != "" {
			waits = append(waits, wait)
			continue
		}
		ready = append(ready, runEnvironment{name: name, description: env.Spec.Description, lease: got,
			tokenSecret: envTokenSecret(task)})
	}
	if len(waits) > 0 {
		return nil, &environmentWait{strings.Join(waits, "; ")}, nil
	}
	return ready, nil, nil
}

// leaseEnvironment takes, or reads back, the lease of env for sandbox. A
// wait it returns says why the run can't go on yet.
func (r *TaskReconciler) leaseEnvironment(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	env *c4v1beta1.TestEnvironment, sandbox string) (ports.Lease, string, error) {
	member := testEnvMember(env)
	provider, err := r.TestEnvironments.For(member)
	if err != nil {
		return ports.Lease{}, "", err
	}
	environment, err := providerEnvironment(ctx, r.APIReader, env, r.SharedPullSecrets)
	if err != nil {
		return ports.Lease{}, "", err
	}
	id := model.LeaseID(env.Name, sandbox)
	held := slices.ContainsFunc(status.Environments, func(l c4v1beta1.TaskEnvironmentLease) bool {
		return l.Lease == id
	})
	refused := func(detail string) (ports.Lease, string, error) {
		return ports.Lease{}, model.EnvironmentWaitMessage(model.EnvCredentialRefused, []string{env.Name}, detail), nil
	}
	if !held {
		// A remote member whose credential its provider refuses takes no
		// call; its CredentialsValid says why (T9).
		if cond := apimeta.FindStatusCondition(env.Status.Conditions,
			c4v1beta1.ConditionTestEnvironmentCredentialsValid); model.TestEnvRemote(member) && cond != nil &&
			cond.Status == metav1.ConditionFalse && cond.Reason != model.TestEnvUnreachable {
			return refused(cond.Message)
		}
		leases, err := provider.Leases(ctx, environment)
		if errors.Is(err, ports.ErrCredentialRefused) {
			return refused(err.Error())
		}
		if err != nil {
			return ports.Lease{}, "", err
		}
		live := 0
		for _, l := range leases {
			if l.ID != id && l.State != model.LeaseEnded {
				live++
			}
		}
		// What today's leases cost so far: the ended ones as the
		// environment last counted them, the live ones as the provider
		// counts them now (T10). A local member has no budget.
		var spent, budget int64
		if model.TestEnvRemote(member) {
			spent = spentToday(env.Status.SpentToday, env.Status.Leases, leases, r.now()).MilliUSD
			budget = model.BudgetMilliUSD(env.Spec.Limits.BudgetUSDPerDay)
		}
		if why := model.LeaseMayStart(live, int(env.Spec.Limits.MaxLeases), spent, budget); why != "" {
			return ports.Lease{}, model.EnvironmentWaitMessage(why, []string{env.Name}, ""), nil
		}
	}
	token, err := r.leaseToken(ctx, task, id)
	if err != nil {
		return ports.Lease{}, "", err
	}
	lease, err := provider.Lease(ctx, ports.LeaseRequest{ID: id, Env: environment, Holder: task.Name, Token: token,
		Deadline: model.LeaseDeadline(int(env.Spec.Limits.MaxLeaseMinutes))})
	if errors.Is(err, ports.ErrCredentialRefused) {
		return refused(err.Error())
	}
	if err != nil {
		return ports.Lease{}, "", err
	}
	if !held {
		now := metav1.NewTime(r.now())
		status.Environments = append(status.Environments, c4v1beta1.TaskEnvironmentLease{Name: env.Name,
			Lease: id, Sandbox: sandbox, Since: &now})
		if !slices.Contains(status.LeasedEnvironments, env.Name) {
			status.LeasedEnvironments = append(status.LeasedEnvironments, env.Name)
			slices.Sort(status.LeasedEnvironments)
		}
		logf.FromContext(ctx).Info("Leased a test environment", "environment", env.Name, "lease", id, "sandbox", sandbox)
	}
	switch lease.State {
	case model.LeaseReady:
		return lease, "", nil
	case model.LeaseFailed:
		// The next pass takes a fresh one.
		if err := provider.Return(ctx, environment, id); err != nil {
			return ports.Lease{}, "", err
		}
		status.Environments = slices.DeleteFunc(status.Environments, func(l c4v1beta1.TaskEnvironmentLease) bool {
			return l.Lease == id
		})
		return ports.Lease{}, model.EnvironmentWaitMessage(model.EnvFailed, []string{env.Name}, lease.Message), nil
	}
	return ports.Lease{}, model.EnvironmentWaitMessage(model.EnvPending, []string{env.Name}, lease.Message), nil
}

// leaseToken is lease id's token in the Task's token Secret, made the first
// time it is asked for. The operator caches no Secrets: it reads live and
// patches what it adds.
func (r *TaskReconciler) leaseToken(ctx context.Context, task *c4v1beta1.Task, id string) (string, error) {
	var secret corev1.Secret
	key := client.ObjectKey{Namespace: task.Namespace, Name: envTokenSecret(task)}
	err := r.APIReader.Get(ctx, key, &secret)
	switch {
	case apierrors.IsNotFound(err):
		token := rand.Text()
		secret = corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name,
			Labels: projectLabels(task.Namespace)}, Data: map[string][]byte{id: []byte(token)}}
		if err := controllerutil.SetControllerReference(task, &secret, r.Scheme); err != nil {
			return "", err
		}
		return token, r.Create(ctx, &secret)
	case err != nil:
		return "", err
	}
	if token, ok := secret.Data[id]; ok {
		return string(token), nil
	}
	token := rand.Text()
	patch := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: key.Namespace, Name: key.Name},
		Data: map[string][]byte{id: []byte(token)}}
	return token, r.Patch(ctx, patch, client.Merge)
}

// takeLeases takes the leases sandbox holds out of status, for dropRun to
// return once the status that forgets them is written.
func takeLeases(status *c4v1beta1.TaskStatus, sandbox string) []c4v1beta1.TaskEnvironmentLease {
	var taken []c4v1beta1.TaskEnvironmentLease
	status.Environments = slices.DeleteFunc(status.Environments, func(l c4v1beta1.TaskEnvironmentLease) bool {
		if l.Sandbox == sandbox {
			taken = append(taken, l)
			return true
		}
		return false
	})
	if len(status.Environments) == 0 {
		status.Environments = nil
	}
	return taken
}

// returnLeases returns leases to their environments. One that can't be is
// left to the TestEnvironment reconciler, which returns a lease its holder
// no longer lists.
func (r *TaskReconciler) returnLeases(ctx context.Context, task *c4v1beta1.Task, leases []c4v1beta1.TaskEnvironmentLease) {
	log := logf.FromContext(ctx)
	for _, l := range leases {
		var env c4v1beta1.TestEnvironment
		err := r.Get(ctx, client.ObjectKey{Namespace: task.Namespace, Name: l.Name}, &env)
		if apierrors.IsNotFound(err) {
			continue // its leases went with it
		}
		if err == nil {
			var provider ports.TestEnvironments
			var environment ports.Environment
			if provider, err = r.TestEnvironments.For(testEnvMember(&env)); err == nil {
				if environment, err = providerEnvironment(ctx, r.APIReader, &env, r.SharedPullSecrets); err == nil {
					err = provider.Return(ctx, environment, l.Lease)
				}
			}
		}
		if err != nil {
			log.Error(err, "Failed to return a test environment's lease; its environment returns it",
				"environment", l.Name, "lease", l.Lease)
			continue
		}
		log.Info("Returned a test environment's lease", "environment", l.Name, "lease", l.Lease, "sandbox", l.Sandbox)
	}
}

// listedEnvironments are the project's test environments as a triage run
// gets them: named and described, not leased.
func listedEnvironments(ctx context.Context, c client.Client, ns string) ([]runEnvironment, error) {
	envs, err := projectEnvironments(ctx, c, ns)
	if err != nil {
		return nil, err
	}
	out := make([]runEnvironment, 0, len(envs))
	for _, e := range envs {
		out = append(out, runEnvironment{name: e.Name, description: e.Description})
	}
	return out, nil
}

// returnFinalLeases returns what a Task that ended still lists: a lease
// taken for a run that never started, or one a failed return left. A final
// Task holds no test environment.
func (r *TaskReconciler) returnFinalLeases(ctx context.Context, task *c4v1beta1.Task, status *c4v1beta1.TaskStatus) {
	if len(status.Environments) == 0 {
		return
	}
	r.returnLeases(ctx, task, status.Environments)
	status.Environments = nil
}

// askedEnvironments are the test environments container's Standard and
// Framework list; one that is gone asks for none.
func askedEnvironments(ctx context.Context, c client.Client, container *c4v1beta1.Container) ([]string, error) {
	choices := container.Choices()
	var asked []string
	if choices.Standard != "" {
		var standard c4v1beta1.Standard
		err := c.Get(ctx, client.ObjectKey{Namespace: container.Namespace, Name: choices.Standard}, &standard)
		if client.IgnoreNotFound(err) != nil {
			return nil, err
		}
		asked = append(asked, standard.Spec.TestEnvironments...)
	}
	if choices.Framework != "" {
		var framework c4v1beta1.Framework
		err := c.Get(ctx, client.ObjectKey{Namespace: container.Namespace, Name: choices.Framework}, &framework)
		if client.IgnoreNotFound(err) != nil {
			return nil, err
		}
		asked = append(asked, framework.Spec.TestEnvironments...)
	}
	return asked, nil
}

// environmentEnv is what a sandbox gets of its leases: each one's endpoint,
// token and pinned certificate (THEOS_ENV_<NAME>_*), and their names and
// descriptions for the prompt (T4, T13).
func environmentEnv(envs []runEnvironment) []corev1.EnvVar {
	if len(envs) == 0 {
		return nil
	}
	out := make([]corev1.EnvVar, 0, 3*len(envs)+1)
	listed := make([]agentrunner.TestEnvironment, 0, len(envs))
	for _, e := range envs {
		listed = append(listed, agentrunner.TestEnvironment{Name: e.name, Description: e.description})
		if e.lease.ID == "" {
			continue // listed for the triager, not leased
		}
		out = append(out,
			corev1.EnvVar{Name: model.TestEnvVar(e.name, model.TestEnvURL), Value: e.lease.Endpoint},
			secretEnv(model.TestEnvVar(e.name, model.TestEnvToken), e.tokenSecret, e.lease.ID))
		if e.lease.CA != "" {
			out = append(out, corev1.EnvVar{Name: model.TestEnvVar(e.name, model.TestEnvCA), Value: e.lease.CA})
		}
	}
	data, _ := json.Marshal(listed)
	return append(out, corev1.EnvVar{Name: agentrunner.EnvEnvironments, Value: string(data)})
}

// waitForEnvironment records that the run waits for its environments.
func (r *TaskReconciler) waitForEnvironment(task *c4v1beta1.Task, status *c4v1beta1.TaskStatus,
	wait *environmentWait) {
	r.progress(task, status, metav1.ConditionTrue, model.ReasonWaitingForEnvironment, wait.message)
}
