// Package argocd is the Deployer of a DeployTarget's argoCD member: each
// release ships as one Argo CD Application in the target's Argo CD namespace
// that points at the Container's repository at the release tag and sets the
// image; Argo CD rolls it out and the Application's status says how far it
// got (docs/plans/k8s-deploy.md, decisions 2 to 4). Nothing is written to the
// repository, and an Application is never deleted.
package argocd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/ports"
)

// FieldManager is the field manager Ship applies the Application with.
const FieldManager = "c4factory"

// Finalizer is Argo CD's resources finalizer: a person deleting the
// Application removes the app it runs.
const Finalizer = "resources-finalizer.argocd.argoproj.io"

// Labels on the Application that find it from the Container, which the
// Application can't own: it lives in another namespace.
const (
	LabelNamespace = "c4.factory.io/namespace"
	LabelContainer = "c4.factory.io/container"
)

// Argo CD's kinds, read and written as unstructured objects: the adapter
// needs a handful of fields, not Argo CD's Go module.
var (
	ApplicationGVK = schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "Application"}
	AppProjectGVK  = schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "AppProject"}
)

// Deployer ships releases as Argo CD Applications through a Kubernetes
// client. Argo CD runs the deploy, so Ship starts it in one call.
type Deployer struct {
	c client.Client
}

var _ ports.Deployer = (*Deployer)(nil)

// New returns the argoCD member's Deployer on c, the cluster Argo CD runs
// in. Check reads an AppProject whose CRD may be missing, so c must read
// uncached, else the manager's cache would start an informer on a kind the
// cluster doesn't have.
func New(c client.Client) *Deployer {
	return &Deployer{c: c}
}

// settings are an argoCD target's fields, defaulted.
type settings struct {
	Namespace, Project, DestinationNamespace, DestinationServer string
}

func read(t ports.Target) settings {
	or := func(key, def string) string {
		if v := t.Settings[key]; v != "" {
			return v
		}
		return def
	}
	return settings{
		Namespace:            or(model.ArgoCDSettingNamespace, model.ArgoCDDefaultNamespace),
		Project:              or(model.ArgoCDSettingProject, model.ArgoCDDefaultProject),
		DestinationNamespace: t.Settings[model.ArgoCDSettingDestinationNamespace],
		DestinationServer:    or(model.ArgoCDSettingDestinationServer, model.ArgoCDDefaultServer),
	}
}

// Check gets the target's AppProject: Argo CD is installed, the project is
// there and the credential may read it.
func (d *Deployer) Check(ctx context.Context, t ports.Target) error {
	cfg := read(t)
	var project unstructured.Unstructured
	project.SetGroupVersionKind(AppProjectGVK)
	err := d.c.Get(ctx, types.NamespacedName{Namespace: cfg.Namespace, Name: cfg.Project}, &project)
	if apierrors.IsNotFound(err) {
		return refused(fmt.Errorf("project %s doesn't exist in namespace %s", cfg.Project, cfg.Namespace))
	}
	return portError(err, "get", "appprojects", cfg.Namespace)
}

// Ship applies s's Application: the repository at the release tag, the
// manifests path, the image, the destination and an automated sync policy.
// Applying the same version again changes nothing, so Argo CD starts
// nothing new. A release without an image is ErrArtifactMissing.
func (d *Deployer) Ship(ctx context.Context, t ports.Target, s ports.Shipment) error {
	if s.Image == "" {
		return &ports.Error{Kind: ports.ErrArtifactMissing, Cause: errors.New("the release has no image")}
	}
	cfg := read(t)
	app := application(cfg, s)
	if err := d.c.Apply(ctx, client.ApplyConfigurationFromUnstructured(app), client.FieldOwner(FieldManager),
		client.ForceOwnership); err != nil {
		return portError(err, "patch", "applications", cfg.Namespace)
	}
	logr.FromContextOrDiscard(ctx).Info("Applied Argo CD Application", "application", app.GetName(),
		"namespace", app.GetNamespace(), "revision", s.Version, "image", s.Image)
	return nil
}

