/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// DeployTier is how much a DeployTarget matters: a production target needs a
// person's approval unless its approval says None.
// +kubebuilder:validation:Enum=development;production
type DeployTier string

// Deploy tiers.
const (
	DeployTierDevelopment DeployTier = "development"
	DeployTierProduction  DeployTier = "production"
)

// DeployApproval says whether a person lets each version into a DeployTarget.
// +kubebuilder:validation:Enum=Required;None
type DeployApproval string

// Deploy approvals.
const (
	DeployApprovalRequired DeployApproval = "Required"
	DeployApprovalNone     DeployApproval = "None"
)

// DeployTargetSpec is a place Containers are shipped to: exactly one member,
// keyed by field name, says which tool owns the place and where in it. Each
// member is added with the adapter that ships to it (docs/plans/done/deploy.md).
// +kubebuilder:validation:XValidation:rule="[has(self.github)].exists_one(x, x)",message="set exactly one target member: github"
type DeployTargetSpec struct {
	// github ships each release as a GitHub release of a repository: its tag,
	// notes and files.
	// +optional
	GitHub *DeployTargetGitHub `json:"github,omitempty"`

	// credentialsSecretRef names a Secret in the DeployTarget's namespace that
	// the user creates; its keys are the member's (token for github). Only the
	// operator reads it: it is never mounted in a sandbox or passed to a CI job.
	// +required
	CredentialsSecretRef LocalRef `json:"credentialsSecretRef"`

	// tier is development or production.
	// +kubebuilder:default=development
	// +optional
	Tier DeployTier `json:"tier,omitempty"`

	// approval is Required when a person lets each version in, None when every
	// release ships by itself. Empty means Required for a production target and
	// None for a development one.
	// +optional
	Approval DeployApproval `json:"approval,omitempty"`

	// parameters are deploy-time values that differ per place and are not
	// secret (an alert email, a certificate ARN, a domain), mapped by the
	// member's adapter. A secret value goes in the Secret, never here.
	// +kubebuilder:validation:MaxProperties=50
	// +optional
	Parameters map[string]string `json:"parameters,omitempty"`
}

// DeployTargetGitHub is a GitHub repository whose releases are the deploys.
type DeployTargetGitHub struct {
	// repository is owner/name on github.com.
	// +kubebuilder:validation:MaxLength=140
	// +kubebuilder:validation:XValidation:rule="self.matches('^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$')",message="repository must be owner/name"
	// +required
	Repository string `json:"repository"`

	// tagPrefix goes before the version in each release's tag. Unset, it is
	// the Container's name and "-" (orders-cli-v0.1.0), so any number of
	// Containers share the target; set, it is used as it is for every
	// Container shipping there, "" giving bare tags (v0.1.0) on a
	// repository of one app.
	// +kubebuilder:validation:MaxLength=40
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9._/-]*$`
	// +kubebuilder:validation:XValidation:rule="!self.matches('^[./-]|//|/[.]|[.][.]|[.]lock(/|$)')",message="tagPrefix must make a git tag name: no leading '.', '/' or '-', no '..', '//' or '/.', and no folder ending in .lock"
	// +optional
	TagPrefix *string `json:"tagPrefix,omitempty"`

	// latest says whether each release shipped becomes the repository's
	// Latest release on GitHub. It defaults to true; set it to false on a
	// target for every app but the one people install, when several ship to
	// one repository, so a library's release doesn't take Latest from it.
	// +optional
	Latest *bool `json:"latest,omitempty"`
}

// ConditionDeployTargetReady on a DeployTarget says whether its Secret holds
// the member's keys and the target accepts the credential. Its reasons are
// Ready, SecretMissing, SecretIncomplete, CredentialRefused and Unreachable.
const ConditionDeployTargetReady = "Ready"

// DeployTargetStatus is what the operator last found of the target.
type DeployTargetStatus struct {
	// conditions: Ready, from the member's check of the credential.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// checkedTime is when the operator last asked the target whether it
	// accepts the credential.
	// +optional
	CheckedTime *metav1.Time `json:"checkedTime,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=c4
// +kubebuilder:printcolumn:name="Repository",type=string,JSONPath=".spec.github.repository"
// +kubebuilder:printcolumn:name="Tier",type=string,JSONPath=".spec.tier"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type==\"Ready\")].status"
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=".status.conditions[?(@.type==\"Ready\")].reason"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// DeployTarget is a place Containers run, with the credential the operator
// ships to it with, its tier and whether a person approves each version.
type DeployTarget struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of DeployTarget
	// +required
	Spec DeployTargetSpec `json:"spec"`

	// status defines the observed state of DeployTarget
	// +optional
	Status DeployTargetStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// DeployTargetList contains a list of DeployTarget
type DeployTargetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DeployTarget `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &DeployTarget{}, &DeployTargetList{})
		return nil
	})
}
