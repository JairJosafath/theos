package argocd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/oci"
	"github.com/JairJosafath/c4factory/internal/ports"
	"github.com/JairJosafath/c4factory/internal/ports/contract"
)

// startRemote runs a second envtest with Argo CD's CRDs, the cluster an
// argoCD target names in its Secret's kubeconfig, and returns a client to it
// and the kubeconfig.
func startRemote(t *testing.T) (client.WithWatch, []byte) {
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
	user, err := env.AddUser(envtest.User{Name: "c4factory", Groups: []string{"system:masters"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	kubeconfig, err := user.KubeConfig()
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.NewWithWatch(cfg, client.Options{Scheme: clientgoscheme.Scheme})
	if err != nil {
		t.Fatal(err)
	}
	return c, kubeconfig
}

// copies records the image copies a Shipment's CopyImage was asked for, as
// host/repository:tag, and fails them with err when it is set.
type copies struct {
	mu   sync.Mutex
	refs []string
	err  error
}

func (c *copies) copy(_ context.Context, to ports.Registry, repository, tag string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	c.refs = append(c.refs, to.Host+"/"+repository+":"+tag)
	return nil
}

// copying is a Deployer whose Shipments copy their image with copies, as
// the operator's do with the Git provider.
type copying struct {
	ports.Deployer
	copies *copies
}

func (d copying) Ship(ctx context.Context, t ports.Target, s ports.Shipment) error {
	s.CopyImage = d.copies.copy
	return d.Deployer.Ship(ctx, t, s)
}

// The registry and Git host as the remote cluster sees them.
const (
	remoteRegistry = "registry.remote:5000/mirror"
	remoteGitURL   = "http://gitea.remote:3000"
)

func remoteTarget(namespace string, kubeconfig []byte) ports.Target {
	t := target(namespace)
	t.Settings[model.ArgoCDSettingImageRegistry] = remoteRegistry
	t.Settings[model.ArgoCDSettingGitURL] = remoteGitURL
	t.Secret = map[string][]byte{model.ArgoCDSecretKubeconfig: kubeconfig,
		model.ArgoCDSecretRegistryUsername: []byte("pusher"), model.ArgoCDSecretRegistryPassword: []byte("pw")}
	return t
}

// TestDeployerContractOnAnotherCluster runs the Deployer's contract on an
// argoCD target in a second envtest, the remote, that the target's Secret
// names in its kubeconfig: the operator's own cluster holds no Argo CD, so
// Check passes only on the remote. Each Application is on the remote with
// the copy of the image at the target's imageRegistry and the repository at
// its gitURL, and nothing is on the operator's cluster (#1590).
func TestDeployerContractOnAnotherCluster(t *testing.T) {
	local := startAPIServer(t)
	remoteC, kubeconfig := startRemote(t)
	argo := newFakeArgoCD(remoteC)
	d := New(local)
	built := 0
	d.newClient = func(cfg *rest.Config) (client.Client, error) {
		built++
		c, err := client.NewWithWatch(cfg, client.Options{Scheme: clientgoscheme.Scheme})
		if err != nil {
			return nil, err
		}
		return argo.intercept(c), nil
	}
	cp := &copies{}
	var targets []ports.Target
	namespaces := 0
	contract.Deployer(t, contract.DeployerEnv{
		Deployer: copying{Deployer: d, copies: cp},
		Target: func(t *testing.T) ports.Target {
			namespaces++
			ns := argoNamespace(t, remoteC, namespaces)
			argo.watch(ns)
			tt := remoteTarget(ns, kubeconfig)
			targets = append(targets, tt)
			return tt
		},
		Refused: func(*testing.T) ports.Target {
			return remoteTarget("argocd-1", []byte("not a kubeconfig"))
		},
		Deploys: func(t *testing.T, tt ports.Target, app string) int {
			name, err := model.ArgoCDApplicationName(app)
			if err != nil {
				t.Fatal(err)
			}
			return argo.deploys(tt.Settings[model.ArgoCDSettingNamespace], name)
		},
	})
	if built != 1 {
		t.Errorf("built %d clients from one kubeconfig, want 1", built)
	}

	ctx := context.Background()
	found := 0
	for _, tt := range targets {
		ns := tt.Settings[model.ArgoCDSettingNamespace]
		var apps unstructured.UnstructuredList
		apps.SetGroupVersionKind(ApplicationGVK.GroupVersion().WithKind("ApplicationList"))
		if err := remoteC.List(ctx, &apps, client.InNamespace(ns)); err != nil {
			t.Fatal(err)
		}
		for _, app := range apps.Items {
			found++
			repoURL, _, _ := unstructured.NestedString(app.Object, "spec", "source", "repoURL")
			if repoURL != "http://gitea.remote:3000/contract/orders-cli.git" {
				t.Errorf("%s: repoURL = %q, want the repository at gitURL", ns, repoURL)
			}
			images, _, _ := unstructured.NestedStringSlice(app.Object, "spec", "source", "kustomize", "images")
			if len(images) != 1 || !strings.HasPrefix(images[0], "orders-cli="+remoteRegistry+"/contract/orders-cli@sha256:") {
				t.Errorf("%s: kustomize.images = %v, want the copy at %s", ns, images, remoteRegistry)
			}
		}
		var localApps unstructured.UnstructuredList
		localApps.SetGroupVersionKind(ApplicationGVK.GroupVersion().WithKind("ApplicationList"))
		if err := local.List(ctx, &localApps); err != nil {
			t.Fatal(err)
		}
		if len(localApps.Items) != 0 {
			t.Errorf("%d Applications on the operator's cluster, want none", len(localApps.Items))
		}
	}
	if found == 0 {
		t.Error("no Application on the remote")
	}
	for _, ref := range cp.refs {
		if !strings.HasPrefix(ref, remoteRegistry+"/contract/orders-cli:v") {
			t.Errorf("copied to %s, want %s/contract/orders-cli:<version>", ref, remoteRegistry)
		}
	}
	if len(cp.refs) == 0 {
		t.Error("no image copied")
	}
}

// copyImage's errors: no CopyImage is a missing artifact, the target's
// registry refusing the login refuses the credential, a failure to read the
// image keeps the caller's error, a tag that names another image fails for
// good and is no port error, so the deploy isn't retried as transient
// (#1770), and any other failure is unreachable; the copy goes under the
// imageRegistry's path, tagged the version, with the Secret's login (#1590),
// over TLS and nested unless the target says otherwise (#1770).
func TestCopyImage(t *testing.T) {
	tt := remoteTarget("argocd", nil)
	cfg := read(tt)
	s := shipment("v1.2.0")
	refusedErr := fmt.Errorf("write: %w", &transport.Error{StatusCode: http.StatusUnauthorized})
	sourceErr := fmt.Errorf("%w: read: boom", oci.ErrSource)
	takenErr := fmt.Errorf("gitea: %w: mirror/webshop/orders-api:v1.2.0 names sha256:ff, not sha256:ab", oci.ErrTagTaken)
	for _, tc := range []struct {
		name     string
		copy     func(context.Context, ports.Registry, string, string) error
		wantKind error
		want     error
	}{
		{name: "no CopyImage", wantKind: ports.ErrArtifactMissing},
		{name: "a refused login", copy: func(context.Context, ports.Registry, string, string) error { return refusedErr },
			wantKind: ports.ErrCredentialRefused},
		{name: "the source unread", copy: func(context.Context, ports.Registry, string, string) error { return sourceErr },
			want: sourceErr},
		{name: "a tag naming another image (#1770)",
			copy: func(context.Context, ports.Registry, string, string) error { return takenErr }, wantKind: oci.ErrTagTaken},
		{name: "a registry down", copy: func(context.Context, ports.Registry, string, string) error {
			return errors.New("connection refused")
		}, wantKind: ports.ErrUnreachable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s.CopyImage = tc.copy
			_, err := copyImage(context.Background(), tt, cfg, s)
			if tc.wantKind != nil && !errors.Is(err, tc.wantKind) {
				t.Errorf("copyImage() = %v, want %v", err, tc.wantKind)
			}
			if tc.want != nil && err != tc.want {
				t.Errorf("copyImage() = %v, want %v as it is", err, tc.want)
			}
			var perr *ports.Error
			if tc.wantKind == oci.ErrTagTaken && errors.As(err, &perr) {
				t.Errorf("copyImage() = %v is the port's %v; a taken tag is no transient failure", err, perr.Kind)
			}
		})
	}

	t.Run("copies under the path with the Secret's login", func(t *testing.T) {
		var got ports.Registry
		var gotRepo, gotTag string
		s.CopyImage = func(_ context.Context, to ports.Registry, repository, tag string) error {
			got, gotRepo, gotTag = to, repository, tag
			return nil
		}
		image, err := copyImage(context.Background(), tt, cfg, s)
		if err != nil {
			t.Fatal(err)
		}
		want := ports.Registry{Host: "registry.remote:5000", Username: "pusher", Password: "pw"}
		if got != want || gotRepo != "mirror/webshop/orders-api" || gotTag != "v1.2.0" {
			t.Errorf("copied to %+v %s:%s", got, gotRepo, gotTag)
		}
		if image != "registry.remote:5000/mirror/webshop/orders-api@sha256:"+strings.Repeat("ab", 32) {
			t.Errorf("image = %s", image)
		}
	})

	t.Run("plain HTTP and the flat layout (#1770)", func(t *testing.T) {
		lab := remoteTarget("argocd", nil)
		lab.Settings[model.ArgoCDSettingImageRegistry] = "registry.lab:5000"
		lab.Settings[model.ArgoCDSettingImageRegistryInsecure] = "true"
		lab.Settings[model.ArgoCDSettingImageRepositoryLayout] = model.ArgoCDLayoutFlat
		var got ports.Registry
		var gotRepo string
		s.CopyImage = func(_ context.Context, to ports.Registry, repository, _ string) error {
			got, gotRepo = to, repository
			return nil
		}
		image, err := copyImage(context.Background(), lab, read(lab), s)
		if err != nil {
			t.Fatal(err)
		}
		want := ports.Registry{Host: "registry.lab:5000", Username: "pusher", Password: "pw", Insecure: true}
		if got != want || gotRepo != "orders-api" {
			t.Errorf("copied to %+v %s", got, gotRepo)
		}
		if image != "registry.lab:5000/orders-api@sha256:"+strings.Repeat("ab", 32) {
			t.Errorf("image = %s", image)
		}
	})
}

// TestRestConfigRefusesWhatRunsInTheOperator: a kubeconfig in a target's
// Secret gives everything inline; a plugin or a file path would run or read
// in the operator's pod (#1590).
func TestRestConfigRefusesWhatRunsInTheOperator(t *testing.T) {
	kubeconfig := func(cluster, user string) []byte {
		return []byte("apiVersion: v1\nkind: Config\ncurrent-context: c\n" +
			"clusters:\n- name: c\n  cluster:\n    server: https://remote.example:6443\n" + cluster +
			"users:\n- name: u\n  user:\n" + user +
			"contexts:\n- name: c\n  context: {cluster: c, user: u}\n")
	}
	for _, tc := range []struct {
		name, cluster, user, want string
	}{
		{name: "an inline token", user: "    token: abc\n"},
		{name: "exec", user: "    exec: {apiVersion: client.authentication.k8s.io/v1, command: sh}\n",
			want: "runs a command"},
		{name: "an auth-provider", user: "    auth-provider: {name: oidc}\n", want: "auth-provider"},
		{name: "tokenFile", user: "    tokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token\n",
			want: "reads a file"},
		{name: "a client key file", user: "    client-certificate: /c.pem\n    client-key: /k.pem\n",
			want: "reads a file"},
		{name: "a certificate-authority file", cluster: "    certificate-authority: /ca.pem\n",
			user: "    token: abc\n", want: "certificate-authority-data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := restConfig(kubeconfig(tc.cluster, tc.user))
			if tc.want == "" {
				if err != nil || cfg.Host != "https://remote.example:6443" {
					t.Fatalf("restConfig = %v, %v; want the remote's config", cfg, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("restConfig error = %v, want one saying %q", err, tc.want)
			}
		})
	}
}
