/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package chart

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

const (
	gitProvidersConfigMap = "c4factory-gitproviders"
	gitProvidersSource    = "templates/gitprovider/gitproviders.yaml"
	gitProvidersApply     = "templates/gitprovider/apply.yaml"
	gitProvidersAccount   = release + "-gitproviders"
)

// TestGitProvidersApplyInPlace renders the chart without Gitea as two
// releases apart, and applies ConfigMap c4factory-gitproviders' objects with
// hack/seed/catalog.sh, as Job c4-factory-gitproviders' ServiceAccount under
// the rendered RBAC, against envtest's API server: the second apply keeps
// every GitProvider's UID, changes its url in place and keeps a label a
// person set. A GitProvider an older chart made as a Helm hook is taken over,
// not re-created.
func TestGitProvidersApplyInPlace(t *testing.T) {
	helm, env, assets := startEnv(t)
	cs := kubernetes.NewForConfigOrDie(env.Config)
	dyn := dynamic.NewForConfigOrDie(env.Config)
	ctx := t.Context()

	for _, ns := range []string{releaseNS, project} {
		if _, err := cs.CoreV1().Namespaces().Create(ctx,
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}

	first := renderGitProviders(ctx, t, helm, "http://git.example:3000")
	createRBAC(ctx, t, cs, first.rbac)

	old, err := dyn.Resource(gvr("gitproviders")).Namespace(project).Create(ctx,
		hookGitProvider("git", "http://old.example:3000", "git-credentials"), metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	apply := applier(ctx, t, env, assets, releaseNS, gitProvidersAccount)
	apply(t, first.objects)
	uids := map[string]types.UID{}
	for _, o := range decodeObjects(t, first.objects) {
		uids[key(o)] = getObject(ctx, t, dyn, o).GetUID()
	}
	if len(uids) != 2 {
		t.Fatalf("ConfigMap %s holds %d GitProviders, want 2", gitProvidersConfigMap, len(uids))
	}
	if got := uids["GitProvider/git"]; got != old.GetUID() {
		t.Errorf("GitProvider git, a hook of the older chart, was re-created: UID %s, was %s", got, old.GetUID())
	}

	// A person labels GitProvider other, a field the chart doesn't set.
	label := []byte(`{"metadata":{"labels":{"hand":"edit"}}}`)
	if _, err := dyn.Resource(gvr("gitproviders")).Namespace(project).Patch(ctx, "other",
		types.MergePatchType, label, metav1.PatchOptions{}); err != nil {
		t.Fatal(err)
	}

	const url = "http://git2.example:3000"
	second := renderGitProviders(ctx, t, helm, url)
	apply(t, second.objects)
	for _, o := range decodeObjects(t, second.objects) {
		got := getObject(ctx, t, dyn, o)
		if want, ok := uids[key(o)]; !ok {
			t.Errorf("%s is new in the second render", key(o))
		} else if got.GetUID() != want {
			t.Errorf("%s was re-created: UID %s, was %s", key(o), got.GetUID(), want)
		}
		if u, _, _ := unstructured.NestedString(got.Object, "spec", "url"); u != url {
			t.Errorf("%s has url %s, want the second render's %s", key(o), u, url)
		}
	}
	if l := getObject(ctx, t, dyn, objectRef("GitProvider", "other")).GetLabels(); l["hand"] != "edit" {
		t.Errorf("GitProvider other's label a person set was not kept: %v", l)
	}
}

// renderGitProviders renders the chart without Gitea, with GitProviders git
// and other in the project namespace on url, and the values sets extra.
func renderGitProviders(ctx context.Context, t *testing.T, helm, url string, extra ...string) rendered {
	t.Helper()
	sets := make([]string, 0, 9+len(extra))
	sets = append(sets,
		"gitea.enabled=false",
		"gitProviders[0].namespace="+project, "gitProviders[0].url="+url,
		"gitProviders[0].owner="+project, "gitProviders[0].existingSecret=git-credentials",
		"gitProviders[1].namespace="+project, "gitProviders[1].name=other", "gitProviders[1].url="+url,
		"gitProviders[1].owner="+project, "gitProviders[1].existingSecret=git-credentials",
	)
	return render(ctx, t, helm, gitProvidersConfigMap, []string{gitProvidersSource, gitProvidersApply},
		append(sets, extra...)...)
}
