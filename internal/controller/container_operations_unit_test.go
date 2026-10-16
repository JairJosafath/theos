/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
)

func TestOperationsStatusRoundTrips(t *testing.T) {
	days := int32(0)
	container := &c4v1alpha1.Container{Spec: c4v1alpha1.ContainerSpec{Operations: &c4v1alpha1.OperationsSpec{
		Backups: &c4v1alpha1.OperationsBackups{RetentionDays: &days, CopyToRegion: "eu-west-1"},
	}}}
	recorded := operationsStatus(specOperations(container))
	got := scaffoldedOperations(container, &c4v1alpha1.ContainerStatus{Operations: recorded})
	if got != specOperations(container) {
		t.Errorf("status.operations reads back as %+v, want %+v", got, specOperations(container))
	}
	if *recorded.Backups.RetentionDays != 0 || recorded.Recovery.RPO != "1h" || recorded.Recovery.RTO != "4h" {
		t.Errorf("status.operations = %+v, want every field set, retention 0 kept", recorded)
	}
}

func TestReportOperationsApplied(t *testing.T) {
	rpo := func(s string) *c4v1alpha1.OperationsSpec {
		return &c4v1alpha1.OperationsSpec{Recovery: &c4v1alpha1.OperationsRecovery{RPO: s}}
	}
	scaffoldedWith := func(spec *c4v1alpha1.OperationsSpec) *c4v1alpha1.OperationsSpec {
		return operationsStatus(specOperations(&c4v1alpha1.Container{Spec: c4v1alpha1.ContainerSpec{Operations: spec}}))
	}
	for _, tt := range []struct {
		name     string
		phase    c4v1alpha1.ContainerPhase
		spec     *c4v1alpha1.OperationsSpec
		recorded *c4v1alpha1.OperationsSpec
		want     string // the reason; empty for no condition
	}{
		{"not scaffolded yet", c4v1alpha1.ContainerScaffolding, rpo("15m"), nil, ""},
		{"scaffolded without the block", c4v1alpha1.ContainerReady, nil, scaffoldedWith(nil),
			c4v1alpha1.ReasonAppliedAtScaffold},
		{"scaffolded with the block", c4v1alpha1.ContainerReady, rpo("15m"), scaffoldedWith(rpo("15m")),
			c4v1alpha1.ReasonAppliedAtScaffold},
		{"the defaults written out are no change", c4v1alpha1.ContainerReady, rpo("1h"), scaffoldedWith(nil),
			c4v1alpha1.ReasonAppliedAtScaffold},
		{"changed after the scaffold", c4v1alpha1.ContainerReady, rpo("5m"), scaffoldedWith(rpo("15m")),
			c4v1alpha1.ReasonChangedAfterScaffold},
		{"added after the scaffold", c4v1alpha1.ContainerReady, rpo("5m"), scaffoldedWith(nil),
			c4v1alpha1.ReasonChangedAfterScaffold},
		{"scaffolded before the factory kept it, no block", c4v1alpha1.ContainerReady, nil, nil, ""},
		{"scaffolded before the factory kept it, an empty block", c4v1alpha1.ContainerReady,
			&c4v1alpha1.OperationsSpec{}, nil, ""},
		{"scaffolded before the factory kept it, a block", c4v1alpha1.ContainerReady, rpo("5m"), nil,
			c4v1alpha1.ReasonChangedAfterScaffold},
	} {
		t.Run(tt.name, func(t *testing.T) {
			container := &c4v1alpha1.Container{Spec: c4v1alpha1.ContainerSpec{Operations: tt.spec}}
			status := &c4v1alpha1.ContainerStatus{Phase: tt.phase, Operations: tt.recorded}
			(&ContainerReconciler{}).reportOperationsApplied(container, status)
			c := meta.FindStatusCondition(status.Conditions, c4v1alpha1.ConditionOperationsApplied)
			switch {
			case tt.want == "" && c != nil:
				t.Errorf("OperationsApplied = %s/%s, want none", c.Status, c.Reason)
			case tt.want != "" && c == nil:
				t.Errorf("no OperationsApplied, want reason %s", tt.want)
			case c != nil && c.Reason != tt.want:
				t.Errorf("OperationsApplied reason = %s (%s), want %s", c.Reason, c.Message, tt.want)
			}
		})
	}
}

func TestStoryObjectWritesOnlyThePlannedOperations(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   *model.OperationsChoices
		want *c4v1alpha1.OperationsSpec
	}{
		{"left out", nil, nil},
		{"one field", &model.OperationsChoices{RPO: "15m"},
			&c4v1alpha1.OperationsSpec{Recovery: &c4v1alpha1.OperationsRecovery{RPO: "15m"}}},
		{"a zero retention is a choice", &model.OperationsChoices{RetentionDays: new(int32(0)), Email: "a@b.io"},
			&c4v1alpha1.OperationsSpec{Backups: &c4v1alpha1.OperationsBackups{RetentionDays: new(int32(0))},
				Alerts: &c4v1alpha1.OperationsAlerts{Email: "a@b.io"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			obj, err := storyObject("shop", model.PlanObject{Kind: model.KindContainer, Name: "api",
				Operations: tt.in})
			if err != nil {
				t.Fatal(err)
			}
			got := obj.(*c4v1alpha1.Container).Spec.Operations
			if !equality.Semantic.DeepEqual(got, tt.want) {
				t.Errorf("spec.operations = %+v, want %+v", got, tt.want)
			}
		})
	}
}
