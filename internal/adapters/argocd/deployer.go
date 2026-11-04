// Package argocd is the Deployer of a DeployTarget's argoCD member: each
// release ships as one Argo CD Application in the target's Argo CD namespace
// that points at the Container's repository at the release tag and sets the
// image; Argo CD rolls it out and the Application's status says how far it
// got (docs/plans/k8s-deploy.md, decisions 2 to 4). Nothing is written to the
// repository, and an Application is never deleted.
package argocd

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/oci"
	"github.com/JairJosafath/theos/internal/ports"
)

// FieldManager is the field manager Ship applies the Application with.
const FieldManager = "theos"

// Finalizer is Argo CD's resources finalizer: a person deleting the
// Application removes the app it runs.
const Finalizer = "resources-finalizer.argocd.argoproj.io"

// Labels on the Application that find it from the Container, which the
// Application can't own: it lives in another namespace.
const (
	LabelNamespace = "theos.factory.io/namespace"
	LabelContainer = "theos.factory.io/container"
)

// Argo CD's kinds, read and written as unstructured objects: the adapter
// needs a handful of fields, not Argo CD's Go module.
var (
	ApplicationGVK = schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "Application"}
	AppProjectGVK  = schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "AppProject"}
)

// Deployer ships releases as Argo CD Applications through a Kubernetes
// client: the operator's, or one built from the kubeconfig in the target's
// Secret. Argo CD runs the deploy, so Ship starts it in one call.
type Deployer struct {
	c client.Client
	// remotes are the clients built from kubeconfigs, by the sha256 of
	// the kubeconfig's bytes.
	remotes sync.Map
	// newClient builds a client from a kubeconfig's config; a test wraps it.
	newClient func(*rest.Config) (client.Client, error)
}

var _ ports.Deployer = (*Deployer)(nil)

// New returns the argoCD member's Deployer on c, the cluster Argo CD runs
// in. Check reads an AppProject whose CRD may be missing, so c must read
// uncached, else the manager's cache would start an informer on a kind the
// cluster doesn't have.
func New(c client.Client) *Deployer {
	return &Deployer{c: c, newClient: func(cfg *rest.Config) (client.Client, error) {
		return client.New(cfg, client.Options{Scheme: clientgoscheme.Scheme})
	}}
}

// client is the client to t's cluster: one built from the kubeconfig in
// t's Secret, or the operator's when there is none. A kubeconfig that
// builds no client refuses the credential.
func (d *Deployer) client(t ports.Target) (client.Client, error) {
	kubeconfig := t.Secret[model.ArgoCDSecretKubeconfig]
	if len(kubeconfig) == 0 {
		return d.c, nil
	}
	key := sha256.Sum256(kubeconfig)
	if c, ok := d.remotes.Load(key); ok {
		return c.(client.Client), nil
	}
	cfg, err := restConfig(kubeconfig)
	if err != nil {
		return nil, refused(fmt.Errorf("the Secret's %s: %w", model.ArgoCDSecretKubeconfig, err))
	}
	c, err := d.newClient(cfg)
	if err != nil {
		return nil, refused(fmt.Errorf("the Secret's %s: %w", model.ArgoCDSecretKubeconfig, err))
	}
	actual, _ := d.remotes.LoadOrStore(key, c)
	return actual.(client.Client), nil
}

// restConfig builds the client config from kubeconfig, which must carry
// everything inline: an exec or auth-provider plugin would run in the
// operator, and a file path (tokenFile, a certificate or key file) would
// read the operator's own files, its ServiceAccount token among them, and
// send them to whatever server the kubeconfig names (#1590).
func restConfig(kubeconfig []byte) (*rest.Config, error) {
	cfg, err := clientcmd.Load(kubeconfig)
	if err != nil {
		return nil, err
	}
	for name, a := range cfg.AuthInfos {
		switch {
		case a.Exec != nil:
			return nil, fmt.Errorf("user %q runs a command (exec); give a token or client certificate inline", name)
		case a.AuthProvider != nil:
			return nil, fmt.Errorf("user %q uses an auth-provider; give a token or client certificate inline", name)
		case a.TokenFile != "" || a.ClientCertificate != "" || a.ClientKey != "":
			return nil, fmt.Errorf("user %q reads a file; give its token, client-certificate-data and "+
				"client-key-data inline", name)
		}
	}
	for name, c := range cfg.Clusters {
		if c.CertificateAuthority != "" {
			return nil, fmt.Errorf("cluster %q reads a file; give certificate-authority-data inline", name)
		}
	}
	return clientcmd.NewDefaultClientConfig(*cfg, &clientcmd.ConfigOverrides{}).ClientConfig()
}

// settings are an argoCD target's fields, defaulted.
type settings struct {
	Namespace, Project, DestinationNamespace, DestinationServer string
	ImageRegistry, ImageRepositoryLayout, GitURL                string
	ImageRegistryInsecure                                       bool
}

