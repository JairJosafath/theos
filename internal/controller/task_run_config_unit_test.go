/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// docs/plans/done/measure-and-improve.md, decision 2: a run's sandbox records the
// Agent configuration it runs on when it is made, the run's state reads it,
// and a resume on a changed Agent records the Agent it resumes on.
func TestSandboxRecordsItsRunsConfiguration(t *testing.T) {
	const name = "add-crop-r1-implement"
	scheme := checkpointScheme(t)
	opus := &c4v1beta1.Agent{ObjectMeta: objectMeta("apps", "implementer"),
		Spec: c4v1beta1.AgentSpec{Model: "claude-opus-5-5"}}
	built, err := buildSandbox(sandboxRun{
		task:      &c4v1beta1.Task{ObjectMeta: objectMeta("apps", "add-crop")},
		status:    &c4v1beta1.TaskStatus{Round: 1, Branch: "task/add-crop"},
		container: &c4v1beta1.Container{ObjectMeta: objectMeta("apps", "image-shrink")},
		agent:     opus,
		name:      name, role: model.RoleImplementer,
	}, scheme, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want := model.RunConfig{Agent: "implementer", Harness: "claude-code", Model: "claude-opus-5-5"}
	if got := runConfigOf(built); got != want {
		t.Errorf("a new sandbox records %+v, want %+v (the harness unset is claude-code)", got, want)
	}

	// A suspended run resumes on the Agent as it is now.
	sandbox, pod := pausedSandbox(t, name, "paused")
	sandbox.SetAnnotations(runConfigAnnotations(opus))
	_ = unstructured.SetNestedField(sandbox.Object, operatingModeSuspended, "spec", "operatingMode")
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sandbox, pod).Build()
	ctx := context.Background()
	run, err := runState(ctx, c, "apps", name, "pod-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.Config != want {
		t.Errorf("runState().Config = %+v, want %+v", run.Config, want)
	}
	if err := c.Delete(ctx, pod); err != nil {
		t.Fatal(err)
	}
	sonnet := &c4v1beta1.Agent{ObjectMeta: objectMeta("apps", "implementer"),
		Spec: c4v1beta1.AgentSpec{Harness: c4v1beta1.HarnessStandIn, Model: "claude-sonnet-5-5"}}
	r := &TaskReconciler{Client: c, Now: time.Now}
	if err := r.resumeSandbox(ctx, "apps", name, &sandboxResume{podTemplate: map[string]any{"spec": map[string]any{}},
		deadline: 30 * time.Minute, config: runConfigAnnotations(sonnet)}); err != nil {
		t.Fatal(err)
	}
	got := newSandbox()
	if err := c.Get(ctx, client.ObjectKey{Namespace: "apps", Name: name}, got); err != nil {
		t.Fatal(err)
	}
	resumed := model.RunConfig{Agent: "implementer", Harness: "stand-in", Model: "claude-sonnet-5-5"}
	if cfg := runConfigOf(got); cfg != resumed {
		t.Errorf("a resumed sandbox records %+v, want the Agent it resumed on, %+v", cfg, resumed)
	}

	// A sandbox from before the record has none, and its run records none.
	old, oldPod := interruptedSandbox("add-crop-r1-review-a1")
	c = fake.NewClientBuilder().WithScheme(scheme).WithObjects(old, oldPod).Build()
	run, err = runState(ctx, c, "apps", old.GetName(), "pod-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.Config != (model.RunConfig{}) {
		t.Errorf("an old sandbox's run records %+v, want nothing", run.Config)
	}
}
