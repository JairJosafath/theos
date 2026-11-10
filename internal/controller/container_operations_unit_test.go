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
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

func TestOperationsStatusRoundTrips(t *testing.T) {
	days := int32(0)
	container := &c4v1beta1.Container{Spec: c4v1beta1.ContainerSpec{Operations: &c4v1beta1.OperationsSpec{
		Backups: &c4v1beta1.OperationsBackups{RetentionDays: &days, CopyToRegion: "eu-west-1"},
	}}}
	recorded := operationsStatus(specOperations(container))
	got := scaffoldedOperations(container, &c4v1beta1.ContainerStatus{Operations: recorded})
	if got != specOperations(container) {
		t.Errorf("status.operations reads back as %+v, want %+v", got, specOperations(container))
	}
	if *recorded.Backups.RetentionDays != 0 || recorded.Recovery.RPO != "1h" || recorded.Recovery.RTO != "4h" {
		t.Errorf("status.operations = %+v, want every field set, retention 0 kept", recorded)
	}
}

func TestReportOperationsApplied(t *testing.T) {
	rpo := func(s string) *c4v1beta1.OperationsSpec {
		return &c4v1beta1.OperationsSpec{Recovery: &c4v1beta1.OperationsRecovery{RPO: s}}
	}
	scaffoldedWith := func(spec *c4v1beta1.OperationsSpec) *c4v1beta1.OperationsSpec {
		return operationsStatus(specOperations(&c4v1beta1.Container{Spec: c4v1beta1.ContainerSpec{Operations: spec}}))
	}
	const updates = "the update acts"
	for _, tt := range []struct {
		name     string
		phase    c4v1beta1.ContainerPhase
		spec     *c4v1beta1.OperationsSpec
		recorded *c4v1beta1.OperationsSpec
		want     string // the reason; empty for no condition, updates for the update's pass to act
	}{
		{"not scaffolded yet", c4v1beta1.ContainerScaffolding, rpo("15m"), nil, ""},
		{"scaffolded without the block", c4v1beta1.ContainerReady, nil, scaffoldedWith(nil),
			c4v1beta1.ReasonAppliedAtScaffold},
		{"scaffolded with the block", c4v1beta1.ContainerReady, rpo("15m"), scaffoldedWith(rpo("15m")),
			c4v1beta1.ReasonAppliedAtScaffold},
		{"the defaults written out are no change", c4v1beta1.ContainerReady, rpo("1h"), scaffoldedWith(nil),
			c4v1beta1.ReasonAppliedAtScaffold},
		// An operations update carries these (#1119).
		{"changed after the scaffold", c4v1beta1.ContainerReady, rpo("5m"), scaffoldedWith(rpo("15m")), updates},
		{"added after the scaffold", c4v1beta1.ContainerReady, rpo("5m"), scaffoldedWith(nil), updates},
		// The Framework doesn't read the block, or the Container was scaffolded
		// before the factory kept it (#1209).
		{"not read by the Framework, no block", c4v1beta1.ContainerReady, nil, nil, ""},
		{"not read by the Framework, an empty block", c4v1beta1.ContainerReady,
			&c4v1beta1.OperationsSpec{}, nil, ""},
		{"not read by the Framework, a block", c4v1beta1.ContainerReady, rpo("5m"), nil,
			c4v1beta1.ReasonNotReadByFramework},
	} {
		t.Run(tt.name, func(t *testing.T) {
			container := &c4v1beta1.Container{Spec: c4v1beta1.ContainerSpec{Operations: tt.spec,
				FrameworkRef: c4v1beta1.LocalRef{Name: "go-cli"}}}
			status := &c4v1beta1.ContainerStatus{Phase: tt.phase, Operations: tt.recorded}
			reported := (&ContainerReconciler{}).reportOperationsApplied(container, status)
			c := meta.FindStatusCondition(status.Conditions, c4v1beta1.ConditionOperationsApplied)
			if reported == (tt.want == updates) {
				t.Fatalf("reportOperationsApplied() = %v, want %v", reported, tt.want != updates)
			}
			if tt.want == updates {
				tt.want = ""
			}
			switch {
			case tt.want == "" && c != nil:
				t.Errorf("OperationsApplied = %s/%s, want none", c.Status, c.Reason)
			case tt.want != "" && c == nil:
				t.Errorf("no OperationsApplied, want reason %s", tt.want)
			case c != nil && c.Reason != tt.want:
				t.Errorf("OperationsApplied reason = %s (%s), want %s", c.Reason, c.Message, tt.want)
			case c != nil && c.Reason == c4v1beta1.ReasonNotReadByFramework &&
				!strings.Contains(c.Message, "Framework go-cli doesn't read spec.operations"):
				t.Errorf("OperationsApplied message = %q, want it to name the Framework", c.Message)
			}
		})
	}
}

