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
package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// AgentSpec is who does a Task's work, and the image it runs in.
type AgentSpec struct {
	// displayName is a human-friendly name.
	// +kubebuilder:validation:MaxLength=100
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// description says what this Agent is good at.
	// +kubebuilder:validation:MaxLength=1000
	// +optional
	Description string `json:"description,omitempty"`

	// image runs c4agent, the agent CLI and the toolchain of the Containers it works on.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	// +required
	Image string `json:"image"`

	// credentialsSecretRef names the Secret with the agent's model credentials, such
	// as CLAUDE_CODE_OAUTH_TOKEN. Every key becomes an environment variable.
	// +optional
	CredentialsSecretRef *LocalRef `json:"credentialsSecretRef,omitempty"`

	// maxRounds is how many implement rounds a Task gets before it fails.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10
	// +kubebuilder:default=3
	// +optional
	MaxRounds int32 `json:"maxRounds,omitempty"`

	// timeoutMinutes bounds one sandbox run.
	// +kubebuilder:validation:Minimum=5
	// +kubebuilder:validation:Maximum=240
	// +kubebuilder:default=45
	// +optional
	TimeoutMinutes int32 `json:"timeoutMinutes,omitempty"`

	// resources for the sandbox's container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// env adds plain settings to the sandbox, such as C4AGENT_FAKE=1 for the stand-in agent.
	// +listType=map
	// +listMapKey=name
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,categories=c4;c4catalog
// +kubebuilder:printcolumn:name="Display Name",type=string,JSONPath=".spec.displayName"
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=".spec.image"
// +kubebuilder:printcolumn:name="Rounds",type=integer,JSONPath=".spec.maxRounds"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Agent is a catalog entry: an agent that does Tasks in a sandbox. It is owned by
// the Project.
type Agent struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the agent's image, credentials and limits
	// +required
	Spec AgentSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// AgentList contains a list of Agent
type AgentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Agent `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Agent{}, &AgentList{})
		return nil
	})
}
