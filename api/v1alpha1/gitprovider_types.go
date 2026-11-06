/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/
package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// GitProviderKind is the kind of Git hosting. Gitea is the only one in v1.
// +kubebuilder:validation:Enum=gitea
type GitProviderKind string

// Git provider kinds.
const (
	GitProviderGitea GitProviderKind = "gitea"
)

// GitProviderSpec defines where Container repositories are created.
type GitProviderSpec struct {
	// provider is the kind of Git hosting.
	// +kubebuilder:default=gitea
	// +optional
	Provider GitProviderKind `json:"provider,omitempty"`

	// url is the base address of the Git host, such as http://gitea.gitea.svc.cluster.local:3000.
	// +kubebuilder:validation:Pattern=`^https?://`
	// +required
	URL string `json:"url"`

	// owner is the organization that holds Container repositories.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	// +required
	Owner string `json:"owner"`

	// credentialsSecretRef names a Secret with keys "username" and "token".
	// +required
	CredentialsSecretRef LocalRef `json:"credentialsSecretRef"`

	// ci picks the CI system and how its runners are kept. Without it, the
	// Git host's own CI serves the project's repositories, but no runner takes
	// their jobs: the install's shared runners serve the templates organization
	// only, so a project's checks wait until a ci block gives it a pool
	// (#1698).
	// +optional
	CI *CISpec `json:"ci,omitempty"`

	// packages names the npm and PyPI registries the owner's packages are
	// published to and installed from. Without it, and for each registry
	// left out, the Git host's own registries under the owner, with the
	// scope @theos: the operator publishes with the GitProvider's
	// credentials and the agents install with the agent bot's.
	// +optional
	Packages *PackagesSpec `json:"packages,omitempty"`
}

// PackagesSpec names the owner's package registries, one per ecosystem.
type PackagesSpec struct {
	// npm is the registry the owner's npm packages go to.
	// +optional
	NPM *NpmPackagesSpec `json:"npm,omitempty"`

	// pypi is the index the owner's Python packages go to.
	// +optional
	PyPI *PyPIPackagesSpec `json:"pypi,omitempty"`
}

// NpmPackagesSpec is the owner's npm registry.
type NpmPackagesSpec struct {
	// scope is the scope the owner's packages are named under, such as
	// @theos; the registry serves it.
	// +kubebuilder:validation:Pattern=`^@[a-z0-9][a-z0-9._-]*$`
	// +kubebuilder:validation:MaxLength=214
	// +optional
	Scope string `json:"scope,omitempty"`

	// registry is the registry's URL, such as https://registry.example.com/npm/.
	// +kubebuilder:validation:Pattern=`^https?://`
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	Registry string `json:"registry,omitempty"`

	// credentialsSecretRef names a Secret with the key "token", which
	// publishes (only the operator reads it), and may have "readToken",
	// which the agents and the owner's CI install with. Without it,
	// installs from another host send no credential.
	// +optional
	CredentialsSecretRef *LocalRef `json:"credentialsSecretRef,omitempty"`
}

// PyPIPackagesSpec is the owner's Python package index.
type PyPIPackagesSpec struct {
	// index is the simple index's URL, where packages are installed from,
	// such as https://pypi.example.com/simple.
	// +kubebuilder:validation:Pattern=`^https?://`
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	Index string `json:"index,omitempty"`

	// upload is the URL packages are published to, such as
	// https://pypi.example.com/legacy/.
	// +kubebuilder:validation:Pattern=`^https?://`
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	Upload string `json:"upload,omitempty"`

	// credentialsSecretRef names a Secret with the key "token", which
	// publishes (only the operator reads it), and may have "readToken",
	// which the agents and the owner's CI install with. Without it,
	// installs from another host send no credential.
	// +optional
	CredentialsSecretRef *LocalRef `json:"credentialsSecretRef,omitempty"`
}

// CISpec picks the CI system, one member at a time. giteaActions is the only
// member in v1alpha1.
type CISpec struct {
	// giteaActions is Gitea Actions on a pool of single-job runner pods the
	// operator keeps in the project's namespace, registered to the GitProvider's
	// owner organization.
	// +optional
	GiteaActions *GiteaActionsSpec `json:"giteaActions,omitempty"`
}

// GiteaActionsSpec is the per-project pool of Gitea Actions runners.
type GiteaActionsSpec struct {
	// runners has one entry per job label. Each keeps size idle pods waiting
	// for a job; a pod takes one job and is replaced.
	// +listType=map
	// +listMapKey=label
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=8
	// +required
	Runners []CIRunnerSpec `json:"runners"`
}

// CIRunnerSpec is one label's runners in a project's pool.
// +kubebuilder:validation:XValidation:rule="!(has(self.goCache) && self.goCache && has(self.freshGoCache) && self.freshGoCache)",message="goCache and freshGoCache exclude each other: a label's pods mount the project's Go cache or start from a fresh one"
type CIRunnerSpec struct {
	// label is the job label the runners take, as the workflows' runs-on names it.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=32
	// +required
	Label string `json:"label"`

	// image is the runner image, such as theos/gitea-runner-go:dev; it
	// registers in an init container and runs the jobs.
	// +kubebuilder:validation:MinLength=1
	// +required
	Image string `json:"image"`

	// size is how many idle pods wait for a job.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=8
	// +kubebuilder:default=1
	// +optional
	Size int32 `json:"size,omitempty"`

	// resources for the runner container, where the jobs run.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// goCache mounts the project's Go build cache from the node, under the
	// operator's --ci-go-cache, so a job compiles only what changed. Only for
	// an image with the Go toolchain.
	// +optional
	GoCache bool `json:"goCache,omitempty"`

	// freshGoCache gives each pod a Go build cache of its own: an emptyDir
	// seeded from the image's warm cache, which nothing shares and the pod
	// takes with it. For the label releases run on (go-release): a release
	// builds signed artifacts, so it must not link a build result a pull
	// request's job left in the project's cache. Excludes goCache. Only for
	// an image with the Go toolchain.
	// +optional
	FreshGoCache bool `json:"freshGoCache,omitempty"`
}

// +kubebuilder:deprecatedversion:warning="theos.factory.io/v1alpha1 is deprecated since v0.30.0; use theos.factory.io/v1beta1. A release 90 or more days after v0.30.0 may drop it (#1963)."
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories=theos
// +kubebuilder:printcolumn:name="Provider",type=string,JSONPath=".spec.provider"
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=".spec.url"
// +kubebuilder:printcolumn:name="Owner",type=string,JSONPath=".spec.owner"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// GitProvider is where repositories are created, with credentials. Owned by the Project.
type GitProvider struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of GitProvider
	// +required
	Spec GitProviderSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// GitProviderList contains a list of GitProvider
type GitProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GitProvider `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &GitProvider{}, &GitProviderList{})
		return nil
	})
}
