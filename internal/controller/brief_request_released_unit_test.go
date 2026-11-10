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

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// A later release whose publishing failed doesn't hide one that went out.
func TestNotePublished(t *testing.T) {
	r := &BriefReconciler{}
	brief := &c4v1beta1.Brief{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "cel-lens"}}
	release := func(tag, state string) *c4v1beta1.CrewRelease {
		return &c4v1beta1.CrewRelease{Tag: tag, Publish: &c4v1beta1.CrewReleasePublish{State: state, URL: "u/" + tag}}
	}
	steps := []struct {
		last *c4v1beta1.CrewRelease
		want model.Shipped
	}{
		{nil, model.Shipped{}},
		{release("v0.1.0", "Pending"), model.Shipped{}},
		{release("v0.1.0", model.PublishPublished), model.Shipped{Tag: "v0.1.0", URL: "u/v0.1.0"}},
		{release("v0.2.0", "Failed"), model.Shipped{Tag: "v0.1.0", URL: "u/v0.1.0"}},
		{release("v0.2.0", model.PublishPublished), model.Shipped{Tag: "v0.2.0", URL: "u/v0.2.0"}},
	}
	for i, s := range steps {
		if got := r.notePublished(brief, "linter", s.last); got != s.want {
			t.Errorf("step %d: notePublished = %v, want %v", i, got, s.want)
		}
	}
	if got := r.notePublished(brief, "server", nil); got != (model.Shipped{}) {
		t.Errorf("another Container's = %v, want none", got)
	}
}
