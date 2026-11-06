/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package v1beta1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// TestEnvironmentSpec is a named set of tools a job runs its tests or
// picture scripts on, such as a browser, a phone emulator or a game engine,
// and the provider that supplies it: exactly one member, keyed by field
// name, as a DeployTarget's (docs/plans/test-environments.md, T1). Each
// member is added with the adapter that leases it.
// +kubebuilder:validation:XValidation:rule="[has(self.local)].exists_one(x, x)",message="set exactly one provider member: local"
type TestEnvironmentSpec struct {
	// description says in one line what the environment has, for people and
	// for the agents and planner that choose it.
	// +kubebuilder:validation:MaxLength=200
	// +optional
	Description string `json:"description,omitempty"`

	// local leases a pod in the project's namespace per run that needs the
	// environment, built from image, fenced like an agent sandbox.
	// +optional
	Local *TestEnvironmentLocal `json:"local,omitempty"`

	// limits cap how many leases run at once and for how long.
	// +kubebuilder:default={}
	// +optional
	Limits TestEnvironmentLimits `json:"limits,omitzero"`
}

// TestEnvironmentLocal is a pod per lease on the cluster the operator runs
// in (T7). The operator copies theos-env into it with an init container and
// runs `theos-env serve` there, so any image with the tools works.
type TestEnvironmentLocal struct {
	// image has the environment's tools, such as
	// ghcr.io/jairjosafath/theos/env-browser:<version>. It is pinned: a tag
	// other than latest, or a digest.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=500
	// +kubebuilder:validation:XValidation:rule="self.matches('@sha256:[0-9a-f]{64}$') || (self.matches('^[^@]+:[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$') && !self.endsWith(':latest'))",message="image must be pinned: a tag other than latest, or a digest"
	// +required
	Image string `json:"image"`

	// resources are the environment container's requests and limits.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// devices are extended resources the pod requests and is limited to,
	// named as the cluster's device plugins name them, such as
	// devices.kubevirt.io/kvm: 1 for an Android emulator or nvidia.com/gpu: 1
	// for a game engine; a setting, since plugins differ per cluster.
	// +kubebuilder:validation:MaxProperties=8
	// +kubebuilder:validation:XValidation:rule="self.all(k, k.contains('/'))",message="devices are extended resources a device plugin names, such as devices.kubevirt.io/kvm; cpu and memory go in resources"
	// +optional
	Devices map[corev1.ResourceName]resource.Quantity `json:"devices,omitempty"`

	// runtimeClassName is the RuntimeClass the pod runs under, such as one
	// a GPU operator installs; unset, the cluster's default.
	// +kubebuilder:validation:MaxLength=253
	// +optional
	RuntimeClassName string `json:"runtimeClassName,omitempty"`

	// nodeSelector puts the pod on nodes with these labels, such as the
	// nodes that have the devices.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// tolerations let the pod run on tainted nodes, such as GPU nodes.
	// +kubebuilder:validation:MaxItems=16
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
}

// TestEnvironmentLimits cap the environment's leases (T10).
type TestEnvironmentLimits struct {
	// maxLeases is how many leases may be live at once; a run that would
	// take one more waits, reason WaitingForEnvironment, AllLeased.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=16
	// +kubebuilder:default=2
	// +optional
	MaxLeases int32 `json:"maxLeases,omitempty"`

	// maxLeaseMinutes is how long one lease may last at most, as long as an
	// agent run by default; the provider ends a lease that outlives it.
	// +kubebuilder:validation:Minimum=5
	// +kubebuilder:validation:Maximum=1440
	// +kubebuilder:default=180
	// +optional
	MaxLeaseMinutes int32 `json:"maxLeaseMinutes,omitempty"`
}

// The conditions and reasons of a TestEnvironment.
const (
	// ConditionTestEnvironmentReady says whether runs can lease the
	// environment: its image is pinned and the project's NetworkPolicy
	// theos-test-environment, which fences its pods, exists. Its reasons are
	// Ready, ImageNotPinned and FenceMissing.
	ConditionTestEnvironmentReady = "Ready"
)

// TestEnvironmentLease is one live lease of the environment.
type TestEnvironmentLease struct {
	// id is the lease's ID, which the provider names it by: for local, the
	// pod's name.
	// +required
	ID string `json:"id"`
	// holder is the Task whose run holds the lease.
	// +optional
	Holder string `json:"holder,omitempty"`
	// state is Pending, Ready, Failed or Ended.
	// +optional
	State string `json:"state,omitempty"`
	// since is when the lease started.
	// +optional
	Since *metav1.Time `json:"since,omitempty"`
	// message says why a lease is Pending or Failed.
	// +optional
	Message string `json:"message,omitempty"`
	// costMilliUSD is what the lease cost so far, by the provider's count; 0
	// for local.
	// +optional
	CostMilliUSD int64 `json:"costMilliUSD,omitempty"`
}

// TestEnvironmentSpend is what the environment's leases cost on one day.
type TestEnvironmentSpend struct {
	// day is the UTC date, such as 2026-11-06.
	// +required
	Day string `json:"day"`
	// milliUSD is the cost of the day's leases so far, the ended ones'
	// included.
	// +optional
	MilliUSD int64 `json:"milliUSD,omitempty"`
}

// TestEnvironmentStatus is what the operator last found of the environment.
type TestEnvironmentStatus struct {
	// leases are the live leases, one per run that holds the environment.
	// +listType=map
	// +listMapKey=id
	// +kubebuilder:validation:MaxItems=64
	// +optional
	Leases []TestEnvironmentLease `json:"leases,omitempty"`

	// spentToday is what the leases cost today.
	// +optional
	SpentToday *TestEnvironmentSpend `json:"spentToday,omitempty"`

	// conditions: Ready.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=theos
// +kubebuilder:validation:XValidation:rule="self.metadata.name.matches('^[a-z]([-a-z0-9]{0,30}[a-z0-9])?$')",message="a TestEnvironment's name is a DNS label of at most 32 characters that starts with a letter: jobs ask for it as env/<name>"
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=".spec.local.image",priority=1
// +kubebuilder:printcolumn:name="Max",type=integer,JSONPath=".spec.limits.maxLeases"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type==\"Ready\")].status"
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=".status.conditions[?(@.type==\"Ready\")].reason"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// TestEnvironment is a named set of tools, such as browser, android or
// godot, that the agents' runs and CI lease from a provider
// (docs/plans/test-environments.md).
type TestEnvironment struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec is the provider and the limits.
	// +required
	Spec TestEnvironmentSpec `json:"spec"`

	// status is the live leases and the environment's readiness.
	// +optional
	Status TestEnvironmentStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// TestEnvironmentList contains a list of TestEnvironment
type TestEnvironmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []TestEnvironment `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &TestEnvironment{}, &TestEnvironmentList{})
		return nil
	})
}