// application is s's Application at a target with cfg, as Ship applies it.
func application(cfg settings, s ports.Shipment) *unstructured.Unstructured {
	namespace, name, _ := strings.Cut(s.App, "/")
	app := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{
			"name":       model.ArgoCDApplicationName(s.App),
			"namespace":  cfg.Namespace,
			"labels":     map[string]any{LabelNamespace: namespace, LabelContainer: name},
			"finalizers": []any{Finalizer},
		},
		"spec": map[string]any{
			"project": cfg.Project,
			"source": map[string]any{
				"repoURL":        s.Repository,
				"targetRevision": s.Version,
				"path":           model.ArgoCDManifestsPath,
				"kustomize":      map[string]any{"images": []any{name + "=" + s.Image}},
			},
			"destination": map[string]any{"server": cfg.DestinationServer, "namespace": cfg.DestinationNamespace},
			"syncPolicy": map[string]any{
				"automated":   map[string]any{"prune": true, "selfHeal": true},
				"syncOptions": []any{"CreateNamespace=true"},
			},
		},
	}}
	app.SetGroupVersionKind(ApplicationGVK)
	return app
}

// State reads app's Application back, as model.ArgoCDDeployState says. One
// version runs at a time and the Application names it, so version is
// ignored.
func (d *Deployer) State(ctx context.Context, t ports.Target, app, _ string) (ports.DeployState, error) {
	cfg := read(t)
	var obj unstructured.Unstructured
	obj.SetGroupVersionKind(ApplicationGVK)
	err := d.c.Get(ctx, types.NamespacedName{Namespace: cfg.Namespace, Name: model.ArgoCDApplicationName(app)}, &obj)
	if apierrors.IsNotFound(err) {
		return ports.DeployState{}, nil
	}
	if err != nil {
		return ports.DeployState{}, portError(err, "get", "applications", cfg.Namespace)
	}
	run := model.ArgoCDDeployState(facts(&obj))
	return ports.DeployState{Version: run.Version, Phase: run.Phase, Health: run.Health, URL: run.URL,
		Reason: run.Reason, Message: run.Message}, nil
}

// facts are what ArgoCDDeployState decides on, read from app.
func facts(app *unstructured.Unstructured) model.ArgoCDAppFacts {
	str := func(path ...string) string {
		v, _, _ := unstructured.NestedString(app.Object, path...)
		return v
	}
	return model.ArgoCDAppFacts{
		Exists:           true,
		TargetRevision:   str("spec", "source", "targetRevision"),
		SyncStatus:       str("status", "sync", "status"),
		SyncedRevision:   str("status", "sync", "comparedTo", "source", "targetRevision"),
		HealthStatus:     str("status", "health", "status"),
		HealthMessage:    str("status", "health", "message"),
		OperationPhase:   str("status", "operationState", "phase"),
		OperationMessage: str("status", "operationState", "message"),
	}
}

func refused(cause error) error {
	return &ports.Error{Kind: ports.ErrCredentialRefused, Cause: cause}
}

// portError makes a failure of verb on resource in namespace one of the
// port's errors where it is one, keeping the API server's: a cluster without
// Argo CD's kinds, a namespace that isn't there or a credential the verb is
// forbidden to refuses the credential; a request the API server rejected as
// invalid is the adapter's own bug and stays a plain error; anything else
// (the network, a timeout, the API server down) is unreachable.
func portError(err error, verb, resource, namespace string) error {
	switch {
	case err == nil:
		return nil
	case meta.IsNoMatchError(err):
		//nolint:staticcheck // ST1005: Argo CD is a name.
		return refused(fmt.Errorf("Argo CD isn't installed (no %s in the cluster): %w", ApplicationGVK.GroupVersion(), err))
	case apierrors.IsNotFound(err):
		return refused(fmt.Errorf("namespace %s doesn't exist: %w", namespace, err))
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		return refused(fmt.Errorf("can't %s %s in namespace %s: %w", verb, resource, namespace, err))
	case apierrors.IsInvalid(err), apierrors.IsBadRequest(err):
		return err
	}
	return &ports.Error{Kind: ports.ErrUnreachable, Cause: err}
}
