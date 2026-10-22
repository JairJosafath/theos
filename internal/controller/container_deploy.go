/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"errors"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/ports"
)

// deployTimeout bounds one deploy pass: a GitHub upload streams a whole file.
const deployTimeout = 30 * time.Minute

// defaultDeployPoll is how often a Container with a deploy in flight reads its
// targets again, unless DeployPoll says.
const defaultDeployPoll = 15 * time.Second

func (r *ContainerReconciler) deployPoll() time.Duration {
	if r.DeployPoll > 0 {
		return r.DeployPoll
	}
	return defaultDeployPoll
}

func (r *ContainerReconciler) deployMember(name string) (model.DeployMember, error) {
	if r.DeployMembers != nil {
		return r.DeployMembers(name)
	}
	return model.DeployMemberByName(name)
}

// deploy ships the Container's newest release, or each entry's pin, to the
// targets of its spec.deploy, in order, and reports them in status.deploys
// and the condition Deployed. It returns how soon to look again, zero
// without spec.deploy.
func (r *ContainerReconciler) deploy(ctx context.Context, container *c4v1alpha1.Container,
	status *c4v1alpha1.ContainerStatus) time.Duration {
	log := logf.FromContext(ctx)
	newest := status.NewestRelease
	if len(container.Spec.Deploy) == 0 {
		status.Deploys = nil
		apimeta.RemoveStatusCondition(&status.Conditions, c4v1alpha1.ConditionDeployed)
		return 0
	}
	ctx, cancel := context.WithTimeout(ctx, deployTimeout)
	defer cancel()
	brings, err := r.deployContainer(ctx, container)
	if err != nil {
		log.Error(err, "Failed to read the Container's Framework")
		return r.deployPoll()
	}
	prevs := map[string]*model.DeployReport{}
	for _, d := range status.Deploys {
		prevs[d.Target] = &model.DeployReport{Target: d.Target, Version: d.Version, WantedVersion: d.WantedVersion,
			Phase: d.Phase, Health: d.Health, URL: d.URL, Reason: d.Reason, Message: d.Message}
	}
	pass := &deployPass{r: r, container: container, log: log, image: status.Image}
	reports := map[string]*model.DeployReport{}
	steps := make([]model.DeployStep, 0, len(container.Spec.Deploy))
	for _, e := range container.Spec.Deploy {
		facts := model.DeployFacts{Container: container.Name, Target: e.Target, After: e.After, Version: e.Version,
			Approved: e.Approved, Newest: newest, Prev: prevs[e.Target], Previous: reports[e.After]}
		target, deployer := pass.read(ctx, &facts, brings)
		step := model.NextDeploy(facts)
		if step.Ship != "" {
			if facts.Failure = pass.ship(ctx, target, deployer, step.Ship); facts.Failure != nil {
				step = model.NextDeploy(facts)
			}
		}
		r.deployEvent(ctx, container, facts.Prev, step.Report)
		reports[e.Target] = &step.Report
		steps = append(steps, step)
	}

	now := metav1.Now()
	deploys := make([]c4v1alpha1.ContainerDeployStatus, 0, len(steps))
	poll := r.checksRecheck()
	for _, s := range steps {
		rep := s.Report
		since := &now
		for _, d := range status.Deploys {
			if d.Target == rep.Target && d.Phase == rep.Phase && d.Reason == rep.Reason &&
				d.WantedVersion == rep.WantedVersion && d.Since != nil {
				since = d.Since
			}
		}
		deploys = append(deploys, c4v1alpha1.ContainerDeployStatus{Target: rep.Target, Version: rep.Version,
			WantedVersion: rep.WantedVersion, Phase: rep.Phase, Health: rep.Health, URL: rep.URL, Reason: rep.Reason,
			Message: rep.Message, Since: since})
		if s.Cause == model.DeployDeploying || s.Cause == model.DeployTargetNotReady && rep.Reason ==
			model.DeployUnreachable {
			poll = r.deployPoll()
		}
	}
	status.Deploys = deploys
	ok, reason, message := model.DeployedCondition(steps)
	value := metav1.ConditionFalse
	if ok {
		value = metav1.ConditionTrue
	}
	setCondition(&status.Conditions, container.Generation, c4v1alpha1.ConditionDeployed, value, reason, message)
	return poll
}

