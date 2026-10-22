/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// ConditionCIRunners reports the project's CI runner pool: True while every
// GitProvider with a ci block has its pool; False, reason UnsupportedCI, when
// one's host cannot register runners, or reason ProviderUnavailable when its
// credentials cannot be read or its host gives no registration token.
const ConditionCIRunners = "CIRunners"

// DefaultRunnerRecordKept is how long an ended pool pod stays readable, with
// its fate annotation, for the Task that asks who cancelled its job.
const DefaultRunnerRecordKept = 2 * time.Hour

// ciPool is one label's pool of one GitProvider: what buildRunnerPod needs,
// the hash of the pod spec it makes, how many pods are not ended, which of
// those the Git host has given no job, whether the pool gets no new pod this
// reconcile (its provider gave no token, or it backs off from failures), and
// whether its host gave no token, so which pods have a job is not known.
// failed are when its kept pods that failed on their own (AnnotationRunnerFailure)
// went, and ended when its newest pod that ran its job went.
type ciPool struct {
	in     runnerPodInput
	spec   string
	live   int32
	idle   []*corev1.Pod
	held   bool
	blind  bool
	failed []time.Time
	ended  time.Time
}

// stale is whether a pod was built from another spec than the pool's: a
// changed image, resources or cache, or an operator that builds pods anew.
func (p *ciPool) stale(pod *corev1.Pod) bool {
	return pod.Annotations[AnnotationRunnerSpec] != p.spec
}

// backoff is how long the pool still waits before it starts a pod: the
// runnerBackoff of its failures since its last ended job, from the newest.
func (p *ciPool) backoff(base time.Duration, now time.Time) time.Duration {
	n, last := 0, time.Time{}
	for _, t := range p.failed {
		if t.After(p.ended) {
			n++
			if t.After(last) {
				last = t
			}
		}
	}
	if n == 0 {
		return 0
	}
	return runnerBackoff(base, n) - now.Sub(last)
}

// ciRunners is what ensureCIRunners tells Reconcile: when the next kept record
// expires or a pool's backoff ends (zero for none), the namespace's
// GitProviders, and an error to retry with when a host gave no token.
type ciRunners struct {
	recheck   time.Duration
	providers []c4v1alpha1.GitProvider
	retry     error
}

// poolKey identifies a pool by its GitProvider and label.
type poolKey struct{ provider, label string }

// gitNamespacesOf are the in-cluster namespaces of the providers' Git hosts,
// sorted, each once: what a fenced pod may reach besides DNS and public HTTPS.
func gitNamespacesOf(providers []c4v1alpha1.GitProvider) []string {
	var namespaces []string
	for _, p := range providers {
		u, err := url.Parse(p.Spec.URL)
		if err != nil {
			continue
		}
		if ns, ok := model.ClusterNamespace(u.Hostname()); ok && !slices.Contains(namespaces, ns) {
			namespaces = append(namespaces, ns)
		}
	}
	slices.Sort(namespaces)
	return namespaces
}

// runnerRecordKept is how long an ended pool pod is kept.
func (r *ProjectReconciler) runnerRecordKept() time.Duration {
	if r.RunnerRecordKept > 0 {
		return r.RunnerRecordKept
	}
	return DefaultRunnerRecordKept
}

// runnerBackoffBase is the wait after a pool pod's first failure in a row.
func (r *ProjectReconciler) runnerBackoffBase() time.Duration {
	if r.RunnerBackoff > 0 {
		return r.RunnerBackoff
	}
	return DefaultRunnerBackoff
}

