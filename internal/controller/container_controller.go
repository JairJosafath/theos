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
	"slices"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// ContainerReconciler makes a Container a child of its SoftwareSystem, checks its
// catalog choices, scaffolds its repository once and counts its active Sessions.
type ContainerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// GitEvents is the receiver of the Git host's deliveries; nil when it is
	// off, and the reconciler polls alone.
	GitEvents *GitEventsReceiver
	// APIReader reads Secrets without caching them.
	APIReader client.Reader
	// Git reaches the Git hosts.
	Git GitClient
	// ScaffoldOptions configure the parts of a scaffold the operator owns.
	ScaffoldOptions scaffold.Options
	// ScaffoldTimeout bounds the Git calls of one scaffold attempt; zero means two minutes.
	ScaffoldTimeout time.Duration
	// ScaffoldSettle is how long a new Container waits after its creation
	// before it is scaffolded, so a model applied in one go has its
	// Relationships there (#43); nil or zero waits not at all.
	ScaffoldSettle func() time.Duration
	// ChecksRecheck is how often a Ready Container reads its checks; zero means a minute.
	ChecksRecheck time.Duration
	// SecretRecheck is how often a Container waiting for its credentials Secret
	// looks again; zero means 15 seconds.
	SecretRecheck time.Duration
	// Recorder emits the Container's Events about its Standard's files and
	// its deploys; nil emits none.
	Recorder events.EventRecorder
	// Deployers ship the Container's releases to its spec.deploy, by
	// DeployTarget member.
	Deployers ports.Deployers
	// DeployMembers returns a DeployTarget member's row; nil means the
	// model's table. Tests give members needs no real member has yet.
	DeployMembers func(name string) (model.DeployMember, error)
	// DeployPoll is how often a Container with a deploy in flight reads its
	// targets again; zero means 15 seconds.
	DeployPoll time.Duration

	// deploySaid remembers each deploy's last Event, by Container UID and
	// target.
	deploySaid sync.Map
	// deploySources is, by Container UID and target, the last release's
	// source read from the Git host (sourceRead) and checked against the
	// target's own Secret, so each poll of the deploy doesn't download the
	// archive again.
	deploySources sync.Map
	// imageSaid remembers each Container's last image Event, by UID: its
	// release's tag and state.
	imageSaid sync.Map
	// imagePushed remembers each Container's last image push, by UID, until
	// a reconcile writes it: the push runs in a goroutine of its own and
	// hands its result over here, and the cache can lag that status write by
	// a reconcile, which must not push again (#1573: liveContainer can't
	// replace it, as the result has to reach the reconcile somewhere).
	imagePushed sync.Map
	// imagePushing holds each Container's image push in flight, an
	// *imagePush by UID, and imagePushEnded wakes the Container when one ends.
	imagePushing   sync.Map
	imagePushEnded chan event.GenericEvent
	// uids remembers each Container's UID by NamespacedName, so a reconcile
	// that finds the Container gone (it carries no finalizer, so the Get is
	// NotFound with no object) still knows whose entries to forget (#1629).
	uids sync.Map
}

// +kubebuilder:rbac:groups=theos.factory.io,resources=containers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=theos.factory.io,resources=containers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=theos.factory.io,resources=containers/finalizers,verbs=update

// +kubebuilder:rbac:groups=theos.factory.io,resources=gitproviders;frameworks;standards;computes;deployments;connectors;bundles;domains,verbs=get;list;watch
// +kubebuilder:rbac:groups=theos.factory.io,resources=relationships;datastores;sessions,verbs=get;list;watch
// +kubebuilder:rbac:groups=theos.factory.io,resources=crews;deploytargets;projects,verbs=get;list;watch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get

// containerWorkers is how many Containers reconcile at once, so one
// Container's long upload to a deploy target holds no other's scaffold,
// checks or deploys (#1446); one Container never runs twice at once. The
// reconciler's own state is sync.Maps keyed by the Container's UID.
const containerWorkers = 4

// defaultSecretRecheck is how often a Container waiting for its credentials
// Secret looks again, unless SecretRecheck says.
const defaultSecretRecheck = 15 * time.Second

func (r *ContainerReconciler) secretRecheck() time.Duration {
	if r.SecretRecheck > 0 {
		return r.SecretRecheck
	}
	return defaultSecretRecheck
}

