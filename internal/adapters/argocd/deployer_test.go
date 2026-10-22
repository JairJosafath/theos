package argocd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/ports"
	"github.com/JairJosafath/c4factory/internal/ports/contract"
)

// startAPIServer runs envtest with Argo CD's CRDs (test/crds/argocd) and
// returns a client, or skips when the envtest binaries aren't installed (run
// through make test).
func startAPIServer(t *testing.T) client.WithWatch {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; run with make test")
	}
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "test", "crds", "argocd")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env.Stop() })
	c, err := client.NewWithWatch(cfg, client.Options{Scheme: clientgoscheme.Scheme})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// argoNamespace makes a fresh Argo CD namespace holding the AppProject
// default, and returns its name.
func argoNamespace(t *testing.T, c client.Client, n int) string {
	t.Helper()
	ctx := context.Background()
	name := fmt.Sprintf("argocd-%d", n)
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}); err != nil {
		t.Fatal(err)
	}
	project := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": model.ArgoCDDefaultProject, "namespace": name},
		"spec":     map[string]any{},
	}}
	project.SetGroupVersionKind(AppProjectGVK)
	if err := c.Create(ctx, project); err != nil {
		t.Fatal(err)
	}
	return name
}

func target(namespace string) ports.Target {
	return ports.Target{Name: "contract/prod", Member: model.DeployMemberArgoCD, Tier: "production",
		Settings: map[string]string{model.ArgoCDSettingNamespace: namespace,
			model.ArgoCDSettingDestinationNamespace: "apps"}}
}

// fakeArgoCD stands in for Argo CD's application controller in the
// namespaces it watches, synchronously: it wraps the Deployer's client, and
// an Apply there syncs the Application it wrote before Ship returns, so the
// contract's Ship-and-State loop, which doesn't wait between calls, sees
// the sync on its next State (#1668). An Application whose targetRevision
// it hasn't compared to yet gets one sync: Synced, Succeeded and Healthy at
// the tag. It counts the syncs per Application: the contract's deploys.
type fakeArgoCD struct {
	c          client.WithWatch
	namespaces map[string]bool
	syncs      map[string]int
}

func newFakeArgoCD(c client.WithWatch) *fakeArgoCD {
	return &fakeArgoCD{c: c, namespaces: map[string]bool{}, syncs: map[string]int{}}
}

// client is the client the Deployer under test gets: every Apply in a
// watched namespace is followed by the fake's sync of the Application.
func (f *fakeArgoCD) client() client.Client {
	return interceptor.NewClient(f.c, interceptor.Funcs{
		Apply: func(ctx context.Context, c client.WithWatch, obj runtime.ApplyConfiguration,
			opts ...client.ApplyOption) error {
			if err := c.Apply(ctx, obj, opts...); err != nil {
				return err
			}
			if app, ok := obj.(client.Object); ok && f.namespaces[app.GetNamespace()] {
				return f.sync(ctx, client.ObjectKeyFromObject(app))
			}
			return nil
		},
	})
}

func (f *fakeArgoCD) watch(namespace string) {
	f.namespaces[namespace] = true
}

func (f *fakeArgoCD) deploys(namespace, name string) int {
	return f.syncs[namespace+"/"+name]
}

// sync runs one sync of the Application at key when its targetRevision
// isn't the one compared to.
func (f *fakeArgoCD) sync(ctx context.Context, key client.ObjectKey) error {
	var app unstructured.Unstructured
	app.SetGroupVersionKind(ApplicationGVK)
	if err := f.c.Get(ctx, key, &app); err != nil {
		return fmt.Errorf("fake Argo CD: %w", err)
	}
	got := facts(&app)
	if got.TargetRevision == got.SyncedRevision {
		return nil
	}
	f.syncs[key.String()]++
	setStatus(&app, status{sync: model.ArgoCDSyncSynced, revision: got.TargetRevision,
		operation: model.ArgoCDOperationSucceeded, health: model.ArgoCDHealthHealthy})
	if err := f.c.Update(ctx, &app); err != nil {
		return fmt.Errorf("fake Argo CD: %w", err)
	}
	return nil
}

