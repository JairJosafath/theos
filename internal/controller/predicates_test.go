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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

// #97: the Task and the Crew that wait for an adopted repository's Standard
// are woken when it is recorded, which changes no phase.
func TestPhaseOrStandardRecorded(t *testing.T) {
	ready := func(mutate func(*c4v1beta1.ContainerStatus)) *c4v1beta1.Container {
		c := &c4v1beta1.Container{ObjectMeta: objectMeta("apps", "orders-api"),
			Status: c4v1beta1.ContainerStatus{Phase: c4v1beta1.ContainerReady}}
		if mutate != nil {
			mutate(&c.Status)
		}
		return c
	}
	at := func(ref string) func(*c4v1beta1.ContainerStatus) {
		return func(s *c4v1beta1.ContainerStatus) {
			s.Standard = &c4v1beta1.ContainerStandardStatus{Name: "python-strict", Ref: ref}
		}
	}
	tests := []struct {
		name     string
		old, new *c4v1beta1.Container
		want     bool
	}{
		{"the Standard recorded", ready(nil), ready(at("v1.0.0")), true},
		{"the phase changed", ready(func(s *c4v1beta1.ContainerStatus) { s.Phase = c4v1beta1.ContainerPending }),
			ready(nil), true},
		{"nothing of the two changed", ready(nil), ready(nil), false},
		{"the adoption's pull request opened", ready(nil), ready(func(s *c4v1beta1.ContainerStatus) {
			s.StandardAdoption = &c4v1beta1.StandardAdoptionStatus{Name: "python-strict", PullRequest: 1}
			setCondition(&s.Conditions, 1, c4v1beta1.ConditionStandardUpdated, metav1.ConditionFalse,
				c4v1beta1.ReasonPullRequestOpen, "Pull request 1")
		}), false},
		{"the recorded Standard moved to a new ref", ready(at("v1.0.0")), ready(at("v1.1.0")), false},
		{"the Standard gone from status", ready(at("v1.0.0")), ready(nil), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := phaseOrStandardRecorded().Update(event.UpdateEvent{ObjectOld: tt.old, ObjectNew: tt.new})
			if got != tt.want {
				t.Errorf("Update() = %v, want %v", got, tt.want)
			}
		})
	}
	p := phaseOrStandardRecorded()
	if c := ready(nil); !p.Create(event.CreateEvent{Object: c}) || !p.Delete(event.DeleteEvent{Object: c}) {
		t.Error("a Container's creation or deletion does not pass")
	}
}
