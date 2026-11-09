/*
Copyright 2026 The theos Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package chart

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestGitProvidersRenderPackages renders the GitProviders of gitea.projects
// and gitProviders with spec.packages (#1230): a gitea.projects entry is a
// name or an object with a name and packages, a name given twice makes one
// GitProvider, and packages lands under spec as given; an entry without it
// renders no spec.packages.
func TestGitProvidersRenderPackages(t *testing.T) {
	helm := helmBinary(t)
	ctx := t.Context()

	t.Run("gitea.projects", func(t *testing.T) {
		r := render(ctx, t, helm, catalogConfigMap, []string{catalogSource, seedSource},
			"gitea.projects[0]="+project,
			"gitea.projects[1].name=shop", "gitea.projects[1].packages.npm.scope=@acme",
			"gitea.projects[1].packages.npm.credentialsSecretRef.name=acme-npm",
			"gitea.projects[2]="+project)
		providers := gitProvidersOf(t, r.objects)
		if len(providers) != 2 {
			t.Fatalf("got %d GitProviders, want one each for %s (named twice) and shop", len(providers), project)
		}
		if _, ok, _ := unstructured.NestedMap(providers[project+"/gitea"].Object, "spec", "packages"); ok {
			t.Errorf("GitProvider of %s, a name, has spec.packages", project)
		}
		shop := providers["shop/gitea"].Object
		if scope, _, _ := unstructured.NestedString(shop, "spec", "packages", "npm", "scope"); scope != "@acme" {
			t.Errorf("GitProvider of shop has npm scope %q, want @acme", scope)
		}
		name, _, _ := unstructured.NestedString(shop, "spec", "packages", "npm", "credentialsSecretRef", "name")
		if name != "acme-npm" {
			t.Errorf("GitProvider of shop has npm credentialsSecretRef %q, want acme-npm", name)
		}
	})

	t.Run("gitProviders", func(t *testing.T) {
		r := renderGitProviders(ctx, t, helm, "http://git.example:3000",
			"gitProviders[1].packages.pypi.index=https://pypi.example/simple")
		providers := gitProvidersOf(t, r.objects)
		if _, ok, _ := unstructured.NestedMap(providers[project+"/git"].Object, "spec", "packages"); ok {
			t.Error("GitProvider git, without packages, has spec.packages")
		}
		index, _, _ := unstructured.NestedString(providers[project+"/other"].Object, "spec", "packages", "pypi", "index")
		if index != "https://pypi.example/simple" {
			t.Errorf("GitProvider other has pypi index %q, want https://pypi.example/simple", index)
		}
	})
}

// gitProvidersOf is the GitProviders among objects, by namespace/name.
func gitProvidersOf(t *testing.T, objects string) map[string]*unstructured.Unstructured {
	t.Helper()
	out := map[string]*unstructured.Unstructured{}
	for _, o := range decodeObjects(t, objects) {
		if o.GetKind() == "GitProvider" {
			out[o.GetNamespace()+"/"+o.GetName()] = o
		}
	}
	return out
}