// status is what a fake Argo CD, or a test, writes to an Application's
// status; the Application CRD has no status subresource, so a plain Update
// writes it. operationRevision is the tag the operation synced, revision
// when "".
type status struct {
	sync, revision, operation, operationMessage, operationRevision, health, healthMessage string
}

func setStatus(app *unstructured.Unstructured, s status) {
	// comparedTo and operationState carry what Argo CD compared and ran,
	// which the CRD requires: the source and destination it synced, the
	// operation and when it started, and its result's revision and source.
	source, _, _ := unstructured.NestedMap(app.Object, "spec", "source")
	destination, _, _ := unstructured.NestedMap(app.Object, "spec", "destination")
	source["targetRevision"] = s.revision
	opSource, _, _ := unstructured.NestedMap(app.Object, "spec", "source")
	opSource["targetRevision"] = s.revision
	if s.operationRevision != "" {
		opSource["targetRevision"] = s.operationRevision
	}
	app.Object["status"] = map[string]any{
		"sync": map[string]any{"status": s.sync,
			"comparedTo": map[string]any{"source": source, "destination": destination}},
		"health": map[string]any{"status": s.health, "message": s.healthMessage},
		"operationState": map[string]any{"phase": s.operation, "message": s.operationMessage,
			"operation":  map[string]any{"sync": map[string]any{"revision": opSource["targetRevision"]}},
			"syncResult": map[string]any{"revision": opSource["targetRevision"], "source": opSource},
			"startedAt":  time.Now().UTC().Format(time.RFC3339)},
	}
}

// TestDeployerContract runs the Deployer's contract on the argoCD adapter,
// against envtest with Argo CD's CRDs and a fake Argo CD that syncs what
// Ship applies.
func TestDeployerContract(t *testing.T) {
	c := startAPIServer(t)
	argo := newFakeArgoCD(c)
	namespaces := 0
	contract.Deployer(t, contract.DeployerEnv{
		Deployer: New(argo.client()),
		Target: func(t *testing.T) ports.Target {
			namespaces++
			ns := argoNamespace(t, c, namespaces)
			argo.watch(ns)
			return target(ns)
		},
		Refused: func(*testing.T) ports.Target { return target("argocd-not-there") },
		Deploys: func(t *testing.T, tt ports.Target, app string) int {
			name, err := model.ArgoCDApplicationName(app)
			if err != nil {
				t.Fatal(err)
			}
			return argo.deploys(tt.Settings[model.ArgoCDSettingNamespace], name)
		},
	})
}

func shipment(version string) ports.Shipment {
	return ports.Shipment{App: "webshop/orders-api", DisplayName: "Orders API", Version: version,
		Repository: "https://gitea.example/webshop/orders-api.git",
		Image:      "registry.example/webshop/orders-api@sha256:" + strings.Repeat("ab", 32)}
}

// getApplication gets the orders API's Application in the default Argo CD
// namespace.
func getApplication(t *testing.T, c client.Client) *unstructured.Unstructured {
	t.Helper()
	var app unstructured.Unstructured
	app.SetGroupVersionKind(ApplicationGVK)
	key := client.ObjectKey{Namespace: model.ArgoCDDefaultNamespace, Name: "webshop-orders-api"}
	if err := c.Get(context.Background(), key, &app); err != nil {
		t.Fatal(err)
	}
	return &app
}