// forget drops what the per-UID maps remember of a Container that is being
// deleted (#1571), and stops its image push: deploySaid's keys are the UID
// and a target, the others the UID alone.
func (r *ContainerReconciler) forget(uid types.UID) {
	if push, ok := r.imagePushing.LoadAndDelete(uid); ok {
		push.(*imagePush).cancel()
	}
	r.imageSaid.Delete(uid)
	r.imagePushed.Delete(uid)
	prefix := string(uid) + "/"
	for _, m := range []*sync.Map{&r.deploySaid, &r.deploySources} {
		m.Range(func(key, _ any) bool {
			if k, ok := key.(string); ok && strings.HasPrefix(k, prefix) {
				m.Delete(key)
			}
			return true
		})
	}
}

// remember keeps the UID of the Container by its name. A Container deleted
// and made again under its name between two reconciles has a new UID, and
// the old one's entries go.
func (r *ContainerReconciler) remember(name types.NamespacedName, uid types.UID) {
	if old, loaded := r.uids.Swap(name, uid); loaded && old.(types.UID) != uid {
		r.forget(old.(types.UID))
	}
}

// forgetName forgets the Container last seen under name, whose Get is
// NotFound: it carries no finalizer, so no reconcile saw it being deleted
// (#1629).
func (r *ContainerReconciler) forgetName(name types.NamespacedName) {
	if uid, ok := r.uids.LoadAndDelete(name); ok {
		r.forget(uid.(types.UID))
	}
}

