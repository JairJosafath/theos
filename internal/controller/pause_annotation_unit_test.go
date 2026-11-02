/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"testing"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/agentrunner"
)

// The runner has no cluster access and mirrors the API's pause annotation:
// the operator sets one name and c4agent reads it (self-hosting step 12).
func TestPauseAnnotationMatchesTheAPI(t *testing.T) {
	if agentrunner.PauseAnnotation != c4v1beta1.AnnotationPause {
		t.Errorf("agentrunner.PauseAnnotation = %q, want c4v1beta1.AnnotationPause, %q",
			agentrunner.PauseAnnotation, c4v1beta1.AnnotationPause)
	}
}