// assertApplied checks app is what Ship writes for s at a target with the
// default project and server, running in webshop-apps: its source, image,
// destination, sync policy, finalizer, labels and field manager.
func assertApplied(t *testing.T, app *unstructured.Unstructured, s ports.Shipment) {
	t.Helper()
	str := func(path ...string) string {
		v, _, _ := unstructured.NestedString(app.Object, path...)
		return v
	}
	want := map[string]string{
		"spec.project":                      "default",
		"spec.source.repoURL":               s.Repository,
		"spec.source.targetRevision":        "v1.2.0",
		"spec.source.path":                  "deploy/k8s",
		"spec.destination.server":           "https://kubernetes.default.svc",
		"spec.destination.namespace":        "webshop-apps",
		"metadata.labels." + LabelNamespace: "webshop",
		"metadata.labels." + LabelContainer: "orders-api",
	}
	for path, w := range want {
		fields := strings.Split(path, ".")
		if after, ok := strings.CutPrefix(path, "metadata.labels."); ok {
			fields = []string{"metadata", "labels", after}
		}
		if got := str(fields...); got != w {
			t.Errorf("%s = %q, want %q", path, got, w)
		}
	}
	images, _, _ := unstructured.NestedStringSlice(app.Object, "spec", "source", "kustomize", "images")
	if len(images) != 1 || images[0] != "orders-api="+s.Image {
		t.Errorf("kustomize.images = %v, want [orders-api=%s]", images, s.Image)
	}
	prune, _, _ := unstructured.NestedBool(app.Object, "spec", "syncPolicy", "automated", "prune")
	selfHeal, _, _ := unstructured.NestedBool(app.Object, "spec", "syncPolicy", "automated", "selfHeal")
	options, _, _ := unstructured.NestedStringSlice(app.Object, "spec", "syncPolicy", "syncOptions")
	if !prune || !selfHeal || len(options) != 1 || options[0] != "CreateNamespace=true" {
		t.Errorf("syncPolicy: prune %v, selfHeal %v, options %v", prune, selfHeal, options)
	}
	if f := app.GetFinalizers(); len(f) != 1 || f[0] != Finalizer {
		t.Errorf("finalizers = %v, want [%s]", f, Finalizer)
	}
	for _, m := range app.GetManagedFields() {
		if m.Manager != FieldManager {
			t.Errorf("field manager %q, want only %s", m.Manager, FieldManager)
		}
	}
	if len(app.GetManagedFields()) == 0 {
		t.Error("no managed fields")
	}
}

// logging returns ctx with a logger that counts the adapter's "Applied"
// lines written to it at Info, V(1) and up dropped, and the count. The
// client logs the API server's warnings (Argo CD's finalizer name isn't
// domain-qualified with a path) to the same logger; those don't count.
func logging(ctx context.Context) (context.Context, *int) {
	n := new(int)
	log := funcr.New(func(_, args string) {
		if strings.Contains(args, `"msg"="Applied Argo CD Application"`) {
			*n++
		}
	}, funcr.Options{})
	return logr.NewContext(ctx, log), n
}