func read(t ports.Target) settings {
	or := func(key, def string) string {
		if v := t.Settings[key]; v != "" {
			return v
		}
		return def
	}
	return settings{
		Namespace:             or(model.ArgoCDSettingNamespace, model.ArgoCDDefaultNamespace),
		Project:               or(model.ArgoCDSettingProject, model.ArgoCDDefaultProject),
		DestinationNamespace:  t.Settings[model.ArgoCDSettingDestinationNamespace],
		DestinationServer:     or(model.ArgoCDSettingDestinationServer, model.ArgoCDDefaultServer),
		ImageRegistry:         t.Settings[model.ArgoCDSettingImageRegistry],
		ImageRepositoryLayout: t.Settings[model.ArgoCDSettingImageRepositoryLayout],
		ImageRegistryInsecure: t.Settings[model.ArgoCDSettingImageRegistryInsecure] == "true",
		GitURL:                t.Settings[model.ArgoCDSettingGitURL],
	}
}

// Check gets the target's AppProject: Argo CD is installed, the project is
// there and the credential may read it.
func (d *Deployer) Check(ctx context.Context, t ports.Target) error {
	cfg := read(t)
	c, err := d.client(t)
	if err != nil {
		return err
	}
	var project unstructured.Unstructured
	project.SetGroupVersionKind(AppProjectGVK)
	err = c.Get(ctx, types.NamespacedName{Namespace: cfg.Namespace, Name: cfg.Project}, &project)
	if apierrors.IsNotFound(err) {
		return refused(fmt.Errorf("project %s doesn't exist in namespace %s", cfg.Project, cfg.Namespace))
	}
	return portError(err, "get", "appprojects", cfg.Namespace)
}

// Ship applies s's Application: the repository at the release tag, the
// manifests path, the image, the destination and an automated sync policy.
// Applying the same version again changes nothing, so Argo CD starts
// nothing new, and only an apply that changed the Application logs at Info;
// a repeat logs at V(1) (#1668). A release without an image, or without a
// repository (an Application with no repoURL can never sync), is
// ErrArtifactMissing. A name over what a label value holds, or an
// Application there whose labels name another Container (two names can join
// to the same, #1669), is a model.DeployNameError naming it: the deploy
// fails NameRejected, and nothing is written. At a target with an
// imageRegistry, the image is copied there first, tagged the version, and
// the Application names the copy; with a gitURL, its repoURL is the
// repository at that host (#1590).
func (d *Deployer) Ship(ctx context.Context, t ports.Target, s ports.Shipment) error {
	if s.Image == "" {
		return &ports.Error{Kind: ports.ErrArtifactMissing, Cause: errors.New("the release has no image")}
	}
	if s.Repository == "" {
		return &ports.Error{Kind: ports.ErrArtifactMissing, Cause: errors.New("the release has no repository")}
	}
	cfg := read(t)
	name, err := model.ArgoCDApplicationName(s.App)
	if err != nil {
		return err
	}
	if s.Repository, err = model.ArgoCDRepoURL(s.Repository, cfg.GitURL); err != nil {
		return err
	}
	c, err := d.client(t)
	if err != nil {
		return err
	}
	existing, err := d.get(ctx, c, cfg, name, s.App)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if cfg.ImageRegistry != "" {
		if s.Image, err = copyImage(ctx, t, cfg, s); err != nil {
			return err
		}
	}
	app := application(cfg, name, s)
	if err := c.Apply(ctx, client.ApplyConfigurationFromUnstructured(app), client.FieldOwner(FieldManager),
		client.ForceOwnership); err != nil {
		return portError(err, "patch", "applications", cfg.Namespace)
	}
	// The apply reads the server's Application back into app: the same
	// resourceVersion as before means it wrote nothing.
	log := logr.FromContextOrDiscard(ctx)
	if existing != nil && existing.GetResourceVersion() == app.GetResourceVersion() {
		log = log.V(1)
	}
	log.Info("Applied Argo CD Application", "application", app.GetName(),
		"namespace", app.GetNamespace(), "revision", s.Version, "image", s.Image)
	return nil
}

// copyImage copies s's image to cfg's imageRegistry, laid out as cfg says
// and over plain HTTP when cfg says so, tagged s's version, with t's
// registry login, and returns the copy's reference. No CopyImage is
// ErrArtifactMissing; the registry refusing the login refuses the
// credential; the tag naming another image already fails the deploy for
// good, as no retry would move it (#1770); any other failure to write the
// copy is unreachable. A failure to read the image keeps the caller's error.
func copyImage(ctx context.Context, t ports.Target, cfg settings, s ports.Shipment) (string, error) {
	cp, err := model.ArgoCDImageCopyOf(s.Image, cfg.ImageRegistry, cfg.ImageRepositoryLayout)
	if err != nil {
		return "", err
	}
	if s.CopyImage == nil {
		return "", &ports.Error{Kind: ports.ErrArtifactMissing, Cause: errors.New("the release's image can't be copied")}
	}
	to := ports.ImageRegistry{Host: cp.ToHost, Username: string(t.Secret[model.ArgoCDSecretRegistryUsername]),
		Password: string(t.Secret[model.ArgoCDSecretRegistryPassword]), Insecure: cfg.ImageRegistryInsecure}
	err = s.CopyImage(ctx, to, cp.ToRepository, s.Version)
	switch {
	case err == nil:
		logr.FromContextOrDiscard(ctx).V(1).Info("Copied the image", "image", s.Image, "copy", cp.Image)
		return cp.Image, nil
	case errors.Is(err, oci.ErrSource):
		return "", err
	case errors.Is(err, oci.ErrTagTaken):
		return "", fmt.Errorf("copy the image to %s: %w", cfg.ImageRegistry, err)
	case oci.Refused(err):
		return "", refused(fmt.Errorf("copy the image to %s: %w", cfg.ImageRegistry, err))
	}
	return "", &ports.Error{Kind: ports.ErrUnreachable, Cause: fmt.Errorf("copy the image to %s: %w",
		cfg.ImageRegistry, err)}
}

