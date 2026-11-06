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
// +kubebuilder:validation:XValidation:rule="[has(self.local), has(self.aws)].exists_one(x, x)",message="set exactly one provider member: local or aws"
// +kubebuilder:validation:XValidation:rule="!has(self.aws) || (has(self.limits) && has(self.limits.budgetUSDPerDay))",message="an aws environment needs limits.budgetUSDPerDay: a remote lease costs money for every minute it is held"
// +kubebuilder:validation:XValidation:rule="!has(self.aws) || !has(self.ci) || (!has(self.ci.size) && !has(self.ci.runnerImage))",message="an aws environment's ci leases a machine for each waiting job, which runs the job on a runner of its own: it keeps no idle runners (ci.size) and takes no ci.runnerImage"
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

	// aws leases an EC2 machine per run that needs the environment, in the
	// user's AWS account, from an AMI with the environment's tools, such as
	// a GPU machine for a game engine (T9, T11). Its leases cost what EC2
	// charges, so it needs limits.budgetUSDPerDay.
	// +optional
	AWS *TestEnvironmentAWS `json:"aws,omitempty"`

	// limits cap how many leases run at once, for how long and, for a
	// remote member, at what cost a day.
	// +kubebuilder:default={}
	// +optional
	Limits TestEnvironmentLimits `json:"limits,omitzero"`

	// ci runs the project's CI jobs labelled env-<name> on the environment's
	// tools, so CI checks what the agents ran with theos-env
	// (docs/plans/test-environments.md, T8). A local environment keeps a
	// pool of runner pods; a remote one, set to {}, leases a machine for
	// each job that waits, within its limits and budget, which it shares
	// with the runs, once a GitProvider's ci block gives the Git host's
	// publicURL (step 8).
	// +optional
	CI *TestEnvironmentCI `json:"ci,omitempty"`
}

// TestEnvironmentCI is a local environment's pool of CI runner pods: one
// per GitProvider whose ci block keeps runners, under the label
// env-<name>, which a workflow names in runs-on. A remote environment's ci
// is empty: each machine it leases for a job registers a runner of its own
// for that job, and ends with it.
type TestEnvironmentCI struct {
	// size is how many idle pods wait for a job; 0, the default, keeps
	// none, so no pod runs until a workflow names the label. Local only.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=8
	// +optional
	Size int32 `json:"size,omitempty"`

	// runnerImage is a CI runner image, such as the release's base runner
	// image: each pod registers with it in an init container, which copies
	// its runner into the pod, and the jobs run on the environment's image. Unset, it is the
	// image of the first runner of the GitProvider's ci block. Local only: a
	// remote machine downloads its runner (the operator's
	// --test-environment-runner-url).
	// +kubebuilder:validation:MaxLength=500
	// +optional
	RunnerImage string `json:"runnerImage,omitempty"`
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

// TestEnvironmentAWS is an EC2 machine per lease in the user's AWS account
// (T9, T11): launched from imageId in subnetId with a public address, with
// no instance role, under a security group of the operator's own that lets
// in theos-env's port alone and lets out only DNS and public HTTPS, and the
// metadata service turned off before the run gets it (T5). User data
// downloads theos-env (the operator's --test-environment-tools-url), serves
// it over HTTPS with a certificate the run pins, and shuts the machine down,
// which terminates it, at the lease's deadline. Return terminates it.
type TestEnvironmentAWS struct {
	// region is the AWS region the machines run in, such as eu-west-1.
	// +kubebuilder:validation:MaxLength=30
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-z]{2}(-[a-z]+)+-[0-9]+$')",message="region must be an AWS region, such as eu-west-1"
	// +required
	Region string `json:"region"`

	// account is the twelve-digit AWS account id the credential must be in:
	// set, a credential of another account is refused, so a key for the
	// wrong account never launches a machine.
	// +kubebuilder:validation:MaxLength=12
	// +kubebuilder:validation:XValidation:rule="self.matches('^[0-9]{12}$')",message="account must be a twelve-digit AWS account id"
	// +optional
	Account string `json:"account,omitempty"`

	// instanceType is the EC2 instance type, such as c7i.large, or g5.xlarge
	// for a GPU. EC2 Mac types, which need a dedicated host, come later
	// (docs/plans/test-environments.md, step 9).
	// +kubebuilder:validation:MaxLength=40
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-z][a-z0-9-]*[.][a-z0-9]+$')",message="instanceType must be an EC2 instance type, such as g5.xlarge"
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('mac')",message="EC2 Mac instance types need a dedicated host, which the aws member doesn't lease yet"
	// +required
	InstanceType string `json:"instanceType"`

	// imageId is the AMI the machines boot, with the environment's tools: a
	// Linux with curl, sha256sum and a cloud-init that runs user data, such
	// as an AWS Deep Learning AMI for a GPU. An AMI id never changes what it
	// names, so it is pinned.
	// +kubebuilder:validation:MaxLength=21
	// +kubebuilder:validation:XValidation:rule="self.matches('^ami-[0-9a-f]{8,17}$')",message="imageId must be an AMI id, such as ami-0123456789abcdef0"
	// +required
	ImageID string `json:"imageId"`

	// subnetId is the subnet the machines start in, which must route to the
	// internet: each gets a public address there, where the run reaches it
	// over HTTPS. The operator makes the machines' security group in its
	// VPC.
	// +kubebuilder:validation:MaxLength=32
	// +kubebuilder:validation:XValidation:rule="self.matches('^subnet-[0-9a-f]{8,17}$')",message="subnetId must be a subnet id, such as subnet-0123456789abcdef0"
	// +required
	SubnetID string `json:"subnetId"`

	// credentialsSecretRef names a Secret in the TestEnvironment's namespace
	// that the user creates, with the keys accessKeyId, secretAccessKey and
	// an optional sessionToken, as a cloudFormation DeployTarget's. Only the
	// operator reads it: it never reaches a sandbox, an environment or a CI
	// job. The key needs the EC2 and pricing calls docs/getting-started.md
	// lists.
	// +required
	CredentialsSecretRef LocalRef `json:"credentialsSecretRef"`
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

	// budgetUSDPerDay is what the environment's leases may cost a UTC day, in
	// US dollars, by the provider's count (on-demand price times time). Past
	// it no lease starts, reason WaitingForEnvironment, OverBudget, and the
	// leases held finish their run. Required for a remote member; a local
	// lease costs nothing.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100000
	// +optional
	BudgetUSDPerDay int32 `json:"budgetUSDPerDay,omitempty"`
}