// The adapter's own cases on envtest, in a namespace no fake Argo CD
// watches, so the Application is exactly what Ship wrote: its fields, a
// repeat applying and logging nothing, no image or no repository as
// ErrArtifactMissing, a missing Argo CD namespace refused by Ship and State
// alike, each faked status as State reports it, and Check on a project that
// isn't there.
func TestDeployer(t *testing.T) {
	c := startAPIServer(t)
	ctx := context.Background()
	d := New(c)
	// The default Argo CD namespace, project and server: the target sets
	// only where the app runs.
	if err := c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: model.ArgoCDDefaultNamespace}}); err != nil {
		t.Fatal(err)
	}
	tt := ports.Target{Name: "webshop/prod", Member: model.DeployMemberArgoCD,
		Settings: map[string]string{model.ArgoCDSettingDestinationNamespace: "webshop-apps"}}
	s := shipment("v1.2.0")

	t.Run("Ship writes the Application and logs it", func(t *testing.T) {
		ctx, lines := logging(ctx)
		if err := d.Ship(ctx, tt, s); err != nil {
			t.Fatalf("Ship() = %v", err)
		}
		app := getApplication(t, c)
		assertApplied(t, app, s)
		if *lines != 1 {
			t.Errorf("%d Info lines, want 1: the apply wrote the Application", *lines)
		}
	})

	t.Run("a repeat applies nothing and logs nothing at Info (#1668)", func(t *testing.T) {
		before := getApplication(t, c)
		ctx, lines := logging(ctx)
		if err := d.Ship(ctx, tt, s); err != nil {
			t.Fatalf("Ship() again = %v", err)
		}
		after := getApplication(t, c)
		if after.GetResourceVersion() != before.GetResourceVersion() || after.GetGeneration() != before.GetGeneration() {
			t.Errorf("resourceVersion %s -> %s, generation %d -> %d; want unchanged", before.GetResourceVersion(),
				after.GetResourceVersion(), before.GetGeneration(), after.GetGeneration())
		}
		if *lines != 0 {
			t.Errorf("%d Info lines, want 0: the apply wrote nothing", *lines)
		}
	})

	missing(t, c, d, tt, s)
	refusals(t, c, d, tt, s)

	t.Run("State ignores the wanted version", func(t *testing.T) {
		st, err := d.State(ctx, tt, s.App, "v0.9.0")
		if err != nil || st.Version != "v1.2.0" || st.Phase != ports.PhaseDeploying || st.Health != ports.HealthUnknown {
			t.Errorf("State() before any sync = %+v, %v; want v1.2.0 Deploying, health Unknown", st, err)
		}
	})

	cases := []struct {
		name   string
		status status
		want   ports.DeployState
	}{
		{"a running sync is Deploying and Progressing",
			status{sync: model.ArgoCDSyncOutOfSync, revision: "v1.2.0", operation: model.ArgoCDOperationRunning,
				health: model.ArgoCDHealthProgressing},
			ports.DeployState{Version: "v1.2.0", Phase: ports.PhaseDeploying, Health: ports.HealthProgressing}},
		{"Synced at the tag with the operation Succeeded is Deployed",
			status{sync: model.ArgoCDSyncSynced, revision: "v1.2.0", operation: model.ArgoCDOperationSucceeded,
				health: model.ArgoCDHealthHealthy},
			ports.DeployState{Version: "v1.2.0", Phase: ports.PhaseDeployed, Health: ports.HealthHealthy}},
		{"a failed sync is Failed, reason SyncFailed, with Argo CD's message",
			status{sync: model.ArgoCDSyncOutOfSync, revision: "v1.2.0", operation: model.ArgoCDOperationFailed,
				operationMessage: "one or more objects failed to apply", health: model.ArgoCDHealthDegraded},
			ports.DeployState{Version: "v1.2.0", Phase: ports.PhaseFailed, Health: ports.HealthDegraded,
				Reason: model.DeployReasonSyncFailed, Message: "one or more objects failed to apply"}},
		{"the previous version's failed sync is Deploying at the new tag (#1667)",
			status{sync: model.ArgoCDSyncOutOfSync, revision: "v1.2.0", operation: model.ArgoCDOperationFailed,
				operationMessage: "one or more objects failed to apply", operationRevision: "v1.1.0",
				health: model.ArgoCDHealthDegraded},
			ports.DeployState{Version: "v1.2.0", Phase: ports.PhaseDeploying, Health: ports.HealthDegraded}},
		{"health Missing is Unknown with the message",
			status{sync: model.ArgoCDSyncSynced, revision: "v1.2.0", operation: model.ArgoCDOperationSucceeded,
				health: model.ArgoCDHealthMissing, healthMessage: "Deployment orders-api not found"},
			ports.DeployState{Version: "v1.2.0", Phase: ports.PhaseDeployed, Health: ports.HealthUnknown,
				Message: "Deployment orders-api not found"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := getApplication(t, c)
			setStatus(app, tc.status)
			if err := c.Update(ctx, app); err != nil {
				t.Fatal(err)
			}
			st, err := d.State(ctx, tt, s.App, "")
			if err != nil || st != tc.want {
				t.Errorf("State() = %+v, %v; want %+v", st, err, tc.want)
			}
		})
	}

	t.Run("a repeat after a status change still applies nothing", func(t *testing.T) {
		// Argo CD wrote status with its own manager; our apply owns none of
		// it and changes nothing.
		before := getApplication(t, c)
		ctx, lines := logging(ctx)
		if err := d.Ship(ctx, tt, s); err != nil {
			t.Fatalf("Ship() again = %v", err)
		}
		after := getApplication(t, c)
		if after.GetResourceVersion() != before.GetResourceVersion() {
			t.Errorf("resourceVersion %s -> %s, want unchanged", before.GetResourceVersion(), after.GetResourceVersion())
		}
		if _, found, _ := unstructured.NestedMap(after.Object, "status"); !found {
			t.Error("the apply dropped the status")
		}
		if *lines != 0 {
			t.Errorf("%d Info lines, want 0: the apply wrote nothing", *lines)
		}
	})

	t.Run("Check on a project that isn't there is ErrCredentialRefused", func(t *testing.T) {
		err := d.Check(ctx, tt)
		if !errors.Is(err, ports.ErrCredentialRefused) || !strings.Contains(err.Error(), "project default doesn't exist in namespace argocd") {
			t.Errorf("Check() = %v, want ErrCredentialRefused: project default doesn't exist in namespace argocd", err)
		}
	})
}

