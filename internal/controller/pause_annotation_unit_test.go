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

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/agentrunner"
)

// The runner has no cluster access and mirrors the API's pause annotation:
// the operator sets one name and theos-agent reads it (self-hosting step 12).
func TestPauseAnnotationMatchesTheAPI(t *testing.T) {
	if agentrunner.PauseAnnotation != c4v1beta1.AnnotationPause {
		t.Errorf("agentrunner.PauseAnnotation = %q, want c4v1beta1.AnnotationPause, %q",
			agentrunner.PauseAnnotation, c4v1beta1.AnnotationPause)
	}
}
