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
	"fmt"
	"slices"
	"strings"
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

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/oci"
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
func (r *ContainerReconciler) deploy(ctx context.Context, container *c4v1beta1.Container,
	status *c4v1beta1.ContainerStatus) time.Duration {
	log := logf.FromContext(ctx)
	newest := status.NewestRelease
	if len(container.Spec.Deploy) == 0 {
		status.Deploys = nil
		apimeta.RemoveStatusCondition(&status.Conditions, c4v1beta1.ConditionDeployed)
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
			Phase: d.Phase, Health: d.Health, URL: d.URL, Image: d.Image, Reason: d.Reason, Message: d.Message}
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
	deploys := make([]c4v1beta1.ContainerDeployStatus, 0, len(steps))
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
		deploys = append(deploys, c4v1beta1.ContainerDeployStatus{Target: rep.Target, Version: rep.Version,
			WantedVersion: rep.WantedVersion, Phase: rep.Phase, Health: rep.Health, URL: rep.URL, Image: rep.Image, Reason: rep.Reason,
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
	setCondition(&status.Conditions, container.Generation, c4v1beta1.ConditionDeployed, value, reason, message)
	return poll
}

// deployPass is one pass over a Container's deploys.
type deployPass struct {
	r         *ContainerReconciler
	container *c4v1beta1.Container
	log       logr.Logger
	// image is the newest release's, status.image.
	image *c4v1beta1.ContainerImageStatus
	// provider and repo reach the Container's repository, once a ship needs it.
	provider gitprovider.Provider
	repo     gitprovider.Repository
}

// read adds to facts what the entry's DeployTarget says and, when the entry
// can ship, what runs there. It returns the target and its Deployer when it
// read what runs there.
func (p *deployPass) read(ctx context.Context, facts *model.DeployFacts,
	brings model.DeployContainer) (ports.Target, ports.Deployer) {
	var dt c4v1beta1.DeployTarget
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
	if cond := apimeta.FindStatusCondition(dt.Status.Conditions, c4v1beta1.ConditionDeployTargetReady); cond != nil {
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
	if ref := dt.Spec.CredentialsSecretRef; ref != nil {
		var secret corev1.Secret
		if err := p.r.APIReader.Get(ctx, client.ObjectKey{Namespace: dt.Namespace, Name: ref.Name},
			&secret); err != nil {
			facts.StateFailure = "reading Secret " + ref.Name + ": " + err.Error()
			return ports.Target{}, nil
		}
		target.Secret = secret.Data
	}
	run, err := deployer.State(ctx, target, deployApp(p.container), model.DeployWanted(facts.Version, facts.Newest))
	if err != nil {
		// A name the tool can't take is a mistake to report, not a read to
		// try again (#1677).
		if _, ok := errors.AsType[*model.DeployNameError](err); ok {
			facts.Rejected = err.Error()
		} else {
			facts.StateFailure = err.Error()
		}
		return ports.Target{}, nil
	}
	facts.Run = &model.DeployRun{Version: run.Version, Phase: run.Phase, Health: run.Health, URL: run.URL,
		Image: run.Image, Reason: run.Reason, Message: run.Message}
	return target, deployer
}

// ship ships version of the Container to target, streaming its files from the
// Git host's release; nil when the deploy is under way, else how it failed.
func (p *deployPass) ship(ctx context.Context, target ports.Target, deployer ports.Deployer,
	version string) *model.DeployShipFailure {
	sourcePath := target.Settings[model.GitHubSettingSourcePath]
	if sourcePath != "" {
		if fail := p.folderOverlaps(ctx, target, sourcePath); fail != nil {
			return fail
		}
	}
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
		Type: string(p.container.Spec.Type), Deployment: p.container.Choices().Deployment,
		Framework: p.container.Choices().Framework, ScaffoldedFrom: p.container.Status.ScaffoldedFrom,
		Version: version, Notes: release.Body, Repository: p.repo.CloneURL}
	if sourcePath != "" {
		if shipment.Source, err = p.source(ctx, target, version); err != nil {
			return &model.DeployShipFailure{Reason: model.DeployGitHostUnreachable, Detail: err.Error()}
		}
		if problems := shipment.Source.Problems; len(problems) > 0 {
			// Nothing of the version is written (#1602).
			return &model.DeployShipFailure{Reason: problems[0].Reason,
				Detail: model.PublishProblemsMessage(problems, shipment.Source.Allowed)}
		}
		shipment.ReadSource = releaseSource(p.provider, p.repo, version)
	}
	if buildsImage(p.container) {
		if shipment.Image, err = p.shippedImage(ctx, version); err != nil {
			return &model.DeployShipFailure{Reason: model.DeployGitHostUnreachable, Detail: err.Error()}
		}
		if shipment.Image != "" {
			shipment.CopyImage = p.copyImage(shipment.Image)
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
	case errors.Is(err, ports.ErrPrerequisiteMissing):
		return &model.DeployShipFailure{Reason: model.DeployPrerequisiteMissing, Detail: err.Error()}
	}
	return &model.DeployShipFailure{Reason: model.DeployFailed, Detail: err.Error()}
}

// source is the release's source at version, read from the Git host once
// per release and checked, target's own Secret among the credentials it must
// not hold.
func (p *deployPass) source(ctx context.Context, target ports.Target, version string) (*model.ReleaseSource, error) {
	// Each target's own Secret is among the values checked (#1602).
	cache := string(p.container.UID) + "/" + target.Name
	if last, ok := p.r.deploySources.Load(cache); ok && last.(sourceRead).tag == version {
		return last.(sourceRead).source, nil
	}
	var own []string
	for _, v := range target.Secret {
		own = append(own, string(v))
	}
	known, err := knownCredentials(ctx, p.r.Client, p.r.APIReader, p.container, own...)
	if err != nil {
		return nil, err
	}
	source, err := readSource(ctx, p.r.Client, p.container, p.provider, p.repo, version, known)
	if err != nil {
		return nil, err
	}
	p.r.deploySources.Store(cache, sourceRead{tag: version, source: source})
	return source, nil
}

// folderOverlaps fails the ship when path, the folder target publishes the
// Container's source to, overlaps the folder of a Container whose name sorts
// before it and that ships to the same target: the first keeps the folder.
func (p *deployPass) folderOverlaps(ctx context.Context, target ports.Target,
	path string) *model.DeployShipFailure {
	var dt c4v1beta1.DeployTarget
	_, name, _ := strings.Cut(target.Name, "/")
	if found, err := p.r.lookup(ctx, p.container.Namespace, name, &dt); err != nil || !found {
		return nil
	}
	var containers c4v1beta1.ContainerList
	if err := p.r.List(ctx, &containers, client.InNamespace(p.container.Namespace)); err != nil {
		return &model.DeployShipFailure{Reason: model.DeployFolderOverlaps,
			Detail: "listing the Containers that ship there: " + err.Error()}
	}
	repository := target.Settings[model.GitHubSettingRepository]
	self := model.SourcePlace{Owner: p.container.Name, Repository: repository, Path: path}
	var earlier []model.SourcePlace
	folders, prefixes := map[string]string{}, map[string]string{}
	for i := range containers.Items {
		c := &containers.Items[i]
		if c.Name >= p.container.Name || !c.DeletionTimestamp.IsZero() ||
			!slices.ContainsFunc(c.Spec.Deploy, func(d c4v1beta1.ContainerDeploy) bool { return d.Target == dt.Name }) {
			continue
		}
		settings := containerTarget(&dt, c).Settings
		folder := settings[model.GitHubSettingSourcePath]
		folders[c.Name], prefixes[c.Name] = folder, sourceTagPrefix(settings, folder)
		earlier = append(earlier, model.SourcePlace{Owner: c.Name, Repository: repository, Path: folder})
	}
	if overlaps := model.SourceOverlaps(self, earlier); len(overlaps) > 0 {
		return &model.DeployShipFailure{Reason: model.DeployFolderOverlaps, Detail: fmt.Sprintf(
			"its folder %q for Container %s overlaps the folder %q of Container %s, which ships there first",
			path, p.container.Name, folders[overlaps[0]], overlaps[0])}
	}
	// A tagPrefix the target sets goes to every Container on it: two folders
	// under one source tag would take each other's tags.
	prefix := sourceTagPrefix(target.Settings, path)
	for _, o := range earlier {
		if prefixes[o.Owner] == prefix {
			return &model.DeployShipFailure{Reason: model.DeployFolderOverlaps, Detail: fmt.Sprintf(
				"its source tag prefix %q for Container %s is that of Container %s, which ships there first",
				prefix, p.container.Name, o.Owner)}
		}
	}
	return nil
}

// sourceTagPrefix is what goes before the version in the source tag of the
// folder path, from a target's per-Container settings.
func sourceTagPrefix(settings map[string]string, path string) string {
	var prefix *string
	if v, set := settings[model.GitHubSettingSourceTagPrefix]; set {
		prefix = &v
	}
	return model.SourceTagPrefix(path, prefix)
}

// copyImage is a Shipment's CopyImage for image, host/repository@digest in
// the owner's registry: the Git provider copies it with its own credential,
// and a failure to read it there is the Git host's (#1590).
func (p *deployPass) copyImage(image string) func(context.Context, ports.Registry, string, string) error {
	return func(ctx context.Context, to ports.Registry, repository, tag string) error {
		_, digest, _ := strings.Cut(image, "@")
		err := p.provider.CopyImage(ctx, p.repo, digest,
			oci.Registry{Host: to.Host, Username: to.Username, Password: to.Password, Insecure: to.Insecure},
			repository, tag)
		if errors.Is(err, oci.ErrSource) {
			return giteaError{err}
		}
		return err
	}
}

// deployEvent logs an entry's change and emits its Event, once: the
// Reconciler remembers what it said, as the last poll's status write can fail
// after its Event, and after a restart the status says it.
func (r *ContainerReconciler) deployEvent(ctx context.Context, container *c4v1beta1.Container,
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
func (r *ContainerReconciler) keepNewestRelease(ctx context.Context, container *c4v1beta1.Container,
	status *c4v1beta1.ContainerStatus) time.Duration {
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
func (r *ContainerReconciler) newestRelease(ctx context.Context, container *c4v1beta1.Container) (string, error) {
	var crews c4v1beta1.CrewList
	if err := r.List(ctx, &crews, client.InNamespace(container.Namespace),
		client.MatchingFields{indexContainer: container.Name}); err != nil {
		return "", err
	}
	var newest *c4v1beta1.CrewRelease
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
	container *c4v1beta1.Container) (model.DeployContainer, error) {
	brings := model.DeployContainer{Deployment: container.Choices().Deployment}
	var framework c4v1beta1.Framework
	found, err := r.lookup(ctx, container.Namespace, container.Choices().Framework, &framework)
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
// Container's name unless the target sets one), whether its releases take
// Latest, and with source set the folder of its source (the Container's name
// unless the target sets one) and its source tag prefix when the target sets
// one.
func containerTarget(dt *c4v1beta1.DeployTarget, container *c4v1beta1.Container) ports.Target {
	target, _ := deployTarget(dt)
	if gh := dt.Spec.GitHub; gh != nil {
		target.Settings[model.GitHubSettingTagPrefix] = model.ReleaseTagPrefix(gh.TagPrefix, container.Name)
		if !model.GitHubTakesLatest(gh.Latest) {
			target.Settings[model.GitHubSettingLatest] = "false"
		}
		if src := gh.Source; src != nil {
			target.Settings[model.GitHubSettingSourcePath] = container.Name
			if src.Path != nil {
				target.Settings[model.GitHubSettingSourcePath] = *src.Path
			}
			if src.TagPrefix != nil {
				target.Settings[model.GitHubSettingSourceTagPrefix] = *src.TagPrefix
			}
		}
	}
	// A gitlab target's source is step 3 of docs/plans/gitlab-deploy.md
	// (#1805): until it lands the source setting stays unset, and a release
	// to a gitlab target with source set ships without its source.
	if gl := dt.Spec.GitLab; gl != nil {
		target.Settings[model.GitLabSettingTagPrefix] = model.ReleaseTagPrefix(gl.TagPrefix, container.Name)
	}
	return target
}

// deployApp names the Container's app uniquely at any target.
func deployApp(container *c4v1beta1.Container) string {
	return container.Namespace + "/" + container.Name
}

// displayName is the Container's display name, else its name.
func displayName(container *c4v1beta1.Container) string {
	if container.Spec.DisplayName != "" {
		return container.Spec.DisplayName
	}
	return container.Name
}

// lastReleaseChanges passes a Crew whose last release or its runs changed: a
// release whose runs succeeded ships.
func lastReleaseChanges() builder.WatchesOption {
	release := func(c *c4v1beta1.Crew) string {
		if r := c.Status.LastRelease; r != nil {
			return r.Tag + "\n" + r.Run
		}
		return ""
	}
	return builder.WithPredicates(predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldCrew, ok1 := e.ObjectOld.(*c4v1beta1.Crew)
			newCrew, ok2 := e.ObjectNew.(*c4v1beta1.Crew)
			return !ok1 || !ok2 || release(oldCrew) != release(newCrew)
		},
	})
}

// deployTargetReadyChanges passes a DeployTarget made, removed, changed, or
// whose Ready condition changed.
func deployTargetReadyChanges() builder.WatchesOption {
	ready := func(dt *c4v1beta1.DeployTarget) string {
		if cond := apimeta.FindStatusCondition(dt.Status.Conditions, c4v1beta1.ConditionDeployTargetReady); cond != nil {
			return string(cond.Status) + "\n" + cond.Reason + "\n" + cond.Message
		}
		return ""
	}
	return builder.WithPredicates(predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldTarget, ok1 := e.ObjectOld.(*c4v1beta1.DeployTarget)
			newTarget, ok2 := e.ObjectNew.(*c4v1beta1.DeployTarget)
			return !ok1 || !ok2 || oldTarget.Generation != newTarget.Generation || ready(oldTarget) != ready(newTarget)
		},
	})
}
