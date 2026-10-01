/*
Copyright 2026.

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
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/agentrunner"
	"github.com/JairJosafath/c4factory/internal/model"
)

// SandboxGVK is Agent Sandbox's Sandbox, used as unstructured to keep the
// dependency to its CRD.
var SandboxGVK = schema.GroupVersionKind{Group: "agents.x-k8s.io", Version: "v1beta1", Kind: "Sandbox"}

// Labels on agent sandboxes and their pods.
const (
	// LabelTask names the Task a sandbox works for.
	LabelTask = "c4.factory.io/task"
	// LabelAgentSandbox marks agent pods; the project's NetworkPolicy selects it.
	LabelAgentSandbox = "c4.factory.io/agent-sandbox"
	// agentSandboxValue is the value of LabelAgentSandbox.
	agentSandboxValue = "true"
)

// sandboxGrace is how long a sandbox object outlives its pod's deadline.
const sandboxGrace = 10 * time.Minute

func newSandbox() *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(SandboxGVK)
	return u
}

// sandboxRun is everything one sandbox run needs.
type sandboxRun struct {
	task *c4v1alpha1.Task
	// status is the Task's status as this step leaves it, not as it was read.
	status    *c4v1alpha1.TaskStatus
	container *c4v1alpha1.Container
	agent     *c4v1alpha1.Agent
	name      string
	role      string
	repoURL   string
	gitSecret string
	context   string
	// protectedPaths are the paths the agent must not change.
	protectedPaths []string
}

// buildSandbox returns the Sandbox for one run, owned by the Task. Its pod has no
// ServiceAccount token: it reports through its termination message.
func buildSandbox(run sandboxRun, scheme *runtime.Scheme, now time.Time) (*unstructured.Unstructured, error) {
	task, agent := run.task, run.agent
	timeout := time.Duration(agent.Spec.TimeoutMinutes) * time.Minute
	if timeout <= 0 {
		timeout = 45 * time.Minute
	}
	env := []corev1.EnvVar{
		{Name: agentrunner.EnvRole, Value: run.role},
		{Name: agentrunner.EnvRepoURL, Value: run.repoURL},
		{Name: agentrunner.EnvBranch, Value: run.status.Branch},
		{Name: agentrunner.EnvBase, Value: "main"},
		{Name: agentrunner.EnvGoal, Value: task.Spec.Goal},
		{Name: agentrunner.EnvContext, Value: run.context},
		{Name: agentrunner.EnvProject, Value: task.Namespace},
		{Name: agentrunner.EnvContainer, Value: run.container.Name},
		{Name: agentrunner.EnvTask, Value: task.Name},
		{Name: agentrunner.EnvRound, Value: strconv.Itoa(int(run.status.Round))},
		{Name: agentrunner.EnvMaxRounds, Value: strconv.Itoa(int(agent.Spec.MaxRounds))},
		{Name: agentrunner.EnvProtectedPaths, Value: strings.Join(run.protectedPaths, ",")},
		secretEnv(agentrunner.EnvGitUsername, run.gitSecret, "username"),
		secretEnv(agentrunner.EnvGitToken, run.gitSecret, "token"),
	}
	// The reviewer gets the feedback too, to check it was addressed.
	if run.status.Feedback != "" {
		env = append(env, corev1.EnvVar{Name: agentrunner.EnvFeedback, Value: run.status.Feedback})
	}
	env = append(env, agent.Spec.Env...)
	var envFrom []corev1.EnvFromSource
	if ref := agent.Spec.CredentialsSecretRef; ref != nil {
		envFrom = append(envFrom, corev1.EnvFromSource{
			SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: ref.Name}},
		})
	}
	deadline := int64(timeout.Seconds())
	no := false
	labels := map[string]string{
		LabelTask:                 task.Name,
		c4v1alpha1.LabelContainer: run.container.Name,
		c4v1alpha1.LabelProject:   task.Namespace,
		LabelAgentSandbox:         "true",
	}
	pod := corev1.PodSpec{
		RestartPolicy:                corev1.RestartPolicyNever,
		AutomountServiceAccountToken: &no,
		EnableServiceLinks:           &no,
		ActiveDeadlineSeconds:        &deadline,
		Containers: []corev1.Container{{
			Name:                     "agent",
			Image:                    agent.Spec.Image,
			ImagePullPolicy:          corev1.PullIfNotPresent,
			Env:                      env,
			EnvFrom:                  envFrom,
			Resources:                agent.Spec.Resources,
			TerminationMessagePolicy: corev1.TerminationMessageReadFile,
			VolumeMounts:             []corev1.VolumeMount{{Name: "workspace", MountPath: "/workspace"}},
		}},
		Volumes: []corev1.Volume{{Name: "workspace", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}},
	}
	podMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&pod)
	if err != nil {
		return nil, err
	}

	sandbox := newSandbox()
	sandbox.SetNamespace(task.Namespace)
	sandbox.SetName(run.name)
	sandbox.SetLabels(labels)
	shutdown := metav1.NewTime(now.Add(timeout + sandboxGrace)).UTC().Format(time.RFC3339)
	sandbox.Object["spec"] = map[string]any{
		"podTemplate": map[string]any{
			"metadata": map[string]any{"labels": toAnyMap(labels)},
			"spec":     podMap,
		},
		"shutdownTime":   shutdown,
		"shutdownPolicy": "Retain",
	}
	if err := controllerutil.SetControllerReference(task, sandbox, scheme); err != nil {
		return nil, err
	}
	return sandbox, nil
}

func secretEnv(name, secret, key string) corev1.EnvVar {
	return corev1.EnvVar{Name: name, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: secret}, Key: key,
	}}}
}

func toAnyMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// runState reads what a sandbox's pod has done. The pod has the sandbox's name.
func runState(ctx context.Context, c client.Client, ns, name string) (model.RunState, error) {
	var state model.RunState
	sandbox := newSandbox()
	err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, sandbox)
	if apierrors.IsNotFound(err) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	state.Started = true

	var pod corev1.Pod
	err = c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &pod)
	if apierrors.IsNotFound(err) {
		if sandboxExpired(sandbox) {
			state.Done, state.Failure = true, "the sandbox reached its shutdown time"
		}
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed {
		return state, nil
	}
	state.Done = true
	state.Failure = pod.Status.Reason
	for _, cs := range pod.Status.ContainerStatuses {
		term := cs.State.Terminated
		if cs.Name != "agent" || term == nil {
			continue
		}
		var result agentrunner.Result
		if term.Message != "" && json.Unmarshal([]byte(term.Message), &result) == nil {
			state.Result = &model.RunResult{
				Commit: result.Commit, Summary: result.Summary, Verdict: result.Verdict, Error: result.Error,
			}
			return state, nil
		}
		state.Failure = term.Reason
		if term.ExitCode != 0 {
			state.Failure += " (exit code " + strconv.Itoa(int(term.ExitCode)) + ")"
		}
	}
	return state, nil
}

func sandboxExpired(sandbox *unstructured.Unstructured) bool {
	conds, _, _ := unstructured.NestedSlice(sandbox.Object, "status", "conditions")
	for _, c := range conds {
		cond, ok := c.(map[string]any)
		if ok && cond["type"] == "Ready" && cond["reason"] == "SandboxExpired" {
			return true
		}
	}
	return false
}
