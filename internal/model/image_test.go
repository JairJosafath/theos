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
		deployment, framework, typ string
		scaffoldedFrom             []string
		want                       bool
	}{
		{"k8s-manifests", "go-service", TypeService, nil, true},
		{"k8s-manifests", "ts-worker", TypeWorker, nil, true},
		{"k8s-manifests", "kubebuilder", TypeOperator, nil, true},
		{"gitea-release", "go-cli", TypeCLI, nil, false},
		{"gitea-release", "kubebuilder", TypeOperator, nil, true},
		// Fargate's stack takes its image by digest; the other AWS
		// Frameworks build none (#1610).
		{"gitea-release", "aws-fargate-ts", TypeService, nil, true},
		{"gitea-release", "aws-ec2-ts", TypeService, nil, false},
		{"gitea-release", "aws-lambda-ts", TypeService, nil, false},
		{"aws-fargate-ts", "aws-fargate-ts", TypeService, nil, false},
		{"", "", TypeService, nil, false},
		// #1797: the scaffolded aws-fargate-ts ref decides, as a Container
		// scaffolded before v2.0.0 has no image job; a commit ref counts as
		// current, and the other builders don't look at the ref.
		{"gitea-release", "aws-fargate-ts", TypeService,
			[]string{"aws-fargate-ts@v1.3.0", "aws-standard@v1.19.1", "gitea-release@v1.6.0"}, false},
		{"gitea-release", "aws-fargate-ts", TypeService,
			[]string{"aws-fargate-ts@v2.0.0", "aws-standard@v1.19.1", "gitea-release@v1.8.0"}, true},
		{"gitea-release", "aws-fargate-ts", TypeService, []string{"aws-fargate-ts@v2.1.0"}, true},
		{"gitea-release", "aws-fargate-ts", TypeService, []string{"aws-fargate-ts@3f1c2a9d"}, true},
		{"gitea-release", "aws-fargate-ts", TypeService, []string{"aws-standard@v1.0.0"}, true},
		{"k8s-manifests", "go-service", TypeService, []string{"go-service@v0.1.0", "k8s-manifests@v1.0.0"}, true},
	} {
		if got := BuildsImage(tt.deployment, tt.framework, tt.typ, tt.scaffoldedFrom); got != tt.want {
			t.Errorf("BuildsImage(%q, %q, %q, %q) = %v, want %v", tt.deployment, tt.framework, tt.typ,
				tt.scaffoldedFrom, got, tt.want)
		}
	}
}

func TestImageSettled(t *testing.T) {
	for state, want := range map[string]bool{
		ImageStatePublished: true, ImageStateNoImage: true, ImageStateInvalid: true, ImageStateTagTaken: true,
		ImageStatePushing: false, ImageStateFailed: false,
	} {
		if got := ImageSettled(state); got != want {
			t.Errorf("ImageSettled(%q) = %v, want %v", state, got, want)
		}
	}
}

func TestImageEvent(t *testing.T) {
	for _, tt := range []struct {
		name                           string
		prevTag, prevState, tag, state string
		reason                         string
		warning                        bool
	}{
		{"first publish", "", "", "v1.0.0", ImageStatePublished, ImageEventPublished, false},
		{"published after a failure", "v1.0.0", ImageStateFailed, "v1.0.0", ImageStatePublished,
			ImageEventPublished, false},
		{"published after its push", "v1.0.0", ImageStatePushing, "v1.0.0", ImageStatePublished,
			ImageEventPublished, false},
		{"pushing", "v0.9.0", ImageStatePublished, "v1.0.0", ImageStatePushing, "", false},
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
