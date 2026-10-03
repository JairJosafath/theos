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
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
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
	// goal is the written goal, or the issue's title and body as read now.
	goal string
	// maxRounds is the Task's round limit, from its implementing Agent.
	maxRounds int32
	// related are the related Containers' repositories, by Container name.
	related map[string]string
	// protectedPaths are the paths the agent must not change.
	protectedPaths []string
	// allowedPaths are the protected paths a Standard change lets this Task
	// change, and nothing else; from spec.allowedProtectedPaths, for both roles.
	allowedPaths []string
	// resumed means the implementer resumes after the Task's Standard change merged.
	resumed bool
	// workspaceMemory, when set, puts the workspace in memory, up to this size.
	workspaceMemory *resource.Quantity
	// goCache, when set, is the directory on the node of the Go build cache the
	// sandboxes share.
	goCache string
}

// Where a sandbox mounts the shared Go build cache, and the variables pointing
// Go and golangci-lint at it. c4-go-cache (hack/toolchain-go) lays it out.
const (
	goCacheMount     = "/cache/go"
	goCacheVolume    = "go-cache"
	goCachePrepShell = "command -v c4-go-cache >/dev/null || exit 0; exec c4-go-cache " + goCacheMount
)

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
		{Name: agentrunner.EnvGoal, Value: run.goal},
		{Name: agentrunner.EnvContext, Value: run.context},
		{Name: agentrunner.EnvProject, Value: task.Namespace},
		{Name: agentrunner.EnvContainer, Value: run.container.Name},
		{Name: agentrunner.EnvTask, Value: task.Name},
		{Name: agentrunner.EnvRound, Value: strconv.Itoa(int(run.status.Round))},
		{Name: agentrunner.EnvMaxRounds, Value: strconv.Itoa(int(run.maxRounds))},
		{Name: agentrunner.EnvHarness, Value: harness(agent)},
		{Name: agentrunner.EnvProtectedPaths, Value: strings.Join(run.protectedPaths, ",")},
		secretEnv(agentrunner.EnvGitUsername, run.gitSecret, "username"),
		secretEnv(agentrunner.EnvGitToken, run.gitSecret, "token"),
	}
	if agent.Spec.Model != "" {
		env = append(env, corev1.EnvVar{Name: agentrunner.EnvModel, Value: agent.Spec.Model})
	}
	if task.Spec.Issue > 0 {
		env = append(env, corev1.EnvVar{Name: agentrunner.EnvIssue, Value: strconv.Itoa(int(task.Spec.Issue))})
	}
	if len(run.allowedPaths) > 0 {
		env = append(env, corev1.EnvVar{Name: agentrunner.EnvAllowedPaths, Value: strings.Join(run.allowedPaths, ",")})
	}
	if run.resumed {
		env = append(env, corev1.EnvVar{Name: agentrunner.EnvResumed, Value: "true"})
	}
	if related := relatedEnv(run.related); related != "" {
		env = append(env, corev1.EnvVar{Name: agentrunner.EnvRelated, Value: related})
	}
	// The reviewer gets the feedback too, to check it was addressed.
	if run.status.Feedback != "" {
		env = append(env, corev1.EnvVar{Name: agentrunner.EnvFeedback, Value: run.status.Feedback})
	}
	if run.goCache != "" {
		env = append(env,
			corev1.EnvVar{Name: "GOCACHE", Value: goCacheMount + "/build"},
			corev1.EnvVar{Name: "GOLANGCI_LINT_CACHE", Value: goCacheMount + "/golangci-lint"})
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
	workspace := &corev1.EmptyDirVolumeSource{}
	if run.workspaceMemory != nil {
		// Checks write a lot; on a small cluster, the disk they share with etcd stalls
		// the API server. The size counts against the agent's memory limit.
		workspace.Medium, workspace.SizeLimit = corev1.StorageMediumMemory, run.workspaceMemory
	}
	mounts := []corev1.VolumeMount{{Name: "workspace", MountPath: "/workspace"}}
	volumes := []corev1.Volume{{Name: "workspace", VolumeSource: corev1.VolumeSource{EmptyDir: workspace}}}
	var initContainers []corev1.Container
	if run.goCache != "" {
		// Compiled packages outlive the pod, so the next round, reviewer and Task
		// don't compile them again. CI keeps its own cache that no sandbox can
		// reach: Go trusts what it reads from a cache, and agents run untrusted code.
		dirOrCreate := corev1.HostPathDirectoryOrCreate
		mount := corev1.VolumeMount{Name: goCacheVolume, MountPath: goCacheMount}
		mounts = append(mounts, mount)
		volumes = append(volumes, corev1.Volume{Name: goCacheVolume, VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: run.goCache, Type: &dirOrCreate},
		}})
		// Seeds the cache from the image's warm one and bounds its size; an image
		// without Go has no c4-go-cache, and skips it.
		initContainers = append(initContainers, corev1.Container{
			Name:            goCacheVolume,
			Image:           agent.Spec.Image,
			ImagePullPolicy: corev1.PullIfNotPresent,
			Command:         []string{"sh", "-c", goCachePrepShell},
			Resources:       agent.Spec.Resources,
			VolumeMounts:    []corev1.VolumeMount{mount},
		})
	}
	pod := corev1.PodSpec{
		RestartPolicy:                corev1.RestartPolicyNever,
		AutomountServiceAccountToken: &no,
		EnableServiceLinks:           &no,
		ActiveDeadlineSeconds:        &deadline,
		InitContainers:               initContainers,
		Containers: []corev1.Container{{
			Name:                     "agent",
			Image:                    agent.Spec.Image,
			ImagePullPolicy:          corev1.PullIfNotPresent,
			Env:                      env,
			EnvFrom:                  envFrom,
			Resources:                agent.Spec.Resources,
			TerminationMessagePolicy: corev1.TerminationMessageReadFile,
			VolumeMounts:             mounts,
		}},
		Volumes: volumes,
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
	expired, missed := sandboxExpiry(sandbox)
	if apierrors.IsNotFound(err) {
		switch {
		case missed:
			state.Done, state.Interrupted = true, true
			state.Failure = "the cluster was down at the sandbox's shutdown time"
		case expired:
			state.Done, state.Failure = true, "the sandbox reached its shutdown time"
		}
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if !metav1.IsControlledBy(&pod, sandbox) {
		// A pod of an earlier sandbox with this name, not yet collected: not this
		// run's. Creation times can't tell: they count whole seconds.
		return state, nil
	}
	if pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed {
		return state, nil
	}
	state.Done = true
	state.Failure = pod.Status.Reason
	state.Interrupted = podDisrupted(&pod)
	// Its own deadline or its sandbox's shutdown time ends a run that took too
	// long: the run's failure, though c4agent sees the same SIGTERM. A shutdown
	// time the cluster was down for is not the run's limit.
	ownLimit := pod.Status.Reason == "DeadlineExceeded" || expired && !missed
	for _, cs := range pod.Status.ContainerStatuses {
		term := cs.State.Terminated
		if cs.Name != "agent" || term == nil {
			continue
		}
		var result agentrunner.Result
		if term.Message != "" && json.Unmarshal([]byte(term.Message), &result) == nil {
			state.Result = &model.RunResult{
				Commit: result.Commit, Summary: result.Summary, Verdict: result.Verdict, Error: result.Error,
				Proposal: result.Proposal, Empty: result.Empty,
				FollowUps: result.FollowUps, Unavailable: result.Unavailable,
			}
			state.Interrupted = (state.Interrupted || result.Interrupted) && !ownLimit
			if result.RetryAt != nil {
				state.Result.RetryAt = *result.RetryAt
			}
			return state, nil
		}
		state.Failure = term.Reason
		// A container the kubelet lost across a node restart.
		state.Interrupted = state.Interrupted || term.Reason == "ContainerStatusUnknown"
		if term.Reason == "OOMKilled" || ownLimit {
			state.Interrupted = false
		}
		if term.ExitCode != 0 {
			state.Failure += " (exit code " + strconv.Itoa(int(term.ExitCode)) + ")"
		}
	}
	return state, nil
}

// podDisrupted reports whether the infrastructure ended the pod: a node
// shutdown, an eviction or a preemption, which Kubernetes marks with the
// DisruptionTarget condition or the pod's reason.
func podDisrupted(pod *corev1.Pod) bool {
	switch pod.Status.Reason {
	case "Evicted", "Preempting", "Terminated", "NodeShutdown", "Shutdown", "NodeLost":
		return true
	}
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.DisruptionTarget && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

// sandboxExpiry reports whether Agent Sandbox found the sandbox past its
// shutdown time, and whether it found that so late that the cluster was down at
// that time (model.MissedShutdown): the Ready condition turns to SandboxExpired
// when Agent Sandbox notices.
func sandboxExpiry(sandbox *unstructured.Unstructured) (expired, missed bool) {
	conds, _, _ := unstructured.NestedSlice(sandbox.Object, "status", "conditions")
	for _, c := range conds {
		cond, ok := c.(map[string]any)
		if !ok || cond["type"] != "Ready" || cond["reason"] != "SandboxExpired" {
			continue
		}
		shutdown, _, _ := unstructured.NestedString(sandbox.Object, "spec", "shutdownTime")
		noticed, _ := cond["lastTransitionTime"].(string)
		return true, model.MissedShutdown(parseTime(shutdown), parseTime(noticed))
	}
	return false, false
}

// parseTime reads an RFC 3339 time; the zero time when it can't.
func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// harness is the Agent's harness, claude-code when unset.
func harness(agent *c4v1alpha1.Agent) string {
	if agent.Spec.Harness == "" {
		return string(c4v1alpha1.HarnessClaudeCode)
	}
	return string(agent.Spec.Harness)
}

// relatedEnv lists repositories as name=url pairs, sorted by name.
func relatedEnv(repos map[string]string) string {
	pairs := make([]string, 0, len(repos))
	for _, name := range slices.Sorted(maps.Keys(repos)) {
		pairs = append(pairs, name+"="+repos[name])
	}
	return strings.Join(pairs, ",")
}