// ensureCIRunners keeps the project's CI runner pool: for every GitProvider with
// a ci.giteaActions block, a Secret with its runner registration token, and per
// label size pods not ended, each registered ephemeral for one job; the job.env
// ConfigMap and the CI NetworkPolicy once the project has any pool. An ended
// pod is annotated with its fate (runnerPodFate), then deleted and replaced; a
// pod of a provider or label no longer in any ci block is deleted too, lost if
// it was still running. The pool follows its ci block (#1700): a pod the host
// has given no job goes when it is above a lowered size, or was built from
// another spec than the block now says, and the pool fills back up with
// pods of the new spec; a pod running a job stays until it ends. A pod that
// failed on its own is replaced only after the pool's backoff (#1699). A
// provider whose host gives no registration token keeps its pods but gets
// none added, and the returned retry says so. A provider whose ci block went
// loses its token Secret, and the ConfigMap and NetworkPolicy go with the
// project's last pool. Every pod keeps its finalizer, and so stays readable
// with its fate, for runnerRecordKept after its deletion was asked; the
// returned recheck says when the next record expires or a backoff ends,
// zero for none, and the namespace's GitProviders are returned for
// ensurePackagesActions.
func (r *ProjectReconciler) ensureCIRunners(ctx context.Context, project *c4v1alpha1.Project,
	status *c4v1alpha1.ProjectStatus) (ciRunners, error) {
	var providers c4v1alpha1.GitProviderList
	if err := r.List(ctx, &providers, client.InNamespace(project.Namespace)); err != nil {
		return ciRunners{}, err
	}
	pools := map[poolKey]*ciPool{}
	busy := map[string]bool{}
	var failures []string
	var retry error
	failedReason := "ProviderUnavailable"
	for _, p := range providers.Items {
		if p.Spec.CI == nil || p.Spec.CI.GiteaActions == nil {
			if err := r.deleteRunnerTokenSecret(ctx, project.Namespace, p.Name); err != nil {
				return ciRunners{}, err
			}
			continue
		}
		token, reason, tokenErr, err := r.runnerToken(ctx, project.Namespace, p.Name, busy)
		if err != nil {
			return ciRunners{}, err
		}
		if reason != "" && tokenErr == nil {
			failures = append(failures, fmt.Sprintf("%s: %s", p.Name, reason))
			if reason == reasonCannotRegister {
				failedReason = "UnsupportedCI"
			}
			continue
		}
		if tokenErr != nil {
			// The host may answer again soon: its pods stay, none is added.
			failures = append(failures, fmt.Sprintf("%s: %s", p.Name, reason))
			retry = cmp.Or(retry, tokenErr)
		} else if err := r.ensureRunnerTokenSecret(ctx, project, p.Name, token); err != nil {
			return ciRunners{}, err
		}
		for _, runner := range p.Spec.CI.GiteaActions.Runners {
			in := runnerPodInput{
				namespace: project.Namespace, provider: p.Name, gitURL: p.Spec.URL,
				tokenSecret: ciRunnerTokenSecret(p.Name), runner: runner, goCacheRoot: r.CIGoCache,
			}
			pools[poolKey{p.Name, runner.Label}] = &ciPool{in: in, spec: runnerSpecHash(in), held: tokenErr != nil, blind: tokenErr != nil}
		}
	}

	next, err := r.sweepRunnerPods(ctx, project.Namespace, pools, busy)
	if err != nil {
		return ciRunners{}, err
	}
	if wait := r.holdBackingOff(ctx, pools); wait > 0 && (next == 0 || wait < next) {
		next = wait
	}
	if len(pools) > 0 {
		if err := r.ensureCINetwork(ctx, project, gitNamespacesOf(providers.Items)); err != nil {
			return ciRunners{}, err
		}
	} else if err := r.deleteCINetwork(ctx, project.Namespace); err != nil {
		return ciRunners{}, err
	}
	if err := r.trimPools(ctx, pools); err != nil {
		return ciRunners{}, err
	}
	if err := r.fillPools(ctx, project, pools); err != nil {
		return ciRunners{}, err
	}
	switch {
	case len(failures) > 0:
		setCondition(&status.Conditions, project.Generation, ConditionCIRunners, metav1.ConditionFalse,
			failedReason, strings.Join(failures, "; "))
	case len(pools) > 0:
		setCondition(&status.Conditions, project.Generation, ConditionCIRunners, metav1.ConditionTrue,
			"PoolKept", fmt.Sprintf("%d pools", len(pools)))
	default:
		meta.RemoveStatusCondition(&status.Conditions, ConditionCIRunners)
	}
	return ciRunners{recheck: next, providers: providers.Items, retry: retry}, nil
}

// holdBackingOff holds every pool still backing off from its pods' failures
// and returns when the first backoff ends, zero for none.
func (r *ProjectReconciler) holdBackingOff(ctx context.Context, pools map[poolKey]*ciPool) time.Duration {
	log := logf.FromContext(ctx)
	base, now := r.runnerBackoffBase(), time.Now()
	var next time.Duration
	for key, pool := range pools {
		left := pool.backoff(base, now)
		if left <= 0 || pool.held {
			continue
		}
		pool.held = true
		log.Info("CI runner pool backing off", "pool", key.provider+"/"+key.label, "wait", left.Round(time.Second))
		if next == 0 || left < next {
			next = left
		}
	}
	return next
}

