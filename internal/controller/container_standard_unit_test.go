/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"reflect"
	"slices"
	"testing"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

func TestScaffoldedConnectors(t *testing.T) {
	for _, tt := range []struct {
		name    string
		applied []c4v1beta1.AppliedConnector
		want    []string
	}{
		{name: "none"},
		{name: "unique and sorted, as the scaffold passed them", applied: []c4v1beta1.AppliedConnector{
			{Relationship: "a-to-orders-db", DataStore: "orders-db", Connector: "postgres"},
			{Relationship: "b-to-cache", DataStore: "cache", Connector: "redis"},
			{Relationship: "c-to-audit-db", DataStore: "audit-db", Connector: "postgres"},
			{Relationship: "d-to-events", DataStore: "events", Connector: "kafka"},
		}, want: []string{"kafka", "postgres", "redis"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := scaffoldedConnectors(&c4v1beta1.ContainerStatus{AppliedConnectors: tt.applied})
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
	container := &c4v1beta1.Container{
		ObjectMeta: objectMeta("webshop", "orders-api"),
		Spec: c4v1beta1.ContainerSpec{
			SoftwareSystemRef: c4v1beta1.LocalRef{Name: "webshop-core"},
			DisplayName:       "Orders API",
			Description:       "Takes and lists the shop's orders.",
			Type:              c4v1beta1.ContainerTypeService,
			FrameworkRef:      c4v1beta1.LocalRef{Name: "python-fastapi"},
			StandardRef:       &c4v1beta1.LocalRef{Name: "python-strict"},
			DeploymentRef:     &c4v1beta1.LocalRef{Name: "k8s-manifests"},
			Operations: &c4v1beta1.OperationsSpec{
				Recovery: &c4v1beta1.OperationsRecovery{RPO: "15m"},
				Alerts:   &c4v1beta1.OperationsAlerts{Email: "ops@example.com"},
			},
		},
	}
	ci, err := model.HostCI("gitea")
	if err != nil {
		t.Fatal(err)
	}
	want := scaffold.Input{
		CI:      ci,
		Project: "webshop", SoftwareSystem: "webshop-core", Container: "orders-api", DisplayName: "Orders API",
		Description: "Takes and lists the shop's orders.",
		Type:        "service", Framework: "python-fastapi", Standard: "python-strict", Deployment: "k8s-manifests",
		Connectors: []string{"postgres"}, Domains: []string{"webshop"}, Checks: "BeforeRelease",
		Operations: model.Operations{
			Backups:  model.OperationsBackups{RetentionDays: model.DefaultRetentionDays},
			Recovery: model.OperationsRecovery{RPO: "15m", RTO: model.DefaultRTO},
			Alerts:   model.OperationsAlerts{Email: "ops@example.com"},
		},
		NpmScope: "@c4factory",
	}
	// An empty scope (a provider that sets none) is the default (#1231).
	got := scaffoldInput(container, ci, []string{"postgres"}, []string{"webshop"}, model.ChecksBeforeRelease, specOperations(container), "")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scaffoldInput() = %+v, want %+v", got, want)
	}
	// The GitProvider's own scope is passed through (#1231).
	want.NpmScope = "@acme"
	got = scaffoldInput(container, ci, []string{"postgres"}, []string{"webshop"}, model.ChecksBeforeRelease, specOperations(container), "@acme")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("scaffoldInput(@acme) = %+v, want %+v", got, want)
	}
}

func TestPlannedFile(t *testing.T) {
	current := scaffold.Tree{"Makefile": {Data: []byte("check:\n")}}
	for _, tc := range []struct {
		name string
		plan scaffold.Update
		want string
	}{
		{"kept", scaffold.Update{}, "check:\n"},
		{"written", scaffold.Update{Files: scaffold.Tree{"Makefile": {Data: []byte("check-light:\n")}}}, "check-light:\n"},
		{"left out", scaffold.Update{LeftOut: []string{"Makefile"}}, "check:\n"},
		{"removed", scaffold.Update{Removes: []string{"Makefile"}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(plannedFile(tc.plan, current, "Makefile")); got != tc.want {
				t.Errorf("plannedFile = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNoLightCheck(t *testing.T) {
	const tail = ", which make check-light on each change needs; CI keeps running the full check on every change " +
		"until the Makefile has one"
	const head = "CI can't change to run the full check only before a release: the repository's Makefile "
	behind := &c4v1beta1.ContainerStandardStatus{Name: "go-standard", Ref: "v1.2.1",
		Behind: []c4v1beta1.StandardFile{{Path: "go.mod", Ref: "v1.0.0"}, {Path: "Makefile", Ref: "v1.1.6"}}}
	for _, tc := range []struct {
		name string
		s    *c4v1beta1.ContainerStandardStatus
		plan scaffold.Update
		want string
	}{
		{"its own", &c4v1beta1.ContainerStandardStatus{Name: "go-standard"}, scaffold.Update{},
			"has no check-light target"},
		// The reporter's case: an earlier update left the Makefile out.
		{"left out before", behind, scaffold.Update{},
			"was left out of the go-standard updates (at v1.1.6) and has no check-light target"},
		{"left out now", &c4v1beta1.ContainerStandardStatus{Name: "go-standard"},
			scaffold.Update{LeftOut: []string{"Makefile"}},
			"is left out of this go-standard update and has no check-light target"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := noLightCheck(tc.s, tc.plan); got != head+tc.want+tail {
				t.Errorf("noLightCheck =\n%s\nwant\n%s", got, head+tc.want+tail)
			}
		})
	}
}
