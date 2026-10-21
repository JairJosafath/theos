/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

// Package chart tests what of the Helm chart only a cluster can show.
//
// templates/gitea/keep-secrets.yaml renders only on an upgrade whose lookup
// finds Secrets gitea-admin or gitea-runner made by a release of the chart up
// to 0.2.0, so helm template, make validate-yaml and Test Chart never see it,
// and Verify Release upgrades only from the release just before the one it
// checks. Here helm template --is-upgrade --dry-run=server runs its lookups
// against envtest's API server, holding such Secrets, and the test then does
// what the hook's Job does, as its ServiceAccount, under the rendered RBAC.
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
	"regexp"
	"slices"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const (
	release    = "c4-factory"
	releaseNS  = "c4-factory-system"
	giteaNS    = "gitea"
	hookName   = "gitea-keep-secrets"
	keepSource = "templates/gitea/keep-secrets.yaml"
	keepPolicy = "helm.sh/resource-policy"
	chartDir   = "../../dist/chart"
)

func TestKeepSecretsHook(t *testing.T) {
	helm := os.Getenv("HELM")
	if helm == "" {
		helm = filepath.Join("..", "..", "bin", "helm")
	}
	if _, err := os.Stat(helm); err != nil {
		t.Skipf("no helm at %s (make install-helm, or run make test): %v", helm, err)
	}

	env := &envtest.Environment{}
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		env.BinaryAssetsDirectory = envtestBinaryDir()
	}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })

	admin, err := env.AddUser(envtest.User{Name: "chart-admin", Groups: []string{"system:masters"}}, nil)
	if err != nil {
		t.Fatalf("add user: %v", err)
	}
	kubeconfig, err := admin.KubeConfig()
	if err != nil {
		t.Fatalf("kubeconfig: %v", err)
	}
	kubeconfigPath := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(kubeconfigPath, kubeconfig, 0o600); err != nil {
		t.Fatal(err)
	}
	cs := kubernetes.NewForConfigOrDie(cfg)
	ctx := t.Context()

	render := func(t *testing.T, upgrade bool) string {
		t.Helper()
		args := []string{"template", release, chartDir, "--namespace", releaseNS,
			"--dry-run=server", "--kubeconfig", kubeconfigPath, "--set", "gitea.projects={apps}"}
		if upgrade {
			args = append(args, "--is-upgrade")
		}
		var stdout, stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, helm, args...)
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("helm %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
		}
		return keepDocs(stdout.String())
	}

	if _, err := cs.CoreV1().Namespaces().Create(ctx,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: giteaNS, Labels: helmLabels, Annotations: helmOwner(release)}},
		metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Run("an install renders no hook", func(t *testing.T) {
		makeSecrets(ctx, t, cs, map[string]string{"gitea-admin": release, "gitea-runner": release})
		if out := render(t, false); out != "" {
			t.Errorf("helm template renders %s on an install:\n%s", keepSource, out)
		}
	})

	t.Run("an upgrade over Secrets Gitea's pod made renders no hook", func(t *testing.T) {
		makeSecrets(ctx, t, cs, nil)
		if out := render(t, true); out != "" {
			t.Errorf("helm template renders %s with no Helm-owned Secret:\n%s", keepSource, out)
		}
	})

	t.Run("an upgrade from 0.2.0 keeps the release's own Secrets", func(t *testing.T) {
		// gitea-runner belongs to another release: the hook leaves it alone.
		makeSecrets(ctx, t, cs, map[string]string{"gitea-admin": release, "gitea-runner": "someone-else"})
		objs := decode(t, render(t, true))
		job := applyHook(ctx, t, cs, objs)

		var role *rbacv1.Role
		for _, o := range objs {
			if r, ok := o.(*rbacv1.Role); ok {
				role = r
			}
		}
		if role == nil || len(role.Rules) != 1 || !slices.Equal(role.Rules[0].ResourceNames, []string{"gitea-admin"}) {
			t.Fatalf("Role %s should grant gitea-admin alone, got %+v", hookName, role)
		}
		annotated := annotateTargets(t, job)
		if !slices.Equal(annotated, []string{"gitea-admin"}) {
			t.Fatalf("Job %s annotates %v, want [gitea-admin]", hookName, annotated)
		}

		asHook := secretsAs(t, cfg, "system:serviceaccount:"+giteaNS+":"+hookName)
		for _, name := range annotated {
			annotateKeep(ctx, t, asHook, name)
		}
		_, err := asHook.Patch(ctx, "gitea-runner", types.MergePatchType, keepPatch, metav1.PatchOptions{})
		if !apierrors.IsForbidden(err) {
			t.Errorf("the hook's ServiceAccount patches a Secret its Role doesn't name: %v", err)
		}
		for name, want := range map[string]string{"gitea-admin": "keep", "gitea-runner": ""} {
			s, err := cs.CoreV1().Secrets(giteaNS).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if got := s.Annotations[keepPolicy]; got != want {
				t.Errorf("Secret %s has %s=%q, want %q", name, keepPolicy, got, want)
			}
		}
	})

	t.Run("an upgrade from 0.2.0 renders both Secrets", func(t *testing.T) {
		makeSecrets(ctx, t, cs, map[string]string{"gitea-admin": release, "gitea-runner": release})
		objs := decode(t, render(t, true))
		var job *batchv1.Job
		for _, o := range objs {
			if j, ok := o.(*batchv1.Job); ok {
				job = j
			}
		}
		if job == nil {
			t.Fatalf("no Job %s rendered", hookName)
		}
		if got := annotateTargets(t, job); !slices.Equal(got, []string{"gitea-admin", "gitea-runner"}) {
			t.Errorf("Job %s annotates %v, want [gitea-admin gitea-runner]", hookName, got)
		}
	})
}

