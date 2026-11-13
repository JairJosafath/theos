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
// name, as a DeployTarget's (docs/plans/done/test-environments.md, T1). Each
// member is added with the adapter that leases it.
// +kubebuilder:validation:XValidation:rule="[has(self.local), has(self.aws), has(self.deviceFarm)].exists_one(x, x)",message="set exactly one provider member: local, aws or deviceFarm"
// +kubebuilder:validation:XValidation:rule="!has(self.aws) || (has(self.limits) && has(self.limits.budgetUSDPerDay))",message="an aws environment needs limits.budgetUSDPerDay: a remote lease costs money for every minute it is held"
// +kubebuilder:validation:XValidation:rule="!has(self.deviceFarm) || (has(self.limits) && has(self.limits.budgetUSDPerDay))",message="a deviceFarm environment needs limits.budgetUSDPerDay: its test runs cost money for every device minute"
// +kubebuilder:validation:XValidation:rule="!has(self.aws) || !has(self.ci) || (!has(self.ci.size) && !has(self.ci.runnerImage))",message="an aws environment's ci leases a machine for each waiting job, which runs the job on a runner of its own: it keeps no idle runners (ci.size) and takes no ci.runnerImage"
// +kubebuilder:validation:XValidation:rule="!has(self.deviceFarm) || !has(self.ci)",message="a deviceFarm environment runs test packages, not CI jobs: it has no machine to run a job's runner on"
// +kubebuilder:validation:XValidation:rule="!has(self.aws) || !has(self.ci) || !self.aws.instanceType.startsWith('mac')",message="an EC2 Mac runs no CI jobs: the CI runner a machine downloads is Linux's, and a Mac host per job would be billed 24 hours each"
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

	// deviceFarm runs test packages, rather than commands, on real phones
	// in AWS Device Farm, in the user's AWS account: a run asks with
	// `theos-env test <name>`, over its Git host, and the operator, which
	// alone holds the credential, starts the run and gives the report back
	// (T12). Its runs cost what Device Farm charges per device minute, so it
	// needs limits.budgetUSDPerDay.
	// +optional
	DeviceFarm *TestEnvironmentDeviceFarm `json:"deviceFarm,omitempty"`

	// limits cap how many leases run at once, for how long and, for a
	// remote member, at what cost a day.
	// +kubebuilder:default={}
	// +optional
	Limits TestEnvironmentLimits `json:"limits,omitzero"`

	// ci runs the project's CI jobs labelled env-<name> on the environment's
	// tools, so CI checks what the agents ran with theos-env
	// (docs/plans/done/test-environments.md, T8). A local environment keeps a
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
// metadata service turned off before the run gets it (T5); an EC2 Mac on a
// dedicated host the operator keeps (mac). User data downloads theos-env
// (the operator's --test-environment-tools-url, Linux's or darwin's), serves
// it over HTTPS with a certificate the run pins, and shuts the machine down,
// which terminates it, at the lease's deadline. Return terminates it.
// +kubebuilder:validation:XValidation:rule="!has(self.mac) || self.instanceType.startsWith('mac')",message="mac is for an EC2 Mac instanceType, such as mac2.metal"
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

	// instanceType is the EC2 instance type, such as c7i.large, g5.xlarge
	// for a GPU, or mac2.metal for macOS and Xcode. An EC2 Mac runs on a
	// dedicated host, which the operator allocates and keeps across leases
	// (mac; docs/plans/done/test-environments.md, step 9).
	// +kubebuilder:validation:MaxLength=40
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-z][a-z0-9-]*[.][a-z0-9]+$')",message="instanceType must be an EC2 instance type, such as g5.xlarge"
	// +required
	InstanceType string `json:"instanceType"`

	// mac is how an EC2 Mac's dedicated hosts are kept: the operator
	// allocates a host for a lease when every host it keeps is busy, places
	// the lease's machine on it, and keeps it for the next leases. Only for
	// a Mac instanceType; unset, the defaults.
	// +optional
	Mac *TestEnvironmentMac `json:"mac,omitempty"`

	// imageId is the AMI the machines boot, with the environment's tools: a
	// Linux with curl, sha256sum and a cloud-init that runs user data, such
	// as an AWS Deep Learning AMI for a GPU, or for a Mac instanceType an
	// EC2 macOS AMI with Xcode, whose ec2-macos-init runs user data. An AMI
	// id never changes what it names, so it is pinned.
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

// TestEnvironmentMac keeps an EC2 Mac's dedicated hosts (T11). EC2 bills a
// Mac host for 24 hours at least from its allocation, so a host torn down
// after every lease would cost a day each time.
type TestEnvironmentMac struct {
	// keepHostHours is how long the operator keeps a host it allocated,
	// from its allocation, for the leases that follow; past it, the host is
	// released once no lease is on it, and a lease that needs one allocates
	// another. A lease that allocates a host counts the host's price for
	// keepHostHours against budgetUSDPerDay at once; one placed on a host
	// kept costs nothing until the host's keepHostHours are over. At least
	// 24, the host's minimum.
	// +kubebuilder:validation:Minimum=24
	// +kubebuilder:validation:Maximum=168
	// +kubebuilder:default=24
	// +optional
	KeepHostHours int32 `json:"keepHostHours,omitempty"`
}