// deployPass is one pass over a Container's deploys.
type deployPass struct {
	r         *ContainerReconciler
	container *c4v1alpha1.Container
	log       logr.Logger
	// image is the newest release's, status.image.
	image *c4v1alpha1.ContainerImageStatus
	// provider and repo reach the Container's repository, once a ship needs it.
	provider gitprovider.Provider
	repo     gitprovider.Repository
}

// read adds to facts what the entry's DeployTarget says and, when the entry
// can ship, what runs there. It returns the target and its Deployer when it
// read what runs there.
func (p *deployPass) read(ctx context.Context, facts *model.DeployFacts,
	brings model.DeployContainer) (ports.Target, ports.Deployer) {
	var dt c4v1alpha1.DeployTarget
	found, err := p.r.lookup(ctx, p.container.Namespace, facts.Target, &dt)
	if err != nil {
		p.log.Error(err, "Failed to read the DeployTarget", "target", facts.Target)
		facts.TargetUnread = err.Error()
		return ports.Target{}, nil
	}
	if !found {
		return ports.Target{}, nil
	}
	facts.TargetFound = true
	target := containerTarget(&dt, p.container)
	member, err := p.r.deployMember(target.Member)
	if err != nil {
		facts.TargetChecked, facts.TargetMessage = true, err.Error()
		return ports.Target{}, nil
	}
	facts.Tool = member.Tool
	facts.Approval = model.DeployApproval(string(dt.Spec.Tier), string(dt.Spec.Approval))
	facts.Lacks = member.Lacks(brings)
	if cond := apimeta.FindStatusCondition(dt.Status.Conditions, c4v1alpha1.ConditionDeployTargetReady); cond != nil {
		facts.TargetChecked = true
		facts.TargetReady = cond.Status == metav1.ConditionTrue
		facts.TargetMessage = cond.Message
	}
	if !model.DeployReadsState(*facts) {
		return ports.Target{}, nil
	}
	deployer, err := p.r.Deployers.For(target.Member)
	if err != nil {
		facts.StateFailure = err.Error()
		return ports.Target{}, nil
	}
	var secret corev1.Secret
	if err := p.r.APIReader.Get(ctx, client.ObjectKey{Namespace: dt.Namespace,
		Name: dt.Spec.CredentialsSecretRef.Name}, &secret); err != nil {
		facts.StateFailure = "reading Secret " + dt.Spec.CredentialsSecretRef.Name + ": " + err.Error()
		return ports.Target{}, nil
	}
	target.Secret = secret.Data
	run, err := deployer.State(ctx, target, deployApp(p.container), model.DeployWanted(facts.Version, facts.Newest))
	if err != nil {
		facts.StateFailure = err.Error()
		return ports.Target{}, nil
	}
	facts.Run = &model.DeployRun{Version: run.Version, Phase: run.Phase, Health: run.Health, URL: run.URL,
		Reason: run.Reason, Message: run.Message}
	return target, deployer
}

