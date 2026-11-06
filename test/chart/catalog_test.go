/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package chart

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const (
	catalogConfigMap = "theos-catalog"
	catalogSource    = "templates/gitea/catalog-configmap.yaml"
	seedSource       = "templates/gitea/seed.yaml"
	project          = "apps"
)

// TestSeedAppliesTheCatalogInPlace renders the chart as two releases apart,
// and applies ConfigMap theos-catalog's objects with the seed's
// hack/seed/catalog.sh, as the seed's ServiceAccount under the rendered RBAC,
// against envtest's API server: the second apply keeps every object's UID,
// changes the Agents' image in place and keeps a field a person set that the
// chart doesn't. A GitProvider an older chart made as a Helm hook is taken
// over, not re-created.
func TestSeedAppliesTheCatalogInPlace(t *testing.T) {
	helm, env, assets := startEnv(t)
	cs := kubernetes.NewForConfigOrDie(env.Config)
	dyn := dynamic.NewForConfigOrDie(env.Config)
	ctx := t.Context()

	for _, ns := range []string{giteaNS, project} {
		if _, err := cs.CoreV1().Namespaces().Create(ctx,
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}

	first := renderCatalog(ctx, t, helm, "first")
	createRBAC(ctx, t, cs, first.rbac)

	// What the chart up to v0.12 made: a hook, created by helm.
	gitea := gvr("gitproviders")
	hook := hookGitProvider("gitea", "http://old.example:3000", "gitea-credentials")
	old, err := dyn.Resource(gitea).Namespace(project).Create(ctx, hook, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	apply := applier(ctx, t, env, assets, giteaNS, "seed")

	apply(t, first.objects)
	uids := map[string]types.UID{}
	for _, o := range decodeObjects(t, first.objects) {
		got := getObject(ctx, t, dyn, o)
		uids[key(o)] = got.GetUID()
	}
	if got := uids["GitProvider/gitea"]; got != old.GetUID() {
		t.Errorf("GitProvider gitea, a hook of the older chart, was re-created: UID %s, was %s", got, old.GetUID())
	}
	if len(uids) < 5 {
		t.Fatalf("ConfigMap %s holds %d objects, want the GitProvider, catalog and Agents", catalogConfigMap, len(uids))
	}
	// The seed's browser (docs/plans/test-environments.md, T14), which its
	// Role lets it apply.
	if _, ok := uids["TestEnvironment/browser"]; !ok {
		t.Errorf("ConfigMap %s makes no TestEnvironment browser", catalogConfigMap)
	}

	// A person adds env to the planner, which the chart doesn't set.
	env0 := []byte(`{"spec":{"env":[{"name":"HAND","value":"edit"}]}}`)
	if _, err := dyn.Resource(gvr("agents")).Namespace(project).Patch(ctx, "planner",
		types.MergePatchType, env0, metav1.PatchOptions{}); err != nil {
		t.Fatal(err)
	}

	second := renderCatalog(ctx, t, helm, "second")
	apply(t, second.objects)
	for _, o := range decodeObjects(t, second.objects) {
		got := getObject(ctx, t, dyn, o)
		if want, ok := uids[key(o)]; !ok {
			t.Errorf("%s is new in the second render", key(o))
		} else if got.GetUID() != want {
			t.Errorf("%s was re-created: UID %s, was %s", key(o), got.GetUID(), want)
		}
		if o.GetKind() != "Agent" {
			continue
		}
		image, _, _ := unstructured.NestedString(got.Object, "spec", "image")
		if !strings.HasSuffix(image, ":second") {
			t.Errorf("Agent %s has image %s, want the second render's tag", o.GetName(), image)
		}
	}
	planner := getObject(ctx, t, dyn, objectRef("Agent", "planner"))
	if env, _, _ := unstructured.NestedSlice(planner.Object, "spec", "env"); len(env) != 1 {
		t.Errorf("Agent planner's env a person set was not kept: %v", env)
	}
	gp := getObject(ctx, t, dyn, objectRef("GitProvider", "gitea"))
	const giteaURL = "http://gitea.gitea.svc.cluster.local:3000"
	if url, _, _ := unstructured.NestedString(gp.Object, "spec", "url"); url != giteaURL {
		t.Errorf("GitProvider gitea's url is %s, want the chart's", url)
	}
}

// rendered is what the test needs of a render: the ConfigMap's objects and
// the seed's RBAC in the project namespace.
type rendered struct {
	objects string
	rbac    []any
}

// renderCatalog renders the chart with the Agents on image tag tag.
func renderCatalog(ctx context.Context, t *testing.T, helm, tag string) rendered {
	t.Helper()
	return render(ctx, t, helm, catalogConfigMap, []string{catalogSource, seedSource},
		"gitea.projects={"+project+"}", "agents.image.tag="+tag)
}

// render renders the chart's templates sources with the values sets, and
// returns ConfigMap configMap's objects and the Role and RoleBinding in the
// project namespace.
func render(ctx context.Context, t *testing.T, helm, configMap string, sources []string, sets ...string) rendered {
	t.Helper()
	args := make([]string, 0, 5+2*len(sources)+2*len(sets))
	args = append(args, "template", release, chartDir, "--namespace", releaseNS)
	for _, s := range sources {
		args = append(args, "--show-only", s)
	}
	for _, s := range sets {
		args = append(args, "--set", s)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, helm, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("helm %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	var r rendered
	reader := utilyaml.NewYAMLReader(bufio.NewReader(&stdout))
	for {
		doc, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(bytes.TrimSpace(doc)) == 0 {
			continue
		}
		obj, _, err := scheme.Codecs.UniversalDeserializer().Decode(doc, nil, nil)
		if err != nil {
			t.Fatalf("decode: %v\n%s", err, doc)
		}
		switch o := obj.(type) {
		case *corev1.ConfigMap:
			if o.Name == configMap {
				r.objects = o.Data["objects.yaml"]
			}
		case *rbacv1.Role:
			if o.Namespace == project {
				r.rbac = append(r.rbac, o)
			}
		case *rbacv1.RoleBinding:
			if o.Namespace == project {
				r.rbac = append(r.rbac, o)
			}
		}
	}
	if r.objects == "" || len(r.rbac) != 2 {
		t.Fatalf("the render has no ConfigMap %s or not one Role and RoleBinding in %s", configMap, project)
	}
	return r
}

// startEnv finds helm and envtest's binaries, skipping the test without them,
// and starts envtest's API server with the CRDs.
func startEnv(t *testing.T) (helm string, env *envtest.Environment, assets string) {
	t.Helper()
	helm = os.Getenv("HELM")
	if helm == "" {
		helm = filepath.Join("..", "..", "bin", "helm")
	}
	if _, err := os.Stat(helm); err != nil {
		t.Skipf("no helm at %s (make install-helm, or run make test): %v", helm, err)
	}
	assets = os.Getenv("KUBEBUILDER_ASSETS")
	if assets == "" {
		assets = envtestBinaryDir()
	}
	if _, err := os.Stat(filepath.Join(assets, "kubectl")); err != nil {
		t.Skipf("no kubectl among envtest's binaries in %q (run make test): %v", assets, err)
	}
	env = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
		BinaryAssetsDirectory: assets,
	}
	if _, err := env.Start(); err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })
	return helm, env, assets
}

// applier returns a func that applies objects with hack/seed/catalog.sh as
// ServiceAccount namespace/name, so only the RBAC the chart renders lets it.
func applier(
	ctx context.Context, t *testing.T, env *envtest.Environment, assets, namespace, name string,
) func(t *testing.T, objects string) {
	t.Helper()
	user, err := env.AddUser(envtest.User{
		Name:   "system:serviceaccount:" + namespace + ":" + name,
		Groups: []string{"system:serviceaccounts", "system:serviceaccounts:" + namespace, "system:authenticated"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	kubeconfig, err := user.KubeConfig()
	if err != nil {
		t.Fatal(err)
	}
	kubeconfigPath := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(kubeconfigPath, kubeconfig, 0o600); err != nil {
		t.Fatal(err)
	}
	return func(t *testing.T, objects string) {
		t.Helper()
		file := filepath.Join(t.TempDir(), "objects.yaml")
		if err := os.WriteFile(file, []byte(objects), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, filepath.Join("..", "..", "hack", "seed", "catalog.sh"), file)
		cmd.Env = append(os.Environ(), "KUBECONFIG="+kubeconfigPath,
			"PATH="+assets+string(os.PathListSeparator)+os.Getenv("PATH"))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("catalog.sh as %s/%s: %v\n%s", namespace, name, err, out)
		}
	}
}

// hookGitProvider is a GitProvider in the project namespace as the chart up
// to v0.12 made it: a Helm hook, created by helm.
func hookGitProvider(name, url, secret string) *unstructured.Unstructured {
	hook := &unstructured.Unstructured{}
	hook.SetAPIVersion("theos.factory.io/v1beta1")
	hook.SetKind("GitProvider")
	hook.SetName(name)
	hook.SetNamespace(project)
	hook.SetAnnotations(map[string]string{"helm.sh/hook": "post-install,post-upgrade"})
	_ = unstructured.SetNestedMap(hook.Object, map[string]any{
		"provider": "gitea", "url": url, "owner": project,
		"credentialsSecretRef": map[string]any{"name": secret},
	}, "spec")
	return hook
}

func createRBAC(ctx context.Context, t *testing.T, cs kubernetes.Interface, objs []any) {
	t.Helper()
	for _, o := range objs {
		var err error
		switch o := o.(type) {
		case *rbacv1.Role:
			_, err = cs.RbacV1().Roles(o.Namespace).Create(ctx, o, metav1.CreateOptions{})
		case *rbacv1.RoleBinding:
			_, err = cs.RbacV1().RoleBindings(o.Namespace).Create(ctx, o, metav1.CreateOptions{})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func decodeObjects(t *testing.T, objects string) []*unstructured.Unstructured {
	t.Helper()
	var out []*unstructured.Unstructured
	reader := utilyaml.NewYAMLReader(bufio.NewReader(strings.NewReader(objects)))
	for {
		doc, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		u := &unstructured.Unstructured{}
		if err := utilyaml.Unmarshal(doc, &u.Object); err != nil {
			t.Fatal(err)
		}
		if len(u.Object) == 0 {
			continue
		}
		if a := u.GetAnnotations(); a["helm.sh/hook"] != "" {
			t.Errorf("%s in the ConfigMap is a Helm hook", key(u))
		}
		out = append(out, u)
	}
}

func objectRef(kind, name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetKind(kind)
	u.SetName(name)
	u.SetNamespace(project)
	return u
}

func gvr(resource string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "theos.factory.io", Version: "v1beta1", Resource: resource}
}

func key(u *unstructured.Unstructured) string { return u.GetKind() + "/" + u.GetName() }

func getObject(
	ctx context.Context, t *testing.T, dyn dynamic.Interface, u *unstructured.Unstructured,
) *unstructured.Unstructured {
	t.Helper()
	resource := strings.ToLower(u.GetKind()) + "s"
	got, err := dyn.Resource(gvr(resource)).Namespace(project).Get(ctx, u.GetName(), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get %s: %v", key(u), err)
	}
	return got
}

// TestSeedImageHasCatalogScript checks that the seed image puts catalog.sh
// next to the seed, where seed.sh calls it, and that its build context lets it in.
func TestSeedImageHasCatalogScript(t *testing.T) {
	for file, want := range map[string]string{
		"Dockerfile":              "COPY --chmod=0755 hack/seed/catalog.sh /usr/local/bin/catalog.sh\n",
		"Dockerfile.dockerignore": "!hack/seed/catalog.sh\n",
		"seed.sh":                 `"$(dirname "$0")/catalog.sh" "$CATALOG"`,
	} {
		got, err := os.ReadFile(filepath.Join("..", "..", "hack", "seed", file))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(got, []byte(want)) {
			t.Errorf("hack/seed/%s lacks %q", file, want)
		}
	}
}