// missing are the adapter's answers to what isn't there, with nothing
// written: a release without an image or a repository is
// ErrArtifactMissing, and a target whose Argo CD namespace isn't there
// refuses the credential from State and Ship alike (#1668).
func missing(t *testing.T, c client.Client, d *Deployer, tt ports.Target, s ports.Shipment) {
	t.Helper()
	ctx := context.Background()
	t.Run("a release with no image is ErrArtifactMissing", func(t *testing.T) {
		noImage := shipment("v1.3.0")
		noImage.App = "webshop/catalog-api"
		noImage.Image = ""
		err := d.Ship(ctx, tt, noImage)
		if !errors.Is(err, ports.ErrArtifactMissing) || !strings.Contains(err.Error(), "the release has no image") {
			t.Errorf("Ship() = %v, want ErrArtifactMissing: the release has no image", err)
		}
		var app unstructured.Unstructured
		app.SetGroupVersionKind(ApplicationGVK)
		if err := c.Get(ctx, client.ObjectKey{Namespace: "argocd", Name: "webshop-catalog-api"}, &app); !apierrors.IsNotFound(err) {
			t.Errorf("Get() of the Application = %v, want NotFound: nothing written", err)
		}
	})

	t.Run("a release with no repository is ErrArtifactMissing (#1668)", func(t *testing.T) {
		noRepo := shipment("v1.3.0")
		noRepo.App = "webshop/catalog-api"
		noRepo.Repository = ""
		err := d.Ship(ctx, tt, noRepo)
		if !errors.Is(err, ports.ErrArtifactMissing) || !strings.Contains(err.Error(), "the release has no repository") {
			t.Errorf("Ship() = %v, want ErrArtifactMissing: the release has no repository", err)
		}
		var app unstructured.Unstructured
		app.SetGroupVersionKind(ApplicationGVK)
		if err := c.Get(ctx, client.ObjectKey{Namespace: "argocd", Name: "webshop-catalog-api"}, &app); !apierrors.IsNotFound(err) {
			t.Errorf("Get() of the Application = %v, want NotFound: nothing written", err)
		}
	})

	t.Run("a missing Argo CD namespace is ErrCredentialRefused from State and Ship alike (#1668)", func(t *testing.T) {
		gone := target("argocd-not-there")
		const want = "namespace argocd-not-there doesn't exist"
		st, err := d.State(ctx, gone, s.App, "")
		if !errors.Is(err, ports.ErrCredentialRefused) || !strings.Contains(err.Error(), want) || st != (ports.DeployState{}) {
			t.Errorf("State() = %+v, %v; want the zero state and ErrCredentialRefused: %s", st, err, want)
		}
		if err := d.Ship(ctx, gone, s); !errors.Is(err, ports.ErrCredentialRefused) || !strings.Contains(err.Error(), want) {
			t.Errorf("Ship() = %v, want ErrCredentialRefused: %s", err, want)
		}
	})
}