// TestEnvironmentDeviceFarm is AWS Device Farm's real phones, which run an
// app's test packages (docs/plans/done/test-environments.md, T12, step 10). A
// lease runs nothing itself: the run that holds it pushes a request, the
// app and its test package, to the lease's branch on its Git host with
// `theos-env test`; the operator checks it against the lease's token, runs
// it on devicePoolArn's devices, pushes the report, screenshots and logs to
// the same branch, where theos-env writes them into the run's working
// tree, and comments the outcome on the Task's pull request. The
// credential never leaves the operator.
type TestEnvironmentDeviceFarm struct {
	// projectArn is the Device Farm project the runs go in, in us-west-2,
	// Device Farm's one region.
	// +kubebuilder:validation:MaxLength=200
	// +kubebuilder:validation:XValidation:rule="self.matches('^arn:aws:devicefarm:us-west-2:[0-9]{12}:project:[0-9a-f-]{36}$')",message="projectArn must be a Device Farm project's ARN, in us-west-2"
	// +required
	ProjectARN string `json:"projectArn"`

	// devicePoolArn is the devices each run runs on: a device pool of the
	// project, or one of Device Farm's own, such as its top devices.
	// +kubebuilder:validation:MaxLength=200
	// +kubebuilder:validation:XValidation:rule="self.matches('^arn:aws:devicefarm:us-west-2:[0-9]{0,12}:devicepool:[0-9a-f-]{36}(/[0-9a-f-]{36})?$')",message="devicePoolArn must be a Device Farm device pool's ARN, in us-west-2"
	// +required
	DevicePoolARN string `json:"devicePoolArn"`

	// account is the twelve-digit AWS account id the credential must be in,
	// as an aws member's: set, a credential of another account is refused.
	// +kubebuilder:validation:MaxLength=12
	// +kubebuilder:validation:XValidation:rule="self.matches('^[0-9]{12}$')",message="account must be a twelve-digit AWS account id"
	// +optional
	Account string `json:"account,omitempty"`

	// credentialsSecretRef names a Secret in the TestEnvironment's namespace
	// with the keys accessKeyId, secretAccessKey and an optional
	// sessionToken, as an aws member's. Only the operator reads it: it never
	// reaches a sandbox or a CI job. The key needs the Device Farm calls
	// docs/getting-started.md lists.
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
	// member's (aws, deviceFarm) provider takes the credential its Secret
	// holds, in the account the member names. Its reasons are Valid, SecretMissing,
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

// TestEnvironmentTestRun is a test run a lease's run asked for, of a
// deviceFarm environment (T12).
type TestEnvironmentTestRun struct {
	// id is the run's ID on the provider: its lease and its request.
	// +required
	ID string `json:"id"`
	// lease is the lease whose branch the request is on, and holder the
	// Task whose run holds it.
	// +required
	Lease string `json:"lease"`
	// +optional
	Holder string `json:"holder,omitempty"`
	// container is the Container on whose repository the lease's branch
	// is.
	// +optional
	Container string `json:"container,omitempty"`
	// request is the request commit on the lease's branch, and commit the
	// run's commit the app and tests were built from.
	// +required
	Request string `json:"request"`
	// +optional
	Commit string `json:"commit,omitempty"`
	// type is the test package's type, such as instrumentation.
	// +optional
	Type string `json:"type,omitempty"`
	// state is Pending or Running on the provider, Reported once its
	// result is on the lease's branch, or Rejected when the operator ran
	// nothing.
	// +optional
	State string `json:"state,omitempty"`
	// result is Passed, Warned, Failed, Skipped, Errored, Stopped or
	// Rejected, once there is one, and outcome its one line.
	// +optional
	Result string `json:"result,omitempty"`
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Outcome string `json:"outcome,omitempty"`
	// url is the run on the provider's console.
	// +optional
	URL string `json:"url,omitempty"`
	// costMilliUSD is what the run cost, by the provider's count.
	// +optional
	CostMilliUSD int64 `json:"costMilliUSD,omitempty"`
	// since is when the operator started it.
	// +optional
	Since *metav1.Time `json:"since,omitempty"`
	// answer is the operator's result commit on the lease's branch.
	// +optional
	Answer string `json:"answer,omitempty"`
	// comment is the pull request comment still to post once the holder
	// has a pull request; empty once posted.
	// +kubebuilder:validation:MaxLength=8192
	// +optional
	Comment string `json:"comment,omitempty"`
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

	// testRuns are a deviceFarm environment's test runs, the last ones of
	// each lease, kept until their outcome is on the holder's pull request.
	// +listType=map
	// +listMapKey=id
	// +kubebuilder:validation:MaxItems=32
	// +optional
	TestRuns []TestEnvironmentTestRun `json:"testRuns,omitempty"`

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
// +kubebuilder:printcolumn:name="Devices",type=string,JSONPath=".spec.deviceFarm.devicePoolArn",priority=1
// +kubebuilder:printcolumn:name="Max",type=integer,JSONPath=".spec.limits.maxLeases"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type==\"Ready\")].status"
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=".status.conditions[?(@.type==\"Ready\")].reason"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// TestEnvironment is a named set of tools, such as browser, android or
// godot, that the agents' runs and CI lease from a provider
// (docs/plans/done/test-environments.md).
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