// sweepRunnerPods goes over the project's pool pods: one not ended, of a pool
// still wanted, counts as live, and as idle unless the host says its runner
// is busy; an ended one, or one of no pool, gets its fate recorded and is
// deleted; a deleted one is kept until its record expires. Returns when the
// next record expires, zero for none.
func (r *ProjectReconciler) sweepRunnerPods(ctx context.Context, ns string, pools map[poolKey]*ciPool,
	busy map[string]bool) (time.Duration, error) {
	log := logf.FromContext(ctx)
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(ns), client.MatchingLabels{LabelCIRunner: ciRunnerValue}); err != nil {
		return 0, err
	}
	var next time.Duration
	for i := range pods.Items {
		pod := &pods.Items[i]
		key := poolKey{pod.Labels[LabelCIGitProvider], pod.Labels[LabelCILabel]}
		pool, wanted := pools[key]
		fate, failure := pod.Annotations[AnnotationRunnerFate], pod.Annotations[AnnotationRunnerFailure]
		if fate == "" {
			fate = runnerPodFate(pod)
			if fate == "" && !wanted {
				fate = RunnerFateLost
			}
			if fate == "" {
				pool.live++
				if !busy[pod.Name] {
					pool.idle = append(pool.idle, pod)
				}
				continue
			}
			if fate == RunnerFateLost {
				failure = runnerPodFailure(pod)
			}
			if err := r.recordRunnerFate(ctx, pod, fate, failure); err != nil {
				return 0, err
			}
			log.Info("CI runner pod ended", "pod", pod.Name, "fate", fate, "failure", failure,
				"pool", key.provider+"/"+key.label)
		}
		if wanted {
			went := runnerFateAt(pod)
			switch {
			case failure != "":
				pool.failed = append(pool.failed, went)
			case fate == RunnerFateEnded && went.After(pool.ended):
				pool.ended = went
			}
		}
		if pod.DeletionTimestamp == nil {
			if err := client.IgnoreNotFound(r.Delete(ctx, pod)); err != nil {
				return 0, err
			}
			continue
		}
		left := r.runnerRecordKept() - time.Since(pod.DeletionTimestamp.Time)
		if left <= 0 {
			if err := r.dropRunnerRecord(ctx, pod); err != nil {
				return 0, err
			}
			continue
		}
		if next == 0 || left < next {
			next = left
		}
	}
	return next, nil
}

// dropPool takes a namespace's pool down once its Project is gone: every pool
// pod is let go and deleted, the kept records too, since no Task of the
// project is left to read how one ended. Garbage collection deletes the pods
// with their owner on a real cluster but cannot drop the record finalizer,
// which held every pod Terminating and the namespace deleting (#1697).
func (r *ProjectReconciler) dropPool(ctx context.Context, ns string) error {
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(ns), client.MatchingLabels{LabelCIRunner: ciRunnerValue}); err != nil {
		return err
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if err := r.dropRunnerRecord(ctx, pod); err != nil {
			return err
		}
		if pod.DeletionTimestamp != nil {
			continue
		}
		if err := client.IgnoreNotFound(r.Delete(ctx, pod)); err != nil {
			return err
		}
	}
	if len(pods.Items) > 0 {
		logf.FromContext(ctx).Info("Took the CI runner pool down with its Project", "namespace", ns, "pods", len(pods.Items))
	}
	return nil
}

// sortedPoolKeys are the pools' keys in name order.
func sortedPoolKeys(pools map[poolKey]*ciPool) []poolKey {
	keys := make([]poolKey, 0, len(pools))
	for key := range pools {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b poolKey) int {
		return cmp.Or(cmp.Compare(a.provider, b.provider), cmp.Compare(a.label, b.label))
	})
	return keys
}

