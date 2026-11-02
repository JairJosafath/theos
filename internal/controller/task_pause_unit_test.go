/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/agentrunner"
	"github.com/JairJosafath/c4factory/internal/model"
)

// Step 12 of docs/plans/self-hosting.md: every sandbox pod sees its own
// annotations at agentrunner.PodInfoDir, one file, through the downward API.
func TestBuildSandboxMountsItsAnnotations(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := c4v1beta1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	for _, class := range []string{"", NoCheckpointClass} {
		sandbox, err := buildSandbox(sandboxRun{
			task:      &c4v1beta1.Task{ObjectMeta: objectMeta("apps", "add-crop")},
			status:    &c4v1beta1.TaskStatus{Round: 1, Branch: "task/add-crop"},
			container: &c4v1beta1.Container{ObjectMeta: objectMeta("apps", "image-shrink")},
			agent:     &c4v1beta1.Agent{ObjectMeta: objectMeta("apps", "claude")},
			name:      "add-crop-r1-implement", role: model.RoleImplementer, checkpointClass: class,
		}, scheme, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		pod := podOf(t, sandbox)
		want := corev1.Volume{Name: "podinfo", VolumeSource: corev1.VolumeSource{
			DownwardAPI: &corev1.DownwardAPIVolumeSource{Items: []corev1.DownwardAPIVolumeFile{{
				Path: "annotations", FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.annotations"}}}}}}
		var volume *corev1.Volume
		for i := range pod.Volumes {
			if pod.Volumes[i].Name == want.Name {
				volume = &pod.Volumes[i]
			}
		}
		if volume == nil || !reflect.DeepEqual(*volume, want) {
			t.Errorf("class %q: podinfo volume %+v, want %+v", class, volume, want)
		}
		mount := corev1.VolumeMount{Name: "podinfo", MountPath: agentrunner.PodInfoDir, ReadOnly: true}
		found := false
		for _, m := range pod.Containers[0].VolumeMounts {
			found = found || reflect.DeepEqual(m, mount)
		}
		if !found {
			t.Errorf("class %q: agent mounts %v, want %+v", class, pod.Containers[0].VolumeMounts, mount)
		}
	}
}

// pausedSandbox is a running sandbox with the checkpoint volume and a
// shutdown time, and its pod, pod-1, whose run paused after costing $0.40.
func pausedSandbox(t *testing.T, name, reason string) (*unstructured.Unstructured, *corev1.Pod) {
	t.Helper()
	sandbox, pod := interruptedSandbox(name)
	_ = unstructured.SetNestedField(sandbox.Object, "2026-11-02T11:00:00Z", "spec", "shutdownTime")
	result, err := json.Marshal(agentrunner.Result{Role: "implement", Paused: true, PauseReason: reason,
		CostMilliUSD: new(int64(400))})
	if err != nil {
		t.Fatal(err)
	}
	pod.Status = corev1.PodStatus{Phase: corev1.PodSucceeded, ContainerStatuses: []corev1.ContainerStatus{{
		Name: agentContainer, State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			Reason: "Completed", Message: string(result)}}}}}
	return sandbox, pod
}

// A run that paused at the factory's ask is counted, cost included, and
// suspended with no resume spent; its sandbox is marked paused and loses its
// shutdown time, which its resume sets again.
func TestPausedRunSuspendsAndResumes(t *testing.T) {
	const name, reason = "add-crop-r1-implement", "Crew linter paused its runs"
	sandbox, pod := pausedSandbox(t, name, reason)
	c := fake.NewClientBuilder().WithScheme(checkpointScheme(t)).WithObjects(sandbox, pod).Build()
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	r := &TaskReconciler{Client: c, Now: func() time.Time { return now }}
	ctx := context.Background()

	run, err := runState(ctx, c, "apps", name, "pod-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.Result == nil || !run.Result.Paused || run.Result.PauseReason != reason {
		t.Fatalf("runState() = %+v, want the paused result", run)
	}
	task := &c4v1beta1.Task{ObjectMeta: objectMeta("apps", "add-crop")}
	status := &c4v1beta1.TaskStatus{Phase: c4v1beta1.TaskImplementing, Round: 1, Resumes: 2, Reruns: 1,
		Sandbox: name, SandboxPod: "pod-1"}
	step := model.NextTaskStep(model.TaskFacts{Phase: model.TaskImplementing, Round: 1, MaxRounds: 3, Resumes: 2,
		Reruns: 1, Pause: reason, Run: run})
	if step.Action != model.ActionPauseRun {
		t.Fatalf("NextTaskStep() = %+v, want PauseRun", step)
	}
	if err := r.suspendRun(ctx, task, status, step, true); err != nil {
		t.Fatal(err)
	}
	if err := r.dropEndedRuns(withEndedRuns(ctx), task); err != nil {
		t.Fatal(err)
	}
	if status.Resumes != 2 || status.Reruns != 1 || status.Round != 1 || status.CostMilliUSD != 400 ||
		len(status.Runs) != 1 || status.CountedPod != "pod-1" {
		t.Errorf("status resumes %d, reruns %d, round %d, cost %d, %d run records, counted pod %q; "+
			"want 2, 1, 1, 400, 1 and pod-1", status.Resumes, status.Reruns, status.Round, status.CostMilliUSD,
			len(status.Runs), status.CountedPod)
	}
	if cond := findCondition(status.Conditions, ConditionProgressing); cond == nil || cond.Reason != model.ReasonPaused {
		t.Errorf("Progressing = %+v, want reason Paused", cond)
	}
}

// suspendSandbox and resumeSandbox mark and unmark a paused run's sandbox.
func TestSuspendSandboxMarksAPausedRun(t *testing.T) {
	const name, reason = "add-crop-r1-implement", "Brief cel-lens paused its runs"
	sandbox, pod := pausedSandbox(t, name, reason)
	c := fake.NewClientBuilder().WithScheme(checkpointScheme(t)).WithObjects(sandbox, pod).Build()
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	r := &TaskReconciler{Client: c, Now: func() time.Time { return now }}
	ctx := context.Background()
	get := func() *unstructured.Unstructured {
		t.Helper()
		got := newSandbox()
		if err := c.Get(ctx, client.ObjectKey{Namespace: "apps", Name: name}, got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	if err := r.suspendSandbox(ctx, "apps", name, reason); err != nil {
		t.Fatal(err)
	}
	got := get()
	_, hasShutdown, _ := unstructured.NestedString(got.Object, "spec", "shutdownTime")
	if !sandboxSuspended(got) || got.GetAnnotations()[annotationPausedRun] != reason || hasShutdown {
		t.Errorf("sandbox spec %v, annotations %v; want Suspended, marked paused, with no shutdown time",
			got.Object["spec"], got.GetAnnotations())
	}
	if err := c.Delete(ctx, pod); err != nil {
		t.Fatal(err)
	}
	run, err := runState(ctx, c, "apps", name, "pod-1")
	if err != nil {
		t.Fatal(err)
	}
	if !run.Suspended || !run.Paused || !run.Checkpoint || run.Pod != "" {
		t.Errorf("runState() = %+v, want a suspended paused run with its checkpoint, its pod gone", run)
	}

	if err := r.resumeSandbox(ctx, "apps", name, &sandboxResume{
		podTemplate: map[string]any{"spec": map[string]any{}}, deadline: 30 * time.Minute}); err != nil {
		t.Fatal(err)
	}
	got = get()
	shutdown, _, _ := unstructured.NestedString(got.Object, "spec", "shutdownTime")
	if _, marked := got.GetAnnotations()[annotationPausedRun]; marked || sandboxSuspended(got) ||
		shutdown != now.Add(30*time.Minute+sandboxGrace).Format(time.RFC3339) {
		t.Errorf("resumed sandbox spec %v, annotations %v; want Running, unmarked, a shutdown time past its deadline",
			got.Object["spec"], got.GetAnnotations())
	}

	// An interrupted run's suspend neither marks it nor drops its shutdown time.
	other, otherPod := interruptedSandbox("add-crop-r1-review-a1")
	_ = unstructured.SetNestedField(other.Object, "2026-11-02T11:00:00Z", "spec", "shutdownTime")
	c = fake.NewClientBuilder().WithScheme(checkpointScheme(t)).WithObjects(other, otherPod).Build()
	r.Client = c
	if err := r.suspendSandbox(ctx, "apps", other.GetName(), ""); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(other), other); err != nil {
		t.Fatal(err)
	}
	shutdown, _, _ = unstructured.NestedString(other.Object, "spec", "shutdownTime")
	if _, marked := other.GetAnnotations()[annotationPausedRun]; marked || shutdown == "" {
		t.Errorf("interrupted run's sandbox annotations %v, shutdown %q; want unmarked, its shutdown time kept",
			other.GetAnnotations(), shutdown)
	}
}

// The operator asks a working run to pause with the pod template's pause
// annotation, takes it back when the pause ends, and changes nothing else
// under the running agent.
func TestAskPauseChangesOnlyTheAnnotation(t *testing.T) {
	scheme := checkpointScheme(t)
	built, err := buildSandbox(sandboxRun{
		task:      &c4v1beta1.Task{ObjectMeta: objectMeta("apps", "add-crop")},
		status:    &c4v1beta1.TaskStatus{Round: 1, Branch: "task/add-crop"},
		container: &c4v1beta1.Container{ObjectMeta: objectMeta("apps", "image-shrink")},
		agent:     &c4v1beta1.Agent{ObjectMeta: objectMeta("apps", "claude")},
		name:      "add-crop-r1-implement", role: model.RoleImplementer,
	}, scheme, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(built).Build()
	r := &TaskReconciler{Client: c}
	ctx := context.Background()
	get := func() *unstructured.Unstructured {
		t.Helper()
		got := newSandbox()
		if err := c.Get(ctx, client.ObjectKeyFromObject(built), got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	before := get()
	const reason = "Crew image-shrink paused its runs"
	if err := r.askPause(ctx, "apps", built.GetName(), reason); err != nil {
		t.Fatal(err)
	}
	paused := get()
	annotations, _, _ := unstructured.NestedStringMap(paused.Object, "spec", "podTemplate", "metadata", "annotations")
	if annotations[c4v1beta1.AnnotationPause] != reason {
		t.Errorf("pod template annotations %v, want %s=%q", annotations, c4v1beta1.AnnotationPause, reason)
	}
	unstructured.RemoveNestedField(paused.Object, "spec", "podTemplate", "metadata", "annotations")
	if !reflect.DeepEqual(paused.Object["spec"], before.Object["spec"]) {
		t.Errorf("the pause changed more than the annotation:\n%v\nwas\n%v", paused.Object["spec"], before.Object["spec"])
	}

	if err := r.askPause(ctx, "apps", built.GetName(), ""); err != nil {
		t.Fatal(err)
	}
	annotations, _, _ = unstructured.NestedStringMap(get().Object, "spec", "podTemplate", "metadata", "annotations")
	if _, ok := annotations[c4v1beta1.AnnotationPause]; ok {
		t.Errorf("pod template annotations %v after the pause ended, want no %s", annotations, c4v1beta1.AnnotationPause)
	}
	if labels, _, _ := unstructured.NestedStringMap(get().Object, "spec", "podTemplate", "metadata", "labels"); labels[LabelTask] != "add-crop" {
		t.Errorf("pod template labels %v, want the Task's kept", labels)
	}
}

// A suspended run whose resume a pause holds loses its sandbox's shutdown
// time, so the pause can last past it.
func TestHoldSandboxDropsTheShutdownTime(t *testing.T) {
	sandbox, _ := interruptedSandbox("add-crop-r1-implement")
	_ = unstructured.SetNestedField(sandbox.Object, operatingModeSuspended, "spec", "operatingMode")
	_ = unstructured.SetNestedField(sandbox.Object, "2026-11-02T11:00:00Z", "spec", "shutdownTime")
	c := fake.NewClientBuilder().WithScheme(checkpointScheme(t)).WithObjects(sandbox).Build()
	r := &TaskReconciler{Client: c}
	for range 2 {
		if err := r.holdSandbox(context.Background(), "apps", sandbox.GetName()); err != nil {
			t.Fatal(err)
		}
	}
	got := newSandbox()
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(sandbox), got); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := unstructured.NestedString(got.Object, "spec", "shutdownTime"); found || !sandboxSuspended(got) {
		t.Errorf("held sandbox spec %v, want suspended with no shutdown time", got.Object["spec"])
	}
}

// The Paused condition a Crew or Brief sets counts the Tasks not final, and,
// when it pauses their runs too, only those whose Paused condition isn't True.
func TestPausedConditionMessage(t *testing.T) {
	task := func(name string, phase c4v1beta1.TaskPhase, stopped bool) *c4v1beta1.Task {
		t := &c4v1beta1.Task{ObjectMeta: objectMeta("apps", name)}
		t.Status.Phase = phase
		if stopped {
			t.Status.Conditions = []metav1.Condition{{Type: c4v1beta1.ConditionPaused, Status: metav1.ConditionTrue}}
		}
		return t
	}
	tasks := []*c4v1beta1.Task{task("a", c4v1beta1.TaskImplementing, true), task("b", c4v1beta1.TaskReviewing, false),
		task("c", c4v1beta1.TaskMerged, false), task("d", c4v1beta1.TaskChecking, false)}
	if got, want := pausedConditionMessage(tasks, false), "Paused; 3 Tasks still running: a, b, d"; got != want {
		t.Errorf("drain: %q, want %q", got, want)
	}
	if got, want := pausedConditionMessage(tasks, true), "Paused, runs too; 2 Tasks still stopping: b, d"; got != want {
		t.Errorf("runs too: %q, want %q", got, want)
	}
}

// #1995: a Brief that pauses its runs counts as still stopping only the
// Tasks its pause stops: those of a Crew paused by hand without pauseRuns,
// which the Brief leaves as it is, run to their end and never stop.
func TestBriefPausedCountsOnlyTheRunsItStops(t *testing.T) {
	brief := &c4v1beta1.Brief{ObjectMeta: objectMeta("apps", "shop"),
		Spec: c4v1beta1.BriefSpec{Paused: true, PauseRuns: true}}
	crew := func(name string, byBrief bool) *c4v1beta1.Crew {
		c := &c4v1beta1.Crew{ObjectMeta: objectMeta("apps", name)}
		c.Labels = map[string]string{c4v1beta1.LabelBrief: "shop"}
		c.Spec.Paused = true
		if byBrief {
			markPaused(c, true)
		}
		return c
	}
	task := func(name, crew string, plan bool) *c4v1beta1.Task {
		t := &c4v1beta1.Task{ObjectMeta: objectMeta("apps", name)}
		t.Labels = map[string]string{LabelCrew: crew}
		if plan {
			t.Labels = map[string]string{c4v1beta1.LabelBrief: "shop"}
			t.Spec.Kind = c4v1beta1.TaskPlan
		}
		t.Status.Phase = c4v1beta1.TaskImplementing
		return t
	}
	marked, byHand := crew("cart", true), crew("orders", false)
	c := fake.NewClientBuilder().WithScheme(checkpointScheme(t)).WithObjects(brief, marked, byHand,
		task("add-cart", "cart", false), task("add-orders", "orders", false), task("plan-1", "", true)).Build()
	r := &BriefReconciler{Client: c}
	if err := r.setPausedCondition(context.Background(), brief, []c4v1beta1.Crew{*marked, *byHand}); err != nil {
		t.Fatal(err)
	}
	cond := findCondition(brief.Status.Conditions, c4v1beta1.ConditionPaused)
	if want := "Paused, runs too; 2 Tasks still stopping: add-cart, plan-1"; cond == nil || cond.Message != want {
		t.Errorf("Paused condition = %+v, want %q", cond, want)
	}
	// A Brief that only drains counts every Task still running.
	brief.Spec.PauseRuns = false
	if err := r.setPausedCondition(context.Background(), brief, []c4v1beta1.Crew{*marked, *byHand}); err != nil {
		t.Fatal(err)
	}
	cond = findCondition(brief.Status.Conditions, c4v1beta1.ConditionPaused)
	if want := "Paused; 3 Tasks still running: add-cart, add-orders, plan-1"; cond == nil || cond.Message != want {
		t.Errorf("Paused condition = %+v, want %q", cond, want)
	}
}

func findCondition(conds []metav1.Condition, typ string) *metav1.Condition {
	for i := range conds {
		if conds[i].Type == typ {
			return &conds[i]
		}
	}
	return nil
}
