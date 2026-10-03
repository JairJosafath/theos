/*
Copyright 2026.

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
	"reflect"
	"slices"
	"testing"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

func TestScaffoldedConnectors(t *testing.T) {
	for _, tt := range []struct {
		name    string
		applied []c4v1alpha1.AppliedConnector
		want    []string
	}{
		{name: "none"},
		{name: "unique and sorted, as the scaffold passed them", applied: []c4v1alpha1.AppliedConnector{
			{Relationship: "a-to-orders-db", DataStore: "orders-db", Connector: "postgres"},
			{Relationship: "b-to-cache", DataStore: "cache", Connector: "redis"},
			{Relationship: "c-to-audit-db", DataStore: "audit-db", Connector: "postgres"},
			{Relationship: "d-to-events", DataStore: "events", Connector: "kafka"},
		}, want: []string{"kafka", "postgres", "redis"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := scaffoldedConnectors(&c4v1alpha1.ContainerStatus{AppliedConnectors: tt.applied})
			if !slices.Equal(got, tt.want) {
				t.Errorf("scaffoldedConnectors() = %q, want %q", got, tt.want)
			}
			// The scaffold's own list, from the same Relationships, is the same.
			var rels []model.Relationship
			connectorOf := map[string]string{}
			for _, a := range tt.applied {
				rels = append(rels, model.Relationship{Name: a.Relationship,
					Source: model.Ref{Kind: model.KindContainer, Name: "orders-api"},
					Target: model.Ref{Kind: model.KindDataStore, Name: a.DataStore}})
				connectorOf[a.DataStore] = a.Connector
			}
			if use := model.DataStoresUsedBy("orders-api", rels, connectorOf); !slices.Equal(use.Connectors, got) {
				t.Errorf("the scaffold passed %q, the update %q", use.Connectors, got)
			}
		})
	}
}

func TestScaffoldInput(t *testing.T) {
	container := &c4v1alpha1.Container{
		ObjectMeta: objectMeta("webshop", "orders-api"),
		Spec: c4v1alpha1.ContainerSpec{
			SoftwareSystemRef: c4v1alpha1.LocalRef{Name: "webshop-core"},
			DisplayName:       "Orders API",
			Type:              c4v1alpha1.ContainerTypeService,
			FrameworkRef:      c4v1alpha1.LocalRef{Name: "python-fastapi"},
			StandardRef:       &c4v1alpha1.LocalRef{Name: "python-strict"},
			DeploymentRef:     &c4v1alpha1.LocalRef{Name: "k8s-manifests"},
		},
	}
	got := scaffoldInput(container, []string{"postgres"})
	want := scaffold.Input{
		Project: "webshop", SoftwareSystem: "webshop-core", Container: "orders-api", DisplayName: "Orders API",
		Type: "service", Framework: "python-fastapi", Standard: "python-strict", Deployment: "k8s-manifests",
		Connectors: []string{"postgres"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scaffoldInput() = %+v, want %+v", got, want)
	}
}
