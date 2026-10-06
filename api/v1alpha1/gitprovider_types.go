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
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories=c4
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