// get reads the Application name in cfg's namespace for app with c: the
// port's error where the Get fails with one, NotFound as the API server's
// own, and a model.DeployNameError naming the other Container when the
// Application's labels name one (#1669).
func (d *Deployer) get(ctx context.Context, c client.Client, cfg settings, name, app string) (
	*unstructured.Unstructured, error) {
	var obj unstructured.Unstructured
	obj.SetGroupVersionKind(ApplicationGVK)
	err := c.Get(ctx, types.NamespacedName{Namespace: cfg.Namespace, Name: name}, &obj)
	if apierrors.IsNotFound(err) {
		return nil, err
	}
	if err != nil {
		return nil, portError(err, "get", "applications", cfg.Namespace)
	}
	labels := obj.GetLabels()
	if err := model.ArgoCDApplicationConflict(app, labels[LabelNamespace], labels[LabelContainer]); err != nil {
		//nolint:staticcheck // ST1005: Application is Argo CD's kind.
		return nil, fmt.Errorf("Application %s in namespace %s %w", name, cfg.Namespace, err)
	}
	return &obj, nil
}

// application is s's Application, named name, at a target with cfg, as Ship
// applies it.
func application(cfg settings, name string, s ports.Shipment) *unstructured.Unstructured {
	namespace, container, _ := strings.Cut(s.App, "/")
	app := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{
			"name":       name,
			"namespace":  cfg.Namespace,
			"labels":     map[string]any{LabelNamespace: namespace, LabelContainer: container},
			"finalizers": []any{Finalizer},
		},
		"spec": map[string]any{
			"project": cfg.Project,
			"source": map[string]any{
				"repoURL":        s.Repository,
				"targetRevision": s.Version,
				"path":           model.ArgoCDManifestsPath,
				"kustomize":      map[string]any{"images": []any{container + "=" + s.Image}},
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
// ignored. No Application is the zero state, unless its namespace isn't
// there either: a Get in a missing namespace is the same NotFound as a
// missing Application, so State gets the Namespace to tell them apart and
// refuses the credential as Ship would (#1668). An Application whose labels
// name another Container is that Container's deploy, not app's: a
// model.DeployNameError naming it, which the operator reports NameRejected,
// Failed, not a wait (#1669, #1677).
func (d *Deployer) State(ctx context.Context, t ports.Target, app, _ string) (ports.DeployState, error) {
	cfg := read(t)
	name, err := model.ArgoCDApplicationName(app)
	if err != nil {
		return ports.DeployState{}, err
	}
	c, err := d.client(t)
	if err != nil {
		return ports.DeployState{}, err
	}
	obj, err := d.get(ctx, c, cfg, name, app)
	if apierrors.IsNotFound(err) {
		var ns corev1.Namespace
		err := c.Get(ctx, types.NamespacedName{Name: cfg.Namespace}, &ns)
		return ports.DeployState{}, portError(err, "get", "namespaces", cfg.Namespace)
	}
	if err != nil {
		return ports.DeployState{}, err
	}
	run := model.ArgoCDDeployState(facts(obj))
	return ports.DeployState{Version: run.Version, Phase: run.Phase, Health: run.Health, URL: run.URL,
		Reason: run.Reason, Message: run.Message}, nil
}

// facts are what ArgoCDDeployState decides on, read from app.
func facts(app *unstructured.Unstructured) model.ArgoCDAppFacts {
	str := func(path ...string) string {
		v, _, _ := unstructured.NestedString(app.Object, path...)
		return v
	}
	// The operation's revision is the source its result records; before the
	// result exists, the source the operation was started with (#1667).
	opRevision := str("status", "operationState", "syncResult", "source", "targetRevision")
	if opRevision == "" {
		opRevision = str("status", "operationState", "operation", "sync", "source", "targetRevision")
	}
	return model.ArgoCDAppFacts{
		Exists:            true,
		TargetRevision:    str("spec", "source", "targetRevision"),
		SyncStatus:        str("status", "sync", "status"),
		SyncedRevision:    str("status", "sync", "comparedTo", "source", "targetRevision"),
		HealthStatus:      str("status", "health", "status"),
		HealthMessage:     str("status", "health", "message"),
		OperationPhase:    str("status", "operationState", "phase"),
		OperationMessage:  str("status", "operationState", "message"),
		OperationRevision: opRevision,
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