// trimPools deletes the idle pods a pool no longer wants: every one built
// from another spec than the pool's, and then, newest first, those above its
// size. Each is recorded lost: it had no job, and if the host gave it one in
// the meantime, the Task reruns that job's checks. A busy pod is never
// deleted here; it ends on its own and the pool replaces it, or not, then.
func (r *ProjectReconciler) trimPools(ctx context.Context, pools map[poolKey]*ciPool) error {
	log := logf.FromContext(ctx)
	for _, key := range sortedPoolKeys(pools) {
		pool := pools[key]
		if pool.blind {
			continue // the host listed no runners: a pod that looks idle may have a job
		}
		slices.SortFunc(pool.idle, func(a, b *corev1.Pod) int {
			if sa, sb := pool.stale(a), pool.stale(b); sa != sb {
				if sa {
					return -1
				}
				return 1
			}
			return b.CreationTimestamp.Compare(a.CreationTimestamp.Time)
		})
		for _, pod := range pool.idle {
			stale := pool.stale(pod)
			if !stale && pool.live <= pool.in.runner.Size {
				break
			}
			if err := r.recordRunnerFate(ctx, pod, RunnerFateLost, ""); err != nil {
				return err
			}
			if err := client.IgnoreNotFound(r.Delete(ctx, pod)); err != nil {
				return err
			}
			pool.live--
			why := "above size"
			if stale {
				why = "spec changed"
			}
			log.Info("Deleted idle CI runner pod", "pod", pod.Name, "pool", key.provider+"/"+key.label, "why", why)
		}
	}
	return nil
}

// fillPools creates the pods each pool not held is short of, pools in name order.
func (r *ProjectReconciler) fillPools(ctx context.Context, project *c4v1alpha1.Project, pools map[poolKey]*ciPool) error {
	log := logf.FromContext(ctx)
	for _, key := range sortedPoolKeys(pools) {
		pool := pools[key]
		if pool.held {
			continue
		}
		for ; pool.live < pool.in.runner.Size; pool.live++ {
			pod := buildRunnerPod(pool.in)
			pod.Annotations = map[string]string{AnnotationRunnerSpec: pool.spec}
			if err := controllerutil.SetControllerReference(project, pod, r.Scheme); err != nil {
				return err
			}
			if err := r.Create(ctx, pod); err != nil {
				return err
			}
			log.Info("Created CI runner pod", "pod", pod.Name, "pool", key.provider+"/"+key.label)
		}
	}
	return nil
}

// reasonCannotRegister is runnerToken's reason for a Git host that cannot
// register runners: the condition's reason is then UnsupportedCI.
const reasonCannotRegister = "its Git host cannot register runners"

// runnerSpecHash names the pod spec buildRunnerPod makes of in, so a pod
// says in AnnotationRunnerSpec which spec it was built from.
func runnerSpecHash(in runnerPodInput) string {
	spec, err := json.Marshal(buildRunnerPod(in).Spec)
	if err != nil {
		panic(err) // a PodSpec always marshals
	}
	sum := sha256.Sum256(spec)
	return hex.EncodeToString(sum[:8])
}

// runnerToken is a GitProvider's runner registration token, and the names of
// its runners the host has given a job are added to busy. A provider whose
// host cannot register runners, or whose credentials cannot be read, gives a
// reason instead and no error: the Project's condition says it. A host that
// gives no token (a 403, Gitea down) gives a reason and tokenErr, which the
// reconcile retries with (#1699).
func (r *ProjectReconciler) runnerToken(ctx context.Context, ns, name string,
	busy map[string]bool) (token, reason string, tokenErr, err error) {
	provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, ns, name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return "", "", nil, err
		}
		return "", err.Error(), nil, nil
	}
	runners, ok := provider.(gitprovider.OwnerRunners)
	if !ok {
		return "", reasonCannotRegister, nil, nil
	}
	token, err = runners.RunnerRegistrationToken(ctx)
	if err != nil {
		return "", "runner registration token: " + err.Error(),
			fmt.Errorf("runner registration token of GitProvider %s: %w", name, err), nil
	}
	registered, err := runners.Runners(ctx)
	if err != nil {
		return "", "", nil, fmt.Errorf("runners of GitProvider %s: %w", name, err)
	}
	for _, runner := range registered {
		if runner.Busy {
			busy[runner.Name] = true
		}
	}
	return token, "", nil, nil
}

// deleteRunnerTokenSecret removes a GitProvider's runner registration token
// Secret, once its ci block is gone; one that is gone already is no error.
func (r *ProjectReconciler) deleteRunnerTokenSecret(ctx context.Context, ns, provider string) error {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: ciRunnerTokenSecret(provider), Namespace: ns}}
	return client.IgnoreNotFound(r.Delete(ctx, secret))
}