// ship ships version of the Container to target, streaming its files from the
// Git host's release; nil when the deploy is under way, else how it failed.
func (p *deployPass) ship(ctx context.Context, target ports.Target, deployer ports.Deployer,
	version string) *model.DeployShipFailure {
	if p.provider == nil {
		provider, err := providerFor(ctx, p.r.Client, p.r.APIReader, p.r.Git, p.container.Namespace,
			p.container.Spec.GitProviderRef.Name)
		if err != nil {
			return &model.DeployShipFailure{Reason: model.DeployGitHostUnreachable, Detail: err.Error()}
		}
		p.provider, p.repo = provider, provider.Repository(p.container.Name)
	}
	release, err := p.provider.GetRelease(ctx, p.repo, version)
	if err != nil {
		return &model.DeployShipFailure{Reason: model.DeployGitHostUnreachable, Detail: err.Error()}
	}
	if release == nil {
		return &model.DeployShipFailure{Reason: model.DeployReleaseNotFound}
	}
	shipment := ports.Shipment{App: deployApp(p.container), DisplayName: displayName(p.container),
		Version: version, Notes: release.Body}
	if buildsImage(p.container) {
		if shipment.Image, err = p.shippedImage(ctx, version); err != nil {
			return &model.DeployShipFailure{Reason: model.DeployGitHostUnreachable, Detail: err.Error()}
		}
	}
	shipment.Files = releaseFiles(p.provider, p.repo, release.Assets)
	log := p.log.WithValues("target", target.Name, "version", version)
	err = deployer.Ship(logr.NewContext(ctx, log), target, shipment)
	switch {
	case err == nil:
		return nil
	case isGiteaError(err):
		return &model.DeployShipFailure{Reason: model.DeployGitHostUnreachable, Detail: err.Error()}
	case errors.Is(err, ports.ErrUnreachable):
		return &model.DeployShipFailure{Reason: model.DeployUnreachable, Detail: err.Error()}
	case errors.Is(err, ports.ErrCredentialRefused):
		return &model.DeployShipFailure{Reason: model.DeployCredentialRefused, Detail: err.Error()}
	case errors.Is(err, ports.ErrArtifactMissing):
		return &model.DeployShipFailure{Reason: model.DeployArtifactMissing, Detail: err.Error()}
	}
	return &model.DeployShipFailure{Reason: model.DeployFailed, Detail: err.Error()}
}

// deployEvent logs an entry's change and emits its Event, once: the
// Reconciler remembers what it said, as the last poll's status write can fail
// after its Event, and after a restart the status says it.
func (r *ContainerReconciler) deployEvent(ctx context.Context, container *c4v1alpha1.Container,
	prev *model.DeployReport, next model.DeployReport) {
	log := logf.FromContext(ctx).WithValues("target", next.Target)
	if prev == nil || prev.Phase != next.Phase || prev.Reason != next.Reason ||
		prev.WantedVersion != next.WantedVersion {
		log.Info("Deploy changed", "phase", next.Phase, "reason", next.Reason, "version", next.Version,
			"wantedVersion", next.WantedVersion)
	}
	reason, said := model.DeployEvent(prev, next)
	key := string(container.UID) + "/" + next.Target
	if reason == "" {
		return
	}
	if last, ok := r.deploySaid.Load(key); ok && last == said {
		return
	}
	r.deploySaid.Store(key, said)
	eventType := corev1.EventTypeNormal
	if reason == model.DeployEventFailed {
		eventType = corev1.EventTypeWarning
	}
	if r.Recorder != nil {
		r.Recorder.Eventf(container, nil, eventType, reason, "Deploy", "%s", next.Message)
	}
}

// keepNewestRelease sets status.newestRelease to the newest release of the
// Container's Crews whose runs succeeded, kept with or without spec.deploy so
// a target added later ships it. It returns how soon to look again after a
// failed read.
func (r *ContainerReconciler) keepNewestRelease(ctx context.Context, container *c4v1alpha1.Container,
	status *c4v1alpha1.ContainerStatus) time.Duration {
	newest, err := r.newestRelease(ctx, container)
	if err != nil {
		logf.FromContext(ctx).Error(err, "Failed to list the Container's Crews")
		return r.deployPoll()
	}
	if newest != "" {
		status.NewestRelease = newest
	}
	return 0
}

