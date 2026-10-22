package argocd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
func startAPIServer(t *testing.T) client.Client {
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
	c, err := client.New(cfg, client.Options{Scheme: clientgoscheme.Scheme})
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
// namespaces it watches: an Application whose targetRevision it hasn't
// compared to yet gets one sync, OutOfSync and Running first, Synced,
// Succeeded and Healthy on the next pass. It counts the syncs per
// Application: the contract's deploys.
type fakeArgoCD struct {
	c          client.Client
	mu         sync.Mutex
	namespaces []string
	syncs      map[string]int
}

func newFakeArgoCD(t *testing.T, c client.Client) *fakeArgoCD {
	t.Helper()
	f := &fakeArgoCD{c: c, syncs: map[string]int{}}
	// The client's first List of a kind maps it through discovery, which
	// takes about a second on a fresh envtest; warm it before the contract's
	// Ship-and-State loop, which doesn't wait between calls, needs the fake.
	var apps unstructured.UnstructuredList
	apps.SetGroupVersionKind(ApplicationGVK.GroupVersion().WithKind("ApplicationList"))
	if err := c.List(context.Background(), &apps); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.run(ctx)
	}()
	t.Cleanup(func() { cancel(); <-done })
	return f
}

func (f *fakeArgoCD) watch(namespace string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.namespaces = append(f.namespaces, namespace)
}

func (f *fakeArgoCD) deploys(namespace, name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.syncs[namespace+"/"+name]
}

func (f *fakeArgoCD) run(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		f.mu.Lock()
		namespaces := append([]string(nil), f.namespaces...)
		f.mu.Unlock()
		for _, ns := range namespaces {
			var apps unstructured.UnstructuredList
			apps.SetGroupVersionKind(ApplicationGVK.GroupVersion().WithKind("ApplicationList"))
			if err := f.c.List(ctx, &apps, client.InNamespace(ns)); err != nil {
				continue
			}
			for i := range apps.Items {
				f.sync(ctx, &apps.Items[i])
			}
		}
	}
}

// sync runs one sync of app when its targetRevision isn't the one compared
// to: OutOfSync and Running, then Synced, Succeeded and Healthy, in one
// pass, since the contract's Ship-and-State loop doesn't wait between calls.
// A conflict with a Ship in between is left for the next pass.
func (f *fakeArgoCD) sync(ctx context.Context, app *unstructured.Unstructured) {
	got := facts(app)
	if got.TargetRevision == got.SyncedRevision {
		return
	}
	f.mu.Lock()
	f.syncs[app.GetNamespace()+"/"+app.GetName()]++
	f.mu.Unlock()
	setStatus(app, status{sync: model.ArgoCDSyncOutOfSync, revision: got.TargetRevision,
		operation: model.ArgoCDOperationRunning, health: model.ArgoCDHealthProgressing})
	if err := f.c.Update(ctx, app); err != nil {
		return
	}
	setStatus(app, status{sync: model.ArgoCDSyncSynced, revision: got.TargetRevision,
		operation: model.ArgoCDOperationSucceeded, health: model.ArgoCDHealthHealthy})
	_ = f.c.Update(ctx, app)
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
	argo := newFakeArgoCD(t, c)
	namespaces := 0
	contract.Deployer(t, contract.DeployerEnv{
		Deployer: New(c),
		Target: func(t *testing.T) ports.Target {
			namespaces++
			ns := argoNamespace(t, c, namespaces)
			argo.watch(ns)
			return target(ns)
		},
		Refused: func(*testing.T) ports.Target { return target("argocd-not-there") },
		Deploys: func(_ *testing.T, tt ports.Target, app string) int {
			return argo.deploys(tt.Settings[model.ArgoCDSettingNamespace], model.ArgoCDApplicationName(app))
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

// The adapter's own cases on envtest, in a namespace no fake Argo CD
// watches, so the Application is exactly what Ship wrote: its fields, a
// repeat applying nothing, no image as ErrArtifactMissing, each faked status
// as State reports it, and Check on a project that isn't there.
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

	t.Run("Ship writes the Application", func(t *testing.T) {
		if err := d.Ship(ctx, tt, s); err != nil {
			t.Fatalf("Ship() = %v", err)
		}
		app := getApplication(t, c)
		assertApplied(t, app, s)
	})

	t.Run("a repeat applies nothing", func(t *testing.T) {
		before := getApplication(t, c)
		if err := d.Ship(ctx, tt, s); err != nil {
			t.Fatalf("Ship() again = %v", err)
		}
		after := getApplication(t, c)
		if after.GetResourceVersion() != before.GetResourceVersion() || after.GetGeneration() != before.GetGeneration() {
			t.Errorf("resourceVersion %s -> %s, generation %d -> %d; want unchanged", before.GetResourceVersion(),
				after.GetResourceVersion(), before.GetGeneration(), after.GetGeneration())
		}
	})

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
	})

	t.Run("Check on a project that isn't there is ErrCredentialRefused", func(t *testing.T) {
		err := d.Check(ctx, tt)
		if !errors.Is(err, ports.ErrCredentialRefused) || !strings.Contains(err.Error(), "project default doesn't exist in namespace argocd") {
			t.Errorf("Check() = %v, want ErrCredentialRefused: project default doesn't exist in namespace argocd", err)
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