// TestSeedImageRunsTheHook checks that the image the hook's Job runs, the
// seed's, still has the kubectl its script calls.
func TestSeedImageRunsTheHook(t *testing.T) {
	dockerfile, err := os.ReadFile(filepath.Join("..", "..", "hack", "seed", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^(RUN|\s*&&)( retry)? apk add .*\bkubectl\b`).Match(dockerfile) {
		t.Errorf("hack/seed/Dockerfile no longer installs kubectl, which %s's Job runs", keepSource)
	}
	tmpl, err := os.ReadFile(filepath.Join(chartDir, keepSource))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(tmpl, []byte(`image: "{{ $seed.image.repository }}`)) {
		t.Errorf("%s's Job no longer runs the seed image; check its new image has kubectl", keepSource)
	}
}

// makeSecrets makes gitea-admin and gitea-runner afresh, each with the
// release-name annotation helm puts on what it owns, or none for "".
func makeSecrets(ctx context.Context, t *testing.T, cs kubernetes.Interface, owners map[string]string) {
	t.Helper()
	for _, name := range []string{"gitea-admin", "gitea-runner"} {
		err := cs.CoreV1().Secrets(giteaNS).Delete(ctx, name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			t.Fatal(err)
		}
		s := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: giteaNS},
			StringData: map[string]string{"token": "not-a-secret"},
		}
		if owner := owners[name]; owner != "" {
			s.Annotations, s.Labels = helmOwner(owner), helmLabels
		}
		if _, err := cs.CoreV1().Secrets(giteaNS).Create(ctx, s, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
}

// helmLabels and helmOwner are the metadata helm puts on what a release owns;
// the release that made the namespace gitea owns it, as on a device.
var helmLabels = map[string]string{"app.kubernetes.io/managed-by": "Helm"}

func helmOwner(release string) map[string]string {
	return map[string]string{"meta.helm.sh/release-name": release, "meta.helm.sh/release-namespace": releaseNS}
}

// keepDocs returns the documents helm template rendered from keep-secrets.yaml.
func keepDocs(out string) string {
	var docs []string
	for doc := range strings.SplitSeq(out, "\n---\n") {
		if strings.Contains(doc, "# Source: c4-factory/"+keepSource+"\n") {
			docs = append(docs, doc)
		}
	}
	return strings.Join(docs, "\n---\n")
}

func decode(t *testing.T, manifest string) []runtime.Object {
	t.Helper()
	if manifest == "" {
		t.Fatalf("helm template renders no %s", keepSource)
	}
	var objs []runtime.Object
	r := utilyaml.NewYAMLReader(bufio.NewReader(strings.NewReader(manifest)))
	for {
		doc, err := r.Read()
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
			t.Fatalf("decode %s: %v\n%s", keepSource, err, doc)
		}
		objs = append(objs, obj)
	}
	return objs
}