func TestStoryObjectWritesOnlyThePlannedOperations(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   *model.OperationsChoices
		want *c4v1beta1.OperationsSpec
	}{
		{"left out", nil, nil},
		{"one field", &model.OperationsChoices{RPO: "15m"},
			&c4v1beta1.OperationsSpec{Recovery: &c4v1beta1.OperationsRecovery{RPO: "15m"}}},
		{"a zero retention is a choice", &model.OperationsChoices{RetentionDays: new(int32(0)), Email: "a@b.io"},
			&c4v1beta1.OperationsSpec{Backups: &c4v1beta1.OperationsBackups{RetentionDays: new(int32(0))},
				Alerts: &c4v1beta1.OperationsAlerts{Email: "a@b.io"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			obj, err := storyObject("shop", model.PlanObject{Kind: model.KindContainer, Name: "api",
				Operations: tt.in})
			if err != nil {
				t.Fatal(err)
			}
			got := obj.(*c4v1beta1.Container).Spec.Operations
			if !equality.Semantic.DeepEqual(got, tt.want) {
				t.Errorf("spec.operations = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestOperationsInFlight(t *testing.T) {
	block := func(rpo string) *c4v1beta1.OperationsSpec {
		return operationsStatus(specOperations(&c4v1beta1.Container{Spec: c4v1beta1.ContainerSpec{
			Operations: &c4v1beta1.OperationsSpec{Recovery: &c4v1beta1.OperationsRecovery{RPO: rpo}}}}))
	}
	open := func(edit func(*c4v1beta1.OperationsUpdateStatus)) *c4v1beta1.OperationsUpdateStatus {
		u := &c4v1beta1.OperationsUpdateStatus{Operations: block("5m"), Branch: "operations-update/1",
			Commit: "c0ffee", PullRequest: 7, PullRequestURL: "http://gitea/shop/api/pulls/7"}
		if edit != nil {
			edit(u)
		}
		return u
	}
	for _, tt := range []struct {
		name   string
		update *c4v1beta1.OperationsUpdateStatus
		// recorded is status.operations, the block main was last rendered with.
		recorded     *c4v1beta1.OperationsSpec
		want, merged bool
	}{
		{"no update", nil, block("1h"), false, false},
		{"an open pull request", open(nil), block("1h"), true, false},
		{"merged at a commit", open(func(u *c4v1beta1.OperationsUpdateStatus) { u.MergeCommit = "beef" }),
			block("5m"), false, true},
		// #1119: a merge the host named no commit for, and main couldn't be read.
		{"merged without a commit", open(nil), block("5m"), false, true},
		// #1119: a person closed it, so reading it again at each recheck finds nothing new.
		{"closed by a person", open(func(u *c4v1beta1.OperationsUpdateStatus) {
			u.Stopped = c4v1beta1.ReasonUpdateClosed
		}), block("1h"), false, false},
		{"refused, still open", open(func(u *c4v1beta1.OperationsUpdateStatus) {
			u.Stopped = c4v1beta1.ReasonUpdateRefused
		}), block("1h"), false, false},
		{"stopped before a pull request", &c4v1beta1.OperationsUpdateStatus{Operations: block("5m"),
			Stopped: c4v1beta1.ReasonChangedAfterScaffold}, block("1h"), false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			status := &c4v1beta1.ContainerStatus{Phase: c4v1beta1.ContainerReady, Operations: tt.recorded,
				OperationsUpdate: tt.update}
			if got := operationsInFlight(status); got != tt.want {
				t.Errorf("operationsInFlight() = %v, want %v", got, tt.want)
			}
			if got := operationsMerged(status); got != tt.merged {
				t.Errorf("operationsMerged() = %v, want %v", got, tt.merged)
			}
		})
	}
}
