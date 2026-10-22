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
// credentials cannot be read.
const ConditionCIRunners = "CIRunners"

// DefaultRunnerRecordKept is how long an ended pool pod stays readable, with
// its fate annotation, for the Task that asks who cancelled its job.
const DefaultRunnerRecordKept = 2 * time.Hour

// ciPool is one label's pool of one GitProvider: what buildRunnerPod needs and
// how many pods are not ended.
type ciPool struct {
	in   runnerPodInput
	live int32
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

// ensureCIRunners keeps the project's CI runner pool: for every GitProvider with
// a ci.giteaActions block, a Secret with its runner registration token, and per
// label size pods not ended, each registered ephemeral for one job; the job.env
// ConfigMap and the CI NetworkPolicy once the project has any pool. An ended
// pod is annotated with its fate (runnerPodFate), then deleted and replaced; a
// pod of a provider or label no longer in any ci block is deleted too, lost if
// it was still running. Every pod keeps its finalizer, and so stays readable
// with its fate, for runnerRecordKept after its deletion was asked; the
// returned duration says when the next record expires, zero for none, and
// the namespace's GitProviders are returned for ensurePackagesActions.
func (r *ProjectReconciler) ensureCIRunners(ctx context.Context, project *c4v1alpha1.Project,
	status *c4v1alpha1.ProjectStatus) (time.Duration, []c4v1alpha1.GitProvider, error) {
	var providers c4v1alpha1.GitProviderList
	if err := r.List(ctx, &providers, client.InNamespace(project.Namespace)); err != nil {
		return 0, nil, err
	}
	pools := map[poolKey]*ciPool{}
	var failures []string
	failedReason := "ProviderUnavailable"
	for _, p := range providers.Items {
		if p.Spec.CI == nil || p.Spec.CI.GiteaActions == nil {
			continue
		}
		token, reason, err := r.runnerToken(ctx, project.Namespace, p.Name)
		if err != nil {
			return 0, nil, err
		}
		if reason != "" {
			failures = append(failures, fmt.Sprintf("%s: %s", p.Name, reason))
			if reason == reasonCannotRegister {
				failedReason = "UnsupportedCI"
			}
			continue
		}
		if err := r.ensureRunnerTokenSecret(ctx, project, p.Name, token); err != nil {
			return 0, nil, err
		}
		for _, runner := range p.Spec.CI.GiteaActions.Runners {
			pools[poolKey{p.Name, runner.Label}] = &ciPool{in: runnerPodInput{
				namespace: project.Namespace, provider: p.Name, gitURL: p.Spec.URL,
				tokenSecret: ciRunnerTokenSecret(p.Name), runner: runner, goCacheRoot: r.CIGoCache,
			}}
		}
	}

	next, err := r.sweepRunnerPods(ctx, project.Namespace, pools)
	if err != nil {
		return 0, nil, err
	}
	if len(pools) > 0 {
		if err := r.ensureCINetwork(ctx, project, gitNamespacesOf(providers.Items)); err != nil {
			return 0, nil, err
		}
	}
	if err := r.fillPools(ctx, project, pools); err != nil {
		return 0, nil, err
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
	return next, providers.Items, nil
}

// sweepRunnerPods goes over the project's pool pods: one not ended, of a pool
// still wanted, counts as live; an ended one, or one of no pool, gets its fate
// recorded and is deleted; a deleted one is kept until its record expires.
// Returns when the next record expires, zero for none.
func (r *ProjectReconciler) sweepRunnerPods(ctx context.Context, ns string, pools map[poolKey]*ciPool) (time.Duration, error) {
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
		fate := pod.Annotations[AnnotationRunnerFate]
		if fate == "" {
			fate = runnerPodFate(pod)
			if fate == "" && !wanted {
				fate = RunnerFateLost
			}
			if fate == "" {
				pool.live++
				continue
			}
			if err := r.recordRunnerFate(ctx, pod, fate); err != nil {
				return 0, err
			}
			log.Info("CI runner pod ended", "pod", pod.Name, "fate", fate, "pool", key.provider+"/"+key.label)
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

// fillPools creates the pods each pool is short of, pools in name order.
func (r *ProjectReconciler) fillPools(ctx context.Context, project *c4v1alpha1.Project, pools map[poolKey]*ciPool) error {
	log := logf.FromContext(ctx)
	keys := make([]poolKey, 0, len(pools))
	for key := range pools {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b poolKey) int {
		return cmp.Or(cmp.Compare(a.provider, b.provider), cmp.Compare(a.label, b.label))
	})
	for _, key := range keys {
		pool := pools[key]
		for ; pool.live < pool.in.runner.Size; pool.live++ {
			pod := buildRunnerPod(pool.in)
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

// runnerToken is a GitProvider's runner registration token. A provider whose
// host cannot register runners, or whose credentials cannot be read, gives a
// reason instead and no error: the Project's condition says it.
func (r *ProjectReconciler) runnerToken(ctx context.Context, ns, name string) (token, reason string, err error) {
	provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, ns, name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return "", "", err
		}
		return "", err.Error(), nil
	}
	runners, ok := provider.(gitprovider.OwnerRunners)
	if !ok {
		return "", reasonCannotRegister, nil
	}
	token, err = runners.RunnerRegistrationToken(ctx)
	if err != nil {
		return "", "", fmt.Errorf("runner registration token of GitProvider %s: %w", name, err)
	}
	return token, "", nil
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
		policy.Spec = fencePolicySpec(map[string]string{LabelCIRunner: ciRunnerValue}, r.ClusterDNS, gitNamespaces)
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

// recordRunnerFate writes a pod's fate as AnnotationRunnerFate, before the
// pool deletes it, so the pool's own delete never reads as someone else's.
func (r *ProjectReconciler) recordRunnerFate(ctx context.Context, pod *corev1.Pod, fate string) error {
	orig := pod.DeepCopy()
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	pod.Annotations[AnnotationRunnerFate] = fate
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