// applyHook creates the hook's objects as helm would and returns its Job.
// envtest runs no Job controller, so creating the Job only validates it.
func applyHook(ctx context.Context, t *testing.T, cs kubernetes.Interface, objs []runtime.Object) *batchv1.Job {
	t.Helper()
	var job *batchv1.Job
	for _, o := range objs {
		var err error
		switch o := o.(type) {
		case *corev1.ServiceAccount:
			_, err = cs.CoreV1().ServiceAccounts(o.Namespace).Create(ctx, o, metav1.CreateOptions{})
		case *rbacv1.Role:
			_, err = cs.RbacV1().Roles(o.Namespace).Create(ctx, o, metav1.CreateOptions{})
		case *rbacv1.RoleBinding:
			_, err = cs.RbacV1().RoleBindings(o.Namespace).Create(ctx, o, metav1.CreateOptions{})
		case *batchv1.Job:
			job = o
			_, err = cs.BatchV1().Jobs(o.Namespace).Create(ctx, o, metav1.CreateOptions{})
		default:
			t.Fatalf("%s renders an unexpected %T", keepSource, o)
		}
		if err != nil {
			t.Fatalf("create %T: %v", o, err)
		}
	}
	if job == nil {
		t.Fatalf("%s renders no Job", keepSource)
	}
	return job
}

var annotateLine = regexp.MustCompile(
	`(?m)^\s*kubectl annotate secret (\S+) ` + regexp.QuoteMeta(keepPolicy) + `=keep --overwrite$`)

// annotateTargets returns the Secrets the Job's script annotates.
func annotateTargets(t *testing.T, job *batchv1.Job) []string {
	t.Helper()
	c := job.Spec.Template.Spec.Containers
	if len(c) != 1 || len(c[0].Args) != 1 {
		t.Fatalf("Job %s should run one container with one script, got %+v", hookName, c)
	}
	matches := annotateLine.FindAllStringSubmatch(c[0].Args[0], -1)
	names := make([]string, 0, len(matches))
	for _, m := range matches {
		names = append(names, m[1])
	}
	return names
}

var keepPatch = []byte(`{"metadata":{"annotations":{"` + keepPolicy + `":"keep"}}}`)

// secretsAs returns a Secrets client for namespace gitea, impersonating user.
func secretsAs(t *testing.T, cfg *rest.Config, user string) typedcorev1.SecretInterface {
	t.Helper()
	c := rest.CopyConfig(cfg)
	c.Impersonate = rest.ImpersonationConfig{UserName: user}
	return kubernetes.NewForConfigOrDie(c).CoreV1().Secrets(giteaNS)
}

// annotateKeep does what the Job's kubectl annotate does: a get, then a merge
// patch.
func annotateKeep(ctx context.Context, t *testing.T, secrets typedcorev1.SecretInterface, name string) {
	t.Helper()
	if _, err := secrets.Get(ctx, name, metav1.GetOptions{}); err != nil {
		t.Fatalf("the hook's ServiceAccount cannot get Secret %s: %v", name, err)
	}
	if _, err := secrets.Patch(ctx, name, types.MergePatchType, keepPatch, metav1.PatchOptions{}); err != nil {
		t.Fatalf("the hook's ServiceAccount cannot annotate Secret %s: %v", name, err)
	}
}

// envtestBinaryDir finds the binaries make setup-envtest installs, for a run
// outside make test.
func envtestBinaryDir() string {
	base := filepath.Join("..", "..", ".dev", "make", "bin", "k8s")
	entries, err := os.ReadDir(base)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			return filepath.Join(base, e.Name())
		}
	}
	return ""
}
