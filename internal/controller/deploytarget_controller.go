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

package controller

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// DeployTargetReconciler checks each DeployTarget's credential with its
// member's Deployer and reports it in the condition Ready.
type DeployTargetReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// APIReader reads the target's Secret live: the operator caches no Secrets,
	// so it reads the Secret on each poll and checks the target again when the
	// Secret changed.
	APIReader client.Reader
	// Deployers are the Deployers by member, as cmd/main.go builds them.
	Deployers ports.Deployers
	// PollInterval is how often the Secret is read; 0 means a minute.
	PollInterval time.Duration
	// CheckInterval is how often a target whose spec and Secret stay the same
	// is checked; 0 means 10 minutes.
	CheckInterval time.Duration

	// checked holds, by DeployTarget namespace/name, the target, generation and Secret version
	// the last check saw; after a restart each target is checked again.
	checked sync.Map
}

// +kubebuilder:rbac:groups=theos.factory.io,resources=deploytargets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=theos.factory.io,resources=deploytargets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=theos.factory.io,resources=deploytargets/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get
// +kubebuilder:rbac:groups=argoproj.io,resources=applications,verbs=get;list;watch;create;patch
// +kubebuilder:rbac:groups=argoproj.io,resources=appprojects,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get

// Reconcile reads the target's Secret (an argoCD target may have none) and,
// when it holds the member's keys, asks the member's Deployer whether the
// target accepts it: on a change of the spec or the Secret, and at most every
// CheckInterval otherwise.
func (r *DeployTargetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var dt c4v1beta1.DeployTarget
	if err := r.Get(ctx, req.NamespacedName, &dt); err != nil {
		if apierrors.IsNotFound(err) {
			r.checked.Delete(req.NamespacedName)
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	log := logf.FromContext(ctx)
	target, where := deployTarget(&dt)
	member, err := model.DeployMemberByName(target.Member)
	if err != nil {
		// The CRD's CEL lets only members the factory ships to through.
		log.Error(err, "DeployTarget names no member the operator knows")
		return ctrl.Result{}, nil
	}
	check := model.DeployCheck{Target: dt.Name, Where: where, Member: member}
	status := dt.Status.DeepCopy()
	poll := ctrl.Result{RequeueAfter: r.pollInterval()}

	// A target without a Secret (argoCD) is checked with the operator's own
	// ServiceAccount.
	var secret corev1.Secret
	if ref := dt.Spec.CredentialsSecretRef; ref != nil {
		check.Secret = ref.Name
		err = r.APIReader.Get(ctx, client.ObjectKey{Namespace: dt.Namespace, Name: check.Secret}, &secret)
		switch {
		case apierrors.IsNotFound(err):
			r.setReady(status, dt.Generation, model.DeployTargetSecretMissing, check)
			return poll, r.writeStatus(ctx, &dt, status)
		case err != nil:
			return ctrl.Result{}, err
		}
		if check.Missing = member.MissingKeys(secret.Data); len(check.Missing) > 0 {
			r.setReady(status, dt.Generation, model.DeployTargetSecretIncomplete, check)
			return poll, r.writeStatus(ctx, &dt, status)
		}
	}

	seen := deployChecked{uid: dt.UID, generation: dt.Generation, secret: secret.ResourceVersion}
	last, ok := r.checked.Load(req.NamespacedName)
	cond := apimeta.FindStatusCondition(status.Conditions, c4v1beta1.ConditionDeployTargetReady)
	// A target that couldn't be reached is asked again on the next poll.
	if ok && last == seen && cond != nil && cond.ObservedGeneration == dt.Generation && status.CheckedTime != nil &&
		cond.Reason != model.DeployTargetUnreachable {
		if wait := r.checkInterval() - time.Since(status.CheckedTime.Time); wait > 0 {
			return ctrl.Result{RequeueAfter: min(wait, r.pollInterval())}, nil
		}
	}

	deployer, err := r.Deployers.For(target.Member)
	if err != nil {
		log.Error(err, "Failed to find the member's deployer", "member", target.Member)
		return ctrl.Result{}, nil
	}
	target.Secret = secret.Data
	err = deployer.Check(ctx, target)
	reason := model.DeployTargetReady
	switch {
	case err == nil:
	case errors.Is(err, ports.ErrCredentialRefused):
		reason, check.Detail = model.DeployTargetCredentialRefused, err.Error()
	default:
		reason, check.Detail = model.DeployTargetUnreachable, err.Error()
	}
	if cond == nil || cond.Reason != reason {
		log.Info("Checked deploy target", "member", target.Member, "reason", reason)
	}
	now := metav1.Now()
	status.CheckedTime = &now
	r.setReady(status, dt.Generation, reason, check)
	if err := r.writeStatus(ctx, &dt, status); err != nil {
		return ctrl.Result{}, err
	}
	r.checked.Store(req.NamespacedName, seen)
	return poll, nil
}

// deployChecked is what a target's last check saw: the target, its
// generation and its Secret's resourceVersion.
type deployChecked struct {
	uid        types.UID
	generation int64
	secret     string
}

// hostedTarget is target as a github or gitlab member reads it: the
// repository or project under pathKey, the instance's url under urlKey when
// self-hosted, and its place as path or host/path.
func hostedTarget(target ports.Target, member, pathKey, path, urlKey, url string) (ports.Target, string) {
	target.Member = member
	target.Settings = map[string]string{pathKey: path}
	if url == "" {
		return target, path
	}
	target.Settings[urlKey] = url
	return target, strings.TrimPrefix(url, "https://") + "/" + path
}

// deployTarget is dt as its member's Deployer reads it, its Secret's data
// left out, and its place in the member's words.
func deployTarget(dt *c4v1beta1.DeployTarget) (ports.Target, string) {
	target := ports.Target{Name: dt.Namespace + "/" + dt.Name, Tier: string(dt.Spec.Tier),
		Parameters: dt.Spec.Parameters}
	if gh := dt.Spec.GitHub; gh != nil {
		return hostedTarget(target, model.DeployMemberGitHub, model.GitHubSettingRepository, gh.Repository,
			model.GitHubSettingURL, gh.URL)
	}
	if gl := dt.Spec.GitLab; gl != nil {
		return hostedTarget(target, model.DeployMemberGitLab, model.GitLabSettingProject, gl.Project,
			model.GitLabSettingURL, gl.URL)
	}
	if a := dt.Spec.ArgoCD; a != nil {
		target.Member = model.DeployMemberArgoCD
		target.Settings = map[string]string{model.ArgoCDSettingNamespace: a.Namespace,
			model.ArgoCDSettingProject: a.Project, model.ArgoCDSettingDestinationNamespace: a.Destination.Namespace,
			model.ArgoCDSettingDestinationServer: a.Destination.Server,
			model.ArgoCDSettingImageRegistry:     a.ImageRegistry, model.ArgoCDSettingGitURL: a.GitURL,
			model.ArgoCDSettingImageRepositoryLayout: a.ImageRepositoryLayout,
			model.ArgoCDSettingImageRegistryInsecure: strconv.FormatBool(a.ImageRegistryInsecure)}
		return target, fmt.Sprintf("project %s in namespace %s (destination %s)", a.Project, a.Namespace,
			a.Destination.Namespace)
	}
	if cf := dt.Spec.CloudFormation; cf != nil {
		target.Member = model.DeployMemberCloudFormation
		target.Settings = map[string]string{model.CloudFormationSettingRegion: cf.Region,
			model.CloudFormationSettingAccount: cf.Account}
		if cf.Account == "" {
			return target, "region " + cf.Region
		}
		return target, fmt.Sprintf("account %s in region %s", cf.Account, cf.Region)
	}
	return target, ""
}

// setReady sets the Ready condition to reason.
func (r *DeployTargetReconciler) setReady(status *c4v1beta1.DeployTargetStatus, generation int64, reason string,
	check model.DeployCheck) {
	value := metav1.ConditionFalse
	if reason == model.DeployTargetReady {
		value = metav1.ConditionTrue
	}
	setCondition(&status.Conditions, generation, c4v1beta1.ConditionDeployTargetReady, value, reason,
		model.DeployCheckMessage(reason, check))
}

// writeStatus patches dt's status to status when it changed.
func (r *DeployTargetReconciler) writeStatus(ctx context.Context, dt *c4v1beta1.DeployTarget,
	status *c4v1beta1.DeployTargetStatus) error {
	if equality.Semantic.DeepEqual(&dt.Status, status) {
		return nil
	}
	orig := dt.DeepCopy()
	dt.Status = *status
	if err := r.Status().Patch(ctx, dt, client.MergeFrom(orig)); err != nil {
		return fmt.Errorf("writing the status of DeployTarget %s: %w", dt.Name, err)
	}
	return nil
}

func (r *DeployTargetReconciler) pollInterval() time.Duration {
	if r.PollInterval > 0 {
		return r.PollInterval
	}
	return time.Minute
}

func (r *DeployTargetReconciler) checkInterval() time.Duration {
	if r.CheckInterval > 0 {
		return r.CheckInterval
	}
	return 10 * time.Minute
}

// SetupWithManager sets up the controller with the Manager. A status write
// starts no reconcile; a spec change does, and each poll reads the Secret,
// when the target names one.
func (r *DeployTargetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&c4v1beta1.DeployTarget{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("deploytarget").
		Complete(r)
}