// newestRelease is the newest release of the Container's Crews whose runs
// succeeded, "" when none has one.
func (r *ContainerReconciler) newestRelease(ctx context.Context, container *c4v1alpha1.Container) (string, error) {
	var crews c4v1alpha1.CrewList
	if err := r.List(ctx, &crews, client.InNamespace(container.Namespace),
		client.MatchingFields{indexContainer: container.Name}); err != nil {
		return "", err
	}
	var newest *c4v1alpha1.CrewRelease
	for _, c := range crews.Items {
		rel := c.Status.LastRelease
		if rel != nil && rel.Run == model.RunSucceeded && (newest == nil || rel.Time.After(newest.Time.Time)) {
			newest = rel
		}
	}
	if newest == nil {
		return "", nil
	}
	return newest.Tag, nil
}

// deployContainer is what the Container brings that a target's member may
// need: its Deployment, and whether its Framework deploys itself.
func (r *ContainerReconciler) deployContainer(ctx context.Context,
	container *c4v1alpha1.Container) (model.DeployContainer, error) {
	brings := model.DeployContainer{Deployment: refName(container.Spec.DeploymentRef)}
	var framework c4v1alpha1.Framework
	found, err := r.lookup(ctx, container.Namespace, container.Spec.FrameworkRef.Name, &framework)
	if found {
		brings.DeploysItself = framework.Spec.DeploysItself
	}
	return brings, err
}

func isGiteaError(err error) bool {
	_, ok := errors.AsType[giteaError](err)
	return ok
}

// containerTarget is dt as container's deploys read it: the target with the
// settings that go per Container, the github member's tag prefix (the
// Container's name unless the target sets one) and whether its releases
// take Latest.
func containerTarget(dt *c4v1alpha1.DeployTarget, container *c4v1alpha1.Container) ports.Target {
	target, _ := deployTarget(dt)
	if gh := dt.Spec.GitHub; gh != nil {
		target.Settings[model.GitHubSettingTagPrefix] = model.GitHubTagPrefix(gh.TagPrefix, container.Name)
		if !model.GitHubTakesLatest(gh.Latest) {
			target.Settings[model.GitHubSettingLatest] = "false"
		}
	}
	return target
}

// deployApp names the Container's app uniquely at any target.
func deployApp(container *c4v1alpha1.Container) string {
	return container.Namespace + "/" + container.Name
}

// displayName is the Container's display name, else its name.
func displayName(container *c4v1alpha1.Container) string {
	if container.Spec.DisplayName != "" {
		return container.Spec.DisplayName
	}
	return container.Name
}

// lastReleaseChanges passes a Crew whose last release or its runs changed: a
// release whose runs succeeded ships.
func lastReleaseChanges() builder.WatchesOption {
	release := func(c *c4v1alpha1.Crew) string {
		if r := c.Status.LastRelease; r != nil {
			return r.Tag + "\n" + r.Run
		}
		return ""
	}
	return builder.WithPredicates(predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldCrew, ok1 := e.ObjectOld.(*c4v1alpha1.Crew)
			newCrew, ok2 := e.ObjectNew.(*c4v1alpha1.Crew)
			return !ok1 || !ok2 || release(oldCrew) != release(newCrew)
		},
	})
}

// deployTargetReadyChanges passes a DeployTarget made, removed, changed, or
// whose Ready condition changed.
func deployTargetReadyChanges() builder.WatchesOption {
	ready := func(dt *c4v1alpha1.DeployTarget) string {
		if cond := apimeta.FindStatusCondition(dt.Status.Conditions, c4v1alpha1.ConditionDeployTargetReady); cond != nil {
			return string(cond.Status) + "\n" + cond.Reason + "\n" + cond.Message
		}
		return ""
	}
	return builder.WithPredicates(predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldTarget, ok1 := e.ObjectOld.(*c4v1alpha1.DeployTarget)
			newTarget, ok2 := e.ObjectNew.(*c4v1alpha1.DeployTarget)
			return !ok1 || !ok2 || oldTarget.Generation != newTarget.Generation || ready(oldTarget) != ready(newTarget)
		},
	})
}
