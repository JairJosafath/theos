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

package controller

import (
	"context"
	"reflect"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/modelfacts"
	portsfake "github.com/JairJosafath/theos/internal/ports/fake"
)

// #268: the operator and the theos binary protect main with one rule. What
// protectRepository puts on main is what modelfacts.Protection returns for
// the same objects, so the two cannot drift.
func TestProtectRepositoryPutsModelfactsRule(t *testing.T) {
	const ns = "apps"
	// Any host the factory knows a CI system of; the rule is built the same
	// way on each (TestHostNeutral keeps this file free of one host's name).
	provider := &c4v1beta1.GitProvider{ObjectMeta: objectMeta(ns, "forge"),
		Spec: c4v1beta1.GitProviderSpec{Provider: c4v1beta1.GitProviderKind("github"), URL: "https://forge.example"}}
	strict := &c4v1beta1.Standard{ObjectMeta: objectMeta(ns, "strict"),
		Spec: c4v1beta1.StandardSpec{ProtectedPaths: []string{"AGENTS.md", "ruff.toml", "docs/agents/"}}}
	plain := &c4v1beta1.Standard{ObjectMeta: objectMeta(ns, "plain")}
	withStandard := func(name string, adopt bool) *c4v1beta1.Container {
		container := newContainer(ns, "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
		container.Spec.GitProviderRef.Name = provider.Name
		if name != "" {
			container.Spec.StandardRef = &c4v1beta1.LocalRef{Name: name}
		}
		container.Spec.Adopt = adopt
		return container
	}

	for _, tc := range []struct {
		name      string
		container *c4v1beta1.Container
		standard  *c4v1beta1.Standard
	}{
		{"a Standard with protected paths", withStandard(strict.Name, false), strict},
		{"a Standard with none", withStandard(plain.Name, false), plain},
		{"an adopted repository with a Standard", withStandard(strict.Name, true), strict},
		{"no Standard", withStandard("", false), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objs := []client.Object{provider, strict, plain, tc.container}
			c := fake.NewClientBuilder().WithScheme(briefScheme(t)).WithObjects(objs...).Build()
			git := portsfake.New()
			repo := git.Repository(tc.container.Name)

			if err := protectRepository(context.Background(), c, git, repo, tc.container); err != nil {
				t.Fatal(err)
			}
			want, protect, err := modelfacts.Protection(tc.container, provider, tc.standard)
			if err != nil {
				t.Fatal(err)
			}
			got, tags := git.BranchRule(repo.Owner, repo.Name)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("the operator put %+v on main, modelfacts.Protection gives %+v", got, want)
			}
			var wantTags []string
			if protect {
				wantTags = []string{model.ReleaseTags}
			}
			if !reflect.DeepEqual(tags, wantTags) {
				t.Errorf("protect = %v, tags protected: %v, want %v", protect, tags, wantTags)
			}
			if protect && len(want.ProtectedPaths) == 0 {
				t.Error("the rule protects no path: the comparison shows nothing")
			}
		})
	}
}
