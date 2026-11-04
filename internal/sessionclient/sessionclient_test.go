package sessionclient

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

// startAPIServer runs envtest with the CRDs and returns a client, or skips when
// the envtest binaries aren't installed (run through make test).
func startAPIServer(t *testing.T) client.Client {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; run with make test")
	}
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env.Stop() })

	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = c4v1beta1.AddToScheme(scheme)
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Create(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "webshop"}}); err != nil {
		t.Fatal(err)
	}
	return c
}

// testLogger sends the client's log to the test's, so a failure shows what it did.
func testLogger(t *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(testWriter{t}, nil))
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}

func sessions(t *testing.T, c client.Client) []c4v1beta1.Session {
	t.Helper()
	var list c4v1beta1.SessionList
	if err := c.List(context.Background(), &list, client.InNamespace("webshop")); err != nil {
		t.Fatal(err)
	}
	return list.Items
}

// waitFor polls cond for up to 20 seconds; CI runners can be slow.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestRunRegistersHeartbeatsAndDeletes(t *testing.T) {
	c := startAPIServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, c, Options{
			Namespace: "webshop", Container: "orders-api", Actor: c4v1beta1.ActorAgent,
			Summary: "Adding order validation", Heartbeat: 200 * time.Millisecond,
			Log: testLogger(t),
		})
	}()

	var first c4v1beta1.Session
	waitFor(t, "an Active Session", func() bool {
		items := sessions(t, c)
		if len(items) == 1 && items[0].Status.Phase == c4v1beta1.SessionActive {
			first = items[0]
			return true
		}
		return false
	})
	if first.Spec.ContainerRef.Name != "orders-api" || first.Spec.Actor != c4v1beta1.ActorAgent ||
		first.Spec.Summary != "Adding order validation" {
		t.Errorf("spec = %+v", first.Spec)
	}

	// Heartbeats have second precision, so wait for the next second to show up.
	waitFor(t, "a later heartbeat", func() bool {
		items := sessions(t, c)
		return len(items) == 1 && items[0].Status.LastHeartbeat.After(first.Status.LastHeartbeat.Time)
	})

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if left := sessions(t, c); len(left) != 0 {
		t.Errorf("Sessions left after stopping: %d", len(left))
	}
}

func TestRunRegistersAgainWhenDeleted(t *testing.T) {
	c := startAPIServer(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := Run(ctx, c, Options{Namespace: "webshop", Container: "orders-api",
			Heartbeat: 100 * time.Millisecond, Log: testLogger(t)}); err != nil {
			t.Errorf("Run() = %v", err)
		}
	}()
	// Stop the client before the test ends: it logs through t.
	defer func() { cancel(); <-done }()

	var name string
	waitFor(t, "a Session", func() bool {
		items := sessions(t, c)
		if len(items) == 1 {
			name = items[0].Name
			return true
		}
		return false
	})
	if err := c.Delete(ctx, &c4v1beta1.Session{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "webshop"}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a new Session", func() bool {
		items := sessions(t, c)
		return len(items) == 1 && items[0].Name != name && items[0].Spec.Actor == c4v1beta1.ActorHuman
	})
}

func TestFindIdentity(t *testing.T) {
	root := t.TempDir()
	identity := filepath.Join(root, ".theos.yaml")
	if err := os.WriteFile(identity, []byte("project: webshop\ncontainer: orders-api\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "app", "sub")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	found, err := FindIdentity(nested)
	if err != nil || found != identity {
		t.Fatalf("FindIdentity() = %q, %v; want %q", found, err, identity)
	}
	var opts Options
	if err := opts.FromIdentity(found); err != nil {
		t.Fatal(err)
	}
	if opts.Namespace != "webshop" || opts.Container != "orders-api" {
		t.Errorf("options = %+v", opts)
	}
	if _, err := FindIdentity(t.TempDir()); err == nil {
		t.Error("FindIdentity found an identity where there is none")
	}
}