// Reconcile adopts the Container, resolves its references, checks its catalog
// choices and scaffolds its repository once.
func (r *ContainerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var container c4v1beta1.Container
	if err := r.Get(ctx, req.NamespacedName, &container); err != nil {
		if apierrors.IsNotFound(err) {
			r.forgetName(req.NamespacedName)
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !container.DeletionTimestamp.IsZero() {
		r.uids.Delete(req.NamespacedName)
		r.forget(container.UID)
		return ctrl.Result{}, nil
	}
	r.remember(req.NamespacedName, container.UID)
	// The waits for the host's checks stretch while its deliveries arrive (#1222).
	ctx = withHostEvents(ctx, r.Client, container.Namespace, container.Spec.GitProviderRef.Name)

	active, err := r.countSessions(ctx, &container)
	if err != nil {
		return ctrl.Result{}, err
	}
	// observe starts the status this reconcile writes from the Container as it
	// is now: after adopt, which reads it back newer than the cache (#1573).
	observe := func() *c4v1beta1.ContainerStatus {
		status := container.Status.DeepCopy()
		status.ActiveSessions = int32(active)
		return status
	}

	var system c4v1beta1.SoftwareSystem
	err = r.Get(ctx, types.NamespacedName{Namespace: container.Namespace, Name: container.Spec.SoftwareSystemRef.Name}, &system)
	switch {
	case apierrors.IsNotFound(err):
		status := observe()
		// A scaffolded Container stays Ready: its repository is still there, and
		// losing Ready would scaffold it again.
		if status.Phase != c4v1beta1.ContainerReady {
			status.Phase = c4v1beta1.ContainerPending
		}
		setCondition(&status.Conditions, container.Generation, c4v1beta1.ConditionReferencesResolved,
			metav1.ConditionFalse, c4v1beta1.ReasonNotFound,
			notFoundMessage([]string{"SoftwareSystem " + container.Spec.SoftwareSystemRef.Name}))
		return ctrl.Result{}, r.writeStatus(ctx, &container, status)
	case err != nil:
		return ctrl.Result{}, err
	}
	labels := projectLabels(container.Namespace)
	labels[c4v1beta1.LabelSoftwareSystem] = system.Name
	if err := adopt(ctx, r.Client, r.Scheme, &system, &container, labels); err != nil {
		return ctrl.Result{}, err
	}
	status := observe()

	// Scaffolding happens once. After that the factory only reports.
	if status.Phase == c4v1beta1.ContainerReady {
		setCondition(&status.Conditions, container.Generation, c4v1beta1.ConditionReferencesResolved,
			metav1.ConditionTrue, c4v1beta1.ReasonResolved, "")
		result := r.refreshChecks(ctx, &container, status)
		live := r.liveContainer(ctx, &container)
		answered := false
		if container.Choices().Standard != "" {
			answered = r.updateStandard(ctx, &container, status, live)
		} else {
			meta.RemoveStatusCondition(&status.Conditions, c4v1beta1.ConditionStandardUpdated)
		}
		r.reportBundleCurrent(ctx, &container, status)
		reportDomainsApplied(&container, status, &system)
		r.reportTestEnvironments(ctx, &container, status)
		failed := r.updateOperations(ctx, &container, status, live)
		// A failed Crew list leaves status.image and status.deploys as they
		// were: the pass would act on a newest release it couldn't read (#1570, #1576).
		if poll := r.keepNewestRelease(ctx, &container, status); poll > 0 {
			result = sooner(result, ctrl.Result{RequeueAfter: poll})
		} else {
			result = sooner(result, ctrl.Result{RequeueAfter: r.publishImage(ctx, &container, status)})
			result = sooner(result, ctrl.Result{RequeueAfter: r.deploy(ctx, &container, status)})
		}
		// A Git error is retried, and a pull request in flight read again, at
		// each recheck, with or without a Standard: the status write doesn't
		// reconcile the Container again.
		if failed && result.RequeueAfter == 0 {
			result.RequeueAfter = r.checksRecheck()
		}
		if operationsInFlight(status) && result.RequeueAfter == 0 {
			result.RequeueAfter = hostRecheck(ctx, r.checksRecheck())
		}
		// After the updates' steps, so it reads what the repository runs now.
		r.reportChecksApplied(ctx, &container, status)
		if err := r.writeStatus(ctx, &container, status); err != nil || !answered {
			return result, err
		}
		return result, r.removeAnswers(ctx, &container)
	}
	r.reportChecksApplied(ctx, &container, status)

	// The factory builds nothing for an External system. A Container scaffolded
	// before its system turned External passed the Ready branch above.
	if system.Spec.Location == c4v1beta1.SystemExternal {
		status.Phase = c4v1beta1.ContainerPending
		setCondition(&status.Conditions, container.Generation, c4v1beta1.ConditionReferencesResolved,
			metav1.ConditionFalse, c4v1beta1.ReasonExternalSystem, externalMessage(system.Name))
		return ctrl.Result{}, r.writeStatus(ctx, &container, status)
	}

	res, missing, err := r.resolve(ctx, &container, &system)
	if err != nil {
		return ctrl.Result{}, err
	}
	// Until the scaffold the expansion follows the Bundle; the scaffold keeps
	// the last one, and from then on the Container reads it.
	status.Bundle = res.bundle
	if len(missing) > 0 {
		status.Phase = c4v1beta1.ContainerPending
		setCondition(&status.Conditions, container.Generation, c4v1beta1.ConditionReferencesResolved,
			metav1.ConditionFalse, c4v1beta1.ReasonNotFound, notFoundMessage(missing))
		// Secrets aren't watched (they're read uncached), so look again for one.
		var result ctrl.Result
		if slices.ContainsFunc(missing, func(m string) bool { return strings.HasPrefix(m, "Secret ") }) {
			result.RequeueAfter = r.secretRecheck()
		}
		return result, r.writeStatus(ctx, &container, status)
	}
	setCondition(&status.Conditions, container.Generation, c4v1beta1.ConditionReferencesResolved,
		metav1.ConditionTrue, c4v1beta1.ReasonResolved, "")

	choices := res.choices
	err = model.CheckChoices(string(container.Spec.Type), res.entries)
	if err == nil {
		err = model.CheckStandard(choices.Framework, res.standard)
	}
	if err == nil {
		err = model.CheckWiring(choices.Framework, res.entries)
	}
	if err == nil {
		err = model.CheckDeploysItself(frameworkEntry(res.entries), string(container.Spec.Type), choices.Compute,
			choices.Deployment)
	}
	err = model.InBundle(choices.Bundle, err)
	if err != nil {
		reason := c4v1beta1.ReasonUnsupportedType
		if _, ok := errors.AsType[*model.FitError](err); ok {
			reason = c4v1beta1.ReasonUnsupportedFramework
		}
		if _, ok := errors.AsType[*model.SelfDeployError](err); ok {
			reason = c4v1beta1.ReasonDeploysItself
		}
		if _, ok := errors.AsType[*model.WiringError](err); ok {
			reason = c4v1beta1.ReasonNotWired
		}
		status.Phase = c4v1beta1.ContainerFailed
		setCondition(&status.Conditions, container.Generation, c4v1beta1.ConditionChoicesValid,
			metav1.ConditionFalse, reason, err.Error())
		log.Info("Rejected catalog choices", "reason", err.Error())
		return ctrl.Result{}, r.writeStatus(ctx, &container, status)
	}
	setCondition(&status.Conditions, container.Generation, c4v1beta1.ConditionChoicesValid,
		metav1.ConditionTrue, c4v1beta1.ReasonValid, "")

	// #43: the Relationships applied with the Container may come after it,
	// and the scaffold reads its Connectors from them once. Resolving again
	// after the window counts every one created by then.
	if left := r.settleLeft(&container); left > 0 {
		status.Phase = c4v1beta1.ContainerPending
		r.condition(&container, status, c4v1beta1.ConditionScaffolded, metav1.ConditionFalse, c4v1beta1.ReasonSettling,
			fmt.Sprintf("Waiting %s after the Container's creation for the Relationships applied with it",
				left.Round(time.Second)))
		return ctrl.Result{RequeueAfter: left}, r.writeStatus(ctx, &container, status)
	}
	return r.scaffold(ctx, &container, res, status)
}

// settleLeft is how much longer the Container waits before its scaffold.
func (r *ContainerReconciler) settleLeft(container *c4v1beta1.Container) time.Duration {
	if r.ScaffoldSettle == nil {
		return 0
	}
	return model.SettleLeft(container.CreationTimestamp.Time, time.Now(), r.ScaffoldSettle())
}

func (r *ContainerReconciler) countSessions(ctx context.Context, container *c4v1beta1.Container) (int, error) {
	var sessions c4v1beta1.SessionList
	if err := r.List(ctx, &sessions, client.InNamespace(container.Namespace),
		client.MatchingFields{indexContainer: container.Name}); err != nil {
		return 0, err
	}
	return countActive(sessions.Items), nil
}

func (r *ContainerReconciler) writeStatus(ctx context.Context, container *c4v1beta1.Container, status *c4v1beta1.ContainerStatus) error {
	status.ObservedGeneration = container.Generation
	if equality.Semantic.DeepEqual(&container.Status, status) {
		return nil
	}
	orig := container.DeepCopy()
	container.Status = *status
	return r.Status().Patch(ctx, container, client.MergeFrom(orig))
}

// removeAnswers removes the person's answer to the Standard files behind once
// the status records what it did, unless someone changed the Container since.
func (r *ContainerReconciler) removeAnswers(ctx context.Context, container *c4v1beta1.Container) error {
	orig := container.DeepCopy()
	delete(container.Annotations, c4v1beta1.AnnotationStandardTake)
	delete(container.Annotations, c4v1beta1.AnnotationStandardKeep)
	return r.Patch(ctx, container, client.MergeFromWithOptions(orig, client.MergeFromWithOptimisticLock{}))
}

// SetupWithManager sets up the controller with the Manager.
func (r *ContainerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	toContainer := handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
		session, ok := obj.(*c4v1beta1.Session)
		if !ok {
			return nil
		}
		return []reconcile.Request{requestFor(session.Namespace, session.Spec.ContainerRef.Name)}
	})
	systemContainers := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		var containers c4v1beta1.ContainerList
		if err := mgr.GetClient().List(ctx, &containers, client.InNamespace(obj.GetNamespace()),
			client.MatchingFields{indexSoftwareSystem: obj.GetName()}); err != nil {
			return nil
		}
		reqs := make([]reconcile.Request, 0, len(containers.Items))
		for _, c := range containers.Items {
			reqs = append(reqs, requestFor(c.Namespace, c.Name))
		}
		return reqs
	})
	// A catalog entry, GitProvider or DataStore appearing or changing its spec can
	// unblock any Container in the namespace that isn't scaffolded yet.
	waitingContainers := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		var containers c4v1beta1.ContainerList
		if err := mgr.GetClient().List(ctx, &containers, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}
		var reqs []reconcile.Request
		for _, c := range containers.Items {
			if c.Status.Phase != c4v1beta1.ContainerReady {
				reqs = append(reqs, requestFor(c.Namespace, c.Name))
			}
		}
		return reqs
	})
	// A Standard changing its spec also moves the Ready Containers on it to its
	// new ref.
	standardContainers := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		var containers c4v1beta1.ContainerList
		if err := mgr.GetClient().List(ctx, &containers, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}
		var reqs []reconcile.Request
		for _, c := range containers.Items {
			if c.Status.Phase != c4v1beta1.ContainerReady ||
				c.Choices().Standard == obj.GetName() {
				reqs = append(reqs, requestFor(c.Namespace, c.Name))
			}
		}
		return reqs
	})
	// A Bundle changing, or going, is reported on the Ready Containers naming
	// it, never applied (#1621).
	bundleContainers := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		var containers c4v1beta1.ContainerList
		if err := mgr.GetClient().List(ctx, &containers, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}
		var reqs []reconcile.Request
		for _, c := range containers.Items {
			if c.Status.Phase != c4v1beta1.ContainerReady ||
				(c.Spec.BundleRef != nil && c.Spec.BundleRef.Name == obj.GetName()) {
				reqs = append(reqs, requestFor(c.Namespace, c.Name))
			}
		}
		return reqs
	})
	sourceContainer := handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
		rel, ok := obj.(*c4v1beta1.Relationship)
		if !ok || rel.Spec.Source.Kind != c4v1beta1.ElementContainer {
			return nil
		}
		return []reconcile.Request{requestFor(rel.Namespace, rel.Spec.Source.Name)}
	})
	// A Crew's release whose runs succeeded ships, and a DeployTarget turning
	// ready lets its Containers ship there.
	crewContainer := handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
		crew, ok := obj.(*c4v1beta1.Crew)
		if !ok {
			return nil
		}
		return []reconcile.Request{requestFor(crew.Namespace, crew.Spec.ContainerRef.Name)}
	})
	targetContainers := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		var containers c4v1beta1.ContainerList
		if err := mgr.GetClient().List(ctx, &containers, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}
		var reqs []reconcile.Request
		for _, c := range containers.Items {
			if slices.ContainsFunc(c.Spec.Deploy, func(d c4v1beta1.ContainerDeploy) bool {
				return d.Target == obj.GetName()
			}) {
				reqs = append(reqs, requestFor(c.Namespace, c.Name))
			}
		}
		return reqs
	})
	r.imagePushEnded = make(chan event.GenericEvent, containerWorkers)
	return ctrl.NewControllerManagedBy(mgr).
		For(&c4v1beta1.Container{}, specOrAsked()).
		WithOptions(controller.Options{MaxConcurrentReconciles: containerWorkers}).
		WatchesRawSource(source.Channel(r.imagePushEnded, &handler.EnqueueRequestForObject{})).
		Watches(&c4v1beta1.Crew{}, crewContainer, lastReleaseChanges()).
		Watches(&c4v1beta1.DeployTarget{}, targetContainers, deployTargetReadyChanges()).
		Watches(&c4v1beta1.Session{}, toContainer, sessionPhaseChanges()).
		Watches(&c4v1beta1.SoftwareSystem{}, systemContainers, specChanged()).
		Watches(&c4v1beta1.Relationship{}, sourceContainer, specChanged()).
		Watches(&c4v1beta1.GitProvider{}, waitingContainers, specChanged()).
		Watches(&c4v1beta1.Framework{}, waitingContainers, specChanged()).
		Watches(&c4v1beta1.Standard{}, standardContainers, specChanged()).
		Watches(&c4v1beta1.Compute{}, waitingContainers, specChanged()).
		Watches(&c4v1beta1.Deployment{}, waitingContainers, specChanged()).
		Watches(&c4v1beta1.Connector{}, waitingContainers, specChanged()).
		Watches(&c4v1beta1.Domain{}, waitingContainers, specChanged()).
		Watches(&c4v1beta1.Bundle{}, bundleContainers, specChanged()).
		Watches(&c4v1beta1.DataStore{}, waitingContainers, specChanged()).
		Watches(&c4v1beta1.TestEnvironment{}, handler.EnqueueRequestsFromMapFunc(namespaceContainers(mgr.GetClient())),
			createOrDelete()).
		// A delivery of the Git host reconciles the Container whose repository a delivery names at once
		// (docs/plans/done/git-events.md, decision 7).
		WatchesRawSource(r.GitEvents.Source(gitEventContainerRequest(mgr.GetClient()))).
		Named("container").
		Complete(r)
}
