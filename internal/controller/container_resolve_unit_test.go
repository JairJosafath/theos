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
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

// #1992: a Relationship created just before its Container may not be in the
// cache yet when the Container first resolves. The scaffold happens once, so
// resolve reads Relationships from the API server and sees it.
func TestResolveSeesARelationshipTheCacheDoesNotHoldYet(t *testing.T) {
	scheme := briefScheme(t)
	system := newSystem("apps", "webshop-core")
	container := newContainer("apps", "orders-api", system.Name, c4v1beta1.ContainerTypeService)
	store := newOrdersDB("apps")
	rel := newRelationship("apps", "orders-api-to-orders-db",
		element(c4v1beta1.ElementContainer, "orders-api"), element(c4v1beta1.ElementDataStore, "orders-db"))
	other := newRelationship("apps", "billing-to-orders-db",
		element(c4v1beta1.ElementContainer, "billing"), element(c4v1beta1.ElementDataStore, "orders-db"))
	cached := fake.NewClientBuilder().WithScheme(scheme).WithObjects(system, container, store).
		WithIndex(&c4v1beta1.Relationship{}, indexSource, func(o client.Object) []string {
			source := o.(*c4v1beta1.Relationship).Spec.Source
			return []string{elementKey(source.Kind, source.Name)}
		}).Build()
	apiServer := fake.NewClientBuilder().WithScheme(scheme).WithObjects(system, container, store, rel, other).Build()
	r := &ContainerReconciler{Client: cached, Scheme: scheme, APIReader: apiServer}

	res, _, err := r.resolve(context.Background(), container, system)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.use.Relationships) != 1 || res.use.Relationships[0].Name != rel.Name {
		t.Fatalf("resolve used %v, want only %s: a Relationship the cache lacks is missed", res.use.Relationships, rel.Name)
	}
	if len(res.use.Connectors) != 1 || res.use.Connectors[0] != "postgres" {
		t.Errorf("connectors are %v, want [postgres]", res.use.Connectors)
	}
}