// The conditions and reasons of a TestEnvironment.
const (
	// ConditionTestEnvironmentReady says whether runs can lease the
	// environment: its image is pinned and the project's NetworkPolicy
	// theos-test-environment, which fences its pods, exists. Its reasons are
	// Ready, ImageNotPinned, FenceMissing and CredentialsInvalid.
	ConditionTestEnvironmentReady = "Ready"
	// ConditionTestEnvironmentCredentialsValid says whether a remote
	// member's provider takes the credential its Secret holds, in the account
	// the member names. Its reasons are Valid, SecretMissing,
	// SecretIncomplete, CredentialRefused and Unreachable. A local
	// environment, which needs none, has no such condition.
	ConditionTestEnvironmentCredentialsValid = "CredentialsValid"
	// ConditionTestEnvironmentCI says whether a remote environment with a
	// ci block runs the project's jobs labelled env-<name>: a GitProvider's
	// ci block keeps runners, gives the Git host's public address and names
	// no env-<name> runner of its own, and the provider and the Git host can
	// start a runner for one job. Its reasons are Serving, NoGitProvider,
	// NoPublicAddress, LabelClash, Unsupported, Unreachable and OverBudget.
	// A local environment's pool is the Project's CIRunners condition's.
	ConditionTestEnvironmentCI = "CI"
)

// TestEnvironmentLease is one live lease of the environment.
type TestEnvironmentLease struct {
	// id is the lease's ID, which the provider names it by: for local, the
	// pod's name; for aws, the machine's lease tag.
	// +required
	ID string `json:"id"`
	// holder is the Task whose run holds the lease, or, for a remote
	// machine that runs a CI job, ci/<GitProvider>/<runner id>: the runner
	// registered for that job.
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

	// conditions: Ready, and CredentialsValid for a remote member.
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
// +kubebuilder:printcolumn:name="Instance",type=string,JSONPath=".spec.aws.instanceType",priority=1
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
