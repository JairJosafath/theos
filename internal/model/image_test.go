/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package model

import "testing"

func TestBuildsImage(t *testing.T) {
	for _, tt := range []struct {
		deployment, typ string
		want            bool
	}{
		{"k8s-manifests", TypeService, true},
		{"k8s-manifests", TypeWorker, true},
		{"k8s-manifests", TypeOperator, true},
		{"gitea-release", TypeCLI, false},
		{"gitea-release", TypeOperator, false},
		{"aws-fargate-ts", TypeService, false},
		{"", TypeService, false},
	} {
		if got := BuildsImage(tt.deployment, tt.typ); got != tt.want {
			t.Errorf("BuildsImage(%q, %q) = %v, want %v", tt.deployment, tt.typ, got, tt.want)
		}
	}
}

func TestImageSettled(t *testing.T) {
	for state, want := range map[string]bool{
		ImageStatePublished: true, ImageStateNoImage: true, ImageStateInvalid: true, ImageStateTagTaken: true,
		ImageStateFailed: false,
	} {
		if got := ImageSettled(state); got != want {
			t.Errorf("ImageSettled(%q) = %v, want %v", state, got, want)
		}
	}
}

func TestImageEvent(t *testing.T) {
	for _, tt := range []struct {
		name                          string
		prevTag, prevState, tag, state string
		reason                        string
		warning                       bool
	}{
		{"first publish", "", "", "v1.0.0", ImageStatePublished, ImageEventPublished, false},
		{"published after a failure", "v1.0.0", ImageStateFailed, "v1.0.0", ImageStatePublished,
			ImageEventPublished, false},
		{"the same release again", "v1.0.0", ImageStatePublished, "v1.0.0", ImageStatePublished, "", false},
		{"the next release", "v1.0.0", ImageStatePublished, "v1.1.0", ImageStatePublished, ImageEventPublished,
			false},
		{"invalid", "", "", "v1.0.0", ImageStateInvalid, ImageEventInvalid, true},
		{"invalid again", "v1.0.0", ImageStateInvalid, "v1.0.0", ImageStateInvalid, "", false},
		{"tag taken", "v0.9.0", ImageStatePublished, "v1.0.0", ImageStateTagTaken, ImageEventTagTaken, true},
		{"no image", "", "", "v1.0.0", ImageStateNoImage, "", false},
		{"failed", "", "", "v1.0.0", ImageStateFailed, "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reason, warning := ImageEvent(tt.prevTag, tt.prevState, tt.tag, tt.state)
			if reason != tt.reason || warning != tt.warning {
				t.Errorf("ImageEvent() = %q, %v; want %q, %v", reason, warning, tt.reason, tt.warning)
			}
		})
	}
}