// deleteCINetwork removes the job.env ConfigMap and the CI NetworkPolicy, once
// the project has no pool left; ones gone already are no error.
func (r *ProjectReconciler) deleteCINetwork(ctx context.Context, ns string) error {
	env := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: CIJobEnvConfigMap, Namespace: ns}}
	if err := client.IgnoreNotFound(r.Delete(ctx, env)); err != nil {
		return err
	}
	policy := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: CINetworkPolicyName, Namespace: ns}}
	return client.IgnoreNotFound(r.Delete(ctx, policy))
}

// ensureRunnerTokenSecret writes a GitProvider's runner registration token to
// the Secret the pool's init containers read.
func (r *ProjectReconciler) ensureRunnerTokenSecret(ctx context.Context, project *c4v1alpha1.Project,
	provider, token string) error {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name: ciRunnerTokenSecret(provider), Namespace: project.Namespace}}
	return r.createOrUpdateUncached(ctx, secret, func() error {
		secret.Labels = projectLabels(project.Namespace)
		secret.Data = map[string][]byte{ciRunnerTokenKey: []byte(token)}
		return controllerutil.SetControllerReference(project, secret, r.Scheme)
	})
}

// ensureCINetwork fences the pool's pods and gives them the job.env ConfigMap.
func (r *ProjectReconciler) ensureCINetwork(ctx context.Context, project *c4v1alpha1.Project, gitNamespaces []string) error {
	env := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: CIJobEnvConfigMap, Namespace: project.Namespace}}
	if err := r.createOrUpdateUncached(ctx, env, func() error {
		env.Labels = projectLabels(project.Namespace)
		env.Data = map[string]string{"job.env": CIJobEnv}
		return controllerutil.SetControllerReference(project, env, r.Scheme)
	}); err != nil {
		return err
	}
	policy := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{
		Name: CINetworkPolicyName, Namespace: project.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, policy, func() error {
		policy.Labels = projectLabels(project.Namespace)
		policy.Spec = fencePolicySpec(map[string]string{LabelCIRunner: ciRunnerValue}, fenceEgress(r.ClusterDNS, gitNamespaces))
		return controllerutil.SetControllerReference(project, policy, r.Scheme)
	})
	return err
}

// createOrUpdateUncached is controllerutil.CreateOrUpdate for a kind the
// operator's cache never holds (Secrets, ConfigMaps): it reads through
// APIReader, so no informer on every one in the cluster starts.
func (r *ProjectReconciler) createOrUpdateUncached(ctx context.Context, obj client.Object, mutate func() error) error {
	err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(obj), obj)
	if apierrors.IsNotFound(err) {
		if err := mutate(); err != nil {
			return err
		}
		return r.Create(ctx, obj)
	}
	if err != nil {
		return err
	}
	before := obj.DeepCopyObject()
	if err := mutate(); err != nil {
		return err
	}
	if equality.Semantic.DeepEqual(before, obj) {
		return nil
	}
	return r.Update(ctx, obj)
}

// runnerFateAt is when a pod's fate was recorded: AnnotationRunnerFateAt, else
// its deletion, else now.
func runnerFateAt(pod *corev1.Pod) time.Time {
	if at, err := time.Parse(time.RFC3339Nano, pod.Annotations[AnnotationRunnerFateAt]); err == nil {
		return at
	}
	if pod.DeletionTimestamp != nil {
		return pod.DeletionTimestamp.Time
	}
	return time.Now()
}

// recordRunnerFate writes a pod's fate as AnnotationRunnerFate, and how it
// failed, if it did, as AnnotationRunnerFailure, before the pool deletes it,
// so the pool's own delete never reads as someone else's.
func (r *ProjectReconciler) recordRunnerFate(ctx context.Context, pod *corev1.Pod, fate, failure string) error {
	orig := pod.DeepCopy()
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[AnnotationRunnerFate] = fate
	pod.Annotations[AnnotationRunnerFateAt] = time.Now().Format(time.RFC3339Nano)
	if failure != "" {
		pod.Annotations[AnnotationRunnerFailure] = failure
	}
	return r.Patch(ctx, pod, client.MergeFrom(orig))
}

// dropRunnerRecord lets a kept pod go: its record has been readable long enough.
func (r *ProjectReconciler) dropRunnerRecord(ctx context.Context, pod *corev1.Pod) error {
	orig := pod.DeepCopy()
	if !controllerutil.RemoveFinalizer(pod, CIRunnerRecordFinalizer) {
		return nil
	}
	return client.IgnoreNotFound(r.Patch(ctx, pod, client.MergeFrom(orig)))
}