// refusals are the adapter's answers of #1669, on the Application Ship
// wrote for s at tt: a Container whose name joins to the same is refused by
// Ship, with nothing written, and by State, both naming s's Container; a
// name over 63 characters is refused before anything is written.
func refusals(t *testing.T, c client.Client, d *Deployer, tt ports.Target, s ports.Shipment) {
	t.Helper()
	ctx := context.Background()
	// webshop-orders/api joins to the same name as webshop/orders-api, whose
	// Application is there (#1669).
	colliding := shipment("v2.0.0")
	colliding.App = "webshop-orders/api"
	const other = "Application webshop-orders-api in namespace argocd belongs to Container webshop/orders-api"

	t.Run("Ship refuses an Application labeled for another Container (#1669)", func(t *testing.T) {
		before := getApplication(t, c)
		err := d.Ship(ctx, tt, colliding)
		if err == nil || !strings.Contains(err.Error(), other) {
			t.Errorf("Ship() = %v, want an error with %q", err, other)
		}
		if errors.Is(err, ports.ErrCredentialRefused) || errors.Is(err, ports.ErrUnreachable) ||
			errors.Is(err, ports.ErrArtifactMissing) {
			t.Errorf("Ship() = %v, want a plain error, not one of the port's", err)
		}
		after := getApplication(t, c)
		if after.GetResourceVersion() != before.GetResourceVersion() {
			t.Errorf("resourceVersion %s -> %s, want unchanged: nothing written", before.GetResourceVersion(),
				after.GetResourceVersion())
		}
		assertApplied(t, after, s)
	})

	t.Run("State refuses an Application labeled for another Container (#1669)", func(t *testing.T) {
		st, err := d.State(ctx, tt, colliding.App, "")
		if err == nil || !strings.Contains(err.Error(), other) || st != (ports.DeployState{}) {
			t.Errorf("State() = %+v, %v; want the zero state and an error with %q", st, err, other)
		}
	})

	t.Run("Ship refuses a name over 63 characters (#1669)", func(t *testing.T) {
		long := shipment("v1.0.0")
		long.App = "webshop/" + strings.Repeat("a", 56)
		err := d.Ship(ctx, tt, long)
		want := "is 64 characters, over the 63 a label value holds"
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Ship() = %v, want an error with %q", err, want)
		}
		var apps unstructured.UnstructuredList
		apps.SetGroupVersionKind(ApplicationGVK.GroupVersion().WithKind("ApplicationList"))
		if err := c.List(ctx, &apps, client.InNamespace(model.ArgoCDDefaultNamespace)); err != nil {
			t.Fatal(err)
		}
		if len(apps.Items) != 1 {
			t.Errorf("%d Applications in argocd, want 1: nothing written", len(apps.Items))
		}
	})

}

// Check's other answers, on a fake client whose Get fails as the API server
// would: a cluster without Argo CD's CRDs, a credential forbidden to read
// AppProjects, and a call that didn't reach it.
func TestCheckErrors(t *testing.T) {
	projects := schema.GroupResource{Group: "argoproj.io", Resource: "appprojects"}
	cases := []struct {
		name string
		err  error
		want error
		text string
	}{
		{"a missing CRD is Argo CD isn't installed",
			&meta.NoKindMatchError{GroupKind: AppProjectGVK.GroupKind(), SearchedVersions: []string{"v1alpha1"}},
			ports.ErrCredentialRefused, "Argo CD isn't installed (no argoproj.io/v1alpha1 in the cluster)"},
		{"Forbidden names the verb",
			apierrors.NewForbidden(projects, "default", errors.New("User \"system:serviceaccount:c4factory:operator\" cannot get resource")),
			ports.ErrCredentialRefused, "can't get appprojects in namespace argocd"},
		{"Unauthorized refuses the credential", apierrors.NewUnauthorized("bad token"),
			ports.ErrCredentialRefused, "can't get appprojects in namespace argocd"},
		{"a server error is unreachable", apierrors.NewInternalError(errors.New("etcdserver: request timed out")),
			ports.ErrUnreachable, "etcdserver: request timed out"},
		{"the network is unreachable", errors.New("dial tcp 10.0.0.1:443: i/o timeout"),
			ports.ErrUnreachable, "i/o timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
				Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
					return tc.err
				}}).Build()
			d := New(c)
			err := d.Check(context.Background(), target("argocd"))
			if !errors.Is(err, tc.want) || !strings.Contains(err.Error(), tc.text) {
				t.Errorf("Check() = %v, want %v with %q", err, tc.want, tc.text)
			}
			if _, err := d.State(context.Background(), target("argocd"), "webshop/orders-api", ""); !errors.Is(err, tc.want) {
				t.Errorf("State() = %v, want %v", err, tc.want)
			}
		})
	}
}
